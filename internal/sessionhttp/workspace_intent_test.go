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

func TestUpdateWorkspaceSyncsCanonicalDescriptionNote(t *testing.T) {
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
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	now := time.Now()
	legacyNote := &session.WorkspaceNote{
		ID:          "legacy-workspace-brief",
		WorkspaceID: workspaceID,
		Name:        "Workspace Brief",
		Content:     "# Workspace Brief\n\n## Primary Goal\nOld launch brief\n",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := handler.store.CreateNote(context.Background(), legacyNote); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	handler.syncNoteToFile(legacyNote)

	oldNotePath := filepath.Join(folderPath, agentworkspace.NotesDir, agentworkspace.NoteFilename("Workspace Brief", legacyNote.ID))
	if _, err := os.Stat(oldNotePath); err != nil {
		t.Fatalf("expected legacy note file before update: %v", err)
	}

	body := `{"description":"Ship launch assets","workspace_bootstrap":{"systems":"Keynote, Google Drive","capabilities":"Slide production","context":"Brand guide lives in /Launch/Brand"}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+workspaceID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

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
	if got := bootstrapMap["goal"]; got != "Ship launch assets" {
		t.Fatalf("workspace_bootstrap.goal = %#v, want %q", got, "Ship launch assets")
	}
	if got := bootstrapMap["systems"]; got != "Keynote, Google Drive" {
		t.Fatalf("workspace_bootstrap.systems = %#v, want %q", got, "Keynote, Google Drive")
	}
	if got := bootstrapMap["capabilities"]; got != "Slide production" {
		t.Fatalf("workspace_bootstrap.capabilities = %#v, want %q", got, "Slide production")
	}
	if got := bootstrapMap["context"]; got != "Brand guide lives in /Launch/Brand" {
		t.Fatalf("workspace_bootstrap.context = %#v, want %q", got, "Brand guide lives in /Launch/Brand")
	}

	updatedNote, err := handler.store.GetNote(context.Background(), legacyNote.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got := updatedNote.Name; got != workspaceDescriptionNoteName {
		t.Fatalf("note name = %q, want %q", got, workspaceDescriptionNoteName)
	}
	for _, fragment := range []string{
		"# Workspace Description",
		"## Description\nShip launch assets",
		"## Apps and Systems\nKeynote, Google Drive",
		"## Key Files or Context\nBrand guide lives in /Launch/Brand",
		"## Special Capabilities or Workflows\nSlide production",
	} {
		if !strings.Contains(updatedNote.Content, fragment) {
			t.Fatalf("expected note content to include %q, got %q", fragment, updatedNote.Content)
		}
	}

	newNotePath := filepath.Join(folderPath, agentworkspace.NotesDir, agentworkspace.NoteFilename(workspaceDescriptionNoteName, legacyNote.ID))
	if _, err := os.Stat(newNotePath); err != nil {
		t.Fatalf("expected canonical note file after update: %v", err)
	}
	if _, err := os.Stat(oldNotePath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy note file to be removed, stat err=%v", err)
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
