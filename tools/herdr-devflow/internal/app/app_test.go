package app

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
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/cleanup"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeinstall"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// devflowConfigFixture is the checked-in bridge configuration every repository
// fixture writes, so a change to the required keys is made in one place.
const devflowConfigFixture = `
[bridge]
enabled = true
min_herdr_version = "0.7.5"
source_id = "ori.devflow"
[primary]
role = "builder"
kind = "claude"
[roles]
default_kind = "claude"
[bootstrap]
timeout_seconds = 30
[scheduler]
retry_window = "15m"
[metadata]
enabled = true
[status]
watch_poll_interval = "2s"
`

type fakeWakeLifecycle struct {
	prepared       wakeinstall.PreparedInstall
	status         wakeinstall.Status
	diagnostics    []wakeinstall.Diagnostic
	prepareCalls   int
	installCalls   int
	uninstallCalls int
}

func (f *fakeWakeLifecycle) PrepareInstall(
	context.Context,
	string,
	int,
) (wakeinstall.PreparedInstall, error) {
	f.prepareCalls++
	return f.prepared, nil
}

func (f *fakeWakeLifecycle) Install(
	_ context.Context,
	_ wakeinstall.PreparedInstall,
) (wakeinstall.Status, error) {
	f.installCalls++
	return f.status, nil
}

func (f *fakeWakeLifecycle) Status(context.Context) (wakeinstall.Status, error) {
	return f.status, nil
}

func (f *fakeWakeLifecycle) Doctor(context.Context) ([]wakeinstall.Diagnostic, error) {
	return append([]wakeinstall.Diagnostic(nil), f.diagnostics...), nil
}

func (f *fakeWakeLifecycle) Uninstall(context.Context, int) (wakeinstall.Status, error) {
	f.uninstallCalls++
	removed := f.status
	removed.Installed = false
	removed.Running = false
	removed.Compatible = false
	removed.Detail = "standalone Herdr wake service is not installed"
	return removed, nil
}

func TestWakeLifecycleCommandsRequireConfirmationAndExposeFixedBoundary(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	lifecycle := &fakeWakeLifecycle{
		prepared: wakeinstall.PreparedInstall{
			ArtifactPath:   filepath.Join(t.TempDir(), "herdr-wake"),
			ArtifactDigest: strings.Repeat("a", 64),
			BuildVersion:   "test-build",
			AllowedUID:     501,
		},
		status: wakeinstall.Status{
			Supported: true, Installed: true, Running: true, Compatible: true,
			AllowedUID: 501, ProtocolVersion: 1, StateVersion: 1,
			DaemonBuild: "test-build", Detail: "standalone Herdr wake service is healthy",
		},
		diagnostics: []wakeinstall.Diagnostic{{
			Name: "health", Status: "PASS", Detail: "protocol 1",
		}},
	}

	t.Run("non-interactive install stages but never elevates without yes", func(t *testing.T) {
		var output, stderr bytes.Buffer
		application := New(Dependencies{
			Stdout: &output, Stderr: &stderr, Getwd: func() (string, error) { return repo, nil },
			GOOS: "darwin", Getuid: func() int { return 501 },
			IsInteractive: func() bool { return false }, WakeLifecycle: lifecycle,
		})
		exit := application.Run(context.Background(), []string{"wake", "install"})
		if exit != 2 || lifecycle.installCalls != 0 {
			t.Fatalf("exit=%d install calls=%d stderr=%q", exit, lifecycle.installCalls, stderr.String())
		}
		if !strings.Contains(output.String(), "/Library/PrivilegedHelperTools/com.ori.herdr-wake") ||
			!strings.Contains(output.String(), "/usr/bin/sudo -k") ||
			!strings.Contains(output.String(), "No password") {
			t.Fatalf("install preview did not expose the fixed boundary: %q", output.String())
		}
	})

	t.Run("explicit yes installs and status doctor are readable", func(t *testing.T) {
		var output, stderr bytes.Buffer
		application := New(Dependencies{
			Stdout: &output, Stderr: &stderr, Getwd: func() (string, error) { return repo, nil },
			GOOS: "darwin", Getuid: func() int { return 501 },
			IsInteractive: func() bool { return false }, WakeLifecycle: lifecycle,
		})
		if exit := application.Run(context.Background(), []string{"wake", "install", "--yes"}); exit != 0 {
			t.Fatalf("install exit=%d stderr=%q", exit, stderr.String())
		}
		if lifecycle.installCalls != 1 {
			t.Fatalf("install calls = %d, want 1", lifecycle.installCalls)
		}
		output.Reset()
		if exit := application.Run(context.Background(), []string{"wake", "status"}); exit != 0 ||
			!strings.Contains(output.String(), "allowed_uid=501") {
			t.Fatalf("status exit=%d output=%q", exit, output.String())
		}
		output.Reset()
		if exit := application.Run(context.Background(), []string{"wake", "doctor"}); exit != 0 ||
			!strings.Contains(output.String(), "[PASS] health") {
			t.Fatalf("doctor exit=%d output=%q", exit, output.String())
		}
	})

	t.Run("uninstall requires its own confirmation", func(t *testing.T) {
		var output, stderr bytes.Buffer
		application := New(Dependencies{
			Stdout: &output, Stderr: &stderr, GOOS: "darwin",
			Getuid: func() int { return 501 }, IsInteractive: func() bool { return false },
			WakeLifecycle: lifecycle,
		})
		exit := application.Run(context.Background(), []string{"wake", "uninstall"})
		if exit != 2 || lifecycle.uninstallCalls != 0 {
			t.Fatalf("exit=%d uninstall calls=%d stderr=%q", exit, lifecycle.uninstallCalls, stderr.String())
		}
		if exit := application.Run(context.Background(), []string{"wake", "uninstall", "--yes"}); exit != 0 {
			t.Fatalf("confirmed uninstall exit=%d stderr=%q", exit, stderr.String())
		}
		if lifecycle.uninstallCalls != 1 {
			t.Fatalf("uninstall calls = %d, want 1", lifecycle.uninstallCalls)
		}
	})
}

