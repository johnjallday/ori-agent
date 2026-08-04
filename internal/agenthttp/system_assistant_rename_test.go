package agenthttp

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func renameTestStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.NewFileStore(
		filepath.Join(t.TempDir(), "agents_index.json"),
		types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return st
}

// The whole point of the rename: "Ori" is the app guide, so the *working*
// assistant must not be called that (PRD FR-28/FR-81).
func TestSystemAssistantIsNotNamedOri(t *testing.T) {
	if systemAssistantAgentName == "Ori" {
		t.Fatal("the working system assistant must not be named Ori; that name belongs to the guide")
	}
	if !isSystemAssistantAgent(systemAssistantAgentName) {
		t.Fatalf("%q should be recognized as the system assistant", systemAssistantAgentName)
	}
	// The old name must stop resolving to the working agent, otherwise the two
	// identities are still conflated.
	if isSystemAssistantAgent("Ori") {
		t.Fatal("Ori must no longer resolve to the working system assistant")
	}
}

// An existing install has an agent named "Ori" on disk with the user's own
// model and prompt. The rename must carry that record forward rather than
// creating a fresh agent beside it and stranding the old one.
func TestLegacyOriIsMigratedForward(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Ori", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Role:         types.RoleOrchestrator,
		Model:        "gpt-4o",
		LLMProvider:  "openai",
		SystemPrompt: "user customized this",
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	migrated, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || migrated == nil {
		t.Fatalf("expected the assistant at %q after migration", systemAssistantAgentName)
	}
	if migrated.Settings.SystemPrompt != "user customized this" {
		t.Errorf("migration lost the user's prompt: %q", migrated.Settings.SystemPrompt)
	}
	if migrated.Settings.Model != "gpt-4o" {
		t.Errorf("migration lost the user's model: %q", migrated.Settings.Model)
	}
	if _, stillThere := st.GetAgent("Ori"); stillThere {
		t.Error("the legacy Ori record should be removed once migrated")
	}
}

func TestLegacyUnderscoreAssistantIsMigratedForward(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("__assistant__", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "ancient",
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	migrated, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || migrated == nil {
		t.Fatal("expected the assistant to be migrated forward")
	}
	if migrated.Settings.SystemPrompt != "ancient" {
		t.Errorf("migration lost the prompt: %q", migrated.Settings.SystemPrompt)
	}
	if _, stillThere := st.GetAgent("__assistant__"); stillThere {
		t.Error("the legacy record should be removed once migrated")
	}
}

// A user may already have their own agent called "Workspace Manager". Migration
// must not overwrite it, and must not destroy the legacy record either — the
// safe outcome is to leave both alone and let the user resolve it.
func TestMigrationDoesNotClobberAUserAgentWithTheSameName(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent(systemAssistantAgentName, &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "MINE — user authored",
	}); err != nil {
		t.Fatalf("seed user agent: %v", err)
	}
	if err := st.CreateAgent("Ori", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "legacy assistant",
	}); err != nil {
		t.Fatalf("seed legacy agent: %v", err)
	}

	if err := migrateLegacySystemAssistantName(st); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mine, _ := st.GetAgent(systemAssistantAgentName)
	if mine.Settings.SystemPrompt != "MINE — user authored" {
		t.Fatalf("migration overwrote the user's own agent: %q", mine.Settings.SystemPrompt)
	}
	if _, ok := st.GetAgent("Ori"); !ok {
		t.Error("the legacy record should be left intact rather than destroyed")
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Ori", &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := range 3 {
		if err := ensureSystemAssistantAgent(st); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if _, ok := st.GetAgent(systemAssistantAgentName); !ok {
		t.Fatal("expected the assistant to survive repeated runs")
	}
	if _, ok := st.GetAgent("Ori"); ok {
		t.Error("the legacy record should not reappear")
	}
}

// Ori's identity lives in the character catalog as a reserved guide entry, not
// as an agent record. This is the assertion behind FR-28: the guide is not in
// the roster because it is not an agent at all.
func TestOriExistsOnlyAsTheReservedGuideIdentity(t *testing.T) {
	st := renameTestStore(t)
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensureSystemAssistantAgent: %v", err)
	}

	for _, name := range st.ListAgents() {
		if name == "Ori" {
			t.Fatal("Ori must not exist as an agent record")
		}
	}

	cat := charactercatalog.MustLoad()
	if cat.Guide().Name != "Ori" {
		t.Fatalf("expected Ori to be the catalog guide, got %q", cat.Guide().Name)
	}
	if cat.IsAssignable(cat.ReservedGuideID) {
		t.Fatal("the guide identity must not be assignable to a working agent")
	}
}
