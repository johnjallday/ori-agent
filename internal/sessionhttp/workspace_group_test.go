package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func createTestGroup(t *testing.T, handler *Handler, name string) string {
	t.Helper()

	body := `{"name":"` + name + `","kind":"group"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("failed to create group: %d - %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode group response: %v", err)
	}

	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder payload in group response")
	}
	if got := folder["kind"]; got != "group" {
		t.Fatalf("expected kind group, got %v", got)
	}

	return folder["id"].(string)
}

func TestCreateGroupCreatesFolderOnDisk(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "BK Nerds")

	ws, err := handler.store.GetWorkspace(context.Background(), groupID)
	if err != nil {
		t.Fatalf("failed to load created group: %v", err)
	}
	if ws.Kind != session.WorkspaceKindGroup {
		t.Fatalf("expected group kind, got %q", ws.Kind)
	}

	// Groups are now real, portable folders: the group folder, its
	// workspace.json, and an empty sub-workspaces/ directory must exist on disk
	// so the grouping survives being copied/synced to another machine.
	groupPath := filepath.Join(baseDir, "bk-nerds")
	if info, err := os.Stat(groupPath); err != nil || !info.IsDir() {
		t.Fatalf("expected group folder at %s, err=%v", groupPath, err)
	}
	if _, err := os.Stat(filepath.Join(groupPath, agentworkspace.WorkspaceConfigFile)); err != nil {
		t.Fatalf("expected group workspace.json on disk: %v", err)
	}
	if info, err := os.Stat(filepath.Join(groupPath, agentworkspace.SubWorkspacesDir)); err != nil || !info.IsDir() {
		t.Fatalf("expected group sub-workspaces/ directory on disk, err=%v", err)
	}
}

func renameWorkspaceViaAPI(t *testing.T, handler *Handler, id, name string, wantCode int) {
	t.Helper()
	body := `{"name":"` + name + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+id+"/rename", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != wantCode {
		t.Fatalf("rename %s: got %d, want %d: %s", id, w.Code, wantCode, w.Body.String())
	}
}

// TestRenameGroupRenamesFolderOnDisk verifies the rename handler now renames a
// group's backing folder (previously skipped for groups) and updates the DB
// display name, with the file store resolving the group to its new path.
func TestRenameGroupRenamesFolderOnDisk(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "BK Nerds")
	oldPath := filepath.Join(baseDir, "bk-nerds")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("expected group folder at %s: %v", oldPath, err)
	}

	renameWorkspaceViaAPI(t, handler, groupID, "BK Wizards", http.StatusOK)

	// DB display name updated.
	ws, err := handler.store.GetWorkspace(context.Background(), groupID)
	if err != nil {
		t.Fatalf("load renamed group: %v", err)
	}
	if ws.Name != "BK Wizards" {
		t.Fatalf("group name = %q, want %q", ws.Name, "BK Wizards")
	}

	// Folder renamed on disk: new slug exists, old slug gone.
	newPath := filepath.Join(baseDir, "bk-wizards")
	if info, err := os.Stat(newPath); err != nil || !info.IsDir() {
		t.Fatalf("expected renamed group folder at %s, err=%v", newPath, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old group folder %s to be gone, err=%v", oldPath, err)
	}
	if got := mustHandlerFolderPath(t, fileStore, groupID); filepath.Clean(got) != filepath.Clean(newPath) {
		t.Fatalf("group folder path = %q, want %q", got, newPath)
	}
}

func mustHandlerFolderPath(t *testing.T, fs *agentworkspace.FileStore, id string) string {
	t.Helper()
	p, err := fs.GetFolderPath(id)
	if err != nil {
		t.Fatalf("GetFolderPath %s: %v", id, err)
	}
	return p
}

func patchWorkspaceParent(t *testing.T, handler *Handler, id, parentID string, wantCode int) {
	t.Helper()
	body := `{"parent_id":"` + parentID + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+id, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != wantCode {
		t.Fatalf("PATCH parent_id: got %d, want %d: %s", w.Code, wantCode, w.Body.String())
	}
}

