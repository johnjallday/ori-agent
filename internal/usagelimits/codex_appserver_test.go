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
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI")
	}
	dir := t.TempDir()
	pid := filepath.Join(dir, "pid")
	t.Setenv("PATH", dir)
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("FAKE_PID_FILE", pid)
	t.Chdir(dir)
	// Invoke the system shell as codex so it reads the app-server argument
	// as a script, rather than launching a newly generated executable.
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	script := `read -r init
case "$init" in *'"name":"ori"'*'"title":"Ori"'*) ;; *) exit 1;; esac
printf '%s\n' '{"id":1,"result":{"userAgent":"fixture"}}'
read -r initialized
[ "$initialized" = '{"method":"initialized"}' ] || exit 1
read -r request
case "$request" in *'"method":"account/rateLimits/read","params":null'*) ;; *) exit 1;; esac
printf '%s\n' "$$" > "$FAKE_PID_FILE"
` + ending
	if err := os.WriteFile(filepath.Join(dir, "app-server"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return pid
}

// startBlockedCodex waits for the fake CLI to accept the request before the
// caller triggers cancellation. The PID file doubles as the readiness signal.
func startBlockedCodex(t *testing.T) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	pid := fakeCodex(t, "read -r hold\n")
	ctx, cancel := context.WithCancel(t.Context())
	result, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		_, err := ReadCodexAppServer(ctx)
		result <- err
	}()
	t.Cleanup(func() { cancel(); <-done })

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case err := <-result:
			t.Fatalf("app-server returned before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("fake CLI did not accept the rate-limits request")
		case <-poll.C:
			if data, err := os.ReadFile(pid); err == nil && len(data) > 0 {
				return pid, cancel, result
			}
		}
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
	pid, cancel, result := startBlockedCodex(t)
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
	pid, cancelRead, result := startBlockedCodex(t)

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
