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

// The migration matrix from the PRD (FR79): every shape of install a user can
// actually be upgrading from, asserted for preservation rather than just for
// "an agent exists afterwards".
func TestSystemAssistantMigrationMatrix(t *testing.T) {
	tests := []struct {
		name string
		// seed returns the records to create, in order.
		seed []string
		// wantAssistantAt is the name the assistant should answer to afterwards.
		wantAssistantAt string
		// wantGone are records that must no longer exist.
		wantGone []string
		// wantKept are records that must still exist untouched.
		wantKept []string
	}{
		{
			name:            "fresh install",
			seed:            nil,
			wantAssistantAt: systemassistant.CanonicalName,
		},
		{
			name:            "current release",
			seed:            []string{"Workspace Manager"},
			wantAssistantAt: systemassistant.CanonicalName,
			wantGone:        []string{"Workspace Manager"},
		},
		{
			name:            "skipped version Ori",
			seed:            []string{"Ori"},
			wantAssistantAt: systemassistant.CanonicalName,
			wantGone:        []string{"Ori"},
		},
		{
			name:            "skipped version __assistant__",
			seed:            []string{"__assistant__"},
			wantAssistantAt: systemassistant.CanonicalName,
			wantGone:        []string{"__assistant__"},
		},
		{
			name:            "already migrated",
			seed:            []string{systemassistant.CanonicalName},
			wantAssistantAt: systemassistant.CanonicalName,
		},
		{
			// Two legacy records: the newest one is the one the user has been
			// using, so it wins and the stale one is left intact rather than
			// destroyed.
			name:            "mixed legacy data",
			seed:            []string{"__assistant__", "Workspace Manager"},
			wantAssistantAt: systemassistant.CanonicalName,
			wantGone:        []string{"Workspace Manager"},
			wantKept:        []string{"__assistant__"},
		},
		{
			// A user-created agent using a retired label must not be swept up.
			name:            "unrelated user agent with a retired-sounding name",
			seed:            []string{"Workspace Assistant", "Workspace Manager"},
			wantAssistantAt: systemassistant.CanonicalName,
			wantGone:        []string{"Workspace Manager"},
			wantKept:        []string{"Workspace Assistant"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := renameTestStore(t)
			for _, name := range tc.seed {
				if err := st.CreateAgent(name, &store.CreateAgentConfig{
					Type:         agent.TypeGeneral,
					SystemPrompt: "seeded:" + name,
				}); err != nil {
					t.Fatalf("seed %q: %v", name, err)
				}
			}

			// Run twice: startup and the settings handler both call this, so
			// idempotency is a live requirement, not a hypothetical (FR54).
			for i := range 2 {
				if err := ensureSystemAssistantAgent(st); err != nil {
					t.Fatalf("ensure run %d: %v", i, err)
				}
			}

			assistant, ok := st.GetAgent(tc.wantAssistantAt)
			if !ok || assistant == nil {
				t.Fatalf("assistant not found at %q; store has %v",
					tc.wantAssistantAt, st.ListAgents())
			}
			if assistant.Metadata == nil ||
				!systemassistant.HasProtectedMarker(assistant.Metadata.Tags) {
				t.Errorf("assistant at %q is not marked protected", tc.wantAssistantAt)
			}

			for _, gone := range tc.wantGone {
				if _, exists := st.GetAgent(gone); exists {
					t.Errorf("record %q should have been migrated away", gone)
				}
			}
			for _, kept := range tc.wantKept {
				ag, exists := st.GetAgent(kept)
				if !exists {
					t.Errorf("record %q must be preserved, store has %v", kept, st.ListAgents())
					continue
				}
				if ag.Settings.SystemPrompt != "seeded:"+kept {
					t.Errorf("record %q was modified: %q", kept, ag.Settings.SystemPrompt)
				}
			}
		})
	}
}

