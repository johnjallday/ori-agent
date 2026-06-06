package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestUpdateWorkspacePersistsDescriptionAndBootstrap(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Launch Ops")

	// A pre-existing user note must be left untouched. The removed sync used to
	// rename intent-style notes ("Workspace Brief") into a canonical
	// "Workspace Description" note and overwrite their content.
	now := time.Now()
	userNote := &session.WorkspaceNote{
		ID:          "user-note-1",
		WorkspaceID: workspaceID,
		Name:        "Workspace Brief",
		Content:     "# Workspace Brief\n\n## Primary Goal\nOld launch brief\n",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := handler.store.CreateNote(context.Background(), userNote); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	body := `{"description":"Ship launch assets","workspace_bootstrap":{"systems":"Keynote, Google Drive","capabilities":"Slide production","context":"Brand guide lives in /Launch/Brand"}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The description and bootstrap (the source of truth the harness reads) persist.
	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := ws.Description; got != "Ship launch assets" {
		t.Fatalf("description = %q, want %q", got, "Ship launch assets")
	}

	bootstrapRaw, ok := ws.SharedData["workspace_bootstrap"]
	if !ok {
		t.Fatalf("expected workspace_bootstrap in shared_data")
	}
	bootstrapMap, ok := bootstrapRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected workspace_bootstrap map, got %T", bootstrapRaw)
	}
	for key, want := range map[string]string{
		"goal":         "Ship launch assets",
		"systems":      "Keynote, Google Drive",
		"capabilities": "Slide production",
		"context":      "Brand guide lives in /Launch/Brand",
	} {
		if got := bootstrapMap[key]; got != want {
			t.Fatalf("workspace_bootstrap.%s = %#v, want %q", key, got, want)
		}
	}

	// No canonical "Workspace Description" note is created, and the pre-existing
	// user note keeps its name and content.
	notes, err := handler.store.ListNotesByWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("ListNotesByWorkspace: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected exactly 1 note (the pre-existing user note), got %d: %+v", len(notes), notes)
	}
	if got := notes[0].Name; got != "Workspace Brief" {
		t.Fatalf("user note name = %q, want %q (sync must not rename it)", got, "Workspace Brief")
	}
	updatedNote, err := handler.store.GetNote(context.Background(), userNote.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if !strings.Contains(updatedNote.Content, "Old launch brief") {
		t.Fatalf("user note content changed unexpectedly: %q", updatedNote.Content)
	}
}

func TestHandleWorkspaceRenameReturnsSlugConflictAndRollsBackDB(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	alphaID := createTestWorkspace(t, handler, "Alpha")
	_ = createTestWorkspace(t, handler, "Beta")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+alphaID+"/rename", bytes.NewBufferString(`{"name":"Beta"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	conflict, ok := resp["conflict"].(map[string]any)
	if !ok {
		t.Fatalf("expected conflict payload")
	}
	if got := conflict["type"]; got != "folder_slug" {
		t.Fatalf("conflict.type = %v, want folder_slug", got)
	}
	if got := conflict["requested_slug"]; got != "beta" {
		t.Fatalf("requested_slug = %v, want beta", got)
	}
	if got := conflict["suggested_slug"]; got != "beta-2" {
		t.Fatalf("suggested_slug = %v, want beta-2", got)
	}

	ws, err := handler.store.GetWorkspace(context.Background(), alphaID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := ws.Name; got != "Alpha" {
		t.Fatalf("workspace name after rollback = %q, want %q", got, "Alpha")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+alphaID, nil)
	getW := httptest.NewRecorder()
	handler.HandleWorkspaces(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected hydrated workspace detail 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var getResp map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode workspace detail response: %v", err)
	}
	if got := getResp["folder_slug"]; got != "alpha" {
		t.Fatalf("hydrated folder_slug after rollback = %v, want alpha", got)
	}

	diskWS, err := fileStore.Get(alphaID)
	if err != nil {
		t.Fatalf("fileStore.Get: %v", err)
	}
	if got := diskWS.Name; got != "Alpha" {
		t.Fatalf("workspace.json name after rollback = %q, want %q", got, "Alpha")
	}
	if got := diskWS.FolderSlug; got != "alpha" {
		t.Fatalf("workspace.json slug after rollback = %q, want %q", got, "alpha")
	}

	alphaPath := filepath.Join(baseDir, "alpha", agentworkspace.WorkspaceConfigFile)
	if _, err := os.Stat(alphaPath); err != nil {
		t.Fatalf("expected alpha workspace folder to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "beta-2")); !os.IsNotExist(err) {
		t.Fatalf("expected suggested folder to remain absent, stat err=%v", err)
	}
}
