package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
)

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
	default:
		return herdr.CommandResult{}, fmt.Errorf("unexpected Herdr command: %s", key)
	}
}

func schemaFixture() string {
	return `{"protocol":17,"schema_version":1,"requests":[
		{"method":{"const":"plugin.link"}},
		{"method":{"const":"plugin.enable"}},
		{"method":{"const":"session.snapshot"}},
		{"method":{"const":"worktree.open"}},
		{"method":{"const":"agent.start"}},
		{"method":{"const":"agent.view.set"}},
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
	var output, errors bytes.Buffer
	builds := 0
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
		GOOS: "darwin",
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
	if !strings.Contains(output.String(), "Ori Herdr Devflow: ready") {
		t.Fatalf("setup output = %q", output.String())
	}
	if got, err := os.ReadFile(globalConfig); err != nil || !bytes.Equal(got, originalGlobalConfig) {
		t.Fatalf("global config changed: %q, %v", got, err)
	}
	if containsIntegrationCommand(runner.calls) {
		t.Fatalf("setup attempted a forbidden integration mutation: %#v", runner.calls)
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
	if err := os.WriteFile(filepath.Join(repo, ".herdr", "devflow.toml"), []byte(`
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
`), 0600); err != nil {
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

func linkCalls(commands []herdr.Command) int {
	count := 0
	for _, command := range commands {
		if len(command.Args) >= 2 && command.Args[0] == "plugin" && command.Args[1] == "link" {
			count++
		}
	}
	return count
}
