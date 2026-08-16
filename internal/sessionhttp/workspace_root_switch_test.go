package sessionhttp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// seedDiskWorkspace writes a workspace folder into root without going through
// the handler, the way a pre-existing workspace directory already looks on disk
// before Ori is ever pointed at it.
func seedDiskWorkspace(t *testing.T, root, id, name, kind, parentID string) string {
	t.Helper()

	store, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(%s): %v", root, err)
	}
	defer func() { _ = store.Close() }()

	ws := &agentworkspace.Workspace{
		ID:         id,
		Name:       name,
		Kind:       kind,
		ParentID:   parentID,
		Status:     agentworkspace.StatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		SharedData: map[string]any{},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed %s in %s: %v", id, root, err)
	}
	folder, err := store.GetFolderPath(id)
	if err != nil {
		t.Fatalf("seed folder path %s: %v", id, err)
	}
	return folder
}

// TestApplyWorkspaceRoot_ImportsPreExistingTargetRootWorkspaces is the core of
// Issue #353: pointing the running process at a directory that already holds
// workspaces must make them visible immediately, with the same recursive
// discovery and physical grouping a restart would produce.
func TestApplyWorkspaceRoot_ImportsPreExistingTargetRootWorkspaces(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	rootA := filepath.Join(t.TempDir(), "Root A")
	rootB := filepath.Join(t.TempDir(), "Root B")

	fileStore, err := agentworkspace.NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore(rootA): %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	// Root B already contains a group with a nested workspace plus a top-level
	// workspace — none of which this process has ever seen.
	seedDiskWorkspace(t, rootB, "ws-b-group", "B Group", string(session.WorkspaceKindGroup), "")
	seedDiskWorkspace(t, rootB, "ws-b-child", "B Child", "", "ws-b-group")
	seedDiskWorkspace(t, rootB, "ws-b-only", "B Only", "", "")

	if ids := listWorkspaceIDs(t, handler); len(ids) != 0 {
		t.Fatalf("expected no workspaces before the switch, got %#v", ids)
	}

	ctx := context.Background()
	refresh, err := handler.ApplyWorkspaceRoot(ctx, rootB)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootB): %v", err)
	}

	if refresh.Imported != 3 {
		t.Fatalf("imported = %d, want 3 (%+v)", refresh.Imported, refresh)
	}
	if refresh.Orphaned != 0 || refresh.Restored != 0 || refresh.Reparented != 0 {
		t.Fatalf("unexpected refresh counts for a first-time root: %+v", refresh)
	}
	if len(refresh.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", refresh.Warnings)
	}

	// Visible through the normal workspace listing, with no restart and no
	// second rescan request.
	ids := listWorkspaceIDs(t, handler)
	for _, id := range []string{"ws-b-group", "ws-b-child", "ws-b-only"} {
		if ids[id] != 1 {
			t.Fatalf("expected %s exactly once in listings, got %#v", id, ids)
		}
	}

	// Physical location wins for grouping and kind.
	child, err := handler.store.GetWorkspace(ctx, "ws-b-child")
	if err != nil {
		t.Fatalf("GetWorkspace(ws-b-child): %v", err)
	}
	if child.ParentID != "ws-b-group" {
		t.Fatalf("child parent = %q, want ws-b-group", child.ParentID)
	}
	group, err := handler.store.GetWorkspace(ctx, "ws-b-group")
	if err != nil {
		t.Fatalf("GetWorkspace(ws-b-group): %v", err)
	}
	if group.Kind != session.WorkspaceKindGroup {
		t.Fatalf("group kind = %q, want group", group.Kind)
	}

	// The live folder store now serves Root B.
	if base := fileStore.BasePath(); !filepath.IsAbs(base) || filepath.Base(base) != "Root B" {
		t.Fatalf("live base path = %q, want Root B", base)
	}
	if idx := fileStore.GetIndex(); idx != nil {
		entries, err := idx.List()
		if err != nil {
			t.Fatalf("index List: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 index entries for Root B, got %d", len(entries))
		}
	}
}

// TestApplyWorkspaceRoot_SameRootDiscoversOutOfBandFolders proves re-saving the
// active directory is idempotent for what is already known while still picking
// up a folder added behind the app's back.
func TestApplyWorkspaceRoot_SameRootDiscoversOutOfBandFolders(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	root := filepath.Join(t.TempDir(), "Root A")
	fileStore, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	existingID := createTestWorkspace(t, handler, "Already Here")

	ctx := context.Background()
	refresh, err := handler.ApplyWorkspaceRoot(ctx, root)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(same root): %v", err)
	}
	if refresh.Imported != 0 || refresh.Orphaned != 0 || refresh.Restored != 0 {
		t.Fatalf("re-saving the active root should change nothing, got %+v", refresh)
	}

	// A folder dropped in out of band (git pull, cloud sync) is discovered.
	seedDiskWorkspace(t, root, "ws-out-of-band", "Out Of Band", "", "")

	refresh, err = handler.ApplyWorkspaceRoot(ctx, root)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(same root, second): %v", err)
	}
	if refresh.Imported != 1 {
		t.Fatalf("imported = %d, want 1 (%+v)", refresh.Imported, refresh)
	}
	if refresh.Orphaned != 0 {
		t.Fatalf("nothing should be hidden by a same-root save, got %+v", refresh)
	}

	ids := listWorkspaceIDs(t, handler)
	if ids[existingID] != 1 || ids["ws-out-of-band"] != 1 {
		t.Fatalf("expected both workspaces exactly once, got %#v", ids)
	}

	// Discovered exactly once: a third save adds nothing.
	refresh, err = handler.ApplyWorkspaceRoot(ctx, root)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(same root, third): %v", err)
	}
	if refresh.Imported != 0 {
		t.Fatalf("expected an idempotent repeat save, got %+v", refresh)
	}
}

// TestApplyWorkspaceRoot_RejectsUnusableRoot proves a target the store cannot
// use fails loudly and leaves the previously active root serving requests.
func TestApplyWorkspaceRoot_RejectsUnusableRoot(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	root := filepath.Join(t.TempDir(), "Root A")
	fileStore, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	existingID := createTestWorkspace(t, handler, "Still Here")
	ctx := context.Background()

	if _, err := handler.ApplyWorkspaceRoot(ctx, "   "); err == nil {
		t.Fatal("expected an empty directory to be rejected")
	}

	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	if _, err := handler.ApplyWorkspaceRoot(ctx, blocked); err == nil {
		t.Fatal("expected a file path to be rejected as a workspace root")
	}

	if base := fileStore.BasePath(); filepath.Base(base) != "Root A" {
		t.Fatalf("expected Root A to stay live, got %q", base)
	}
	if ids := listWorkspaceIDs(t, handler); ids[existingID] != 1 {
		t.Fatalf("expected the live workspace to remain listed, got %#v", ids)
	}
}

// TestApplyWorkspaceRoot_WithoutFolderStoreIsANoOp keeps builds that have no
// folder store able to save a directory for the next start.
func TestApplyWorkspaceRoot_WithoutFolderStoreIsANoOp(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	refresh, err := handler.ApplyWorkspaceRoot(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("expected no error without a folder store, got %v", err)
	}
	if refresh.Imported != 0 || refresh.Warnings == nil {
		t.Fatalf("expected an empty refresh, got %+v", refresh)
	}
}
