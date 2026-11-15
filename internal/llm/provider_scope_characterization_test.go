package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests record the provider characterization performed against Codex CLI
// 0.147.0 and Claude Code 2.1.233 on macOS before runtime grants were designed.
// They intentionally inspect documented CLI help rather than assuming a flag:
//
//   - Codex exposes workspace-write plus --add-dir, and a real home-directory
//     fixture proved workspace + one added root writable while a sibling was
//     denied. (Fixtures under /tmp are invalid evidence because Codex's sandbox
//     intentionally allows the system temporary directory.)
//   - Claude exposes --add-dir and acceptEdits. A real fixture run with
//     acceptEdits wrote through Write to cwd + one --add-dir root and denied a
//     sibling. bypassPermissions is therefore unnecessary and cannot be used as
//     evidence of confinement.
//   - Neither CLI exposes a loopback-only shell-network flag. Runtime scope must
//     keep general shell network disabled and reach loopback only through a
//     compiled, capability-owned MCP/helper operation.
func TestInstalledCLIsExposeCharacterizedNarrowFilesystemMechanisms(t *testing.T) {
	cases := []struct {
		binary string
		args   []string
		want   []string
	}{
		{binary: "codex", args: []string{"exec", "--help"}, want: []string{"--add-dir", "workspace-write"}},
		{binary: "claude", args: []string{"--help"}, want: []string{"--add-dir", "acceptEdits"}},
	}
	for _, tc := range cases {
		t.Run(tc.binary, func(t *testing.T) {
			path, err := exec.LookPath(tc.binary)
			if err != nil {
				t.Skipf("%s is not installed", tc.binary)
			}
			// Help describes the binary, not the user's login/configuration.
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			output, err := runCLIHelpProbe(ctx, path, t.TempDir(), tc.args...)
			if err != nil {
				t.Fatalf("%s help: %v: %s", tc.binary, err, output)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(output), want) {
					t.Errorf("%s does not advertise %q", tc.binary, want)
				}
			}
		})
	}
}

// installedCLIHelpCommand keeps a metadata-only probe away from personal CLI
// homes and provider credentials. A deadline also bounds a broken installation;
// WaitDelay bounds output draining if a wrapper leaves a pipe open after exit.
func installedCLIHelpCommand(ctx context.Context, path, home string, args ...string) *exec.Cmd {
	// #nosec G204 -- path is exec.LookPath's installed CLI; callers supply fixed help arguments.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = home
	cmd.WaitDelay = time.Second
	cmd.Env = []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"CODEX_HOME=" + home, "CLAUDE_CONFIG_DIR=" + home,
		"XDG_CONFIG_HOME=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home,
		"TMPDIR=" + home, "TEMP=" + home, "TMP=" + home,
	}
	// Keep only process-launch essentials, not API keys, login tokens, config
	// overrides or proxy credentials inherited by the test runner.
	for _, key := range []string{"PATH", "SystemRoot", "SYSTEMROOT", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	return cmd
}

// Keep one command policy for both the real help and both subprocess suites.
func runCLIHelpProbe(ctx context.Context, path, home string, args ...string) ([]byte, error) {
	output, err := installedCLIHelpCommand(ctx, path, home, args...).CombinedOutput()
	if ctx.Err() != nil {
		return output, fmt.Errorf("CLI help deadline: %w", ctx.Err())
	}
	return output, err
}

func TestCLIHelpProbeIsIsolatedAndBounded(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORI_CLI_HELP_ENV_SENTINEL", "must-not-reach-child")
	for _, mode := range []string{"help", "hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			output, err := runCLIHelpProbe(ctx, path, t.TempDir(), "-test.run=^TestCLIHelpProbeProcess$", "-test.v", "--", mode)
			if mode == "hang" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("hung probe did not expire: %v", err)
				}
				return
			}
			if err != nil || !strings.Contains(string(output), "isolated help") {
				t.Fatalf("isolated probe: %v: %s", err, output)
			}
		})
	}
}

func TestCLIHelpProbeProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	if os.Args[len(os.Args)-1] == "hang" {
		time.Sleep(time.Minute)
		return
	}
	for _, key := range []string{"ORI_CLI_HELP_ENV_SENTINEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if os.Getenv(key) != "" {
			t.Fatalf("probe inherited %s", key)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Stat(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		dir, err := os.Stat(os.Getenv(key))
		if err != nil || !os.SameFile(wd, dir) {
			t.Fatalf("probe did not isolate %s", key)
		}
	}
	t.Log("isolated help")
}

// This starts red by design: the pre-feature Claude native posture used
// bypassPermissions. The installed CLI characterization above proves
// acceptEdits + --add-dir can enforce the approved roots, so implementation
// must add a distinct capability scope rather than bless this broad posture.
func TestClaudeRuntimeGrantMustNotRelyOnBypassPermissions(t *testing.T) {
	nat := &claudeNativeMCP{WorkspaceDir: "/workspace", AdditionalWritableRoots: []string{"/runner"}, Scoped: true}
	args, err := buildClaudeArgs("haiku", "", "scope test", nil, nat)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "bypassPermissions") {
		t.Fatal("runtime capability scope cannot use bypassPermissions as proof of confinement")
	}
}
