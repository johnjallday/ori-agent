package sessionhttp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// seedDiskWorkspace writes a workspace folder into root without going through
// the handler, the way a pre-existing workspace directory already looks on disk
// before Ori is ever pointed at it.
func seedDiskWorkspace(t *testing.T, root, id, name, kind, parentID string) {
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
	if _, err := store.GetFolderPath(id); err != nil {
		t.Fatalf("seed folder path %s: %v", id, err)
	}
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

// treeFingerprint records every file path and its bytes under root, so a test
// can prove a root switch moved, copied, rewrote, and deleted nothing.
func treeFingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// The root-scoped index is a cache the store owns; its bytes change
		// whenever it is opened, which says nothing about workspace content.
		if strings.HasPrefix(filepath.Base(path), agentworkspace.IndexDBFile) {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- test fixture under t.TempDir()
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

func assertTreeUnchanged(t *testing.T, label string, before, after map[string]string) {
	t.Helper()
	for path, content := range before {
		got, ok := after[path]
		if !ok {
			t.Fatalf("%s: %s disappeared from disk", label, path)
		}
		if got != content {
			t.Fatalf("%s: %s was rewritten on disk", label, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Fatalf("%s: %s appeared on disk", label, path)
		}
	}
}

// TestApplyWorkspaceRoot_HidesPreviousRootAndRestoresOnSwitchBack is the
// restart-equivalence requirement: only the active root's workspaces are
// listed, the previous root's are hidden without touching a byte on disk, and
// selecting that root again brings them back exactly once with their
// session-only state intact.
func TestApplyWorkspaceRoot_HidesPreviousRootAndRestoresOnSwitchBack(t *testing.T) {
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

	ctx := context.Background()
	aOnlyID := createTestWorkspace(t, handler, "A Only")

	// Session-only state: a description that lives in SQLite and a note row.
	// Neither can survive the row being recreated from disk, so they prove the
	// original workspace record was preserved rather than re-imported.
	aOnly, err := handler.store.GetWorkspace(ctx, aOnlyID)
	if err != nil {
		t.Fatalf("GetWorkspace(A Only): %v", err)
	}
	aOnly.Description = "session-only description"
	if err := handler.store.UpdateWorkspace(ctx, aOnly); err != nil {
		t.Fatalf("UpdateWorkspace(A Only): %v", err)
	}
	note := &session.WorkspaceNote{
		ID: "note-a-only", WorkspaceID: aOnlyID, Name: "A Note", Content: "kept",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := handler.store.CreateNote(ctx, note); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	seedDiskWorkspace(t, rootB, "ws-b-group", "B Group", string(session.WorkspaceKindGroup), "")
	seedDiskWorkspace(t, rootB, "ws-b-child", "B Child", "", "ws-b-group")
	seedDiskWorkspace(t, rootB, "ws-b-only", "B Only", "", "")

	folderA, err := fileStore.GetFolderPath(aOnlyID)
	if err != nil {
		t.Fatalf("GetFolderPath(A Only): %v", err)
	}
	beforeA := treeFingerprint(t, rootA)
	beforeB := treeFingerprint(t, rootB)

	// A → B: Root A's workspace is hidden, Root B's three appear.
	refresh, err := handler.ApplyWorkspaceRoot(ctx, rootB)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootB): %v", err)
	}
	if refresh.Imported != 3 || refresh.Orphaned != 1 || refresh.Restored != 0 {
		t.Fatalf("A→B refresh = %+v, want imported 3 / orphaned 1 / restored 0", refresh)
	}

	ids := listWorkspaceIDs(t, handler)
	if ids[aOnlyID] != 0 {
		t.Fatalf("expected the previous root's workspace to be hidden, got %#v", ids)
	}
	for _, id := range []string{"ws-b-group", "ws-b-child", "ws-b-only"} {
		if ids[id] != 1 {
			t.Fatalf("expected %s listed once under Root B, got %#v", id, ids)
		}
	}

	hidden, err := handler.store.GetWorkspace(ctx, aOnlyID)
	if err != nil {
		t.Fatalf("GetWorkspace(A Only) after switch: %v", err)
	}
	if hidden.Status != session.WorkspaceStatusMissing {
		t.Fatalf("hidden workspace status = %q, want missing", hidden.Status)
	}
	// Hidden, not deleted: the folder is still exactly where it was.
	if info, statErr := os.Stat(filepath.Join(folderA, agentworkspace.WorkspaceConfigFile)); statErr != nil || info.IsDir() {
		t.Fatalf("previous root's folder must be left untouched: %v", statErr)
	}

	// B → A: the hidden workspace returns once, with its session-only state.
	refresh, err = handler.ApplyWorkspaceRoot(ctx, rootA)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootA): %v", err)
	}
	if refresh.Restored != 1 || refresh.Orphaned != 3 || refresh.Imported != 0 {
		t.Fatalf("B→A refresh = %+v, want restored 1 / orphaned 3 / imported 0", refresh)
	}

	ids = listWorkspaceIDs(t, handler)
	if ids[aOnlyID] != 1 {
		t.Fatalf("expected the restored workspace listed exactly once, got %#v", ids)
	}
	for _, id := range []string{"ws-b-group", "ws-b-child", "ws-b-only"} {
		if ids[id] != 0 {
			t.Fatalf("expected %s hidden under Root A, got %#v", id, ids)
		}
	}

	restored, err := handler.store.GetWorkspace(ctx, aOnlyID)
	if err != nil {
		t.Fatalf("GetWorkspace(A Only) after switching back: %v", err)
	}
	if restored.Status == session.WorkspaceStatusMissing {
		t.Fatal("expected the workspace to be visible again")
	}
	if restored.Description != "session-only description" {
		t.Fatalf("session-only state was lost: description = %q", restored.Description)
	}
	notes, err := handler.store.ListNotesByWorkspace(ctx, aOnlyID)
	if err != nil {
		t.Fatalf("ListNotesByWorkspace: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected the note row to survive the round trip, got %d", len(notes))
	}

	// Switching back again restores Root B's three exactly once, with no
	// duplicate rows anywhere.
	refresh, err = handler.ApplyWorkspaceRoot(ctx, rootB)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootB, second): %v", err)
	}
	if refresh.Restored != 3 || refresh.Orphaned != 1 || refresh.Imported != 0 {
		t.Fatalf("A→B (second) refresh = %+v, want restored 3 / orphaned 1 / imported 0", refresh)
	}
	ids = listWorkspaceIDs(t, handler)
	for _, id := range []string{"ws-b-group", "ws-b-child", "ws-b-only"} {
		if ids[id] != 1 {
			t.Fatalf("expected %s listed exactly once after switching back, got %#v", id, ids)
		}
	}

	// Re-applying the active root changes nothing.
	refresh, err = handler.ApplyWorkspaceRoot(ctx, rootB)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootB, repeat): %v", err)
	}
	if refresh.Imported != 0 || refresh.Orphaned != 0 || refresh.Restored != 0 {
		t.Fatalf("repeating the active root should be idempotent, got %+v", refresh)
	}

	// Nothing was moved, copied, rewritten, or deleted under either root.
	assertTreeUnchanged(t, "Root A", beforeA, treeFingerprint(t, rootA))
	assertTreeUnchanged(t, "Root B", beforeB, treeFingerprint(t, rootB))
}

