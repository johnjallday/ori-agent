//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetincident"
)

// These drive the real recovery host (runResetRecoveryServer) over a loopback
// listener against a disposable installation reproduced with production code:
// an accepted Start Fresh, an emptied workspace, a recovery launch blocked at
// revision 3. No user data, port owner or real credential store is involved.

type recoveryHost struct {
	incident *resetincident.Incident
	base     string
	port     int
	done     chan error
	exited   atomic.Bool
	pipe     *os.File // read end of the supervisor's restart pipe, when managed
}

func startRecoveryHost(t *testing.T, managed bool) *recoveryHost {
	t.Helper()
	incident := resetincident.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := &recoveryHost{incident: incident, port: listener.Addr().(*net.TCPAddr).Port, done: make(chan error, 1)}
	host.base = fmt.Sprintf("http://127.0.0.1:%d", host.port)
	var supervisor *inheritedResetSupervisor
	if managed {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		host.pipe = reader
		supervisor = &inheritedResetSupervisor{pipe: writer, pid: os.Getppid()}
	}
	go func() {
		err := runResetRecoveryServer(incident.Lease, host.port, true, listener, supervisor)
		host.exited.Store(true) // before the signal handler is gone, so cleanup never signals a bare process
		host.done <- err
	}()
	t.Cleanup(func() {
		if host.exited.Load() {
			return
		}
		// The host's own signal handler turns this into a graceful stop.
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		deadline := time.Now().Add(10 * time.Second)
		for !host.exited.Load() && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
	})
	return host
}

