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
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// postWorkspaceRescan runs POST /api/workspaces/rescan and decodes the response.
func postWorkspaceRescan(t *testing.T, handler *Handler) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/rescan", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for rescan, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rescan response: %v", err)
	}
	return resp
}

// listWorkspaceIDs returns the IDs in the GET /api/workspaces response.
func listWorkspaceIDs(t *testing.T, handler *Handler) map[string]int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for workspace list, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Workspaces []session.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}
	ids := make(map[string]int, len(resp.Workspaces))
	for _, ws := range resp.Workspaces {
		ids[ws.ID]++
	}
	return ids
}

func rescanCount(t *testing.T, resp map[string]any, key string) int {
	t.Helper()
	value, ok := resp[key].(float64)
	if !ok {
		t.Fatalf("expected numeric %q in rescan response, got %#v", key, resp[key])
	}
	return int(value)
}

// TestRescanBackgroundCooldownSkips verifies that background rescans (hub page
// loads) honor the server-side cooldown while explicit rescans always run.
func TestRescanBackgroundCooldownSkips(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	rescan := func(background bool) map[string]any {
		t.Helper()
		url := "/api/workspaces/rescan"
		if background {
			url += "?background=1"
		}
		req := httptest.NewRequest(http.MethodPost, url, nil)
		w := httptest.NewRecorder()
		handler.HandleWorkspaces(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for rescan, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode rescan response: %v", err)
		}
		return resp
	}

	if resp := rescan(true); resp["skipped"] == true {
		t.Fatalf("first background rescan should run, got %#v", resp)
	}
	if resp := rescan(true); resp["skipped"] != true {
		t.Fatalf("second background rescan within cooldown should be skipped, got %#v", resp)
	}
	if resp := rescan(false); resp["skipped"] == true {
		t.Fatalf("explicit rescan must ignore the cooldown, got %#v", resp)
	}
}

// TestRescanMarksMissingWorkspaceAndRecreateRestores covers the externally
// deleted folder flow: the rescan hides the stale row instead of leaving it in
// listings, and the sync recreate action recovers it.
func TestRescanMarksMissingWorkspaceAndRecreateRestores(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Vanishing")

	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	// Delete the folder out from under the app, as Finder or another instance
	// sharing the workspaces root would.
	if err := os.RemoveAll(folderPath); err != nil {
		t.Fatalf("RemoveAll workspace folder: %v", err)
	}

	resp := postWorkspaceRescan(t, handler)
	if got := rescanCount(t, resp, "orphaned"); got != 1 {
		t.Fatalf("expected orphaned=1, got %d (%#v)", got, resp)
	}

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.Status != session.WorkspaceStatusMissing {
		t.Fatalf("expected status missing, got %q", ws.Status)
	}

	if ids := listWorkspaceIDs(t, handler); ids[workspaceID] != 0 {
		t.Fatalf("expected missing workspace to be hidden from listings, got %#v", ids)
	}

	// The sync-status panel must still surface it for recovery.
	statusReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/sync-status", nil)
	statusW := httptest.NewRecorder()
	handler.HandleWorkspaces(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync status, got %d: %s", statusW.Code, statusW.Body.String())
	}
	var status agentworkspace.SyncStatus
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode sync status: %v", err)
	}
	foundOrphan := false
	for _, info := range status.Orphaned {
		if info.ID == workspaceID {
			foundOrphan = true
			break
		}
	}
	if !foundOrphan {
		t.Fatalf("expected missing workspace in sync-status orphaned list, got %#v", status.Orphaned)
	}

	// Recreating the folder through the sync flow restores the workspace.
	payload, _ := json.Marshal(map[string]any{"recreate": []string{workspaceID}})
	syncReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/sync", bytes.NewBuffer(payload))
	syncReq.Header.Set("Content-Type", "application/json")
	syncW := httptest.NewRecorder()
	handler.HandleWorkspaces(syncW, syncReq)
	if syncW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync recreate, got %d: %s", syncW.Code, syncW.Body.String())
	}
	var syncResp map[string]any
	if err := json.Unmarshal(syncW.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if got := int(syncResp["recreated"].(float64)); got != 1 {
		t.Fatalf("expected recreated=1, got %d (%#v)", got, syncResp)
	}

	ws, err = handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace after recreate: %v", err)
	}
	if ws.Status == session.WorkspaceStatusMissing {
		t.Fatalf("expected recreate to clear missing status, got %q", ws.Status)
	}
	if ids := listWorkspaceIDs(t, handler); ids[workspaceID] != 1 {
		t.Fatalf("expected recreated workspace back in listings, got %#v", ids)
	}
}