func TestGroupingMovesWorkspaceFolderOnDisk(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")

	// The child starts at the workspaces root.
	if got := mustHandlerFolderPath(t, fileStore, childID); filepath.Dir(got) != baseDir {
		t.Fatalf("expected child at root, got %s", got)
	}

	// Moving the child into the group physically relocates its folder into the
	// group's sub-workspaces/ directory.
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusOK)

	wantPath := filepath.Join(baseDir, "client-work", agentworkspace.SubWorkspacesDir, "brooklyn-nerds")
	gotPath := mustHandlerFolderPath(t, fileStore, childID)
	if filepath.Clean(gotPath) != filepath.Clean(wantPath) {
		t.Fatalf("child path = %q, want %q", gotPath, wantPath)
	}
	if _, err := os.Stat(filepath.Join(gotPath, agentworkspace.WorkspaceConfigFile)); err != nil {
		t.Fatalf("expected workspace.json at new location: %v", err)
	}

	ws, err := handler.store.GetWorkspace(context.Background(), childID)
	if err != nil {
		t.Fatalf("failed to reload child: %v", err)
	}
	if ws.ParentID != groupID {
		t.Fatalf("child parent_id = %q, want %q", ws.ParentID, groupID)
	}
}

func TestRewriteWorkspaceProjectPath(t *testing.T) {
	const oldPath = "/workspaces/old-slug"
	const newPath = "/workspaces/group/sub-workspaces/old-slug"

	cases := []struct {
		name        string
		in          string
		wantPath    string
		wantChanged bool
	}{
		{"absolute inside the moved folder is rewritten", oldPath + "/code", newPath + "/code", true},
		{"external absolute path is left alone", "/elsewhere/repo", "/elsewhere/repo", false},
		{"relative path (projects root) is left alone", "code/app", "code/app", false},
		{"empty stays empty", "", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := &session.Workspace{ProjectPath: c.in}
			changed := rewriteWorkspaceProjectPath(ws, oldPath, newPath)
			if changed != c.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, c.wantChanged)
			}
			if ws.ProjectPath != c.wantPath {
				t.Fatalf("project_path = %q, want %q", ws.ProjectPath, c.wantPath)
			}
		})
	}
}

func deleteWorkspaceReq(t *testing.T, handler *Handler, id, query string, wantCode int) {
	t.Helper()
	url := "/api/workspaces/" + id
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != wantCode {
		t.Fatalf("DELETE %s: got %d, want %d: %s", url, w.Code, wantCode, w.Body.String())
	}
}

func setupGroupWithChild(t *testing.T, handler *Handler) (baseDir, groupID, childID string, fs *agentworkspace.FileStore) {
	t.Helper()
	baseDir = t.TempDir()
	var err error
	fs, err = agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fs)

	groupID = createTestGroup(t, handler, "Client Work")
	childID = createTestWorkspace(t, handler, "Brooklyn Nerds")
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusOK)
	return baseDir, groupID, childID, fs
}

