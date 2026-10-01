package workspace_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The standing staffing consent is a new field inside the Home envelope. A
// field the SQLite mirror has no column for can be dropped silently, so prove
// it survives both mirrors, a database restart and a folder re-read.
func TestProjectStaffingConsentSurvivesBothMirrorsAndARestart(t *testing.T) {
	root := t.TempDir()
	files, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db, err := database.Open(context.Background(), &database.Config{Path: dbPath, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	mirrored := workspace.NewSyncStore(primary, files)

	home := &workspace.Workspace{ID: "music-home", Name: "Music Home", OwnerUserID: "local", Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, StateRevision: 1, PluginAvailable: true,
		Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	})
	if err := mirrored.Save(home); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	consents := workspace.NewProjectStaffingConsents(mirrored)
	if _, err := consents.Grant(home.ID, workspace.ProjectStaffingConsentGrant{
		Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: "offer-1", PluginID: "ori-reaper",
		BlueprintID: "reaper-song", TeamDigest: digest, RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := consents.ReserveAgent(home.ID, "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if err := consents.RecordAgent(home.ID, "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	check := func(name string, store workspace.Store) {
		t.Helper()
		got, err := workspace.NewProjectStaffingConsents(store).Read(home.ID)
		if err != nil || got == nil || !got.Active() || got.TeamDigest != digest || got.OfferID != "offer-1" ||
			len(got.Roles) != 1 || got.Roles[0].AgentName != "REAPER Assistant" {
			t.Fatalf("%s lost the consent: %+v %v", name, got, err)
		}
	}
	check("primary", primary)
	check("folder", files)
	// The mirrors still agree, so the library and every fenced Home write keep working.
	if !workspace.AssistantProgramLibraryMirrorsAgree(mirrored, mustGet(t, mirrored, home.ID)) {
		t.Fatal("the consent write split the Home mirrors")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(context.Background(), &database.Config{Path: dbPath, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	restarted := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(reopened, 10))
	check("primary after a restart", restarted)
	refolded, err := workspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	check("folder after a restart", refolded)

	// Switching it off is one more fenced write that both mirrors carry.
	if _, err := workspace.NewProjectStaffingConsents(workspace.NewSyncStore(restarted, refolded)).Revoke(home.ID); err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]workspace.Store{"primary": restarted, "folder": refolded} {
		got, err := workspace.NewProjectStaffingConsents(store).Read(home.ID)
		if err != nil || got == nil || got.RevokedAt == nil || got.Roles[0].AgentName != "REAPER Assistant" {
			t.Fatalf("%s after revoke: %+v %v", name, got, err)
		}
	}
}

func mustGet(t *testing.T, store workspace.Store, id string) *workspace.Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}
