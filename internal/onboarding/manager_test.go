package onboarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestManager_AssistantProgress_Defaults(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)

	progress := mgr.GetAssistantProgress()
	if progress.Level != 0 {
		t.Errorf("expected default level 0, got %d", progress.Level)
	}
	if progress.Experience != 0 {
		t.Errorf("expected default experience 0, got %d", progress.Experience)
	}
	if progress.Rank == "" {
		t.Error("expected default rank to be set")
	}
	if progress.Unlocks == nil {
		t.Error("expected unlocks to be initialized")
	}
}

func TestManager_AssistantProgress_PersistenceRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)

	err := mgr.SetAssistantProgress(&types.AssistantProgress{
		Level:      4,
		Experience: 320,
		Rank:       "captain",
		Unlocks:    []string{"path-selection", "feed-bonus"},
	})
	if err != nil {
		t.Fatalf("SetAssistantProgress() failed: %v", err)
	}

	reloaded := NewManager(statePath)
	progress := reloaded.GetAssistantProgress()

	if progress.Level != 4 {
		t.Errorf("expected level 4, got %d", progress.Level)
	}
	if progress.Experience != 320 {
		t.Errorf("expected experience 320, got %d", progress.Experience)
	}
	if progress.Rank != "captain" {
		t.Errorf("expected rank captain, got %q", progress.Rank)
	}
	if len(progress.Unlocks) != 2 {
		t.Errorf("expected 2 unlocks, got %d", len(progress.Unlocks))
	}
}

func TestManager_AssistantProgress_BackwardCompatibleLoad(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")

	legacyState := map[string]interface{}{
		"onboarding": map[string]interface{}{
			"completed":       false,
			"current_step":    0,
			"steps_completed": []string{},
		},
		"version": "v0.0.1",
	}
	data, err := json.Marshal(legacyState)
	if err != nil {
		t.Fatalf("failed to marshal legacy state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatalf("failed to write legacy state: %v", err)
	}

	mgr := NewManager(statePath)
	progress := mgr.GetAssistantProgress()

	if progress.Level != 0 {
		t.Errorf("expected default level 0 from legacy load, got %d", progress.Level)
	}
	if progress.Rank == "" {
		t.Error("expected default rank from legacy load")
	}
}

func TestManager_SetNames_PersistenceRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)

	if err := mgr.SetNames("Jules", "Ari"); err != nil {
		t.Fatalf("SetNames() failed: %v", err)
	}

	userName, assistantName := mgr.GetNames()
	if userName != "Jules" {
		t.Fatalf("expected user name Jules, got %q", userName)
	}
	if assistantName != "Ari" {
		t.Fatalf("expected assistant name Ari, got %q", assistantName)
	}

	reloaded := NewManager(statePath)
	userName, assistantName = reloaded.GetNames()
	if userName != "Jules" {
		t.Fatalf("expected persisted user name Jules, got %q", userName)
	}
	if assistantName != "Ari" {
		t.Fatalf("expected persisted assistant name Ari, got %q", assistantName)
	}
}

func TestManager_SetNames_DefaultAssistantName(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)

	if err := mgr.SetNames("Jules", ""); err != nil {
		t.Fatalf("SetNames() failed: %v", err)
	}

	_, assistantName := mgr.GetNames()
	if assistantName != "Assistant" {
		t.Fatalf("expected default assistant name Assistant, got %q", assistantName)
	}
}