// TestDeleteGroupWithContents covers the permanent-delete path. delete_sessions=true
// forces it (the default contents delete is now a reversible move to the system
// Trash, which is covered by TestDeleteGroupWithContentsTrashable). Using the
// permanent path here also keeps the test off the real system Trash, since the
// temp workspace root may live on a different volume.
func TestDeleteGroupWithContents(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir, groupID, childID, _ := setupGroupWithChild(t, handler)

	deleteWorkspaceReq(t, handler, groupID, "confirm=true&delete_mode=contents&delete_sessions=true", http.StatusNoContent)

	ctx := context.Background()
	if _, err := handler.store.GetWorkspace(ctx, groupID); err != session.ErrWorkspaceNotFound {
		t.Fatalf("expected group deleted, got err=%v", err)
	}
	if _, err := handler.store.GetWorkspace(ctx, childID); err != session.ErrWorkspaceNotFound {
		t.Fatalf("expected member deleted with contents, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "client-work")); !os.IsNotExist(err) {
		t.Fatalf("expected group folder removed from disk, stat err=%v", err)
	}
}

// TestDeleteGroupWithContentsTrashable covers the reversible soft-delete path:
// the whole group folder tree moves to the system Trash, the group and its
// members are marked trashed (rows preserved), and a restore brings the entire
// subtree back to active. Rooted under $HOME so the trash move (a rename into the
// per-user trash) stays on a single volume, mirroring store_trash_test.go.
func TestDeleteGroupWithContentsTrashable(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("path-based trash round trip not supported on %s", runtime.GOOS)
	}

	handler, cleanup := createTestHandler(t)
	defer cleanup()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	baseDir, err := os.MkdirTemp(home, ".ws-group-trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(baseDir) }()

	fs, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fs)

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusOK)

	ctx := context.Background()

	// Soft delete: the contents delete (without delete_sessions) trashes the tree.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+groupID+"?confirm=true&delete_mode=contents", nil)
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete contents: got %d, want 200: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if trashed, _ := resp["trashed"].(bool); !trashed {
		t.Fatalf("expected trashed=true in response, got %v", resp)
	}

	// Ensure the trashed folder is cleaned up even if restore fails below.
	grp, err := handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("group row should survive trashing, got err=%v", err)
	}
	if _, trashedPath := workspaceTrashPaths(grp); trashedPath != "" {
		defer func() { _ = os.RemoveAll(trashedPath) }()
	}

	// Group and member rows are preserved but marked trashed; folder is gone.
	if grp.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("group status = %q, want trashed", grp.Status)
	}
	child, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("member row should survive trashing, got err=%v", err)
	}
	if child.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("member status = %q, want trashed", child.Status)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "client-work")); !os.IsNotExist(err) {
		t.Fatalf("expected group folder moved out of root, stat err=%v", err)
	}

	// Restore: the whole subtree comes back as active.
	restoreW := httptest.NewRecorder()
	restoreReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+groupID+"/restore", nil)
	handler.HandleWorkspaces(restoreW, restoreReq)
	if restoreW.Code != http.StatusOK {
		t.Fatalf("restore: got %d, want 200: %s", restoreW.Code, restoreW.Body.String())
	}

	grp, err = handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("group after restore, got err=%v", err)
	}
	if grp.Status != session.WorkspaceStatusActive {
		t.Fatalf("group status after restore = %q, want active", grp.Status)
	}
	child, err = handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("member after restore, got err=%v", err)
	}
	if child.Status != session.WorkspaceStatusActive {
		t.Fatalf("member status after restore = %q, want active", child.Status)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "client-work")); err != nil {
		t.Fatalf("expected group folder restored to root, stat err=%v", err)
	}
}

func TestDeleteGroupOnlyUnnestsMembers(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir, groupID, childID, fs := setupGroupWithChild(t, handler)

	// delete_sessions=true forces the permanent path (the default is an
	// undoable trash move, covered by TestDeleteGroupOnlyTrashable).
	deleteWorkspaceReq(t, handler, groupID, "confirm=true&delete_mode=group_only&delete_sessions=true", http.StatusNoContent)

	ctx := context.Background()
	if _, err := handler.store.GetWorkspace(ctx, groupID); err != session.ErrWorkspaceNotFound {
		t.Fatalf("expected group deleted, got err=%v", err)
	}
	child, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("expected member to survive un-nest, got err=%v", err)
	}
	if child.ParentID != "" {
		t.Fatalf("expected member un-nested to root, parent_id=%q", child.ParentID)
	}
	if got := mustHandlerFolderPath(t, fs, childID); filepath.Dir(got) != baseDir {
		t.Fatalf("expected member folder back at root, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "client-work")); !os.IsNotExist(err) {
		t.Fatalf("expected empty group folder removed, stat err=%v", err)
	}
}

