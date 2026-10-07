package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func profileReceiptHome(profile *workspace.HomeProfile) *workspace.Workspace {
	home := &workspace.Workspace{ID: "music-home", Name: "Music Production Home", FolderSlug: "music-home", OwnerUserID: "local"}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
		HomeProfile:   profile,
	})
	return home
}

func TestProfileReceiptRowLinksToTheHomesProfileCard(t *testing.T) {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	profile := &workspace.HomeProfile{
		SchemaVersion: workspace.HomeProfileSchemaVersion, Revision: 1, DetectedAt: &at,
		DeclaredBy: workspace.HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0", Title: "Your studio",
			Labels: map[string]string{workspace.HomeProfileKindMainApp: "Main DAW"}},
		Apps: []workspace.HomeProfileApp{
			{ID: "reaper", Name: "REAPER", Detected: true, DetectedAt: &at},
			{ID: "logic-pro", Name: "Logic Pro", Detected: true, DetectedAt: &at},
		},
	}
	row, ok := profileReceiptRow(profileReceiptHome(profile))
	if !ok || row.Kind != personalassistant.FolderPlanProfile || row.Name != "Your studio" ||
		row.Detail != "REAPER and Logic Pro · pick your main DAW" || row.Route != "/workspaces/music-home/assistant#homeProfilePanel" {
		t.Fatalf("row = %+v ok = %v", row, ok)
	}
	// A Home without a profile, or one with nothing found, adds no row.
	if _, ok := profileReceiptRow(profileReceiptHome(nil)); ok {
		t.Fatal("a Home without a profile added a receipt row")
	}
	profile.Apps = nil
	if _, ok := profileReceiptRow(profileReceiptHome(profile)); ok {
		t.Fatal("a profile with nothing found added a receipt row")
	}
}

func TestFolderProfileStepIsUnavailableWithoutTheHandlerAndPlansNothingWithoutABuilder(t *testing.T) {
	if err := (folderProfileStep{}).Setup(context.Background(), "music-home", true); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("err = %v, want setup unavailable", err)
	}
	host := &folderSetupHost{}
	if facts := host.profileFacts(reviewedintegration.HomeProviders()[0], "ori_reaper", nil); facts != nil {
		t.Fatalf("a host without plugins produced a profile line: %+v", facts)
	}
}
