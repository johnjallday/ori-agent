package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// A workspace snapshot is the second persistence seam, and the dangerous one:
// after the global store migrates, an old snapshot read back could reintroduce
// the retired schema. These tests pin that it cannot (PRD FR-69, risk 7.6).

func writeLegacySnapshot(t *testing.T, workspaceFolder, agentName, configJSON string) string {
	t.Helper()
	dir, err := workspaceAgentDir(workspaceFolder, agentName)
	if err != nil {
		t.Fatalf("resolve agent dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create agent dir: %v", err)
	}
	path := filepath.Join(dir, WorkspaceAgentConfigFile)
	if err := os.WriteFile(path, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return path
}

func TestWorkspaceSnapshotMigratesLegacyAppearance(t *testing.T) {
	folder := t.TempDir()
	writeLegacySnapshot(t, folder, "Scout", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini","system_prompt":"workspace-local prompt"},
		"metadata":{"description":"local","avatar_color":"#3366FF","character":{"display_mode":"fallback","voice_enabled":true}}
	}`)

	ag, found, err := readWorkspaceAgent(folder, "Scout")
	if err != nil || !found {
		t.Fatalf("read snapshot: found=%v err=%v", found, err)
	}
	if ag.Appearance == nil {
		t.Fatal("a snapshot read must produce a canonical appearance")
	}
	if ag.Appearance.Mode != types.AppearanceModeGenerated {
		t.Errorf("mode = %q, want generated", ag.Appearance.Mode)
	}
	if ag.Appearance.GeneratedColor() != "#3366ff" {
		t.Errorf("colour = %q, want the migrated legacy colour", ag.Appearance.GeneratedColor())
	}
	// Workspace snapshots own workspace-local settings; an appearance migration
	// is never a licence to rewrite them (FR-95).
	if ag.Settings.SystemPrompt != "workspace-local prompt" {
		t.Errorf("workspace-local prompt was disturbed: %q", ag.Settings.SystemPrompt)
	}
	if ag.Metadata == nil || ag.Metadata.Description != "local" {
		t.Error("workspace-local metadata was disturbed")
	}
}

func TestWorkspaceSnapshotWritesOnlyCanonicalAppearance(t *testing.T) {
	folder := t.TempDir()
	path := writeLegacySnapshot(t, folder, "Scout", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"avatar_color":"#3366ff","avatar_image":"atlas.webp","character":{"display_mode":"character","catalog_id":"gone","voice_enabled":true}}
	}`)

	ag, _, err := readWorkspaceAgent(folder, "Scout")
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if err := writeWorkspaceAgent(folder, "Scout", ag); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	written := string(raw)
	for _, retired := range []string{"avatar_color", "avatar_image", "display_mode", "voice_enabled"} {
		if strings.Contains(written, retired) {
			t.Errorf("snapshot still contains %q:\n%s", retired, written)
		}
	}

	var record struct {
		Appearance *types.AgentAppearance `json:"appearance"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if record.Appearance == nil {
		t.Fatal("snapshot is missing the canonical appearance")
	}
	// The referenced character is unknown to the catalog, so it cannot be the
	// active source — but the reference is kept so an editor can say which
	// selection went missing rather than showing a mysterious revert (FR-73).
	if record.Appearance.Mode != types.AppearanceModeGenerated {
		t.Errorf("mode = %q, want generated for an unavailable character", record.Appearance.Mode)
	}
	if record.Appearance.CharacterCatalogID() != "gone" {
		t.Errorf("the unavailable selection should be retained for diagnostics, got %q", record.Appearance.CharacterCatalogID())
	}
}

func TestWorkspaceSnapshotWriteCanonicalizesEvenWithoutARead(t *testing.T) {
	folder := t.TempDir()
	// A caller that constructs an agent in memory and writes it directly must
	// still produce a canonical snapshot; otherwise one write path becomes the
	// hole that keeps the retired schema alive (FR-77).
	if err := writeWorkspaceAgent(folder, "Fresh", &agent.Agent{Type: "general"}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	ag, found, err := readWorkspaceAgent(folder, "Fresh")
	if err != nil || !found {
		t.Fatalf("read back: found=%v err=%v", found, err)
	}
	if ag.Appearance == nil || ag.Appearance.Mode != types.AppearanceModeGenerated {
		t.Fatalf("expected a generated appearance, got %+v", ag.Appearance)
	}
	if ag.Appearance.Generated == nil {
		t.Error("generated must be materialized on write")
	}
}

func TestSnapshotMigrationScopesItsNoteToTheWorkspace(t *testing.T) {
	agent.ResetAppearanceMigrationNotes()
	t.Cleanup(agent.ResetAppearanceMigrationNotes)

	folder := filepath.Join(t.TempDir(), "field-ops")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("create workspace folder: %v", err)
	}
	writeLegacySnapshot(t, folder, "Scout", `{
		"type":"general",
		"Settings":{"model":"gpt-4o-mini"},
		"metadata":{"character":{"display_mode":"hologram","voice_enabled":true}}
	}`)

	if _, _, err := readWorkspaceAgent(folder, "Scout"); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	notes := agent.AppearanceMigrationNotes()
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %+v", notes)
	}
	if notes[0].Scope != "workspace:field-ops" {
		t.Errorf("scope = %q, want the workspace folder name", notes[0].Scope)
	}
	// The scope names the workspace, not its path: enough to find the snapshot,
	// without putting a filesystem path in front of the user (FR-73).
	if strings.ContainsAny(strings.TrimPrefix(notes[0].Scope, "workspace:"), "/\\") {
		t.Errorf("scope leaks a path: %q", notes[0].Scope)
	}
}