// TestApplyWorkspaceRoot_PreservesImportsAndUnmanagedRows proves the
// previous-root rule is scoped to workspaces the old root actually managed:
// explicit folder imports, folders recovered from outside the root, legacy
// database-only rows, and already hidden or trashed rows keep their
// established behavior.
func TestApplyWorkspaceRoot_PreservesImportsAndUnmanagedRows(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	rootA := filepath.Join(t.TempDir(), "Root A")
	rootB := filepath.Join(t.TempDir(), "Root B")
	outside := t.TempDir()

	fileStore, err := agentworkspace.NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore(rootA): %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	ctx := context.Background()

	// A normal Root A workspace: the control that must be hidden.
	managedID := createTestWorkspace(t, handler, "Managed By A")

	// An explicit folder import living outside both roots.
	externalFolder := filepath.Join(outside, "imported-elsewhere")
	registerImportedWorkspace(t, handler, fileStore, "ws-import-outside", "Import Outside", externalFolder)

	// An explicit folder import that happens to sit inside Root A: import
	// ownership wins over location.
	insideFolder := filepath.Join(rootA, "imported-inside")
	registerImportedWorkspace(t, handler, fileStore, "ws-import-inside", "Import Inside", insideFolder)

	// A workspace recovered ("located") at an absolute path outside the root.
	recoveredFolder := filepath.Join(outside, "recovered")
	registerRecoveredWorkspace(t, handler, fileStore, "ws-recovered", "Recovered Elsewhere", recoveredFolder)

	// A legacy database-only row with nothing on disk at all.
	createSessionOnlyWorkspace(t, handler, "ws-db-only", "Database Only", session.WorkspaceStatusActive)
	// A row that was already hidden before the switch, and a trashed one.
	createSessionOnlyWorkspace(t, handler, "ws-already-missing", "Already Missing", session.WorkspaceStatusMissing)
	createSessionOnlyWorkspace(t, handler, "ws-trashed", "Trashed", session.WorkspaceStatusTrashed)

	refresh, err := handler.ApplyWorkspaceRoot(ctx, rootB)
	if err != nil {
		t.Fatalf("ApplyWorkspaceRoot(rootB): %v", err)
	}

	// Only the workspace Root A actually managed changes visibility.
	if refresh.Orphaned != 1 {
		t.Fatalf("orphaned = %d, want 1 — only the root-managed workspace may be hidden (%+v)", refresh.Orphaned, refresh)
	}

	ids := listWorkspaceIDs(t, handler)
	if ids[managedID] != 0 {
		t.Fatalf("expected the root-managed workspace hidden, got %#v", ids)
	}
	for _, id := range []string{"ws-import-outside", "ws-import-inside", "ws-recovered", "ws-db-only"} {
		if ids[id] != 1 {
			t.Fatalf("expected %s to keep its established visibility, got %#v", id, ids)
		}
	}
	for _, id := range []string{"ws-already-missing", "ws-trashed"} {
		if ids[id] != 0 {
			t.Fatalf("expected %s to stay out of listings, got %#v", id, ids)
		}
	}

	// Statuses of the rows the switch must not own are untouched.
	for id, want := range map[string]session.WorkspaceStatus{
		"ws-already-missing": session.WorkspaceStatusMissing,
		"ws-trashed":         session.WorkspaceStatusTrashed,
		"ws-import-outside":  session.WorkspaceStatusActive,
		"ws-import-inside":   session.WorkspaceStatusActive,
		"ws-recovered":       session.WorkspaceStatusActive,
		"ws-db-only":         session.WorkspaceStatusActive,
	} {
		ws, getErr := handler.store.GetWorkspace(ctx, id)
		if getErr != nil {
			t.Fatalf("GetWorkspace(%s): %v", id, getErr)
		}
		if ws.Status != want {
			t.Fatalf("%s status = %q, want %q", id, ws.Status, want)
		}
	}

	// The imported folders were neither relocated nor copied.
	for _, folder := range []string{externalFolder, insideFolder, recoveredFolder} {
		if _, statErr := os.Stat(filepath.Join(folder, agentworkspace.WorkspaceConfigFile)); statErr != nil {
			t.Fatalf("expected %s to remain in place: %v", folder, statErr)
		}
	}
}