func TestWakeJSONAndUnsupportedPlatformAreStableAndSideEffectFree(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	lifecycle := &fakeWakeLifecycle{
		prepared: wakeinstall.PreparedInstall{
			ArtifactPath:   filepath.Join(t.TempDir(), "herdr-wake"),
			ArtifactDigest: strings.Repeat("b", 64), BuildVersion: "test", AllowedUID: 501,
		},
		status: wakeinstall.Status{Supported: true, Installed: true, Running: true, Compatible: true},
	}
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout: &output, Stderr: &stderr, Getwd: func() (string, error) { return repo, nil },
		GOOS: "darwin", Getuid: func() int { return 501 },
		IsInteractive: func() bool { return false }, WakeLifecycle: lifecycle,
	})
	if exit := application.Run(context.Background(), []string{"--json", "wake", "install"}); exit != 2 {
		t.Fatalf("JSON confirmation exit = %d", exit)
	}
	var payload map[string]any
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("JSON output is not one document: %v\n%s", err, output.String())
	}
	if payload["status"] != "confirmation_required" || lifecycle.installCalls != 0 {
		t.Fatalf("payload=%v install calls=%d", payload, lifecycle.installCalls)
	}

	output.Reset()
	unsupported := New(Dependencies{
		Stdout: &output, Stderr: &stderr, GOOS: "linux", Getuid: func() int { return 501 },
		WakeLifecycle: lifecycle,
	})
	if exit := unsupported.Run(context.Background(), []string{"wake", "install", "--yes"}); exit != 1 {
		t.Fatalf("unsupported exit = %d", exit)
	}
	if lifecycle.prepareCalls != 1 {
		t.Fatalf("unsupported host staged an artifact; prepare calls=%d", lifecycle.prepareCalls)
	}
}

type setupRunner struct {
	mu         sync.Mutex
	calls      []herdr.Command
	pluginRoot string
}

