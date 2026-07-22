package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestCreateWorkspace_ScaffoldsBacklogMarkdown covers task-list 3.17/3.22
// (PRD workspace-backlog FR67, 69): a newly created normal workspace gets an
// Ori-managed BACKLOG.md at its folder root, immediately visible through the
// default workspace-files binding (which is scoped to the whole folder for a
// normal workspace).
func TestCreateWorkspace_ScaffoldsBacklogMarkdown(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Launch Plan")

	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	path := filepath.Join(folderPath, agentworkspace.BacklogMarkdownFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected BACKLOG.md to be scaffolded at %s: %v", path, err)
	}
	if !bytes.Contains(data, []byte("type: ori_workspace_backlog")) {
		t.Fatalf("scaffolded file missing Ori frontmatter:\n%s", data)
	}
}

// TestCreateGroupWorkspace_ScaffoldsBacklogMarkdownUnderFiles covers the
// group-specific placement rule (FR69): group agents are scoped to files/
// and notes/ only, so BACKLOG.md must live under files/, not the group
// folder root, to remain visible through the default workspace-files
// binding.
func TestCreateGroupWorkspace_ScaffoldsBacklogMarkdownUnderFiles(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces",
		bytes.NewBufferString(`{"name":"Album","kind":"group"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed to create group workspace: %d - %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder in response: %#v", resp)
	}
	workspaceID := folder["id"].(string)

	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	groupPath := filepath.Join(folderPath, agentworkspace.FilesDir, agentworkspace.BacklogMarkdownFileName)
	if _, err := os.Stat(groupPath); err != nil {
		t.Fatalf("expected BACKLOG.md under group files/ at %s: %v", groupPath, err)
	}
	rootPath := filepath.Join(folderPath, agentworkspace.BacklogMarkdownFileName)
	if _, err := os.Stat(rootPath); err == nil {
		t.Fatalf("BACKLOG.md must not also exist at the group folder root")
	}
}

// TestBackfillBacklogMarkdown_RepairsExistingWorkspacesIdempotently covers
// task-list 3.18/3.22 (FR68, 99): a workspace created before this feature
// shipped (simulated by creating it, then deleting its BACKLOG.md) gets it
// restored by the backfill sweep, and rerunning the sweep is a no-op change
// (no duplicate files, no error).
func TestBackfillBacklogMarkdown_RepairsExistingWorkspacesIdempotently(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Pre-existing Workspace")
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	path := filepath.Join(folderPath, agentworkspace.BacklogMarkdownFileName)
	if err := os.Remove(path); err != nil {
		t.Fatalf("simulate pre-feature workspace by removing BACKLOG.md: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("setup failed: BACKLOG.md still present")
	}

	written, errs := agentworkspace.BackfillBacklogMarkdownForAllWorkspaces(fileStore)
	if len(errs) != 0 {
		t.Fatalf("unexpected backfill errors: %v", errs)
	}
	if written == 0 {
		t.Fatalf("expected the backfill to write at least the repaired workspace")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("BACKLOG.md not restored by backfill: %v", err)
	}

	writtenAgain, errsAgain := agentworkspace.BackfillBacklogMarkdownForAllWorkspaces(fileStore)
	if len(errsAgain) != 0 {
		t.Fatalf("unexpected errors on repeated backfill: %v", errsAgain)
	}
	if writtenAgain != written {
		t.Fatalf("repeated backfill wrote a different count: first=%d second=%d", written, writtenAgain)
	}
}
