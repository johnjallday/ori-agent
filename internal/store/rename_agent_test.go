package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func renameStore(t *testing.T) (*fileStore, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := NewFileStore(filepath.Join(dir, "agents_index.json"), types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	fs, ok := st.(*fileStore)
	if !ok {
		t.Fatalf("NewFileStore returned %T, want *fileStore", st)
	}
	return fs, filepath.Join(dir, "agents")
}

func TestFileStoreImplementsAgentRenamer(t *testing.T) {
	fs, _ := renameStore(t)
	if _, ok := any(fs).(AgentRenamer); !ok {
		t.Fatal("fileStore must implement AgentRenamer so identity migration can move records losslessly")
	}
}

// The reason this primitive exists: the old migration did SetAgent(new) then
// DeleteAgent(old), and DeleteAgent does os.RemoveAll on the agent folder — so
// every sidecar that is not part of the in-memory record was destroyed.
func TestRenameAgentSupportsCaseOnlyIdentityChange(t *testing.T) {
	fs, _ := renameStore(t)
	if err := fs.CreateAgent("Atlas", &CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatal(err)
	}
	if err := fs.RenameAgent("Atlas", "ATLAS"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	if _, oldExists := fs.GetAgent("Atlas"); oldExists {
		t.Fatal("old profile key remained after case-only rename")
	}
	if _, newExists := fs.GetAgent("ATLAS"); !newExists {
		t.Fatal("case-only destination profile is missing")
	}
}

func TestRenameAgentPreservesSidecarFiles(t *testing.T) {
	fs, agentsDir := renameStore(t)

	if err := fs.CreateAgent("Workspace Manager", &CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Model:        "claude-opus-5",
		LLMProvider:  "anthropic",
		SystemPrompt: "user customized this",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Sidecars the store never round-trips through agent.Agent: the per-agent
	// skill registry and installed per-agent skill payloads.
	oldDir := filepath.Join(agentsDir, "Workspace Manager")
	skillDir := filepath.Join(oldDir, "skills", "reaper-control")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("installed skill body"), 0o644); err != nil {
		t.Fatalf("seed skill body: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "skills_state.json"),
		[]byte(`{"skills":{"reaper-control":{"enabled":true,"trusted":true}}}`), 0o644); err != nil {
		t.Fatalf("seed skills state: %v", err)
	}

	if err := fs.RenameAgent("Workspace Manager", "Ask Ori"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}

	newDir := filepath.Join(agentsDir, "Ask Ori")

	body, err := os.ReadFile(filepath.Join(newDir, "skills", "reaper-control", "SKILL.md"))
	if err != nil {
		t.Fatalf("installed skill did not survive the rename: %v", err)
	}
	if string(body) != "installed skill body" {
		t.Errorf("skill body = %q, want %q", body, "installed skill body")
	}

	state, err := os.ReadFile(filepath.Join(newDir, "skills_state.json"))
	if err != nil {
		t.Fatalf("skills_state.json did not survive the rename: %v", err)
	}
	if len(state) == 0 {
		t.Error("skills_state.json survived but is empty")
	}

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("the source folder should be gone after a successful move, stat err = %v", err)
	}
}

func TestRenameAgentPreservesTheAgentRecord(t *testing.T) {
	fs, _ := renameStore(t)

	if err := fs.CreateAgent("Workspace Manager", &CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Role:         types.RoleOrchestrator,
		Model:        "claude-opus-5",
		LLMProvider:  "anthropic",
		SystemPrompt: "user customized this",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	before, _ := fs.GetAgent("Workspace Manager")
	before.Metadata = &types.AgentMetadata{
		Description: "kept",
		Tags:        []string{"system", "custom-tag"},
		Favorite:    true,
	}
	if err := fs.SetAgent("Workspace Manager", before); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}

	if err := fs.RenameAgent("Workspace Manager", "Ask Ori"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}

	moved, ok := fs.GetAgent("Ask Ori")
	if !ok || moved == nil {
		t.Fatal("expected the record under the new name")
	}
	if moved.Settings.SystemPrompt != "user customized this" {
		t.Errorf("prompt = %q", moved.Settings.SystemPrompt)
	}
	if moved.Settings.Model != "claude-opus-5" || moved.Settings.Provider != "anthropic" {
		t.Errorf("model/provider = %q/%q", moved.Settings.Model, moved.Settings.Provider)
	}
	if moved.Role != types.RoleOrchestrator {
		t.Errorf("role = %q", moved.Role)
	}
	if moved.Metadata == nil || moved.Metadata.Description != "kept" || !moved.Metadata.Favorite {
		t.Errorf("metadata lost: %+v", moved.Metadata)
	}
	if _, stillThere := fs.GetAgent("Workspace Manager"); stillThere {
		t.Error("the source record should be gone after the move")
	}
}

// FR55/FR60: a move must never clobber an existing record. The caller decides
// how to resolve a collision; the store refuses to make that choice destructively.
func TestRenameAgentRefusesToOverwriteAnExistingAgent(t *testing.T) {
	fs, _ := renameStore(t)

	if err := fs.CreateAgent("Workspace Manager", &CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "system record",
	}); err != nil {
		t.Fatalf("seed system: %v", err)
	}
	if err := fs.CreateAgent("Ask Ori", &CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "MINE — user authored",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := fs.RenameAgent("Workspace Manager", "Ask Ori"); err == nil {
		t.Fatal("RenameAgent must fail rather than overwrite an existing agent")
	}

	mine, ok := fs.GetAgent("Ask Ori")
	if !ok || mine.Settings.SystemPrompt != "MINE — user authored" {
		t.Fatalf("the user's agent was damaged: %+v", mine)
	}
	source, ok := fs.GetAgent("Workspace Manager")
	if !ok || source.Settings.SystemPrompt != "system record" {
		t.Fatalf("the source record must be left intact after a refused move: %+v", source)
	}
}

func TestRenameAgentRejectsUnknownSourceAndEmptyNames(t *testing.T) {
	fs, _ := renameStore(t)

	if err := fs.RenameAgent("Nope", "Ask Ori"); err == nil {
		t.Error("renaming an agent that does not exist must fail")
	}
	if err := fs.CreateAgent("Real", &CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fs.RenameAgent("Real", "   "); err == nil {
		t.Error("renaming to a blank name must fail")
	}
	if err := fs.RenameAgent("Real", "Real"); err != nil {
		t.Errorf("renaming to the same name should be a no-op, got %v", err)
	}
	if _, ok := fs.GetAgent("Real"); !ok {
		t.Error("the record must survive a same-name rename")
	}
}

// A record can exist in memory without a folder yet (nothing has forced a save).
// The move must still complete rather than fail on the missing source folder.
func TestRenameAgentSucceedsWhenTheSourceFolderIsMissing(t *testing.T) {
	fs, agentsDir := renameStore(t)

	if err := fs.CreateAgent("Workspace Manager", &CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "no folder",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(agentsDir, "Workspace Manager")); err != nil {
		t.Fatalf("remove source folder: %v", err)
	}

	if err := fs.RenameAgent("Workspace Manager", "Ask Ori"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}

	moved, ok := fs.GetAgent("Ask Ori")
	if !ok || moved.Settings.SystemPrompt != "no folder" {
		t.Fatalf("record did not move: %+v", moved)
	}
	if _, err := os.Stat(filepath.Join(agentsDir, "Ask Ori", "agent_settings.json")); err != nil {
		t.Errorf("the moved record must be persisted: %v", err)
	}
}