// TestDeleteGroupOnlyTrashable covers the reversible group_only path: members
// are un-nested to the root and stay active, while the group itself (with its
// own sessions and content) moves to the system Trash and can be restored.
// Rooted under $HOME so the trash move stays on a single volume.
func TestDeleteGroupOnlyTrashable(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("path-based trash round trip not supported on %s", runtime.GOOS)
	}

	handler, cleanup := createTestHandler(t)
	defer cleanup()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	baseDir, err := os.MkdirTemp(home, ".ws-group-only-trash-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer func() { _ = os.RemoveAll(baseDir) }()

	fs, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fs)

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusOK)

	ctx := context.Background()

	// The group holds its own session, which must survive trash + restore.
	sessionReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"title":"Group Session","folder_id":"`+groupID+`"}`))
	sessionReq.Header.Set("Content-Type", "application/json")
	sessionW := httptest.NewRecorder()
	handler.HandleSessions(sessionW, sessionReq)
	if sessionW.Code != http.StatusCreated {
		t.Fatalf("create group session: got %d: %s", sessionW.Code, sessionW.Body.String())
	}
	var sessionResp struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(sessionW.Body.Bytes(), &sessionResp); err != nil {
		t.Fatalf("decode session response: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+groupID+"?confirm=true&delete_mode=group_only", nil)
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete group_only: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if trashed, _ := resp["trashed"].(bool); !trashed {
		t.Fatalf("expected trashed=true in response, got %v", resp)
	}

	grp, err := handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("group row should survive trashing, got err=%v", err)
	}
	if _, trashedPath := workspaceTrashPaths(grp); trashedPath != "" {
		defer func() { _ = os.RemoveAll(trashedPath) }()
	}
	if grp.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("group status = %q, want trashed", grp.Status)
	}

	// Member is un-nested, active, and physically back at the root.
	child, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("member should survive group_only delete: %v", err)
	}
	if child.Status != session.WorkspaceStatusActive {
		t.Fatalf("member status = %q, want active", child.Status)
	}
	if child.ParentID != "" {
		t.Fatalf("expected member un-nested to root, parent_id=%q", child.ParentID)
	}
	if got := mustHandlerFolderPath(t, fs, childID); filepath.Dir(got) != baseDir {
		t.Fatalf("expected member folder back at root, got %s", got)
	}

	// Restore brings back just the group, with its session intact.
	restoreW := httptest.NewRecorder()
	restoreReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+groupID+"/restore", nil)
	handler.HandleWorkspaces(restoreW, restoreReq)
	if restoreW.Code != http.StatusOK {
		t.Fatalf("restore: got %d, want 200: %s", restoreW.Code, restoreW.Body.String())
	}

	grp, err = handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("group after restore: %v", err)
	}
	if grp.Status != session.WorkspaceStatusActive {
		t.Fatalf("group status after restore = %q, want active", grp.Status)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "client-work")); err != nil {
		t.Fatalf("expected group folder restored to root, stat err=%v", err)
	}

	restoredSession, err := handler.store.GetSession(ctx, sessionResp.Session.ID)
	if err != nil {
		t.Fatalf("group session after restore: %v", err)
	}
	if restoredSession.FolderID != groupID {
		t.Fatalf("session folder_id after restore = %q, want %q", restoredSession.FolderID, groupID)
	}

	// The restored member must NOT have been re-nested or reactivated twice;
	// it stayed active at the root the whole time.
	child, err = handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("member after restore: %v", err)
	}
	if child.ParentID != "" {
		t.Fatalf("member parent_id after restore = %q, want empty", child.ParentID)
	}
}

func TestDeleteGroupOnlyBlockedByActiveWork(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	_, groupID, childID, _ := setupGroupWithChild(t, handler)

	ctx := context.Background()
	ws, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("load member: %v", err)
	}
	ws.TasksJSON = []byte(`[{"id":"t1","status":"in_progress"}]`)
	if err := handler.store.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("set active task: %v", err)
	}

	deleteWorkspaceReq(t, handler, groupID, "confirm=true&delete_mode=group_only", http.StatusConflict)

	// Both must still exist.
	if _, err := handler.store.GetWorkspace(ctx, groupID); err != nil {
		t.Fatalf("group should remain after blocked delete: %v", err)
	}
	if _, err := handler.store.GetWorkspace(ctx, childID); err != nil {
		t.Fatalf("member should remain after blocked delete: %v", err)
	}
}

func TestRescanReparentsFromDisk(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")

	// Simulate an out-of-band folder move (e.g. git pull / cloud sync / manual
	// reorg): physically relocate the child into the group on disk without
	// telling the running app.
	oldPath := filepath.Join(baseDir, "brooklyn-nerds")
	newPath := filepath.Join(baseDir, "client-work", agentworkspace.SubWorkspacesDir, "brooklyn-nerds")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("simulate disk move: %v", err)
	}

	// Before rescan the session store still thinks the child is at the root.
	ctx := context.Background()
	if ws, _ := handler.store.GetWorkspace(ctx, childID); ws.ParentID != "" {
		t.Fatalf("precondition: expected child parent empty, got %q", ws.ParentID)
	}

	// Rescan reconciles structure from disk: physical location wins.
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/rescan", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rescan: got %d: %s", w.Code, w.Body.String())
	}

	ws, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("reload child: %v", err)
	}
	if ws.ParentID != groupID {
		t.Fatalf("after rescan child parent_id = %q, want %q", ws.ParentID, groupID)
	}
}

func TestGroupingHardBlockedByActiveWork(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Busy Workspace")

	// Give the child an in-progress task.
	ctx := context.Background()
	ws, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("failed to load child: %v", err)
	}
	ws.TasksJSON = []byte(`[{"id":"t1","status":"in_progress"}]`)
	if err := handler.store.UpdateWorkspace(ctx, ws); err != nil {
		t.Fatalf("failed to set active task: %v", err)
	}

	// The move is hard-blocked while work is in flight.
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusConflict)

	// The folder must not have moved and the parent must be unchanged.
	if got := mustHandlerFolderPath(t, fileStore, childID); filepath.Dir(got) != baseDir {
		t.Fatalf("child should remain at root, got %s", got)
	}
	reloaded, err := handler.store.GetWorkspace(ctx, childID)
	if err != nil {
		t.Fatalf("failed to reload child: %v", err)
	}
	if reloaded.ParentID != "" {
		t.Fatalf("child parent_id = %q, want empty (move blocked)", reloaded.ParentID)
	}
}

func TestListWorkspacesFlatIncludesGroups(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	groupID := createTestGroup(t, handler, "Client Work")
	childID := createTestWorkspace(t, handler, "Brooklyn Nerds")

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+childID, bytes.NewBufferString(`{"parent_id":"`+groupID+`"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	handler.HandleWorkspaces(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("expected 200 when moving workspace into group, got %d: %s", updateW.Code, updateW.Body.String())
	}

	flatReq := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	flatW := httptest.NewRecorder()
	handler.HandleWorkspaces(flatW, flatReq)
	if flatW.Code != http.StatusOK {
		t.Fatalf("expected 200 for flat list, got %d: %s", flatW.Code, flatW.Body.String())
	}

	var flatResp map[string]any
	if err := json.Unmarshal(flatW.Body.Bytes(), &flatResp); err != nil {
		t.Fatalf("failed to decode flat list response: %v", err)
	}
	flatWorkspaces, ok := flatResp["workspaces"].([]any)
	if !ok {
		t.Fatalf("expected workspaces list in flat response")
	}
	if len(flatWorkspaces) != 2 {
		t.Fatalf("expected group and workspace in flat list, got %d entries", len(flatWorkspaces))
	}

	kindsByID := make(map[string]string, len(flatWorkspaces))
	for _, entry := range flatWorkspaces {
		ws := entry.(map[string]any)
		kind, _ := ws["kind"].(string)
		kindsByID[ws["id"].(string)] = kind
	}
	if got := kindsByID[groupID]; got != "group" {
		t.Fatalf("expected flat list to include group %q with kind group, got %q", groupID, got)
	}
	if _, ok := kindsByID[childID]; !ok {
		t.Fatalf("expected flat list to contain child workspace %q", childID)
	}

	treeReq := httptest.NewRequest(http.MethodGet, "/api/workspaces?tree=true", nil)
	treeW := httptest.NewRecorder()
	handler.HandleWorkspaces(treeW, treeReq)
	if treeW.Code != http.StatusOK {
		t.Fatalf("expected 200 for tree list, got %d: %s", treeW.Code, treeW.Body.String())
	}

	var treeResp map[string]any
	if err := json.Unmarshal(treeW.Body.Bytes(), &treeResp); err != nil {
		t.Fatalf("failed to decode tree response: %v", err)
	}
	treeWorkspaces, ok := treeResp["workspaces"].([]any)
	if !ok || len(treeWorkspaces) != 1 {
		t.Fatalf("expected one root group in tree response, got %#v", treeResp["workspaces"])
	}
	root := treeWorkspaces[0].(map[string]any)
	if got := root["kind"]; got != "group" {
		t.Fatalf("expected root kind group, got %v", got)
	}
	children, ok := root["children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("expected group to contain one child, got %#v", root["children"])
	}
}