// FR51: the settings a user actually customized have to survive, not just the
// two fields the original rename test happened to check.
func TestMigrationPreservesEveryCustomizedSetting(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Role:         types.RoleOrchestrator,
		Model:        "claude-opus-5",
		LLMProvider:  "anthropic",
		SystemPrompt: "my own prompt",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	seeded, _ := st.GetAgent("Workspace Manager")
	seeded.Settings.Temperature = 0.25
	seeded.Capabilities = []string{"planning", "routing"}
	seeded.Status = types.AgentStatusActive
	seeded.Metadata = &types.AgentMetadata{
		Description: "my description",
		Tags:        []string{"mine", "custom"},
		Favorite:    true,
	}
	if err := st.SetAgent("Workspace Manager", seeded); err != nil {
		t.Fatalf("customize: %v", err)
	}

	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	got, ok := st.GetAgent(systemassistant.CanonicalName)
	if !ok || got == nil {
		t.Fatal("assistant missing after migration")
	}
	if got.Settings.SystemPrompt != "my own prompt" {
		t.Errorf("prompt = %q", got.Settings.SystemPrompt)
	}
	if got.Settings.Model != "claude-opus-5" || got.Settings.Provider != "anthropic" {
		t.Errorf("model/provider = %q/%q", got.Settings.Model, got.Settings.Provider)
	}
	if got.Settings.Temperature != 0.25 {
		t.Errorf("temperature = %v", got.Settings.Temperature)
	}
	if len(got.Capabilities) != 2 {
		t.Errorf("capabilities = %v", got.Capabilities)
	}
	if got.Metadata == nil || got.Metadata.Description != "my description" || !got.Metadata.Favorite {
		t.Errorf("metadata = %+v", got.Metadata)
	}
	// User tags are additive with the system ones, never replaced.
	for _, want := range []string{"mine", "custom"} {
		if !containsNormalized(got.Metadata.Tags, want) {
			t.Errorf("user tag %q was dropped: %v", want, got.Metadata.Tags)
		}
	}
}

// FR60/FR9: an upgrade interrupted after the folder moved but before anything
// else finished must converge on the next startup rather than double-migrating.
func TestMigrationRecoversFromAnInterruptedUpgrade(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "agents_index.json")
	agentsDir := filepath.Join(dir, "agents")

	first, err := store.NewFileStore(indexPath, types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := first.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		SystemPrompt: "survivor",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := ensureSystemAssistantAgent(first); err != nil {
		t.Fatalf("first ensure: %v", err)
	}

	// Simulate a crash that left a stale legacy folder behind on disk after the
	// record had already moved.
	staleDir := filepath.Join(agentsDir, "Workspace Manager")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("seed stale folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "agent_settings.json"),
		[]byte(`{"type":"general","Settings":{"system_prompt":"stale"}}`), 0o644); err != nil {
		t.Fatalf("seed stale settings: %v", err)
	}

	// A fresh boot reloads from disk and sees both.
	second, err := store.NewFileStore(indexPath, types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := ensureSystemAssistantAgent(second); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	got, ok := second.GetAgent(systemassistant.CanonicalName)
	if !ok || got == nil {
		t.Fatalf("assistant lost after recovery; store has %v", second.ListAgents())
	}
	if got.Settings.SystemPrompt != "survivor" {
		t.Errorf("recovery overwrote the migrated record with stale data: %q",
			got.Settings.SystemPrompt)
	}
}

// FR52/FR57: nothing about the migration may depend on rewriting stored
// references, so a workspace roster entry written under the old name must still
// resolve after the assistant has moved.
func TestPersistedLegacyReferenceStillResolvesAfterMigration(t *testing.T) {
	st := renameTestStore(t)
	if err := st.CreateAgent("Workspace Manager", &store.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// This is what a workspace's entry_agent_name still says on disk.
	resolved, name, ok := store.ResolveAgent(st, "Workspace Manager")
	if !ok || resolved == nil {
		t.Fatal("a persisted legacy reference stopped resolving after migration")
	}
	if name != systemassistant.CanonicalName {
		t.Errorf("resolved to %q, want %q", name, systemassistant.CanonicalName)
	}
}
