package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestListWorkspacesHydratesDesignationField asserts that a folder-store
// workspace with a personal_hq designation surfaces "designation":"personal_hq"
// in the API list payload, and that a plain workspace omits the field
// entirely (empty value + omitempty). designation has no SQLite column, so
// this exercises the same read-time hydration path as project_path.
func TestListWorkspacesHydratesDesignationField(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	hqID := createTestWorkspace(t, handler, "Designated HQ")
	plainID := createTestWorkspace(t, handler, "Plain Workspace")

	hqWS, err := fileStore.Get(hqID)
	if err != nil {
		t.Fatalf("failed to load workspace %q from file store: %v", hqID, err)
	}
	hqWS.Designation = "personal_hq"
	if err := fileStore.Save(hqWS); err != nil {
		t.Fatalf("failed to save designated HQ fixture: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var hqEntry, plainEntry map[string]any
	for _, entry := range resp.Workspaces {
		switch entry["id"] {
		case hqID:
			hqEntry = entry
		case plainID:
			plainEntry = entry
		}
	}
	if hqEntry == nil {
		t.Fatalf("designated HQ workspace %q not found in list", hqID)
	}
	if plainEntry == nil {
		t.Fatalf("plain workspace %q not found in list", plainID)
	}

	if got := hqEntry["designation"]; got != "personal_hq" {
		t.Errorf(`designated HQ: designation = %v, want "personal_hq"`, got)
	}
	if _, present := plainEntry["designation"]; present {
		t.Errorf("plain workspace: designation = %v, want field omitted", plainEntry["designation"])
	}
}