func (h *recoveryHost) offer(t *testing.T) (settingsreset.Operation, *settingsreset.CancelOffer) {
	t.Helper()
	var response struct {
		Operation settingsreset.Operation    `json:"operation"`
		Cancel    *settingsreset.CancelOffer `json:"cancel"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(h.base + "/api/reset/operations/" + h.incident.OperationID) // #nosec G107 -- loopback listener owned by this test
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && json.Unmarshal(body, &response) == nil {
				return response.Operation, response.Cancel
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery host did not answer: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (h *recoveryHost) cancel(t *testing.T, token string, revision uint64) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"revision":%d}`, revision)
	request, err := http.NewRequest(http.MethodPost, h.base+"/api/reset/operations/"+h.incident.OperationID+"/cancel", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Origin", h.base)
	request.Header.Set(settingsreset.CancelTokenHeader, token)
	resp, err := http.DefaultClient.Do(request) // #nosec G107 -- loopback listener owned by this test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func (h *recoveryHost) restartPipe(t *testing.T) string {
	t.Helper()
	if err := h.pipe.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(io.LimitReader(h.pipe, maxHandoffBytes))
	return string(data)
}

func (h *recoveryHost) state(t *testing.T) settingsreset.OperationState {
	t.Helper()
	op, err := settingsreset.CurrentRecoveryOperation(t.Context(), h.incident.Lease)
	if err != nil {
		t.Fatal(err)
	}
	return op.State
}

func TestRecoveryHostCancelsAndAsksTheSupervisorForExactlyOneReplacement(t *testing.T) {
	host := startRecoveryHost(t, true)
	op, offer := host.offer(t)
	if op.State != settingsreset.StateBlocked || offer == nil || !offer.Available || offer.Revision != 3 || offer.Token == "" {
		t.Fatalf("the incident did not offer cancellation: %+v %+v", op, offer)
	}
	if got := host.restartPipe(t); got != "" {
		t.Fatalf("a restart was requested before any cancellation: %q", got)
	}

	status, reply := host.cancel(t, offer.Token, offer.Revision)
	if status != http.StatusOK || reply["canceled"] != true {
		t.Fatalf("cancel = %d %v", status, reply)
	}
	operation, _ := reply["operation"].(map[string]any)
	restart, _ := operation["restart"].(map[string]any)
	if operation["state"] != "canceled" || restart["managed"] != true {
		t.Fatalf("a managed host must say it will reopen Ori: %v", operation)
	}
	if host.state(t) != settingsreset.StateCanceled {
		t.Fatal("the canceled receipt is not durable")
	}
	// A duplicate click is harmless and asks for nothing more.
	if status, reply := host.cancel(t, offer.Token, offer.Revision); status != http.StatusOK || reply["canceled"] != true {
		t.Fatalf("duplicate = %d %v", status, reply)
	}

	// The host stops serving and only then hands off, once, for that operation.
	select {
	case err := <-host.done:
		if err != nil {
			t.Fatalf("recovery host: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the recovery host did not stop after a durable cancel")
	}
	if got, want := host.restartPipe(t), "canceled "+host.incident.OperationID+"\n"; got != want {
		t.Fatalf("restart handoff = %q, want %q", got, want)
	}
	if _, err := http.Get(host.base + "/api/reset/recovery"); err == nil { // #nosec G107 -- loopback listener owned by this test
		t.Fatal("the recovery host still answers after handing off")
	}
}

func TestRecoveryHostWithoutARestartCapabilityKeepsServingManualSteps(t *testing.T) {
	host := startRecoveryHost(t, false)
	_, offer := host.offer(t)
	if offer == nil {
		t.Fatal("no offer")
	}
	status, reply := host.cancel(t, offer.Token, offer.Revision)
	operation, _ := reply["operation"].(map[string]any)
	restart, _ := operation["restart"].(map[string]any)
	if status != http.StatusOK || reply["canceled"] != true || restart["managed"] == true {
		t.Fatalf("a manual launch must not claim it will reopen Ori: %d %v", status, reply)
	}
	// Longer than the managed grace period: nothing shuts this host down.
	time.Sleep(restartReplyGrace + time.Second)
	select {
	case err := <-host.done:
		t.Fatalf("a manual-launch recovery host stopped itself after cancel: %v", err)
	default:
	}
	resp, err := http.Get(host.base + "/reset/status?operation=" + host.incident.OperationID) // #nosec G107 -- loopback listener owned by this test
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{"Reset canceled", "one full restart", "Control+C", "Quit"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("manual guidance omitted %q", want)
		}
	}
}

// After a cancellation a plain command-line launch — no supervisor, the manual
// path — is admitted to normal startup, and stays admitted on the next launch:
// the canceled receipt is recognised, retained and never re-run.
func TestResetMainStartsNormallyAfterCancellationOnEveryLaunch(t *testing.T) {
	host := startRecoveryHost(t, false)
	_, offer := host.offer(t)
	if offer == nil {
		t.Fatal("no offer")
	}
	if status, reply := host.cancel(t, offer.Token, offer.Revision); status != http.StatusOK || reply["canceled"] != true {
		t.Fatalf("cancel = %d %v", status, reply)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM) // the manual user quits the recovery host
	select {
	case <-host.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the recovery host did not quit")
	}
	if err := host.incident.Lease.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var receipt []byte
	for launch := 1; launch <= 2; launch++ {
		// #nosec G204 -- only the current test binary and a fixed helper test.
		// A launch held in recovery would serve forever; bound the wait so a
		// regression fails in seconds instead of hanging the suite.
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetMainProcessHelper$")
		cmd.Dir = host.incident.DataDir
		cmd.Env = []string{"HOME=" + host.incident.Fixture.Paths().Home, "ORI_DATA_DIR=" + host.incident.DataDir, "ORI_RESET_MAIN_TEST_CHILD=handoff"}
		output, _ := cmd.CombinedOutput()
		if !strings.Contains(string(output), "fixture reached owned server handoff") {
			t.Fatalf("launch %d did not reach normal startup after the cancellation:\n%s", launch, output)
		}
		if strings.Contains(string(output), "reset recovery") || strings.Contains(string(output), "Cannot open installation") {
			t.Fatalf("launch %d was held in recovery:\n%s", launch, output)
		}
		data, err := os.ReadFile(filepath.Join(host.incident.DataDir, resetstate.Directory, string(resetstate.OperationRecord))) // #nosec G304 -- disposable fixture path
		if err != nil {
			t.Fatal(err)
		}
		if launch > 1 && !bytes.Equal(receipt, data) {
			t.Fatal("a later launch rewrote the canceled receipt")
		}
		receipt = data
	}
}

func TestRecoveryHostNeverHandsOffARefusedCancel(t *testing.T) {
	host := startRecoveryHost(t, true)
	_, offer := host.offer(t)
	if offer == nil {
		t.Fatal("no offer")
	}
	for name, call := range map[string]struct {
		token    string
		revision uint64
		want     int
	}{
		"a stale revision": {offer.Token, 2, http.StatusConflict},
		"a wrong token":    {strings.Repeat("x", len(offer.Token)), offer.Revision, http.StatusForbidden},
	} {
		if status, reply := host.cancel(t, call.token, call.revision); status != call.want || reply["canceled"] == true {
			t.Fatalf("%s = %d %v", name, status, reply)
		}
	}
	time.Sleep(restartReplyGrace + 500*time.Millisecond)
	if host.state(t) != settingsreset.StateBlocked {
		t.Fatal("a refused request changed the receipt")
	}
	if got := host.restartPipe(t); got != "" {
		t.Fatalf("a refused request asked for a restart: %q", got)
	}
	select {
	case err := <-host.done:
		t.Fatalf("the host stopped after a refused request: %v", err)
	default:
	}
}
