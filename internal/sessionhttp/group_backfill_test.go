package sessionhttp

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func newTestFileStore(t *testing.T, handler *Handler) (*agentworkspace.FileStore, string) {
	t.Helper()
	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)
	return fileStore, baseDir
}

func workspaceFilesRootsForTest(t *testing.T, ws *session.Workspace) []string {
	t.Helper()
	bindings, err := decodeWorkspaceMCPBindings(ws.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("failed to decode MCP bindings: %v", err)
	}
	for _, binding := range bindings {
		if binding.Alias != workspaceFilesMCPAlias {
			continue
		}
		switch typed := binding.Config["roots"].(type) {
		case []string:
			return typed
		case []any:
			roots := make([]string, 0, len(typed))
			for _, value := range typed {
				if text, ok := value.(string); ok {
					roots = append(roots, text)
				}
			}
			return roots
		}
	}
	return nil
}

func assertScopedGroupScaffolding(t *testing.T, handler *Handler, groupID, groupPath string) {
	t.Helper()

	for _, dir := range []string{
		filepath.Join(groupPath, agentworkspace.SubWorkspacesDir),
		filepath.Join(groupPath, agentworkspace.FilesDir),
		filepath.Join(groupPath, agentworkspace.NotesDir),
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s, err=%v", dir, err)
		}
	}

	ws, err := handler.store.GetWorkspace(context.Background(), groupID)
	if err != nil {
		t.Fatalf("failed to load group: %v", err)
	}

	refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("failed to decode directory references: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected exactly one directory reference, got %d", len(refs))
	}
	wantRef := filepath.Join(groupPath, agentworkspace.FilesDir)
	if refs[0].Path != wantRef {
		t.Fatalf("directory reference = %q, want %q (scoped to files/)", refs[0].Path, wantRef)
	}

	roots := workspaceFilesRootsForTest(t, ws)
	wantRoots := map[string]bool{
		filepath.Join(groupPath, agentworkspace.FilesDir): false,
		filepath.Join(groupPath, agentworkspace.NotesDir): false,
	}
	for _, root := range roots {
		if root == groupPath {
			t.Fatalf("MCP roots must never include the group folder root %q (exposes sub-workspaces/)", groupPath)
		}
		if _, ok := wantRoots[root]; ok {
			wantRoots[root] = true
		}
	}
	for root, seen := range wantRoots {
		if !seen {
			t.Fatalf("expected MCP root %q, got %v", root, roots)
		}
	}
}

func TestCreateGroupProvisionsScopedScaffolding(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	_, baseDir := newTestFileStore(t, handler)

	groupID := createTestGroup(t, handler, "Scoped Group")
	assertScopedGroupScaffolding(t, handler, groupID, filepath.Join(baseDir, "scoped-group"))
}

func TestBackfillGroupScaffoldingProvisionsLegacyGroup(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	fileStore, baseDir := newTestFileStore(t, handler)
	ctx := context.Background()

	// Legacy group: session row plus bare folder (workspace.json +
	// sub-workspaces/), no directory reference or MCP binding.
	ws := &session.Workspace{
		ID:         uuid.New().String(),
		Name:       "Legacy Group",
		Kind:       session.WorkspaceKindGroup,
		FolderSlug: "legacy-group",
	}
	if err := handler.store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("failed to create legacy group row: %v", err)
	}
	if err := fileStore.Save(&agentworkspace.Workspace{
		ID:         ws.ID,
		Name:       ws.Name,
		Kind:       string(ws.Kind),
		FolderSlug: ws.FolderSlug,
		Status:     agentworkspace.StatusActive,
	}); err != nil {
		t.Fatalf("failed to create legacy group folder: %v", err)
	}

	if err := handler.BackfillGroupScaffolding(ctx); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	assertScopedGroupScaffolding(t, handler, ws.ID, filepath.Join(baseDir, "legacy-group"))

	// Second run must be a no-op.
	before, err := handler.store.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("failed to reload group: %v", err)
	}
	if err := handler.BackfillGroupScaffolding(ctx); err != nil {
		t.Fatalf("second backfill failed: %v", err)
	}
	after, err := handler.store.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("failed to reload group after second run: %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("second backfill modified the group (updated_at %v -> %v)", before.UpdatedAt, after.UpdatedAt)
	}
	if string(after.DirectoryReferencesJSON) != string(before.DirectoryReferencesJSON) {
		t.Fatalf("second backfill changed directory references")
	}
	if string(after.MCPBindingsJSON) != string(before.MCPBindingsJSON) {
		t.Fatalf("second backfill changed MCP bindings")
	}
}

func TestBackfillGroupScaffoldingCreatesFolderForDBOnlyGroup(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	_, baseDir := newTestFileStore(t, handler)
	ctx := context.Background()

	ws := &session.Workspace{
		ID:         uuid.New().String(),
		Name:       "DB Only Group",
		Kind:       session.WorkspaceKindGroup,
		FolderSlug: "db-only-group",
	}
	if err := handler.store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("failed to create DB-only group row: %v", err)
	}

	if err := handler.BackfillGroupScaffolding(ctx); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	assertScopedGroupScaffolding(t, handler, ws.ID, filepath.Join(baseDir, "db-only-group"))
}

func TestBackfillGroupScaffoldingLeavesProvisionedGroupsUntouched(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	newTestFileStore(t, handler)
	ctx := context.Background()

	groupID := createTestGroup(t, handler, "Fresh Group")
	before, err := handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("failed to load group: %v", err)
	}

	if err := handler.BackfillGroupScaffolding(ctx); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}

	after, err := handler.store.GetWorkspace(ctx, groupID)
	if err != nil {
		t.Fatalf("failed to reload group: %v", err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("backfill modified a freshly provisioned group")
	}
}

func TestRenameGroupKeepsScopedScaffolding(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	_, baseDir := newTestFileStore(t, handler)

	groupID := createTestGroup(t, handler, "Before Rename")
	renameWorkspaceViaAPI(t, handler, groupID, "After Rename", http.StatusOK)

	assertScopedGroupScaffolding(t, handler, groupID, filepath.Join(baseDir, "after-rename"))
}
