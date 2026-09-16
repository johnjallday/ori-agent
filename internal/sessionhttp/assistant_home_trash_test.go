package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestReviewedHomeRemovalIsUndoneByRestore covers the round trip the delete
// dialog relies on: a reviewed Home removal moves the Home to the system Trash
// (folder and row), and POST /api/workspaces/{id}/restore brings it back as a
// Home again, empty, with the retained project still standalone. Rooted under
// $HOME so the trash move stays on one volume, like the group trash tests.
func TestReviewedHomeRemovalIsUndoneByRestore(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("path-based trash round trip not supported on %s", runtime.GOOS)
	}

	handler, cleanup := createTestHandler(t)
	defer cleanup()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	baseDir, err := os.MkdirTemp(home, ".ws-home-trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(baseDir) }()

	fs, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	handler.SetWorkspaceStore(fs)
	store := agentworkspace.NewSyncStore(session.NewWorkspaceStoreAdapter(handler.store), fs)
	handler.SetWorkspaceTaskStore(store)

	declaration := &agentworkspace.AssistantProgramDeclaration{
		SchemaVersion: agentworkspace.AssistantProgramSchemaVersion,
		ID:            "trash-guide", StationName: "Trash Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []agentworkspace.AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Scope: agentworkspace.AssistantRoleScopeHome, Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Coordinate."},
			{ID: "lead", Label: "Lead", Scope: agentworkspace.AssistantRoleScopeProject, Required: true, Primary: true, Role: "orchestrator", SystemPrompt: "Lead."},
		},
		Stages:     []agentworkspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: agentworkspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Find stable preferences."},
	}
	project := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Trash Project"})
	project.OwnerUserID = "local"
	project.SetTemplateProvenance(&agentworkspace.TemplateProvenance{
		TemplateID:       "plugin:test:project",
		PluginOwner:      &agentworkspace.PluginTemplateOwner{PluginID: "test", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: declaration,
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	programs := agentworkspace.NewAssistantProgramStore(store)
	station, _, err := programs.EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	station, _ = store.Get(station.ID)
	key := station.GetAssistantProgramState().Key
	originalPath, err := fs.GetFolderPath(station.ID)
	if err != nil {
		t.Fatalf("Home folder path: %v", err)
	}

	// Review and commit exactly as the delete dialog does.
	reviewRecorder := httptest.NewRecorder()
	handler.ReviewAssistantHomeRemoval(reviewRecorder, assistantProgramRequest(http.MethodPost, "/remove-home/review", station.ID,
		`{"state_revision":`+strconv.FormatInt(station.GetAssistantProgramState().StateRevision, 10)+`}`))
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("Home removal review = %d: %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	var review agentworkspace.AssistantHomeRemovalReview
	if err := json.Unmarshal(reviewRecorder.Body.Bytes(), &review); err != nil || review.Token == "" {
		t.Fatalf("Home removal review body = %#v, %v", review, err)
	}
	commitRecorder := httptest.NewRecorder()
	handler.CommitAssistantHomeRemoval(commitRecorder, assistantProgramRequest(http.MethodPost, "/remove-home/commit", station.ID, `{"token":"`+review.Token+`"}`))
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("Home removal commit = %d: %s", commitRecorder.Code, commitRecorder.Body.String())
	}
	var receipt agentworkspace.AssistantHomeRemovalReceipt
	if err := json.Unmarshal(commitRecorder.Body.Bytes(), &receipt); err != nil || !receipt.Trashed || receipt.RetainedProjects != 1 {
		t.Fatalf("Home removal receipt = %#v, %v", receipt, err)
	}

	ctx := context.Background()
	row, err := handler.store.GetWorkspace(ctx, station.ID)
	if err != nil {
		t.Fatalf("Home row should survive trashing, got err=%v", err)
	}
	if row.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("Home status = %q, want trashed", row.Status)
	}
	if _, trashedPath := workspaceTrashPaths(row); trashedPath != "" {
		defer func() { _ = os.RemoveAll(trashedPath) }()
	} else {
		t.Fatal("trashed Home carries no trash path")
	}
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("Home folder should have moved to the Trash, stat err=%v", err)
	}
	if _, err := programs.FindStation(key); err == nil {
		t.Fatal("a trashed Home must not answer as the program's station")
	}
	retained, err := store.Get(project.ID)
	if err != nil || retained.GetAssistantProjectLink() != nil || retained.ParentID != "" {
		t.Fatalf("retained project = %#v, %v", retained, err)
	}

	// Undo: the generic restore endpoint the dialog's Undo already calls.
	restoreRecorder := httptest.NewRecorder()
	handler.HandleWorkspaces(restoreRecorder, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+station.ID+"/restore", nil))
	if restoreRecorder.Code != http.StatusOK {
		t.Fatalf("restore = %d: %s", restoreRecorder.Code, restoreRecorder.Body.String())
	}
	var restoreResponse map[string]any
	if err := json.Unmarshal(restoreRecorder.Body.Bytes(), &restoreResponse); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if restored, _ := restoreResponse["home_restored"].(bool); !restored {
		t.Fatalf("restore should report the Home came back as a Home: %v", restoreResponse)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("Home folder should be back at %s: %v", originalPath, err)
	}
	restoredHome, err := programs.FindStation(key)
	if err != nil || restoredHome == nil || restoredHome.ID != station.ID {
		t.Fatalf("restored Home lookup = %#v, %v", restoredHome, err)
	}
	if restoredHome.Status != agentworkspace.StatusActive {
		t.Fatalf("restored Home status = %q, want active", restoredHome.Status)
	}
	state := restoredHome.GetAssistantProgramState()
	if state == nil || len(state.LinkedProjectIDs) != 0 || state.Declaration == nil || state.Declaration.ID != declaration.ID {
		t.Fatalf("restored Home state = %#v", state)
	}
	if _, stashed := restoredHome.GetSharedData(agentworkspace.RemovedAssistantProgramStateKey); stashed {
		t.Fatal("restore left the program-state stash behind")
	}
	if _, trashMeta := restoredHome.GetSharedData(agentworkspace.TrashSharedDataKey); trashMeta {
		t.Fatal("restore left the trash metadata behind")
	}
	// The retained project is still standalone; Undo does not re-link it.
	retained, err = store.Get(project.ID)
	if err != nil || retained.GetAssistantProjectLink() != nil || retained.ParentID != "" {
		t.Fatalf("retained project after restore = %#v, %v", retained, err)
	}
}
