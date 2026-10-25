package config

import (
	"path/filepath"
	"testing"
)

func TestAnthropicWorkspaceIDSettingsFirstThenEnv(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := m.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	t.Setenv("ANTHROPIC_WORKSPACE_ID", "")
	if got := m.GetAnthropicWorkspaceID(); got != "" {
		t.Fatalf("expected no workspace, got %q", got)
	}

	// Env fallback when nothing is configured.
	t.Setenv("ANTHROPIC_WORKSPACE_ID", " wrkspc_env ")
	if got := m.GetAnthropicWorkspaceID(); got != "wrkspc_env" {
		t.Fatalf("expected env fallback, got %q", got)
	}

	// The setting takes precedence over env and persists across a reload.
	settings := m.Get()
	settings.AnthropicWorkspaceID = "wrkspc_cfg"
	if err := m.Update(settings); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	m2 := NewManager(m.filePath)
	if err := m2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := m2.GetAnthropicWorkspaceID(); got != "wrkspc_cfg" {
		t.Fatalf("setting should win over env and persist, got %q", got)
	}
}

func TestValidateAnthropicWorkspaceID(t *testing.T) {
	for _, id := range []string{"", "wrkspc_011CZkZaBF1tNoB5wlCeusgy"} {
		if err := ValidateAnthropicWorkspaceID(id); err != nil {
			t.Errorf("ValidateAnthropicWorkspaceID(%q) = %v, want nil", id, err)
		}
	}
	// Sent as a request header, so nothing but the ID's own characters passes.
	for _, id := range []string{"wrkspc_", "workspace-1", "wrkspc_abc\r\nX-Other: 1", "wrkspc_abc def", "sk-ant-api03-abc"} {
		if err := ValidateAnthropicWorkspaceID(id); err == nil {
			t.Errorf("ValidateAnthropicWorkspaceID(%q) = nil, want an error", id)
		}
	}
}
