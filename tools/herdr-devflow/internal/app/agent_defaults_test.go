package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

const agentDefaultsConfigFixture = `[bridge]
schema_version = 1
enabled = true
min_herdr_version = "0.7.5"
source_id = "ori.devflow"
[primary]
role = "builder"
kind = "claude"
model = ""
[roles]
default_kind = "claude"
default_model = ""
[roles.defaults]
reviewer = "claude"
[bootstrap]
template = "primary-v1"
timeout_seconds = 30
[scheduler]
retry_window = "15m"
[metadata]
enabled = true
[status]
watch_poll_interval = "2s"
`

type noHerdrCallsRunner struct{ calls int }

func (r *noHerdrCallsRunner) Run(context.Context, herdr.Command) (herdr.CommandResult, error) {
	r.calls++
	return herdr.CommandResult{}, fmt.Errorf("Herdr must not be called by config agent-defaults")
}

func TestAgentDefaultsCommandReadsUpdatesAndClearsConfiguredPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	if err := os.WriteFile(path, []byte(agentDefaultsConfigFixture), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &noHerdrCallsRunner{}
	newApplication := func(stdout, stderr *bytes.Buffer) *App {
		return New(Dependencies{
			Stdout: stdout, Stderr: stderr, Runner: runner,
			LookupEnv: func(key string) (string, bool) {
				if key == worktree.ConfigOverrideEnv {
					return path, true
				}
				return "", false
			},
		})
	}

	var stdout, stderr bytes.Buffer
	if exit := newApplication(&stdout, &stderr).Run(context.Background(), []string{"config", "agent-defaults"}); exit != 0 {
		t.Fatalf("read exit = %d stderr=%q", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Primary:      kind=claude model=integration default") ||
		!strings.Contains(stdout.String(), "Role fallback: kind=claude model=integration default") {
		t.Fatalf("human output did not render both empty-model pairs: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	update := []string{
		"--json", "config", "agent-defaults",
		"--primary-kind", "pi", "--primary-model", "[openai] gpt-5.1 codex",
		"--role-kind", "codex", "--role-model", "openai/fallback",
	}
	if exit := newApplication(&stdout, &stderr).Run(context.Background(), update); exit != 0 {
		t.Fatalf("update exit = %d stderr=%q", exit, stderr.String())
	}
	var payload struct {
		Status  string `json:"status"`
		Config  string `json:"config"`
		Primary struct {
			Kind  string `json:"kind"`
			Model string `json:"model"`
		} `json:"primary"`
		RoleFallback struct {
			Kind  string `json:"kind"`
			Model string `json:"model"`
		} `json:"role_fallback"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("update output is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "updated" || payload.Config != path ||
		payload.Primary.Kind != "pi" || payload.Primary.Model != "[openai] gpt-5.1 codex" ||
		payload.RoleFallback.Kind != "codex" || payload.RoleFallback.Model != "openai/fallback" {
		t.Fatalf("unexpected update payload: %+v", payload)
	}

	stdout.Reset()
	stderr.Reset()
	clear := []string{"--json", "config", "agent-defaults", "--clear-primary-model", "--clear-role-model"}
	if exit := newApplication(&stdout, &stderr).Run(context.Background(), clear); exit != 0 {
		t.Fatalf("clear exit = %d stderr=%q", exit, stderr.String())
	}
	payload = struct {
		Status  string `json:"status"`
		Config  string `json:"config"`
		Primary struct {
			Kind  string `json:"kind"`
			Model string `json:"model"`
		} `json:"primary"`
		RoleFallback struct {
			Kind  string `json:"kind"`
			Model string `json:"model"`
		} `json:"role_fallback"`
	}{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("clear output is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Primary.Model != "" || payload.RoleFallback.Model != "" || payload.Primary.Kind != "pi" || payload.RoleFallback.Kind != "codex" {
		t.Fatalf("clear changed the wrong values: %+v", payload)
	}
	if runner.calls != 0 {
		t.Fatalf("config command made %d Herdr call(s)", runner.calls)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %o, want 640", info.Mode().Perm())
	}
}

func TestAgentDefaultsCommandRejectsInvalidArgumentsBeforeMutation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	if err := os.WriteFile(path, []byte(agentDefaultsConfigFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing subcommand", args: []string{"config"}},
		{name: "unknown subcommand", args: []string{"config", "other"}},
		{name: "missing kind value", args: []string{"config", "agent-defaults", "--primary-kind"}},
		{name: "duplicate kind", args: []string{"config", "agent-defaults", "--primary-kind", "pi", "--primary-kind", "codex"}},
		{name: "unsupported kind", args: []string{"config", "agent-defaults", "--role-kind", "invented"}},
		{name: "flag-shaped model", args: []string{"config", "agent-defaults", "--primary-model", "-x"}},
		{name: "control model", args: []string{"config", "agent-defaults", "--role-model", "bad\nmodel"}},
		{name: "model and clear", args: []string{"config", "agent-defaults", "--primary-model", "openai/model", "--clear-primary-model"}},
		{name: "unknown option", args: []string{"config", "agent-defaults", "--all", "pi"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runner := &noHerdrCallsRunner{}
			application := New(Dependencies{
				Stdout: &stdout, Stderr: &stderr, Runner: runner,
				LookupEnv: func(key string) (string, bool) {
					if key == worktree.ConfigOverrideEnv {
						return path, true
					}
					return "", false
				},
			})
			if exit := application.Run(context.Background(), testCase.args); exit != 2 {
				t.Fatalf("exit = %d, want usage failure; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("invalid command changed the config")
			}
			if runner.calls != 0 {
				t.Fatalf("invalid command made %d Herdr call(s)", runner.calls)
			}
		})
	}
}