// registerImportedWorkspace creates a session row carrying the explicit
// folder-import marker and registers its folder with the file store, the shape
// the single-folder import flow leaves behind.
func registerImportedWorkspace(t *testing.T, handler *Handler, fileStore *agentworkspace.FileStore, id, name, folder string) {
	t.Helper()
	registerFolderWorkspace(t, fileStore, id, name, folder)

	ws := &session.Workspace{
		ID:     id,
		Name:   name,
		Status: session.WorkspaceStatusActive,
		SharedData: map[string]any{
			"folder_import": map[string]any{"enabled": true, "path": folder},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace(%s): %v", id, err)
	}
}

// registerRecoveredWorkspace creates an ordinary session row whose folder was
// located outside the workspace root, as the sync "locate" action leaves it.
func registerRecoveredWorkspace(t *testing.T, handler *Handler, fileStore *agentworkspace.FileStore, id, name, folder string) {
	t.Helper()
	registerFolderWorkspace(t, fileStore, id, name, folder)
	createSessionOnlyWorkspace(t, handler, id, name, session.WorkspaceStatusActive)
}

func registerFolderWorkspace(t *testing.T, fileStore *agentworkspace.FileStore, id, name, folder string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatalf("create folder %s: %v", folder, err)
	}
	folderWS := &agentworkspace.Workspace{
		ID:         id,
		Name:       name,
		Status:     agentworkspace.StatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		SharedData: map[string]any{},
	}
	if err := fileStore.RebindExistingFolder(folderWS, folder); err != nil {
		t.Fatalf("RebindExistingFolder(%s): %v", id, err)
	}
}

