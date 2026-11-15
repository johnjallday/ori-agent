package usagelimits

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeCodex(t *testing.T, ending string) string {
	t.Helper()
	return fakeCodexAfterStartup(t, "", ending)
}

func fakeCodexAfterStartup(t *testing.T, startup, ending string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI")
	}
	dir := t.TempDir()
	pid := filepath.Join(dir, "pid")
	t.Setenv("PATH", dir)
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("FAKE_PID_FILE", pid)
	script := `#!/bin/sh
[ "$1" = app-server ] || exit 1
` + startup + `
printf '%s' "$$" > "$FAKE_PID_FILE"
read -r init
case "$init" in *'"name":"ori"'*'"title":"Ori"'*) ;; *) exit 1;; esac
printf '%s\n' '{"id":1,"result":{"userAgent":"fixture"}}'
read -r initialized
[ "$initialized" = '{"method":"initialized"}' ] || exit 1
read -r request
case "$request" in *'"method":"account/rateLimits/read","params":null'*) ;; *) exit 1;; esac
` + ending
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitForCLIStarted(t *testing.T, pidFile string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(pidFile); err == nil && len(data) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fake CLI did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertCLIStopped(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if exec.Command("/bin/kill", "-0", strings.TrimSpace(string(data))).Run() == nil {
		t.Fatal("app-server process survived the read")
	}
}

func TestCodexAppServerHandshakeAndMapping(t *testing.T) {
	result := strings.TrimSpace(string(fixture(t, "codex-appserver-example.json")))
	pid := fakeCodex(t, `printf '%s\n' '{"method":"notification"}' '{"id":2,"result":`+result+`}'
read -r hold
`)
	p, err := ReadCodexAppServer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != SourceCodexAppServer || p.Plan != "prolite" || p.Windows[0].ID != "weekly" || p.Credits == nil || p.Credits.Balance != "0" || p.FetchedAt.IsZero() {
		t.Fatalf("provider=%+v", p)
	}
	assertCLIStopped(t, pid)
}

func TestCodexAppServerCancellationKillsChild(t *testing.T) {
	pid := fakeCodex(t, "read -r hold\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := ReadCodexAppServer(ctx); result <- err }()
	waitForCLIStarted(t, pid)
	start := time.Now()
	cancel()
	if err := <-result; err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation was not bounded")
	}
	assertCLIStopped(t, pid)
}

func TestCodexAppServerTimeoutKillsChild(t *testing.T) {
	// Longer than the read deadline: this deterministically catches starting
	// that deadline before the child has had a chance to record its PID.
	pid := fakeCodexAfterStartup(t, "/bin/sleep 1.1\n", "read -r hold\n")
	ctx, cancelRead := context.WithCancel(t.Context())
	result, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		_, err := ReadCodexAppServer(ctx)
		result <- err
	}()
	defer func() { cancelRead(); <-done }()
	waitForCLIStarted(t, pid)

	// Exercise expiry against a running child, not the scheduler's ability to
	// start a process within one second on a loaded race-test host.
	deadline, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stop := context.AfterFunc(deadline, cancelRead)
	defer stop()
	start := time.Now()
	if err := <-result; err == nil {
		t.Fatal("expected timeout")
	}
	if deadline.Err() != context.DeadlineExceeded {
		t.Fatal("app-server returned before the deadline")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatal("timeout exceeded bound")
	}
	assertCLIStopped(t, pid)
}

func TestCodexAppServerAuthError(t *testing.T) {
	pid := fakeCodex(t, `printf '%s\n' '{"id":2,"error":{"code":-32000,"message":"Not authenticated: private@example.test"}}'
read -r hold
`)
	p, err := ReadCodexAppServer(context.Background())
	if err != nil || p.State != StateNotSignedIn || p.Detail != "Run `codex login`" {
		t.Fatalf("got %+v,%v", p, err)
	}
	assertCLIStopped(t, pid)
}

func TestCodexAppServerMalformedAndNoLimits(t *testing.T) {
	for _, body := range []string{`{}`, `{"rateLimits":null}`, `{"rateLimits":{"primary":{"windowDurationMins":-1}}}`} {
		if _, err := parseCodexLiveResult([]byte(body), time.Now()); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	p, err := parseCodexLiveResult([]byte(`{"rateLimits":{"primary":null,"secondary":null}}`), time.Now())
	if err != nil || p.State != StateNoLimits {
		t.Fatalf("got %+v,%v", p, err)
	}
}
