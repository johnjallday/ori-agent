package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use the test executable rather than requiring an installed provider or shell.
func TestInstalledCLIHelpProcess(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--help-fixture" || i+1 >= len(os.Args) {
			continue
		}
		switch os.Args[i+1] {
		case "environment":
			cwd, err := os.Getwd()
			if err != nil {
				os.Exit(2)
			}
			if err := json.NewEncoder(os.Stdout).Encode(struct {
				Directory   string
				Environment []string
			}{cwd, os.Environ()}); err != nil {
				os.Exit(2)
			}
		case "hang":
			// The deadline must stop this process, not wait for its own exit.
			time.Sleep(time.Minute)
		case "failure":
			if _, err := fmt.Fprintln(os.Stderr, "fixture help failed"); err != nil {
				os.Exit(2)
			}
			os.Exit(3)
		default:
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func helpFixtureCommand(t *testing.T, ctx context.Context, home, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return installedCLIHelpCommand(ctx, executable, home,
		"-test.run=^TestInstalledCLIHelpProcess$", "--", "--help-fixture", mode)
}

func TestInstalledCLIHelpUsesDisposableConfigurationWithoutCredentials(t *testing.T) {
	for _, key := range []string{
		"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
		"HTTP_PROXY", "HTTPS_PROXY",
	} {
		t.Setenv(key, "personal-config-or-secret-must-not-reach-help")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := helpFixtureCommand(t, ctx, home, "environment")
	if cmd.WaitDelay != time.Second {
		t.Fatalf("unbounded pipe draining: WaitDelay = %v", cmd.WaitDelay)
	}
	// The cover-instrumented test executable may warn on stderr when its
	// subprocess has no GOCOVERDIR. Only stdout is the fixture's JSON protocol.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("fixture help: %v: %s", err, stderr.String())
	}
	var got struct {
		Directory   string
		Environment []string
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if got.Directory != home {
		t.Fatalf("help ran in %q, want disposable %q", got.Directory, home)
	}
	environment := strings.Join(got.Environment, "\n")
	if strings.Contains(environment, "personal-config-or-secret-must-not-reach-help") {
		t.Fatal("help inherited personal configuration or credentials")
	}
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		found := false
		for _, entry := range got.Environment {
			if entry == key+"="+home {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not use the disposable home", key)
		}
	}
}

func TestInstalledCLIHelpDeadlineStopsHungProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	cmd := helpFixtureCommand(t, ctx, t.TempDir(), "hang")
	started := time.Now()
	_, err := cmd.CombinedOutput()
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("hung help must fail on its deadline: command=%v context=%v", err, ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("deadline did not bound help: %v", elapsed)
	}
}

func TestInstalledCLIHelpPreservesFailedProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := helpFixtureCommand(t, ctx, t.TempDir(), "failure").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(string(output), "fixture help failed") {
		t.Fatalf("help failure was lost: %v: %s", err, output)
	}
}
