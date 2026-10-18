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
	script := `#!/bin/sh
[ "$1" = app-server ] || exit 1
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
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(pid); err == nil && len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake CLI did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
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
	pid := fakeCodex(t, "read -r hold\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := ReadCodexAppServer(ctx); err == nil {
		t.Fatal("expected timeout")
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
