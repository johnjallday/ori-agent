package agenthttp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// renameTestStoreWithDir is renameTestStore plus the agents/ directory, for the
// tests that have to inspect what actually landed on disk.
func renameTestStoreWithDir(t *testing.T) (store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.NewFileStore(
		filepath.Join(dir, "agents_index.json"),
		types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return st, filepath.Join(dir, "agents")
}

// Issue #350 / FR48: a fresh install must come up as "Ask Ori" without ever
// creating a "Workspace Manager" record first (FR59).
func TestFreshInstallCreatesAskOri(t *testing.T) {
	st := renameTestStore(t)

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	if _, ok := st.GetAgent(systemassistant.CanonicalName); !ok {
		t.Fatalf("expected a %q record on a fresh install, got %v",
			systemassistant.CanonicalName, st.ListAgents())
	}
	for _, legacy := range systemassistant.LegacyNames {
		if _, ok := st.GetAgent(legacy); ok {
			t.Errorf("fresh install must not create the legacy record %q", legacy)
		}
	}
}

// The package-local constant must be the shared contract, not a second copy of
// the string (FR49).
func TestSystemAssistantNameComesFromTheSharedContract(t *testing.T) {
	if systemAssistantAgentName != systemassistant.CanonicalName {
		t.Fatalf("systemAssistantAgentName = %q, want the shared canonical %q",
			systemAssistantAgentName, systemassistant.CanonicalName)
	}
}

// FR50/FR57: "Workspace Manager" is what existing installs have on disk. It must
// migrate forward carrying the user's own configuration.
func TestWorkspaceManagerInstallMigratesToAskOri(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Role:         types.RoleOrchestrator,
		Model:        "claude-opus-5",
		LLMProvider:  "anthropic",
		SystemPrompt: "user customized this",
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	migrated, ok := st.GetAgent(systemassistant.CanonicalName)
	if !ok || migrated == nil {
		t.Fatalf("expected the assistant at %q, got %v",
			systemassistant.CanonicalName, st.ListAgents())
	}
	if migrated.Settings.SystemPrompt != "user customized this" {
		t.Errorf("migration lost the user's prompt: %q", migrated.Settings.SystemPrompt)
	}
	if migrated.Settings.Model != "claude-opus-5" {
		t.Errorf("migration lost the user's model: %q", migrated.Settings.Model)
	}
	if migrated.Settings.Provider != "anthropic" {
		t.Errorf("migration lost the user's provider: %q", migrated.Settings.Provider)
	}
	if _, stillThere := st.GetAgent("Workspace Manager"); stillThere {
		t.Error("the legacy Workspace Manager record should be gone once migrated")
	}
}

// FR49: protection must key on the canonical identity, not on whichever raw
// literal a given handler happened to hardcode.
func TestProtectionRecognizesOnlyTheCanonicalIdentity(t *testing.T) {
	if !isSystemAssistantAgent(systemassistant.CanonicalName) {
		t.Errorf("%q must be protected", systemassistant.CanonicalName)
	}
	// A retired name is a migration concern, not a live protected identity — a
	// user is free to own an agent by that name after migrating.
	for _, legacy := range systemassistant.LegacyNames {
		if isSystemAssistantAgent(legacy) {
			t.Errorf("legacy name %q must not itself be treated as the protected agent", legacy)
		}
	}
	for _, unrelated := range []string{"Workspace Assistant", "Task Assistant", "Research Buddy"} {
		if isSystemAssistantAgent(unrelated) {
			t.Errorf("%q must not be treated as the protected agent", unrelated)
		}
	}
}

// FR55: the migrated record carries the durable marker, so later code can tell
// it apart from a user-created agent with the same name.
func TestMigratedAssistantCarriesTheProtectedMarker(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	migrated, ok := st.GetAgent(systemassistant.CanonicalName)
	if !ok || migrated == nil || migrated.Metadata == nil {
		t.Fatal("expected a migrated record with metadata")
	}
	if !systemassistant.HasProtectedMarker(migrated.Metadata.Tags) {
		t.Fatalf("migrated assistant is missing the protected marker: %v", migrated.Metadata.Tags)
	}
}

// FR51: the migration must carry the agent's whole folder, not just the fields
// that happen to round-trip through agent.Agent. The old SetAgent+DeleteAgent
// pair passed the record tests while silently deleting these files.
func TestMigrationPreservesPerAgentSidecarState(t *testing.T) {
	st, agentsDir := renameTestStoreWithDir(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	legacyDir := filepath.Join(agentsDir, "Workspace Manager")
	skillDir := filepath.Join(legacyDir, "skills", "reaper-control")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("installed skill"), 0o644); err != nil {
		t.Fatalf("seed skill body: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "skills_state.json"),
		[]byte(`{"skills":{"reaper-control":{"enabled":true,"trusted":true}}}`), 0o644); err != nil {
		t.Fatalf("seed skills state: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	canonicalDir := filepath.Join(agentsDir, systemassistant.CanonicalName)
	body, err := os.ReadFile(filepath.Join(canonicalDir, "skills", "reaper-control", "SKILL.md"))
	if err != nil {
		t.Fatalf("the per-agent installed skill was lost in migration: %v", err)
	}
	if string(body) != "installed skill" {
		t.Errorf("skill body = %q", body)
	}

	state, err := os.ReadFile(filepath.Join(canonicalDir, "skills_state.json"))
	if err != nil {
		t.Fatalf("skills_state.json was lost in migration: %v", err)
	}
	if len(state) == 0 {
		t.Error("skills_state.json survived but is empty")
	}
}

// FR55: the user's own "Ask Ori" is untouchable. The assistant stays under its
// legacy name rather than hijacking that record or stranding its own settings.
func TestUserCreatedAskOriBlocksMigrationWithoutDataLoss(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent(systemassistant.CanonicalName, &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "MINE — user authored",
	}); err != nil {
		t.Fatalf("seed user agent: %v", err)
	}
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "the real assistant",
		Model:        "claude-opus-5",
	}); err != nil {
		t.Fatalf("seed system agent: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	mine, ok := st.GetAgent(systemassistant.CanonicalName)
	if !ok || mine == nil {
		t.Fatal("the user's agent disappeared")
	}
	if mine.Settings.SystemPrompt != "MINE — user authored" {
		t.Errorf("the user's agent was overwritten: %q", mine.Settings.SystemPrompt)
	}
	if mine.Metadata != nil && systemassistant.HasProtectedMarker(mine.Metadata.Tags) {
		t.Error("the user's agent must not be marked as the protected system assistant")
	}

	system, ok := st.GetAgent("Workspace Manager")
	if !ok || system == nil {
		t.Fatal("the real system assistant was destroyed by the collision path")
	}
	if system.Settings.SystemPrompt != "the real assistant" || system.Settings.Model != "claude-opus-5" {
		t.Errorf("the system assistant's settings were not preserved: %+v", system.Settings)
	}
	// Both records survive and are told apart by the marker, not by name.
	if system.Metadata == nil || !systemassistant.HasProtectedMarker(system.Metadata.Tags) {
		t.Error("the system assistant should still be marked as protected under its legacy name")
	}
}

// Repeated startups (and the settings handler's re-run) must not duplicate,
// re-move, or re-resolve anything (FR54).
func TestRepeatedEnsureIsStable(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "carried forward",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := range 3 {
		if err := ensureSystemAssistantAgent(st); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	names := st.ListAgents()
	if len(names) != 1 || names[0] != systemassistant.CanonicalName {
		t.Fatalf("expected exactly one %q record, got %v", systemassistant.CanonicalName, names)
	}
	ag, _ := st.GetAgent(systemassistant.CanonicalName)
	if ag.Settings.SystemPrompt != "carried forward" {
		t.Errorf("prompt drifted across runs: %q", ag.Settings.SystemPrompt)
	}
}

func TestFreshInstallCarriesTheProtectedMarker(t *testing.T) {
	st := renameTestStore(t)
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	created, ok := st.GetAgent(systemassistant.CanonicalName)
	if !ok || created == nil || created.Metadata == nil {
		t.Fatal("expected a created record with metadata")
	}
	if !systemassistant.HasProtectedMarker(created.Metadata.Tags) {
		t.Fatalf("fresh assistant is missing the protected marker: %v", created.Metadata.Tags)
	}
}