func (r *setupRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, command)
	key := strings.Join(command.Args, " ")
	switch {
	case key == "--version":
		return herdr.CommandResult{Stdout: []byte("herdr 0.7.5\n")}, nil
	case key == "api schema --json":
		return herdr.CommandResult{Stdout: []byte(schemaFixture())}, nil
	case key == "plugin list --plugin ori.devflow --json":
		if r.pluginRoot == "" {
			return herdr.CommandResult{Stdout: []byte(`{"result":{"plugins":[]}}`)}, nil
		}
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"plugins":[{"plugin_id":"ori.devflow","plugin_root":%q,"enabled":true}]}}`, r.pluginRoot))}, nil
	case strings.HasPrefix(key, "plugin link --enabled "):
		r.pluginRoot = command.Args[len(command.Args)-1]
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"plugin":{"plugin_id":"ori.devflow","plugin_root":%q,"enabled":true}}}`, r.pluginRoot))}, nil
	case key == "plugin action invoke ori.devflow.refresh":
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"plugin_action_invoke"}}`)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected Herdr command: %s", key)
	}
}

func schemaFixture() string {
	return `{"protocol":17,"schema_version":1,"requests":[
		{"method":{"const":"ping"}},
		{"method":{"const":"plugin.link"}},
		{"method":{"const":"plugin.enable"}},
		{"method":{"const":"plugin.list"}},
		{"method":{"const":"plugin.action.invoke"}},
		{"method":{"const":"session.snapshot"}},
		{"method":{"const":"worktree.open"}},
		{"method":{"const":"workspace.close"}},
		{"method":{"const":"workspace.list"}},
		{"method":{"const":"tab.create"}},
		{"method":{"const":"tab.close"}},
		{"method":{"const":"tab.list"}},
		{"method":{"const":"tab.get"}},
		{"method":{"const":"pane.split"}},
		{"method":{"const":"pane.get"}},
		{"method":{"const":"pane.process_info"}},
		{"method":{"const":"agent.start"}},
		{"method":{"const":"agent.list"}},
		{"method":{"const":"agent.get"}},
		{"method":{"const":"agent.prompt"}},
		{"method":{"const":"agent.rename"}},
		{"method":{"const":"agent.focus"}},
		{"method":{"const":"agent.read"}},
		{"method":{"const":"workspace.report_metadata"}},
		{"method":{"const":"pane.report_metadata"}},
		{"method":{"const":"agent.view.set"}},
		{"method":{"const":"agent.view.clear"}},
		{"method":{"const":"events.subscribe"}}
	]}`
}

func TestSetupBuildsStableRuntimeLinksOnceAndLeavesGlobalConfigUntouched(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSetupFixture(t, repo)
	home := filepath.Join(t.TempDir(), "ori-devflow-home")
	globalConfig := filepath.Join(t.TempDir(), "global-config.toml")
	originalGlobalConfig := []byte("[external]\nkeep = true\n")
	if err := os.WriteFile(globalConfig, originalGlobalConfig, 0600); err != nil {
		t.Fatal(err)
	}

	runner := &setupRunner{}
	wakeLifecycle := &fakeWakeLifecycle{status: wakeinstall.Status{
		Supported: true,
		Detail:    "standalone Herdr wake service is not installed",
	}}
	var output, errors bytes.Buffer
	builds := 0
	launchHome := t.TempDir()
	application := New(Dependencies{
		Stdout:    &output,
		Stderr:    &errors,
		Getwd:     func() (string, error) { return repo, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
		LookPath:  func(string) (string, error) { return "/test/claude", nil },
		Runner:    runner,
		BuildHelper: func(_ context.Context, _ string, destination string) error {
			builds++
			return os.WriteFile(destination, []byte("helper"), 0755)
		},
		GOOS:        "darwin",
		UserHomeDir: func() (string, error) { return launchHome, nil },
		Getuid:      func() int { return 501 },
		LaunchctlRun: func(context.Context, string, ...string) error {
			return nil
		},
		WakeLifecycle: wakeLifecycle,
	})
	args := []string{"--repo-root", repo, "--home", home, "setup"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("first setup exit = %d; stderr=%s", exit, errors.String())
	}
	if _, err := os.Stat(filepath.Join(home, "bin", "herdr-devflow")); err != nil {
		t.Fatalf("stable helper missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugin", "herdr-plugin.toml")); err != nil {
		t.Fatalf("stable plugin manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "state.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(launchHome, "Library", "LaunchAgents", "com.ori.herdr-devflow.plist")); err != nil {
		t.Fatalf("LaunchAgent plist missing: %v", err)
	}
	if !strings.Contains(output.String(), "Ori Herdr Devflow: ready") {
		t.Fatalf("setup output = %q", output.String())
	}
	if !strings.Contains(output.String(), "wt herd wake install") ||
		!strings.Contains(output.String(), "--stay-awake") {
		t.Fatalf("setup did not report explicit wake alternatives: %q", output.String())
	}
	if wakeLifecycle.prepareCalls != 0 || wakeLifecycle.installCalls != 0 {
		t.Fatalf(
			"setup crossed the wake install boundary: prepare=%d install=%d",
			wakeLifecycle.prepareCalls, wakeLifecycle.installCalls,
		)
	}
	if got, err := os.ReadFile(globalConfig); err != nil || !bytes.Equal(got, originalGlobalConfig) {
		t.Fatalf("global config changed: %q, %v", got, err)
	}
	if containsIntegrationCommand(runner.calls) {
		t.Fatalf("setup attempted a forbidden integration mutation: %#v", runner.calls)
	}
	if !containsHerdrCommand(runner.calls, "plugin action invoke ori.devflow.refresh") {
		t.Fatalf("setup did not invoke the installed plugin refresh action: %#v", runner.calls)
	}

	output.Reset()
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("second setup exit = %d; stderr=%s", exit, errors.String())
	}
	if builds != 2 {
		t.Fatalf("stable helper should rebuild safely on refresh; builds = %d, want 2", builds)
	}
	if linkCalls(runner.calls) != 1 {
		t.Fatalf("plugin link calls = %d, want 1 after stable relink check", linkCalls(runner.calls))
	}
}

func TestSetupDisabledDoesNotBuildOrCallHerdr(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSetupFixture(t, repo)
	configPath := filepath.Join(repo, ".herdr", "devflow.toml")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(string(contents), "enabled = true", "enabled = false", 1))
	if err := os.WriteFile(configPath, contents, 0600); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{}
	built := false
	application := New(Dependencies{
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
		Getwd:       func() (string, error) { return repo, nil },
		LookupEnv:   func(string) (string, bool) { return "", false },
		Runner:      runner,
		BuildHelper: func(context.Context, string, string) error { built = true; return nil },
	})
	if exit := application.Run(context.Background(), []string{"--repo-root", repo, "--home", t.TempDir(), "setup"}); exit != 0 {
		t.Fatalf("disabled setup exit = %d", exit)
	}
	if built || len(runner.calls) != 0 {
		t.Fatalf("disabled setup mutated state: built=%v calls=%#v", built, runner.calls)
	}
}

func TestDisabledHandoffIsANoOpAfterArgumentValidation(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeSetupFixture(t, repo)
	configPath := filepath.Join(repo, ".herdr", "devflow.toml")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Replace(string(contents), "enabled = true", "enabled = false", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &setupRunner{}
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:    &output,
		Stderr:    &stderr,
		Getwd:     func() (string, error) { return repo, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
		Runner:    runner,
	})
	exit := application.Run(context.Background(), []string{"--repo-root", repo, "--home", t.TempDir(), "handoff", "--feature", "bridge", "--worktree", filepath.Join(repo, "bridge")})
	if exit != 0 || !strings.Contains(output.String(), "disabled") || len(runner.calls) != 0 {
		t.Fatalf("disabled handoff exit=%d output=%q stderr=%q calls=%#v", exit, output.String(), stderr.String(), runner.calls)
	}
}

func TestParseHandoffArgsRequiresAnExplicitInitialTargetAndGatesResend(t *testing.T) {
	t.Parallel()
	parsed, err := parseHandoffArgs([]string{"--feature", "bridge", "--worktree", "/tmp/bridge", "--branch", "feature/bridge", "--kind", "codex"}, false)
	if err != nil || parsed.feature != "bridge" || parsed.worktree != "/tmp/bridge" || parsed.branch != "feature/bridge" || parsed.kind != "codex" {
		t.Fatalf("parseHandoffArgs() = %#v, %v", parsed, err)
	}
	if _, err := parseHandoffArgs([]string{"--feature", "bridge"}, false); err == nil {
		t.Fatal("initial handoff accepted a missing worktree")
	}
	parsed, err = parseHandoffArgs([]string{"--resend"}, true)
	if err != nil || !parsed.resend {
		t.Fatalf("retry parse = %#v, %v", parsed, err)
	}
	if _, err := parseHandoffArgs([]string{"--resend"}, false); err == nil {
		t.Fatal("initial handoff accepted --resend")
	}
	if _, err := parseHandoffArgs([]string{"--kind", "codex"}, true); err == nil {
		t.Fatal("retry accepted a primary-kind override")
	}
	if _, err := parseHandoffArgs([]string{"--feature", "bridge", "--worktree", "/tmp/bridge", "--kind", "unknown"}, false); err == nil {
		t.Fatal("handoff accepted an unsupported primary kind")
	}
}

func TestParseScopedAgentCommandsKeepsContextAndTargetExplicit(t *testing.T) {
	t.Parallel()
	add, err := parseAddAgentArgs([]string{"reviewer", "--kind", "codex", "--feature", "bridge"})
	if err != nil || add.Role != "reviewer" || add.Kind != "codex" || add.Context.FeatureName != "bridge" {
		t.Fatalf("parseAddAgentArgs() = %#v, %v", add, err)
	}
	prompt, err := parsePromptAgentArgs([]string{"reviewer", "Please", "inspect", "this", "--target", "w1:p2", "--worktree", "/tmp/bridge"})
	if err != nil || prompt.Role != "reviewer" || prompt.Target != "w1:p2" || prompt.Text != "Please inspect this" || prompt.Context.WorktreePath != "/tmp/bridge" {
		t.Fatalf("parsePromptAgentArgs() = %#v, %v", prompt, err)
	}
	read, lines, err := parseTargetAgentArgs([]string{"--target", "ori-bridge-reviewer", "--lines", "240", "reviewer"}, "read", true)
	if err != nil || read.Role != "reviewer" || read.Target != "ori-bridge-reviewer" || lines != 240 {
		t.Fatalf("parseTargetAgentArgs() = %#v, %d, %v", read, lines, err)
	}
	renamed, err := parseRenameAgentArgs([]string{"reviewer", "tester", "--feature", "bridge"})
	if err != nil || renamed.Role != "reviewer" || renamed.NewRole != "tester" || renamed.Context.FeatureName != "bridge" {
		t.Fatalf("parseRenameAgentArgs() = %#v, %v", renamed, err)
	}
	rebound, err := parseRebindAgentArgs([]string{"reviewer", "--target", "w1:p4"})
	if err != nil || rebound.Role != "reviewer" || rebound.Target != "w1:p4" {
		t.Fatalf("parseRebindAgentArgs() = %#v, %v", rebound, err)
	}
	if _, _, err := parseTargetAgentArgs([]string{"reviewer", "tester"}, "focus", false); err == nil {
		t.Fatal("focus parser accepted two roles")
	}
}

func TestParseOneTimeContinuationAndScheduleCommands(t *testing.T) {
	t.Parallel()
	continuation, err := parseContinueArgs([]string{"reviewer", "--at", "2026-07-24 09:30", "--prompt", "Resume safely", "--wake", "--feature", "bridge"})
	if err != nil || continuation.role != "reviewer" || continuation.at != "2026-07-24 09:30" || continuation.prompt != "Resume safely" || !continuation.wake || continuation.context.FeatureName != "bridge" {
		t.Fatalf("parseContinueArgs() = %#v, %v", continuation, err)
	}
	if _, err := parseContinueArgs([]string{"--at", "every hour"}); err != nil {
		t.Fatalf("parseContinueArgs() should leave recurrence rejection to timestamp validation: %v", err)
	}
	if _, err := parseContinueArgs([]string{"builder"}); err == nil {
		t.Fatal("parseContinueArgs accepted a missing --at")
	}
	list, err := parseScheduleArgs([]string{"list", "--worktree", "/tmp/bridge"})
	if err != nil || list.command != "list" || list.context.WorktreePath != "/tmp/bridge" {
		t.Fatalf("parseScheduleArgs(list) = %#v, %v", list, err)
	}
	show, err := parseScheduleArgs([]string{"show", "sch-123", "--feature", "bridge"})
	if err != nil || show.command != "show" || show.id != "sch-123" || show.context.FeatureName != "bridge" {
		t.Fatalf("parseScheduleArgs(show) = %#v, %v", show, err)
	}
	if _, err := parseScheduleArgs([]string{"cancel"}); err == nil {
		t.Fatal("parseScheduleArgs accepted cancel without an id")
	}
	status, err := parseStatusArgs([]string{"--current", "--watch", "--json", "--no-color"})
	if err != nil || !status.current || !status.watch || !status.json || !status.noColor {
		t.Fatalf("parseStatusArgs() = %#v, %v", status, err)
	}
	if _, err := parseStatusArgs([]string{"--current", "--feature", "bridge"}); err == nil {
		t.Fatal("parseStatusArgs accepted a conflicting current/feature filter")
	}
	cleanup, err := parseCleanupArgs([]string{"--worktree", "/tmp/bridge", "--override"})
	if err != nil || cleanup.worktree != "/tmp/bridge" || !cleanup.override {
		t.Fatalf("parseCleanupArgs() = %#v, %v", cleanup, err)
	}
	if _, err := parseCleanupArgs([]string{"--override"}); err == nil {
		t.Fatal("parseCleanupArgs accepted a missing worktree")
	}
}

type cleanupRunner struct {
	status    model.AgentStatus
	closeErr  error
	blockList bool
	calls     []string
}

func (r *cleanupRunner) Run(ctx context.Context, command herdr.Command) (herdr.CommandResult, error) {
	key := strings.Join(command.Args, " ")
	r.calls = append(r.calls, key)
	switch key {
	case "agent list":
		if r.blockList {
			<-ctx.Done()
			return herdr.CommandResult{}, ctx.Err()
		}
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"agents":[{"agent":"claude","name":"ori-repo-bridge-builder","agent_status":%q,"workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term-2","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-123"}}]}}`, r.status))}, nil
	case "workspace list":
		return herdr.CommandResult{Stdout: []byte(`{"result":{"workspaces":[{"workspace_id":"w1","label":"shared","tab_count":2}]}}`)}, nil
	case "tab list":
		return herdr.CommandResult{Stdout: []byte(`{"result":{"tabs":[{"tab_id":"w1:t1","workspace_id":"w1"},{"tab_id":"w1:t2","workspace_id":"w1"}]}}`)}, nil
	case "tab close w1:t2":
		if r.closeErr != nil {
			return herdr.CommandResult{}, r.closeErr
		}
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"ok"}}`)}, r.closeErr
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected cleanup Herdr command: %s", key)
	}
}

func TestCleanupFailsClosedWhenLiveAgentLookupExceedsDeadline(t *testing.T) {
	_, feature := createLinkedFeatureWorktree(t)
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2"}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature},
		WorkspaceID: "w1",
		TabID:       "w1:t2",
		Agents:      map[string]model.RoleAgent{"builder": agent},
		Schedules:   map[string]model.Schedule{},
	}
	if err := state.New(paths.StateDir).Save(bridgeState); err != nil {
		t.Fatal(err)
	}
	runner := &cleanupRunner{blockList: true}
	var output, stderr bytes.Buffer
	application := New(Dependencies{Stdout: &output, Stderr: &stderr, LookupEnv: func(string) (string, bool) { return "", false }, Runner: runner})
	application.cleanupTimeout = 5 * time.Millisecond
	started := time.Now()
	exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "cleanup", "--worktree", feature})
	if exit != cleanup.ExitNeedsOverride || time.Since(started) > time.Second {
		t.Fatalf("cleanup timeout exit=%d elapsed=%s stdout=%s stderr=%s", exit, time.Since(started), output.String(), stderr.String())
	}
	if containsHerdrCommandFromStrings(runner.calls, "workspace close w1") || !strings.Contains(output.String(), "cannot be verified") {
		t.Fatalf("timed out cleanup did not fail closed: calls=%#v output=%q", runner.calls, output.String())
	}
}

func TestCleanupPreflightClosesOnlySettledFeatureTabAndBlocksActiveAgents(t *testing.T) {
	_, feature := createLinkedFeatureWorktree(t)
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature},
		WorkspaceID: "w1",
		TabID:       "w1:t2",
		Agents:      map[string]model.RoleAgent{"builder": agent},
		Schedules:   map[string]model.Schedule{},
	}
	if err := state.New(paths.StateDir).Save(bridgeState); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		status    model.AgentStatus
		closeErr  error
		override  bool
		wantExit  int
		wantClose bool
		wantAudit bool
	}{
		{name: "idle closes only the feature tab", status: model.AgentIdle, wantExit: 0, wantClose: true},
		{name: "working blocks before close", status: model.AgentWorking, wantExit: cleanup.ExitBlocked},
		{name: "explicit override records orphan-risk audit without session data", status: model.AgentIdle, closeErr: errors.New("tab close unavailable"), override: true, wantExit: 0, wantClose: true, wantAudit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &cleanupRunner{status: test.status, closeErr: test.closeErr}
			var output, stderr bytes.Buffer
			application := New(Dependencies{Stdout: &output, Stderr: &stderr, LookupEnv: func(string) (string, bool) { return "", false }, Runner: runner})
			args := []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "cleanup", "--worktree", feature}
			if test.override {
				args = append(args, "--override")
			}
			exit := application.Run(context.Background(), args)
			if exit != test.wantExit {
				t.Fatalf("cleanup exit = %d, want %d; stdout=%s stderr=%s", exit, test.wantExit, output.String(), stderr.String())
			}
			closed := containsHerdrCommandFromStrings(runner.calls, "tab close w1:t2")
			if closed != test.wantClose {
				t.Fatalf("tab close=%v, want %v; calls=%#v", closed, test.wantClose, runner.calls)
			}
			// The call that could cascade must not appear at all.
			for _, call := range runner.calls {
				if strings.HasPrefix(call, "workspace close") {
					t.Fatalf("cleanup closed a Herdr workspace: %#v", runner.calls)
				}
			}
			for _, call := range runner.calls {
				if strings.HasPrefix(call, "worktree ") {
					t.Fatalf("cleanup must never ask Herdr to mutate a Git worktree: %#v", runner.calls)
				}
			}
			if !strings.Contains(output.String(), "Agent builder") {
				t.Fatalf("cleanup output did not identify scoped agent: %q", output.String())
			}
			if test.wantAudit {
				auditPath := filepath.Join(home, "logs", "events.jsonl")
				audit, err := os.ReadFile(auditPath)
				if err != nil || !strings.Contains(string(audit), `"operation":"cleanup"`) || strings.Contains(string(audit), "native-123") {
					t.Fatalf("cleanup audit = %q, err=%v", audit, err)
				}
				if info, err := os.Stat(auditPath); err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("cleanup audit permissions = %v, %v", info, err)
				}
			}
		})
	}
}

type dispatchRunner struct {
	mu          sync.Mutex
	promptCalls int
}

func (r *dispatchRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.Join(command.Args, " ")
	switch {
	case key == "agent list":
		return herdr.CommandResult{Stdout: []byte(`{"result":{"agents":[{"agent":"claude","name":"ori-repo-bridge-builder","agent_status":"idle","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term-2","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-123"}}]}}`)}, nil
	case strings.HasPrefix(key, "agent prompt ori-repo-bridge-builder "):
		r.promptCalls++
		return herdr.CommandResult{Stdout: []byte(`{"result":{"agent":{"agent":"claude","name":"ori-repo-bridge-builder","agent_status":"idle","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term-2","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-123"}}}}`)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected dispatcher Herdr command: %s", key)
	}
}

func TestDetachedDispatchUsesOnlyUserLocalStateAndDeliversOnce(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "runtime")
	now := time.Now().UTC()
	feature := model.Feature{RepositoryID: "repo-123", Name: "bridge", Path: "/tmp/bridge"}
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native}
	schedule := model.Schedule{ID: "sch-dispatch", FeaturePath: feature.Path, Role: agent.Role, AgentName: agent.Name, AgentKind: agent.Kind, WorkspaceID: agent.WorkspaceID, PaneID: agent.PaneID, TerminalID: agent.TerminalID, NativeSession: native, DueAt: now.Add(-time.Minute), RetryUntil: now.Add(10 * time.Minute), Prompt: "Resume safely.", State: model.SchedulePending, CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)}
	bridgeState := model.NewBridgeState()
	bridgeState.Features["repo-123:bridge"] = model.FeatureState{Feature: feature, WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": agent}, Schedules: map[string]model.Schedule{schedule.ID: schedule}}
	store := state.New(filepath.Join(home, "state"))
	if err := store.Save(bridgeState); err != nil {
		t.Fatal(err)
	}
	runner := &dispatchRunner{}
	var output, stderr bytes.Buffer
	application := New(Dependencies{Stdout: &output, Stderr: &stderr, LookupEnv: func(string) (string, bool) { return "", false }, Runner: runner})
	args := []string{"--home", home, "--herdr-bin", "fake-herdr", "--json", "dispatch"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("dispatch exit = %d; stderr=%s", exit, stderr.String())
	}
	if runner.promptCalls != 1 || !strings.Contains(output.String(), `"state": "delivered"`) {
		t.Fatalf("dispatch output=%s promptCalls=%d", output.String(), runner.promptCalls)
	}
	output.Reset()
	if exit := application.Run(context.Background(), args); exit != 0 || runner.promptCalls != 1 {
		t.Fatalf("second dispatch exit=%d promptCalls=%d stderr=%s", exit, runner.promptCalls, stderr.String())
	}
}

type continuationRunner struct{}

func (continuationRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	key := strings.Join(command.Args, " ")
	switch {
	case key == "--version":
		return herdr.CommandResult{Stdout: []byte("herdr 0.7.5\n")}, nil
	case key == "api schema --json":
		return herdr.CommandResult{Stdout: []byte(schemaFixture())}, nil
	case key == "agent list":
		return herdr.CommandResult{Stdout: []byte(`{"result":{"agents":[{"agent":"claude","name":"ori-repo-bridge-builder","agent_status":"idle","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term-2","agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-123"}}]}}`)}, nil
	case strings.HasPrefix(key, "workspace report-metadata "):
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"workspace_metadata"}}`)}, nil
	case strings.HasPrefix(key, "pane report-metadata "):
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"pane_metadata"}}`)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected continuation Herdr command: %s", key)
	}
}

type continuationWake struct {
	registeredID string
	registeredAt time.Time
	canceledID   string
	readiness    wakeclient.OwnerReadiness
}

func (w *continuationWake) RegisterCandidate(
	_ context.Context,
	id string,
	wakeAt time.Time,
	_ string,
) (wakeclient.Evidence, error) {
	w.registeredID = id
	w.registeredAt = wakeAt
	return wakeclient.Evidence{
		CandidateID: id, RequestedAt: wakeAt,
		ProtocolVersion: wakeprotocol.Version, DaemonBuild: "test-daemon",
		HelperBuild: "test-helper", Result: wakeprotocol.ResultSuccess, Code: wakeprotocol.CodeOK,
	}, nil
}

func (w *continuationWake) VerifyCandidate(
	_ context.Context,
	id string,
	wakeAt time.Time,
) (wakeclient.Evidence, error) {
	if id != w.registeredID || !wakeAt.Equal(w.registeredAt) {
		return wakeclient.Evidence{}, errors.New("wake identity mismatch")
	}
	return wakeclient.Evidence{
		CandidateID: id, RequestedAt: wakeAt, ProgrammedAt: wakeAt.Add(-time.Minute),
		VerifiedAt: time.Now().UTC(), ProtocolVersion: wakeprotocol.Version,
		DaemonBuild: "test-daemon", HelperBuild: "test-helper",
		Result: wakeprotocol.ResultSuccess, Code: wakeprotocol.CodeOK,
	}, nil
}

func (w *continuationWake) CancelCandidate(
	_ context.Context,
	id string,
) (wakeclient.Evidence, error) {
	w.canceledID = id
	return wakeclient.Evidence{
		CandidateID: id, ProtocolVersion: wakeprotocol.Version,
		DaemonBuild: "test-daemon", HelperBuild: "test-helper",
		Result: wakeprotocol.ResultSuccess, Code: wakeprotocol.CodeOK,
	}, nil
}

func (w *continuationWake) Owner() wakeclient.OwnerReadiness {
	return w.readiness
}

func TestContinueCreatesOneTimeScheduleAfterExactFeatureScopedResolution(t *testing.T) {
	repo, feature := createLinkedFeatureWorktree(t)
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
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
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native, Status: model.AgentIdle}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{Feature: model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature}, WorkspaceID: "w1", Agents: map[string]model.RoleAgent{"builder": agent}, Schedules: map[string]model.Schedule{}, Handoff: model.HandoffState{PrimaryRole: "builder", PrimaryAgentName: agent.Name}}
	store := state.New(paths.StateDir)
	if err := store.Save(bridgeState); err != nil {
		t.Fatal(err)
	}

	launchHome := t.TempDir()
	wake := &continuationWake{readiness: wakeclient.OwnerReadiness{Running: true, Ready: true}}
	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:      &output,
		Stderr:      &stderr,
		Getwd:       func() (string, error) { return feature, nil },
		LookupEnv:   func(string) (string, bool) { return "", false },
		Runner:      continuationRunner{},
		GOOS:        "darwin",
		UserHomeDir: func() (string, error) { return launchHome, nil },
		Getuid:      func() int { return 501 },
		LaunchctlRun: func(context.Context, string, ...string) error {
			return nil
		},
		NewContinuationWake: func() (WakeCoordinator, error) { return wake, nil },
	})
	due := time.Now().Add(time.Hour).Format(time.RFC3339)
	privatePrompt := "Continue after reading OPENAI_API_KEY=sk-not-a-real-secret and do not expose this prompt."
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "continue", "--at", due, "--prompt", privatePrompt, "--wake"}); exit != 0 {
		t.Fatalf("continue exit = %d; stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(output.String(), "Continuation preview (not saved yet):") || !strings.Contains(output.String(), "scheduled sch-") || !strings.Contains(output.String(), "standalone macOS wake verified") {
		t.Fatalf("continue output = %q", output.String())
	}
	stateAfter, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	schedules := stateAfter.Features[paths.RepositoryID+":bridge"].Schedules
	if len(schedules) != 1 {
		t.Fatalf("schedule count = %d, want 1", len(schedules))
	}
	scheduleID := ""
	for _, record := range schedules {
		scheduleID = record.ID
		if record.AgentName != agent.Name || record.State != model.SchedulePending ||
			record.Prompt != privatePrompt || !record.WakeRequired ||
			record.WakeVerifiedAt.IsZero() || record.WakeProtocol != wakeprotocol.Version ||
			record.WakeDaemonBuild != "test-daemon" ||
			record.WakePurpose != string(wakeprotocol.PurposeContinuation) {
			t.Fatalf("schedule = %#v", record)
		}
	}
	if wake.registeredID != scheduleID {
		t.Fatalf("registered wake = %q, want %q", wake.registeredID, scheduleID)
	}
	output.Reset()
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "schedule", "show", scheduleID}); exit != 0 {
		t.Fatalf("schedule show exit = %d; stderr=%s", exit, stderr.String())
	}
	if strings.Contains(output.String(), "This is a scheduled continuation") || !strings.Contains(output.String(), "stored continuation prompt") {
		t.Fatalf("schedule show leaked prompt or omitted safe summary: %q", output.String())
	}
	output.Reset()
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "schedule", "cancel", scheduleID}); exit != 0 {
		t.Fatalf("schedule cancel exit = %d; stderr=%s", exit, stderr.String())
	}
	stateAfter, err = store.Load()
	if err != nil || stateAfter.Features[paths.RepositoryID+":bridge"].Schedules[scheduleID].State != model.ScheduleCanceled {
		t.Fatalf("schedule cancel state = %#v, %v", stateAfter.Features[paths.RepositoryID+":bridge"].Schedules[scheduleID], err)
	}
	if wake.canceledID != scheduleID {
		t.Fatalf("canceled wake = %q, want %q", wake.canceledID, scheduleID)
	}
	if _, err := os.Stat(filepath.Join(launchHome, "Library", "LaunchAgents", "com.ori.herdr-devflow.plist")); err != nil {
		t.Fatalf("continuation did not install a stable LaunchAgent: %v", err)
	}
	audit, err := os.ReadFile(filepath.Join(home, "logs", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), "\"operation\":\"continue\"") || strings.Contains(string(audit), privatePrompt) || strings.Contains(string(audit), "OPENAI_API_KEY") || strings.Contains(string(audit), "sk-not-a-real-secret") {
		t.Fatalf("continuation audit exposed prompt data: %q", audit)
	}
	wake.readiness = wakeclient.OwnerReadiness{Running: true, Detail: "standalone Herdr wake service is not ready"}
	output.Reset()
	stderr.Reset()
	nextDue := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "continue", "--at", nextDue, "--wake"}); exit != 1 {
		t.Fatalf("wake-disabled continue exit = %d; output=%s stderr=%s", exit, output.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "wake_unavailable") || !strings.Contains(stderr.String(), "standalone") {
		t.Fatalf("wake-disabled error = %q", stderr.String())
	}
	stateAfter, err = store.Load()
	if err != nil || len(stateAfter.Features[paths.RepositoryID+":bridge"].Schedules) != 1 {
		t.Fatalf("wake-disabled continuation created a schedule: %#v, %v", stateAfter, err)
	}
	if filepath.Clean(repo) == filepath.Clean(feature) {
		t.Fatal("fixture did not create a linked feature worktree")
	}
}

func TestStatusUsesOnlyLiveAgentsAndFeatureOverviewStaysSeparate(t *testing.T) {
	primary, feature := createLinkedFeatureWorktree(t)
	home := filepath.Join(t.TempDir(), "runtime")
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	agent := model.RoleAgent{Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude", WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native, UpdatedAt: time.Now().Add(-time.Minute)}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature},
		WorkspaceID: "w1",
		Agents: map[string]model.RoleAgent{
			"builder": agent,
			"ghost": {
				Role: "ghost", Name: "saved-but-closed", Kind: "claude",
				WorkspaceID: "w-old", PaneID: "w-old:p1", TerminalID: "term-old",
			},
		},
		Schedules: map[string]model.Schedule{},
	}
	if err := state.New(paths.StateDir).Save(bridgeState); err != nil {
		t.Fatal(err)
	}

	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:    &output,
		Stderr:    &stderr,
		Getwd:     func() (string, error) { return feature, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
		Runner:    primaryCheckoutRunner{primary: primary, feature: feature},
	})
	args := []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "status", "--json"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("status exit=%d, want 0 for a successful live roster; stderr=%s", exit, stderr.String())
	}
	var roster liveAgentRoster
	if err := json.Unmarshal(output.Bytes(), &roster); err != nil {
		t.Fatalf("status JSON = %q: %v", output.String(), err)
	}
	if len(roster.Agents) != 2 {
		t.Fatalf("live roster = %#v, want exactly the two open agents", roster.Agents)
	}
	for _, agent := range roster.Agents {
		if agent.Agent == "saved-but-closed" {
			t.Fatalf("live roster included a closed saved bridge record: %#v", roster.Agents)
		}
	}
	if strings.Contains(output.String(), "schema_version") || strings.Contains(output.String(), "features") {
		t.Fatalf("status JSON leaked the feature overview contract: %s", output.String())
	}

	output.Reset()
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "overview", "--json"}); exit != 0 {
		t.Fatalf("overview alias exit=%d stderr=%s", exit, stderr.String())
	}
	var alias liveAgentRoster
	if err := json.Unmarshal(output.Bytes(), &alias); err != nil || len(alias.Agents) != len(roster.Agents) {
		t.Fatalf("overview alias = %#v, %v; want same live roster as status", alias, err)
	}

	// The full normalized snapshot remains available to the shell-only
	// feature-overview command used by `wt status`.
	output.Reset()
	stderr.Reset()
	featureArgs := []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "feature-overview", "--feature", "bridge", "--json"}
	if exit := application.Run(context.Background(), featureArgs); exit != 1 {
		t.Fatalf("feature overview exit=%d, want 1 while GitHub is unavailable; stderr=%s", exit, stderr.String())
	}
	var snapshot overview.Snapshot
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatalf("feature overview JSON = %q: %v", output.String(), err)
	}
	row, ok := snapshot.Feature("bridge")
	if !ok || row.Plan.Progress.NextActionable.Text != "Continue implementation" {
		t.Fatalf("feature overview lost its plan snapshot: %#v", snapshot.Features)
	}

	output.Reset()
	stderr.Reset()
	if exit := application.Run(context.Background(), []string{"--repo-root", feature, "--home", home, "--herdr-bin", "fake-herdr", "status", "--no-color"}); exit != 0 {
		t.Fatalf("human status exit=%d, want 0; stderr=%s", exit, stderr.String())
	}
	if strings.ContainsRune(output.String(), '\x1b') {
		t.Fatalf("no-color output contained escape sequences: %q", output.String())
	}
	for _, want := range []string{"Open agents: 2", "ori-repo-bridge-builder", "claude", "idle", "bridge"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("human status = %q, want it to contain %q", output.String(), want)
		}
	}
	for _, unwanted := range []string{"saved-but-closed", "INCOMPLETE", "phase:", "overnight:"} {
		if strings.Contains(output.String(), unwanted) {
			t.Fatalf("human status included %q from the old feature snapshot:\n%s", unwanted, output.String())
		}
	}

	output.Reset()
	stderr.Reset()
	if exit := application.Run(context.Background(), []string{"--home", home, "--herdr-bin", "fake-herdr", "plugin", "refresh"}); exit != 0 {
		t.Fatalf("plugin refresh exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(output.String(), "Ori Herdr Devflow: ready") {
		t.Fatalf("plugin refresh output = %q", output.String())
	}
}

// primaryCheckoutRunner answers as Herdr does when agents are open in both the
// repository's primary `dev` checkout and a feature worktree. The primary
// checkout is not a feature, and its agents are the ones the feature-first
// roster drops today.
type primaryCheckoutRunner struct {
	primary string
	feature string
}

func (r primaryCheckoutRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	key := strings.Join(command.Args, " ")
	switch {
	case key == "--version":
		return herdr.CommandResult{Stdout: []byte("herdr 0.7.5\n")}, nil
	case key == "api schema --json":
		return herdr.CommandResult{Stdout: []byte(schemaFixture())}, nil
	case key == "agent list":
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"agents":[
			{"agent":"claude","name":"ori-repo-bridge-builder","agent_status":"idle","workspace_id":"w1","pane_id":"w1:p2","terminal_id":"term-2","cwd":%q,"agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-123"}},
			{"agent":"claude","name":"ori-dev-claude","agent_status":"working","workspace_id":"w-dev","pane_id":"w-dev:p1","terminal_id":"term-dev-1","cwd":%q,"agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"native-dev"}}
		]}}`, r.feature, r.primary))}, nil
	case key == "workspace list":
		return herdr.CommandResult{Stdout: []byte(fmt.Sprintf(`{"result":{"workspaces":[
			{"workspace_id":"w1","cwd":%q,"label":"bridge","tab_count":1},
			{"workspace_id":"w-dev","cwd":%q,"label":"ori-agent-dev","tab_count":1}
		]}}`, r.feature, r.primary))}, nil
	case strings.HasPrefix(key, "workspace report-metadata "):
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"workspace_metadata"}}`)}, nil
	case strings.HasPrefix(key, "pane report-metadata "):
		return herdr.CommandResult{Stdout: []byte(`{"result":{"type":"pane_metadata"}}`)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected Herdr command: %s", key)
	}
}

func TestStatusCurrentListsOnlyAgentsInTheCurrentCheckout(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")

	var output, stderr bytes.Buffer
	application := New(Dependencies{
		Stdout:    &output,
		Stderr:    &stderr,
		Getwd:     func() (string, error) { return primary, nil },
		LookupEnv: func(string) (string, bool) { return "", false },
		Runner:    primaryCheckoutRunner{primary: primary, feature: feature},
	})
	args := []string{"--repo-root", primary, "--home", home, "--herdr-bin", "fake-herdr", "status", "--current", "--json"}
	if exit := application.Run(context.Background(), args); exit != 0 {
		t.Fatalf("status --current exit=%d stderr=%s", exit, stderr.String())
	}

	var roster liveAgentRoster
	if err := json.Unmarshal(output.Bytes(), &roster); err != nil {
		t.Fatalf("status JSON = %q: %v", output.String(), err)
	}
	if len(roster.Agents) != 1 || roster.Agents[0].Agent != "ori-dev-claude" || roster.Agents[0].Worktree != primary {
		t.Fatalf("status --current roster = %#v, want only the primary-checkout agent", roster.Agents)
	}
}

func TestStatusSelectorsFilterTheLiveRosterByCanonicalWorktree(t *testing.T) {
	primary, feature := createPrimaryCheckoutWithFeature(t)
	home := filepath.Join(t.TempDir(), "runtime")

	run := func(args ...string) string {
		t.Helper()
		var output, stderr bytes.Buffer
		application := New(Dependencies{
			Stdout:    &output,
			Stderr:    &stderr,
			Getwd:     func() (string, error) { return primary, nil },
			LookupEnv: func(string) (string, bool) { return "", false },
			Runner:    primaryCheckoutRunner{primary: primary, feature: feature},
		})
		base := []string{"--repo-root", primary, "--home", home, "--herdr-bin", "fake-herdr", "status", "--no-color"}
		if exit := application.Run(context.Background(), append(base, args...)); exit != 0 {
			t.Fatalf("status selector exit=%d stderr=%s", exit, stderr.String())
		}
		return output.String()
	}

	fromPrimary := run("--current")
	if !strings.Contains(fromPrimary, "ori-dev-claude") || strings.Contains(fromPrimary, "ori-repo-bridge-builder") {
		t.Fatalf("status --current did not isolate the current checkout:\n%s", fromPrimary)
	}

	fromFeature := run("--worktree", feature)
	if !strings.Contains(fromFeature, "ori-repo-bridge-builder") || strings.Contains(fromFeature, "ori-dev-claude") {
		t.Fatalf("status --worktree did not select the feature:\n%s", fromFeature)
	}

	fromSlug := run("--feature", "bridge")
	if !strings.Contains(fromSlug, "ori-repo-bridge-builder") || strings.Contains(fromSlug, "ori-dev-claude") {
		t.Fatalf("status --feature did not resolve the feature worktree:\n%s", fromSlug)
	}
}

// writePrimaryCheckoutBridgeState saves the bridge record used by tests of the
// feature overview and unattended-run eligibility.
func writePrimaryCheckoutBridgeState(t *testing.T, home, feature string) {
	t.Helper()
	paths, err := worktree.Resolve(feature, func(key string) (string, bool) {
		if key == worktree.HomeOverrideEnv {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	native := model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "native-123"}
	bridgeState := model.NewBridgeState()
	bridgeState.Features[paths.RepositoryID+":bridge"] = model.FeatureState{
		Feature:     model.Feature{RepositoryID: paths.RepositoryID, Name: "bridge", Branch: "feature/bridge", Path: feature},
		WorkspaceID: "w1",
		Agents: map[string]model.RoleAgent{"builder": {
			Role: "builder", Name: "ori-repo-bridge-builder", Kind: "claude",
			WorkspaceID: "w1", PaneID: "w1:p2", TerminalID: "term-2", NativeSession: native,
			UpdatedAt: time.Now().Add(-time.Minute),
		}},
		Schedules: map[string]model.Schedule{},
	}
	if err := state.New(paths.StateDir).Save(bridgeState); err != nil {
		t.Fatal(err)
	}
}

// createPrimaryCheckoutWithFeature builds a repository whose primary checkout
// is named like the real one (`ori-agent-dev`) plus one linked feature
// worktree, so `--current` resolution can be exercised from either side.
func createPrimaryCheckoutWithFeature(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	primary := filepath.Join(root, "ori-agent-dev")
	runAppGit(t, "", "init", "-b", "dev", primary)
	if err := os.MkdirAll(filepath.Join(primary, ".herdr"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, ".herdr", "devflow.toml"), []byte(devflowConfigFixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(primary, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "tasks", "prd-bridge.md"), []byte("# PRD: Bridge\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runAppGit(t, primary, "add", ".")
	runAppGit(t, primary, "-c", "user.name=Ori Test", "-c", "user.email=ori@example.test", "commit", "-m", "fixture")

	feature := filepath.Join(root, "worktrees", "bridge")
	runAppGit(t, primary, "worktree", "add", "-b", "feature/bridge", feature)
	if err := os.MkdirAll(filepath.Join(feature, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "tasks", "tasks-bridge.md"), []byte("- [ ] 1.1 Continue implementation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return primary, feature
}

func createLinkedFeatureWorktree(t *testing.T) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	runAppGit(t, "", "init", "-b", "dev", repo)
	if err := os.MkdirAll(filepath.Join(repo, ".herdr"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".herdr", "devflow.toml"), []byte(devflowConfigFixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runAppGit(t, repo, "add", ".")
	runAppGit(t, repo, "-c", "user.name=Ori Test", "-c", "user.email=ori@example.test", "commit", "-m", "fixture")
	feature := filepath.Join(filepath.Dir(repo), "bridge")
	runAppGit(t, repo, "worktree", "add", "-b", "feature/bridge", feature)
	if err := os.MkdirAll(filepath.Join(feature, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "tasks", "tasks-bridge.md"), []byte("- [ ] 1.1 Continue implementation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return repo, feature
}

func runAppGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	if directory != "" {
		args = append([]string{"-C", directory}, args...)
	}
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestVerifyCompatibilityRejectsOldHerdrAndMissingHandoffMethods(t *testing.T) {
	t.Parallel()
	old := herdr.New("fake-herdr", "", compatibilityRunner{version: "0.7.4", schema: schemaFixture()})
	err := verifyCompatibility(context.Background(), old, configForTest())
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrHerdrIncompatible {
		t.Fatalf("old Herdr error = %#v", err)
	}

	missing := herdr.New("fake-herdr", "", compatibilityRunner{version: "0.7.5", schema: `{"protocol":17,"schema_version":1,"requests":[{"method":{"const":"ping"}},{"method":{"const":"worktree.open"}}]}`})
	err = verifyCompatibility(context.Background(), missing, configForTest())
	if !errors.As(err, &stage) || stage.Code != model.ErrSchemaUnsupported || !strings.Contains(stage.Message, "plugin.link") {
		t.Fatalf("missing handoff method error = %#v", err)
	}
}

type compatibilityRunner struct {
	version string
	schema  string
}

func (r compatibilityRunner) Run(_ context.Context, command herdr.Command) (herdr.CommandResult, error) {
	switch strings.Join(command.Args, " ") {
	case "--version":
		return herdr.CommandResult{Stdout: []byte("herdr " + r.version + "\n")}, nil
	case "api schema --json":
		return herdr.CommandResult{Stdout: []byte(r.schema)}, nil
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected Herdr command: %s", strings.Join(command.Args, " "))
	}
}

func configForTest() config.Config {
	return config.Default()
}

func writeSetupFixture(t *testing.T, repo string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(repo, ".git"),
		filepath.Join(repo, ".herdr"),
		filepath.Join(repo, "tools", "herdr-devflow"),
	} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".herdr", "devflow.toml"), []byte(devflowConfigFixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "herdr-devflow", "herdr-plugin.toml"), []byte("id = \"ori.devflow\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "herdr-devflow", "plugin.sh"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
}

func containsIntegrationCommand(commands []herdr.Command) bool {
	for _, command := range commands {
		if len(command.Args) > 0 && command.Args[0] == "integration" {
			return true
		}
	}
	return false
}

func containsHerdrCommand(commands []herdr.Command, want string) bool {
	for _, command := range commands {
		if strings.Join(command.Args, " ") == want {
			return true
		}
	}
	return false
}

func containsHerdrCommandFromStrings(commands []string, want string) bool {
	for _, command := range commands {
		if command == want {
			return true
		}
	}
	return false
}

func linkCalls(commands []herdr.Command) int {
	count := 0
	for _, command := range commands {
		if len(command.Args) >= 2 && command.Args[0] == "plugin" && command.Args[1] == "link" {
			count++
		}
	}
	return count
}