// TestRescanRestoresReappearedWorkspaceFolder covers the self-heal direction:
// a folder that comes back (cloud sync restore, drive remount) clears the
// missing status without user action.
func TestRescanRestoresReappearedWorkspaceFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Roundtrip")

	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	stash := filepath.Join(t.TempDir(), filepath.Base(folderPath))
	if err := os.Rename(folderPath, stash); err != nil {
		t.Fatalf("move folder away: %v", err)
	}

	resp := postWorkspaceRescan(t, handler)
	if got := rescanCount(t, resp, "orphaned"); got != 1 {
		t.Fatalf("expected orphaned=1 after folder removal, got %d", got)
	}

	if err := os.Rename(stash, folderPath); err != nil {
		t.Fatalf("move folder back: %v", err)
	}

	resp = postWorkspaceRescan(t, handler)
	if got := rescanCount(t, resp, "restored"); got != 1 {
		t.Fatalf("expected restored=1 after folder reappeared, got %d (%#v)", got, resp)
	}

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.Status == session.WorkspaceStatusMissing {
		t.Fatalf("expected reappeared workspace to be active, got %q", ws.Status)
	}
	if ids := listWorkspaceIDs(t, handler); ids[workspaceID] != 1 {
		t.Fatalf("expected reappeared workspace in listings, got %#v", ids)
	}
}

// TestRescanHidesWorkspaceSupersededByRecreatedFolder covers the duplicate-name
// case: the folder was deleted and recreated externally with a new workspace
// ID. The rescan imports the new workspace and hides the stale row instead of
// showing two entries with the same name.
func TestRescanHidesWorkspaceSupersededByRecreatedFolder(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	staleID := createTestWorkspace(t, handler, "Grouptest")

	folderPath, err := fileStore.GetFolderPath(staleID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	slug := filepath.Base(folderPath)

	if err := os.RemoveAll(folderPath); err != nil {
		t.Fatalf("RemoveAll workspace folder: %v", err)
	}

	// Recreate the folder at the same path as a different app instance would:
	// same name and slug, brand-new workspace ID.
	recreated := &agentworkspace.Workspace{
		ID:         "ws-recreated-elsewhere",
		Name:       "Grouptest",
		FolderSlug: slug,
		Status:     agentworkspace.StatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	data, err := recreated.ToJSON()
	if err != nil {
		t.Fatalf("serialize recreated workspace: %v", err)
	}
	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		t.Fatalf("recreate folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folderPath, agentworkspace.WorkspaceConfigFile), data, 0o644); err != nil {
		t.Fatalf("write recreated workspace.json: %v", err)
	}

	resp := postWorkspaceRescan(t, handler)
	if got := rescanCount(t, resp, "imported"); got != 1 {
		t.Fatalf("expected imported=1 for recreated folder, got %d (%#v)", got, resp)
	}
	if got := rescanCount(t, resp, "orphaned"); got != 1 {
		t.Fatalf("expected orphaned=1 for superseded row, got %d (%#v)", got, resp)
	}

	staleWS, err := handler.store.GetWorkspace(context.Background(), staleID)
	if err != nil {
		t.Fatalf("GetWorkspace stale: %v", err)
	}
	if staleWS.Status != session.WorkspaceStatusMissing {
		t.Fatalf("expected superseded row to be missing, got %q", staleWS.Status)
	}

	ids := listWorkspaceIDs(t, handler)
	if ids[staleID] != 0 {
		t.Fatalf("expected stale workspace hidden from listings, got %#v", ids)
	}
	if ids[recreated.ID] != 1 {
		t.Fatalf("expected recreated workspace in listings exactly once, got %#v", ids)
	}

	// Global slug reconciliation moved the replacement workspace to a numeric
	// suffix, so recreating the missing original is now safe and restores both
	// registrations without an ambiguous slug or folder overwrite.
	payload, _ := json.Marshal(map[string]any{"recreate": []string{staleID}})
	syncReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/sync", bytes.NewBuffer(payload))
	syncReq.Header.Set("Content-Type", "application/json")
	syncW := httptest.NewRecorder()
	handler.HandleWorkspaces(syncW, syncReq)
	if syncW.Code != http.StatusOK {
		t.Fatalf("expected 200 for sync recreate, got %d: %s", syncW.Code, syncW.Body.String())
	}
	var syncResp map[string]any
	if err := json.Unmarshal(syncW.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if got := int(syncResp["recreated"].(float64)); got != 1 {
		t.Fatalf("expected missing workspace to be recreated after suffix migration, got recreated=%d", got)
	}

	rawOnDisk, err := os.ReadFile(filepath.Join(folderPath, agentworkspace.WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read restored workspace.json: %v", err)
	}
	onDisk, err := agentworkspace.FromJSON(rawOnDisk)
	if err != nil {
		t.Fatalf("parse restored workspace.json: %v", err)
	}
	if onDisk.ID != staleID {
		t.Fatalf("expected original folder to be restored for stale workspace, got %q", onDisk.ID)
	}
	replacementPath := filepath.Join(filepath.Dir(folderPath), "grouptest-2", agentworkspace.WorkspaceConfigFile)
	replacementData, err := os.ReadFile(replacementPath)
	if err != nil {
		t.Fatalf("read suffix-migrated replacement workspace.json: %v", err)
	}
	replacement, err := agentworkspace.FromJSON(replacementData)
	if err != nil {
		t.Fatalf("parse replacement workspace.json: %v", err)
	}
	if replacement.ID != recreated.ID || replacement.FolderSlug != "grouptest-2" {
		t.Fatalf("replacement workspace = %#v, want %s/grouptest-2", replacement, recreated.ID)
	}
}
