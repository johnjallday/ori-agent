package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateAgentDefaultsPreservesUnrelatedTOMLAndFileMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "devflow.toml")
	original := strings.Join([]string{
		"# keep this comment",
		"[bridge]",
		"schema_version = 1",
		"enabled = true",
		"min_herdr_version = \"0.7.5\"",
		"source_id = \"ori.devflow\"",
		"",
		"[primary] # primary comment",
		"role = \"builder\"",
		"kind = \"claude\" # keep inline",
		"model = \"\"",
		"",
		"[roles]",
		"default_kind = 'claude'",
		"default_model = \"\" # integration chooses",
		"",
		"[roles.defaults]",
		"reviewer = \"codex\"",
		"",
		"[roles.models]",
		"reviewer = \"existing/reviewer\"",
		"",
		"[bootstrap]",
		"template = \"primary-v1\"",
		"timeout_seconds = 30",
		"",
		"[scheduler]",
		"retry_window = \"15m\"",
		"",
		"[metadata]",
		"enabled = true",
		"",
		"[status]",
		"watch_poll_interval = \"2s\"",
	}, "\r\n") + "\r\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	proposed := AgentDefaults{
		Primary:      AgentSelection{Kind: "pi", Model: "[openai] gpt-5.1 codex"},
		RoleFallback: AgentSelection{Kind: "codex", Model: "openai/fallback"},
	}
	got, err := UpdateAgentDefaults(path, proposed)
	if err != nil {
		t.Fatalf("UpdateAgentDefaults() error = %v", err)
	}
	if got != proposed {
		t.Fatalf("UpdateAgentDefaults() = %#v, want %#v", got, proposed)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original, `kind = "claude" # keep inline`, `kind = "pi" # keep inline`, 1)
	want = strings.Replace(want, `model = ""`, `model = "[openai] gpt-5.1 codex"`, 1)
	want = strings.Replace(want, `default_kind = 'claude'`, `default_kind = "codex"`, 1)
	want = strings.Replace(want, `default_model = ""`, `default_model = "openai/fallback"`, 1)
	if string(updated) != want {
		t.Fatalf("updated TOML changed unrelated bytes\n--- got ---\n%s\n--- want ---\n%s", updated, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o640 {
		t.Fatalf("mode = %o, want 640", gotMode)
	}
	if !strings.Contains(string(updated), "\r\n") || strings.Contains(strings.ReplaceAll(string(updated), "\r\n", ""), "\n") {
		t.Fatal("CRLF newline style was not preserved")
	}
}

func TestUpdateAgentDefaultsFailsClosedWithoutChangingTheOriginal(t *testing.T) {
	t.Parallel()
	valid := `[bridge]
schema_version = 1
min_herdr_version = "0.7.5"
source_id = "ori.devflow"
[primary]
role = "builder"
kind = "claude"
model = ""
[roles]
default_kind = "claude"
default_model = ""
[bootstrap]
template = "primary-v1"
timeout_seconds = 30
[scheduler]
retry_window = "15m"
[status]
watch_poll_interval = "2s"
`
	proposed := AgentDefaults{
		Primary:      AgentSelection{Kind: "pi", Model: "openai/primary"},
		RoleFallback: AgentSelection{Kind: "codex", Model: "openai/fallback"},
	}
	cases := []struct {
		name      string
		contents  string
		makePath  func(t *testing.T, path string)
		proposal  AgentDefaults
		mutateOps func(*agentDefaultsFileOps)
	}{
		{name: "malformed TOML", contents: valid + "broken = [\n", proposal: proposed},
		{name: "duplicate target", contents: strings.Replace(valid, "kind = \"claude\"", "kind = \"claude\"\nkind = \"pi\"", 1), proposal: proposed},
		{name: "missing target", contents: strings.Replace(valid, "model = \"\"\n", "", 1), proposal: proposed},
		{name: "invalid primary kind", contents: valid, proposal: AgentDefaults{Primary: AgentSelection{Kind: "invented"}, RoleFallback: proposed.RoleFallback}},
		{name: "invalid role model", contents: valid, proposal: AgentDefaults{Primary: proposed.Primary, RoleFallback: AgentSelection{Kind: "pi", Model: "--flag"}}},
		{
			name:     "symlink path",
			contents: valid,
			proposal: proposed,
			makePath: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				if err := os.WriteFile(target, []byte(valid), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "temporary write failure",
			contents: valid,
			proposal: proposed,
			mutateOps: func(ops *agentDefaultsFileOps) {
				ops.writeAll = func(*os.File, []byte) error { return errors.New("injected write failure") }
			},
		},
		{
			name:     "atomic rename failure",
			contents: valid,
			proposal: proposed,
			mutateOps: func(ops *agentDefaultsFileOps) {
				ops.rename = func(string, string) error { return errors.New("injected rename failure") }
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "devflow.toml")
			if testCase.makePath != nil {
				testCase.makePath(t, path)
			} else if err := os.WriteFile(path, []byte(testCase.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ops := defaultAgentDefaultsFileOps()
			if testCase.mutateOps != nil {
				testCase.mutateOps(&ops)
			}
			if _, err := updateAgentDefaults(path, testCase.proposal, ops); err == nil {
				t.Fatal("UpdateAgentDefaults() unexpectedly succeeded")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("refused update changed the original\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}
		})
	}
}