func TestCreateWorkspaceRejectsNonGroupParent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	parentID := createTestWorkspace(t, handler, "Standalone Workspace")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(`{"name":"Child Workspace","parent_id":"`+parentID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-group parent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSessionAllowsGroupAssignment(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	groupID := createTestGroup(t, handler, "Ops")

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"title":"Grouped Session","folder_id":"`+groupID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleSessions(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for group session assignment, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Session struct {
			ID       string `json:"id"`
			FolderID string `json:"folder_id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode session response: %v", err)
	}
	if resp.Session.FolderID != groupID {
		t.Fatalf("expected session folder_id %q, got %q", groupID, resp.Session.FolderID)
	}
}

// TestGroupNotesAndSettings verifies that groups support direct work that was
// previously gated: creating/listing notes and reading/updating settings.
func TestGroupNotesAndSettings(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	newTestFileStore(t, handler)

	groupID := createTestGroup(t, handler, "Notes Group")

	noteReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+groupID+"/notes", bytes.NewBufferString(`{"name":"Plan","content":"# plan"}`))
	noteReq.Header.Set("Content-Type", "application/json")
	noteW := httptest.NewRecorder()
	handler.HandleWorkspaces(noteW, noteReq)
	if noteW.Code != http.StatusCreated {
		t.Fatalf("create note on group: got %d, want 201: %s", noteW.Code, noteW.Body.String())
	}

	listW := httptest.NewRecorder()
	handler.HandleWorkspaces(listW, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+groupID+"/notes", nil))
	if listW.Code != http.StatusOK {
		t.Fatalf("list group notes: got %d, want 200: %s", listW.Code, listW.Body.String())
	}
	if !bytes.Contains(listW.Body.Bytes(), []byte("Plan")) {
		t.Fatalf("expected listed notes to contain the created note, got %s", listW.Body.String())
	}

	getW := httptest.NewRecorder()
	handler.HandleWorkspaces(getW, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+groupID+"/settings", nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("get group settings: got %d, want 200: %s", getW.Code, getW.Body.String())
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+groupID+"/settings", bytes.NewBufferString(`{}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchW := httptest.NewRecorder()
	handler.HandleWorkspaces(patchW, patchReq)
	if patchW.Code != http.StatusOK {
		t.Fatalf("update group settings: got %d, want 200: %s", patchW.Code, patchW.Body.String())
	}
}

// TestCreateGroupWithEntryAgent verifies the New Group modal flow: a group
// created with entry_agent_name resolves that agent as the default session
// agent, and a group created without one gets a "<Name> Manager" entry agent
// auto-created (with numeric suffixes on name collisions).
func TestCreateGroupWithEntryAgent(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	newTestFileStore(t, handler)
	ctx := context.Background()

	if err := handler.agentStore.CreateAgent("Existing Manager", &agentstore.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Explicit entry agent: used as-is, no auto-creation.
	body := `{"name":"Managed Group","kind":"group","entry_agent_name":"Existing Manager"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create group with entry agent: got %d, want 201: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	groupID := resp["folder"].(map[string]any)["id"].(string)

	if got := handler.defaultSessionAgentNameForWorkspace(ctx, groupID); got != "Existing Manager" {
		t.Fatalf("default session agent for group = %q, want %q", got, "Existing Manager")
	}
	if _, exists := handler.agentStore.GetAgent("Managed Group Manager"); exists {
		t.Fatalf("explicit entry agent must suppress auto-creation")
	}

	// Omitted entry agent: a "<Name> Manager" agent is auto-created and set.
	plainID := createTestGroup(t, handler, "Plain Group")
	if got := handler.defaultSessionAgentNameForWorkspace(ctx, plainID); got != "Plain Group Manager" {
		t.Fatalf("default session agent for plain group = %q, want %q", got, "Plain Group Manager")
	}
	if _, exists := handler.agentStore.GetAgent("Plain Group Manager"); !exists {
		t.Fatalf("expected auto-created agent %q in the agent store", "Plain Group Manager")
	}

	// Name collision: the auto-created agent gets a numeric suffix instead of
	// adopting the existing agent (entry agents are deleted with their group).
	if err := handler.agentStore.CreateAgent("Collide Group Manager", &agentstore.CreateAgentConfig{
		Type: agent.TypeGeneral,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	collideID := createTestGroup(t, handler, "Collide Group")
	if got := handler.defaultSessionAgentNameForWorkspace(ctx, collideID); got != "Collide Group Manager 2" {
		t.Fatalf("default session agent for colliding group = %q, want %q", got, "Collide Group Manager 2")
	}
}
