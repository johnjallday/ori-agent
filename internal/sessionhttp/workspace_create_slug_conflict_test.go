package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCreateWorkspaceReturnsSlugConflictWithSuggestion(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	firstID := createTestWorkspace(t, handler, "Spain Trip")
	firstPath := filepath.Join(baseDir, "spain-trip", agentworkspace.WorkspaceConfigFile)
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("expected first workspace folder to exist: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(`{"name":"Spain Trip"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	conflict, ok := resp["conflict"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected conflict payload in response")
	}
	if got := conflict["type"]; got != "folder_slug" {
		t.Fatalf("expected conflict type folder_slug, got %v", got)
	}
	if got := conflict["requested_slug"]; got != "spain-trip" {
		t.Fatalf("expected requested_slug spain-trip, got %v", got)
	}
	if got := conflict["suggested_slug"]; got != "spain-trip-2" {
		t.Fatalf("expected suggested_slug spain-trip-2, got %v", got)
	}
	if got := conflict["location"]; got != baseDir {
		t.Fatalf("expected conflict location %q, got %v", baseDir, got)
	}

	workspaces, err := handler.store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("failed to list workspaces: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("expected only the original workspace to remain, got %d", len(workspaces))
	}
	if workspaces[0].ID != firstID {
		t.Fatalf("expected original workspace %q to remain, got %q", firstID, workspaces[0].ID)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "spain-trip-2")); !os.IsNotExist(err) {
		t.Fatalf("expected suggested workspace folder to not exist yet, stat err=%v", err)
	}
}

func TestCreateWorkspaceAcceptsProvidedSuggestedSlug(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	createTestWorkspace(t, handler, "Spain Trip")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(`{"name":"Spain Trip","folder_slug":"spain-trip-2"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	folder, ok := resp["folder"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected folder in response")
	}
	if got := folder["folder_slug"]; got != "spain-trip-2" {
		t.Fatalf("expected folder_slug spain-trip-2, got %v", got)
	}

	secondPath := filepath.Join(baseDir, "spain-trip-2", agentworkspace.WorkspaceConfigFile)
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("expected second workspace folder to exist: %v", err)
	}
}
