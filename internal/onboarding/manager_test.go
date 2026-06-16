package onboarding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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

	legacyState := map[string]any{
		"onboarding": map[string]any{
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
	if assistantName != DefaultAssistantName {
		t.Fatalf("expected default assistant name %s, got %q", DefaultAssistantName, assistantName)
	}
}

func TestManager_TimezonePersistenceAndProfileWriteThrough(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)

	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)
	mgr.SetUserStore(store)
	if err := mgr.SetNames("Jules", "Ori"); err != nil {
		t.Fatalf("SetNames: %v", err)
	}
	if err := mgr.SetTimezone("America/New_York"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}

	got, err := store.Get(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("Get profile: %v", err)
	}
	if got.DisplayName != "Jules" || got.Timezone != "America/New_York" {
		t.Fatalf("expected name/timezone write-through, got %#v", got)
	}

	reloaded := NewManager(statePath)
	if timezone := reloaded.GetTimezone(); timezone != "America/New_York" {
		t.Fatalf("expected persisted timezone, got %q", timezone)
	}
}

func TestManager_SeedLocalUserProfileFromOnboardingState(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)

	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)
	if err := mgr.SetNames("Jules", "Ori"); err != nil {
		t.Fatalf("SetNames: %v", err)
	}
	if err := mgr.SetTimezone("America/New_York"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}
	if err := mgr.SetUserProfile(&types.UserProfile{
		PrimaryCategory: "developer",
		Specializations: []string{"Go developer", "Tooling"},
	}); err != nil {
		t.Fatalf("SetUserProfile: %v", err)
	}
	mgr.SetUserStore(store)
	if err := mgr.SeedLocalUserProfile(ctx); err != nil {
		t.Fatalf("SeedLocalUserProfile: %v", err)
	}

	got, err := store.Get(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("Get profile: %v", err)
	}
	if got.DisplayName != "Jules" || got.Timezone != "America/New_York" || got.RoleCategory != "developer" {
		t.Fatalf("seed did not populate expected fields: %#v", got)
	}
	if len(got.Specializations) != 2 {
		t.Fatalf("expected specializations to seed, got %#v", got.Specializations)
	}
}

func TestManager_SeedLocalUserProfileDoesNotOverwriteExistingProfile(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)
	if err := store.Upsert(ctx, &userprofile.UserProfile{
		ID:          userprofile.LocalUserID,
		DisplayName: "Existing",
		Timezone:    "America/Los_Angeles",
	}); err != nil {
		t.Fatalf("seed existing profile: %v", err)
	}

	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)
	if err := mgr.SetNames("Jules", "Ori"); err != nil {
		t.Fatalf("SetNames: %v", err)
	}
	if err := mgr.SetTimezone("America/New_York"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}
	mgr.SetUserStore(store)
	if err := mgr.SeedLocalUserProfile(ctx); err != nil {
		t.Fatalf("SeedLocalUserProfile: %v", err)
	}

	got, err := store.Get(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("Get profile: %v", err)
	}
	if got.DisplayName != "Existing" || got.Timezone != "America/Los_Angeles" {
		t.Fatalf("existing profile should not be overwritten, got %#v", got)
	}
}

func TestManager_NotesOpenBehavior_DefaultsToModal(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)
	if got := mgr.GetNotesOpenBehavior(); got != "modal" {
		t.Fatalf("expected default notes_open_behavior 'modal', got %q", got)
	}
}

func TestManager_NotesOpenBehavior_RoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app_state.json")
	mgr := NewManager(statePath)

	if err := mgr.SetNotesOpenBehavior("page"); err != nil {
		t.Fatalf("SetNotesOpenBehavior(page) failed: %v", err)
	}
	if got := mgr.GetNotesOpenBehavior(); got != "page" {
		t.Fatalf("expected 'page', got %q", got)
	}

	if err := mgr.SetNotesOpenBehavior("page-new-tab"); err != nil {
		t.Fatalf("SetNotesOpenBehavior(page-new-tab) failed: %v", err)
	}

	// Reload should preserve the value.
	reloaded := NewManager(statePath)
	if got := reloaded.GetNotesOpenBehavior(); got != "page-new-tab" {
		t.Fatalf("expected persisted 'page-new-tab', got %q", got)
	}
}

func TestManager_NotesOpenBehavior_RejectsInvalid(t *testing.T) {
	mgr := NewManager(filepath.Join(t.TempDir(), "app_state.json"))
	if err := mgr.SetNotesOpenBehavior("inline"); err == nil {
		t.Fatalf("expected error for invalid value, got nil")
	}
	if got := mgr.GetNotesOpenBehavior(); got != "modal" {
		t.Fatalf("expected fallback to 'modal' after rejection, got %q", got)
	}
}
