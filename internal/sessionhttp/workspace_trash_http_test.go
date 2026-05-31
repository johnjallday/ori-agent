package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func trashListIDs(t *testing.T, handler *Handler) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/trash", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from trash listing, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode trash listing: %v", err)
	}
	ids := make([]string, 0, len(resp.Workspaces))
	for _, ws := range resp.Workspaces {
		ids = append(ids, ws.ID)
	}
	return ids
}

func activeWorkspaceIDs(t *testing.T, handler *Handler) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from workspace listing, got %d: %s", w.Code, w.Body.String())
	}
	// The list endpoint returns {"workspaces": [...], "folders": [...]}.
	var resp struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode workspace listing: %v", err)
	}
	ids := make([]string, 0, len(resp.Workspaces))
	for _, ws := range resp.Workspaces {
		ids = append(ids, ws.ID)
	}
	return ids
}

// TestWorkspaceTrashRoundTrip exercises the trash → list → restore HTTP dispatch
// through HandleWorkspaces: a default delete trashes (and hides) the workspace,
// it appears in the trash listing, and restoring brings it back to the active set.
func TestWorkspaceTrashRoundTrip(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	id := createTestWorkspace(t, handler, "Round Trip")

	// Default delete moves the workspace to Trash.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"?confirm=true", nil)
	delResp := httptest.NewRecorder()
	handler.HandleWorkspaces(delResp, delReq)
	if delResp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on trash, got %d: %s", delResp.Code, delResp.Body.String())
	}

	// It should be hidden from the active list and present in Trash.
	if slices.Contains(activeWorkspaceIDs(t, handler), id) {
		t.Errorf("trashed workspace should not appear in active list")
	}
	if !slices.Contains(trashListIDs(t, handler), id) {
		t.Errorf("trashed workspace should appear in trash listing")
	}

	// Restore it.
	restoreReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/restore", nil)
	restoreResp := httptest.NewRecorder()
	handler.HandleWorkspaces(restoreResp, restoreReq)
	if restoreResp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on restore, got %d: %s", restoreResp.Code, restoreResp.Body.String())
	}

	// Back in the active list, gone from Trash.
	if !slices.Contains(activeWorkspaceIDs(t, handler), id) {
		t.Errorf("restored workspace should appear in active list")
	}
	if slices.Contains(trashListIDs(t, handler), id) {
		t.Errorf("restored workspace should no longer be in trash listing")
	}
}

// TestWorkspacePermanentDeleteFromTrash verifies the permanent path removes the
// workspace entirely (not in active list, not in Trash).
func TestWorkspacePermanentDeleteFromTrash(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	id := createTestWorkspace(t, handler, "Permanent")

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"?confirm=true&permanent=true", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on permanent delete, got %d: %s", w.Code, w.Body.String())
	}

	if slices.Contains(activeWorkspaceIDs(t, handler), id) {
		t.Errorf("permanently deleted workspace should not be in active list")
	}
	if slices.Contains(trashListIDs(t, handler), id) {
		t.Errorf("permanently deleted workspace should not be in trash listing")
	}
}
