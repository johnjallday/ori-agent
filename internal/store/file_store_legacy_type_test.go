package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func writeLegacyAgentSettings(t *testing.T, dir, name, body string) string {
	t.Helper()
	agentDir := filepath.Join(dir, "agents", name)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	path := filepath.Join(agentDir, "agent_settings.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write agent settings: %v", err)
	}
	return path
}

func readAgentSettingsKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read agent settings: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decode agent settings: %v", err)
	}
	return keys
}

func countTag(ag *types.AgentMetadata, tag string) int {
	if ag == nil {
		return 0
	}
	n := 0
	for _, t := range ag.Tags {
		if t == tag {
			n++
		}
	}
	return n
}

func TestNewFileStore_StripsLegacyTypeAndKeepsModel(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")
	// A model absent from every retired tier table: the old migration would have
	// rewritten it to gpt-5-nano.
	settingsPath := writeLegacyAgentSettings(t, tempDir, "alpha",
		`{"type":"research","role":"researcher","Settings":{"model":"claude-sonnet-5","provider":"anthropic","temperature":1}}`)

	s, err := NewFileStore(indexPath, types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	got, ok := s.GetAgent("alpha")
	if !ok || got == nil {
		t.Fatal("expected alpha to load")
	}
	if got.Settings.Model != "claude-sonnet-5" {
		t.Errorf("model = %q, want claude-sonnet-5", got.Settings.Model)
	}
	if got.Role != types.RoleResearcher {
		t.Errorf("role = %q, want %q", got.Role, types.RoleResearcher)
	}
	if countTag(got.Metadata, "workspace-manager") != 0 {
		t.Error("a non-workspace-manager type must not add the workspace-manager tag")
	}

	keys := readAgentSettingsKeys(t, settingsPath)
	if _, has := keys["type"]; has {
		t.Errorf("rewritten agent_settings.json still has a type key: %s", keys["type"])
	}
}

func TestNewFileStore_LegacyWorkspaceManagerTypeBecomesTag(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")
	settingsPath := writeLegacyAgentSettings(t, tempDir, "manager",
		`{"type":"workspace-manager","Settings":{"model":"gpt-5-mini","temperature":1}}`)
	// Already tagged: the migration must not add a duplicate.
	taggedPath := writeLegacyAgentSettings(t, tempDir, "tagged",
		`{"type":"workspace-manager","metadata":{"tags":["workspace-manager"]},"Settings":{"model":"gpt-5-mini","temperature":1}}`)

	s, err := NewFileStore(indexPath, types.Settings{})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	for _, name := range []string{"manager", "tagged"} {
		got, ok := s.GetAgent(name)
		if !ok || got == nil {
			t.Fatalf("expected %s to load", name)
		}
		if n := countTag(got.Metadata, "workspace-manager"); n != 1 {
			t.Errorf("%s: workspace-manager tag count = %d, want 1", name, n)
		}
		if got.Settings.Model != "gpt-5-mini" {
			t.Errorf("%s: model = %q, want gpt-5-mini", name, got.Settings.Model)
		}
	}

	for _, path := range []string{settingsPath, taggedPath} {
		if _, has := readAgentSettingsKeys(t, path)["type"]; has {
			t.Errorf("%s still has a type key", path)
		}
	}
}

func TestNewFileStore_LegacyTypeStripIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "agents_index.json")
	settingsPath := writeLegacyAgentSettings(t, tempDir, "manager",
		`{"type":"workspace-manager","Settings":{"model":"gpt-5-mini","temperature":1}}`)

	if _, err := NewFileStore(indexPath, types.Settings{}); err != nil {
		t.Fatalf("first NewFileStore: %v", err)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after first boot: %v", err)
	}

	s, err := NewFileStore(indexPath, types.Settings{})
	if err != nil {
		t.Fatalf("second NewFileStore: %v", err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after second boot: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("second boot changed agent_settings.json\nfirst:  %s\nsecond: %s", first, second)
	}
	got, _ := s.GetAgent("manager")
	if got == nil || countTag(got.Metadata, "workspace-manager") != 1 {
		t.Errorf("expected exactly one workspace-manager tag after second boot, got %+v", got)
	}
}
