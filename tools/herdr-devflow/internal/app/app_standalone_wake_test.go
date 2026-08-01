package app

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// daemonPower is deliberately small: it records the privileged boundary while
// preserving unrelated macOS events so the app integration never invokes the
// host's pmset or touches another owner's wake.
type daemonPower struct {
	mu     sync.Mutex
	events []wakeservice.PowerEvent
}

func (p *daemonPower) Events(context.Context) ([]wakeservice.PowerEvent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wakeservice.PowerEvent(nil), p.events...), nil
}

func (p *daemonPower) Schedule(_ context.Context, wakeAt time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, wakeservice.PowerEvent{Type: wakeservice.PMSetEventType, At: wakeAt.UTC(), Owner: wakeservice.PMSetOwner})
	return nil
}

func (p *daemonPower) Cancel(_ context.Context, wakeAt time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.events[:0]
	for _, event := range p.events {
		if event.Owner == wakeservice.PMSetOwner && event.At.Equal(wakeAt.UTC()) {
			continue
		}
		kept = append(kept, event)
	}
	p.events = kept
	return nil
}

func TestContinueWakeUsesStandaloneDaemonAndPersistsWithdrawal(t *testing.T) {
	if runtime.GOOS != "darwin" || !wakeservice.PlatformSupported() {
		t.Skip("the standalone launchd socket service is supported on macOS 12+")
	}
	repo, feature := createLinkedFeatureWorktree(t)
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		return home, key == worktree.HomeOverrideEnv
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.HelperPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.HelperPath, []byte("helper"), 0755); err != nil {
		t.Fatal(err)
	}
	agent := model.RoleAgent{
		Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2",
		NativeSession: model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}, Status: model.AgentIdle,
	}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature},
		WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": agent}, Schedules: map[string]model.Schedule{},
		Handoff: model.HandoffState{PrimaryRole: "builder", PrimaryAgentName: agent.Name},
	}
	store := state.New(paths.StateDir)
	if err := store.Save(bridgeState); err != nil {
		t.Fatal(err)
	}

	due := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	power := &daemonPower{events: []wakeservice.PowerEvent{{Type: wakeservice.PMSetEventType, At: due, Owner: "com.example.foreign"}}}
	// Darwin's Unix-domain sockets have a short path limit, so do not use the
	// deeply nested Go test directory for this transport-only fixture.
	socketDir, err := os.MkdirTemp("/private/tmp", "herdr-wake-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "wake.sock")
	service, err := wakeservice.New(wakeservice.Config{
		BuildVersion: "integration-daemon", SocketPath: socket, StateDir: filepath.Join(t.TempDir(), "daemon-state"),
		AllowedUID: os.Getuid(), Power: power, RequireRoot: false,
		PeerUID: func(net.Conn) (int, error) { return os.Getuid(), nil },
		Chown:   func(string, int, int) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonCtx, stopDaemon := context.WithCancel(context.Background())
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- service.Serve(daemonCtx) }()
	t.Cleanup(func() {
		stopDaemon()
		select {
		case err := <-serveErrors:
			if err != nil {
				t.Errorf("standalone daemon stopped with error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("standalone daemon did not stop")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		select {
		case err := <-serveErrors:
			t.Fatalf("standalone daemon stopped before serving: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for standalone daemon socket")
		}
		time.Sleep(10 * time.Millisecond)
	}

	client := wakeclient.NewForSource(socket, wakeprotocol.SourceContinuation)
	client.BuildVersion = "integration-helper"
	launchHome := t.TempDir()
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout: &output, Stderr: &stderr, Getwd: func() (string, error) { return feature, nil },
		LookupEnv: func(string) (string, bool) { return "", false }, Runner: continuationRunner{}, GOOS: "darwin",
		UserHomeDir: func() (string, error) { return launchHome, nil }, Getuid: func() int { return os.Getuid() },
		LaunchctlRun:        func(context.Context, string, ...string) error { return nil },
		NewContinuationWake: func() (WakeCoordinator, error) { return client, nil },
	})
	args := []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "continue", "--at", due.Format(time.RFC3339), "--wake"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("continue --wake exit=%d stderr=%s", exit, stderr.String())
	}
	afterCreate, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var schedule model.Schedule
	for _, record := range afterCreate.Features[paths.RepositoryID+":bridge"].Schedules {
		schedule = record
	}
	if schedule.ID == "" || schedule.WakeSource != string(wakeprotocol.SourceContinuation) || schedule.WakePurpose != string(wakeprotocol.PurposeContinuation) || schedule.WakeDaemonBuild != "integration-daemon" || schedule.WakeVerifiedAt.IsZero() {
		t.Fatalf("schedule did not retain standalone wake proof: %#v", schedule)
	}
	if events, _ := power.Events(context.Background()); len(events) != 2 {
		t.Fatalf("wake registration events=%#v, want foreign plus exact Herdr event", events)
	}
	output.Reset()
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "schedule", "cancel", schedule.ID}); exit != 0 {
		t.Fatalf("schedule cancel exit=%d stderr=%s", exit, stderr.String())
	}
	afterCancel, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	canceled := afterCancel.Features[paths.RepositoryID+":bridge"].Schedules[schedule.ID]
	if canceled.State != model.ScheduleCanceled || canceled.WakeWithdrawnAt.IsZero() || canceled.WakeRollbackState != string(wakeprotocol.ResultSuccess) || canceled.WakeUncertain {
		t.Fatalf("cancellation did not retain exact standalone withdrawal proof: %#v", canceled)
	}
	events, _ := power.Events(context.Background())
	if len(events) != 1 || events[0].Owner != "com.example.foreign" || !events[0].At.Equal(due) {
		t.Fatalf("standalone cancellation touched a foreign wake event: %#v", events)
	}
	if filepath.Clean(repo) == filepath.Clean(feature) {
		t.Fatal("fixture did not create a linked feature worktree")
	}
}