func createSessionOnlyWorkspace(t *testing.T, handler *Handler, id, name string, status session.WorkspaceStatus) {
	t.Helper()
	ws := &session.Workspace{
		ID:        id,
		Name:      name,
		Status:    status,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := handler.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace(%s): %v", id, err)
	}
}

// Note on the warning path: reconcile warnings are non-fatal and are carried
// through to the refresh result, which TestWorkspaceRootSettingsHandler_Post_
// AppliesRootToRunningProcess proves end to end with an injected updater. They
// are deliberately not reproduced here — the folder loader derives every
// parent from physical location and quarantines unparseable files, so no
// fixture reaches the reconcile in a state that warns without adding a
// fault-injection seam this fix does not otherwise need.

// TestApplyWorkspaceRoot_ConcurrentWithExplicitRescan exercises the real
// rescanMu shared by both paths. Run with -race it proves the two serialize,
// complete, and leave one coherent root — and that the explicit Rescan
// endpoint's cooldown exemption is unchanged.
func TestApplyWorkspaceRoot_ConcurrentWithExplicitRescan(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	rootA := filepath.Join(t.TempDir(), "Root A")
	rootB := filepath.Join(t.TempDir(), "Root B")

	fileStore, err := agentworkspace.NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	createTestWorkspace(t, handler, "A Only")
	seedDiskWorkspace(t, rootB, "ws-b-only", "B Only", "", "")

	ctx := context.Background()
	var wg sync.WaitGroup
	const rounds = 6

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			target := rootB
			if i%2 == 1 {
				target = rootA
			}
			if _, err := handler.ApplyWorkspaceRoot(ctx, target); err != nil {
				t.Errorf("ApplyWorkspaceRoot: %v", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds*2; i++ {
			// Explicit rescans ignore the cooldown and must never be starved
			// or corrupted by a concurrent root application.
			resp := postWorkspaceRescan(t, handler)
			if resp["skipped"] == true {
				t.Error("an explicit rescan must never be skipped by the cooldown")
				return
			}
		}
	}()

	wg.Wait()

	// Whichever root won the last write, the visible set matches it exactly.
	base := fileStore.BasePath()
	ids := listWorkspaceIDs(t, handler)
	if filepath.Base(base) == "Root B" {
		if ids["ws-b-only"] != 1 {
			t.Fatalf("final root is Root B but its workspace is not listed: %#v", ids)
		}
	} else if ids["ws-b-only"] != 0 {
		t.Fatalf("final root is Root A but Root B's workspace is listed: %#v", ids)
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
