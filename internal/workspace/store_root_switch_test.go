package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// seedRootWorkspace writes a workspace folder directly under root, the same
// shape NewFileStore discovers at startup.
func seedRootWorkspace(t *testing.T, root, id, name string) string {
	t.Helper()

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(%s): %v", root, err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close seed store: %v", err)
		}
	}()

	ws := newTestWorkspace(id, name)
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	folder, err := store.GetFolderPath(id)
	if err != nil {
		t.Fatalf("seed folder path %s: %v", id, err)
	}
	return folder
}

// seedNestedWorkspace writes a child workspace inside parent's sub-workspaces
// directory so the loader derives its parent from physical location.
func seedNestedWorkspace(t *testing.T, root, parentID, id, name string) string {
	t.Helper()

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(%s): %v", root, err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close seed store: %v", err)
		}
	}()

	child := newTestWorkspace(id, name)
	child.ParentID = parentID
	if err := store.Save(child); err != nil {
		t.Fatalf("seed nested %s: %v", id, err)
	}
	folder, err := store.GetFolderPath(id)
	if err != nil {
		t.Fatalf("seed nested folder path %s: %v", id, err)
	}
	return folder
}

func sortedIDs(t *testing.T, store *FileStore) []string {
	t.Helper()
	ids, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(ids)
	return ids
}

func indexIDs(t *testing.T, store *FileStore) []string {
	t.Helper()
	idx := store.GetIndex()
	if idx == nil {
		t.Fatal("expected an index on the live store")
	}
	entries, err := idx.List()
	if err != nil {
		t.Fatalf("index List: %v", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestFileStore_SetBasePath_SwitchesRootAndBack proves a live root switch shows
// exactly the target root's workspaces — including physical grouping — and that
// switching back restores the original root's view.
func TestFileStore_SetBasePath_SwitchesRootAndBack(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "Root A")
	rootB := filepath.Join(t.TempDir(), "Root B")

	folderA := seedRootWorkspace(t, rootA, "ws-a-only", "A Only")
	groupB := seedRootWorkspace(t, rootB, "ws-b-group", "B Group")
	folderB := seedRootWorkspace(t, rootB, "ws-b-only", "B Only")
	nestedB := seedNestedWorkspace(t, rootB, "ws-b-group", "ws-b-child", "B Child")

	store, err := NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore(rootA): %v", err)
	}
	defer func() { _ = store.Close() }()

	if got := sortedIDs(t, store); !equalIDs(got, []string{"ws-a-only"}) {
		t.Fatalf("expected only Root A workspaces, got %v", got)
	}

	change, err := store.SetBasePath(rootB)
	if err != nil {
		t.Fatalf("SetBasePath(rootB): %v", err)
	}
	if !change.Switched {
		t.Fatal("expected Switched=true for a genuine root change")
	}
	if !sameWorkspaceRoot(change.PreviousRoot, rootA) {
		t.Fatalf("expected PreviousRoot %s, got %s", rootA, change.PreviousRoot)
	}
	if got := change.PreviousFolders["ws-a-only"]; !sameWorkspaceRoot(got, folderA) {
		t.Fatalf("expected previous folder %s, got %s", folderA, got)
	}

	if got := store.BasePath(); !sameWorkspaceRoot(got, rootB) {
		t.Fatalf("expected BasePath %s, got %s", rootB, got)
	}
	if got := sortedIDs(t, store); !equalIDs(got, []string{"ws-b-child", "ws-b-group", "ws-b-only"}) {
		t.Fatalf("expected Root B workspaces, got %v", got)
	}
	if got := indexIDs(t, store); !equalIDs(got, []string{"ws-b-child", "ws-b-group", "ws-b-only"}) {
		t.Fatalf("expected Root B index entries, got %v", got)
	}
	if _, err := store.Get("ws-a-only"); err == nil {
		t.Fatal("expected Root A workspace to be absent after the switch")
	}

	// Absolute folder paths, cached metadata, and derived parents all follow B.
	gotFolder, err := store.GetFolderPath("ws-b-only")
	if err != nil {
		t.Fatalf("GetFolderPath(ws-b-only): %v", err)
	}
	if !sameWorkspaceRoot(gotFolder, folderB) {
		t.Fatalf("expected folder %s, got %s", folderB, gotFolder)
	}
	gotNested, err := store.GetFolderPath("ws-b-child")
	if err != nil {
		t.Fatalf("GetFolderPath(ws-b-child): %v", err)
	}
	if !sameWorkspaceRoot(gotNested, nestedB) {
		t.Fatalf("expected nested folder %s, got %s", nestedB, gotNested)
	}
	child, err := store.Get("ws-b-child")
	if err != nil {
		t.Fatalf("Get(ws-b-child): %v", err)
	}
	if child.ParentID != "ws-b-group" {
		t.Fatalf("expected parent derived from physical location, got %q", child.ParentID)
	}
	if cached := store.CachedWorkspaces(); cached["ws-b-group"] == nil || cached["ws-b-group"].Name != "B Group" {
		t.Fatalf("expected Root B metadata in the cache, got %#v", cached["ws-b-group"])
	}

	// The store built fresh against B must agree with the switched store.
	fresh, err := NewFileStore(rootB)
	if err != nil {
		t.Fatalf("NewFileStore(rootB): %v", err)
	}
	defer func() { _ = fresh.Close() }()
	if got, want := sortedIDs(t, store), sortedIDs(t, fresh); !equalIDs(got, want) {
		t.Fatalf("switched store %v does not match a fresh store %v", got, want)
	}

	back, err := store.SetBasePath(rootA)
	if err != nil {
		t.Fatalf("SetBasePath(rootA): %v", err)
	}
	if !back.Switched {
		t.Fatal("expected Switched=true switching back")
	}
	if got := back.PreviousFolders["ws-b-only"]; !sameWorkspaceRoot(got, folderB) {
		t.Fatalf("expected previous folder %s, got %s", folderB, got)
	}
	if got := sortedIDs(t, store); !equalIDs(got, []string{"ws-a-only"}) {
		t.Fatalf("expected Root A workspaces after switching back, got %v", got)
	}
	if _, err := store.Get("ws-a-only"); err != nil {
		t.Fatalf("expected Root A workspace to be readable again: %v", err)
	}

	// Neither root was moved, copied, or deleted by the switching.
	for _, folder := range []string{folderA, folderB, nestedB, groupB} {
		if info, err := os.Stat(filepath.Join(folder, WorkspaceConfigFile)); err != nil || info.IsDir() {
			t.Fatalf("expected %s to remain on disk untouched: %v", folder, err)
		}
	}
}

// TestFileStore_SetBasePath_SameRootReloads proves re-applying the active root
// discovers folders added out of band instead of short-circuiting.
func TestFileStore_SetBasePath_SameRootReloads(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Root A")
	seedRootWorkspace(t, root, "ws-first", "First")

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Arrives out of band, as a git pull or cloud sync would deliver it.
	seedRootWorkspace(t, root, "ws-out-of-band", "Out Of Band")

	change, err := store.SetBasePath(root)
	if err != nil {
		t.Fatalf("SetBasePath(same root): %v", err)
	}
	if change.Switched {
		t.Fatal("expected Switched=false when the root did not change")
	}
	if len(change.PreviousFolders) != 0 {
		t.Fatalf("expected no previous-root snapshot for a same-root reload, got %v", change.PreviousFolders)
	}
	if got := sortedIDs(t, store); !equalIDs(got, []string{"ws-first", "ws-out-of-band"}) {
		t.Fatalf("expected the out-of-band workspace to be discovered, got %v", got)
	}
	if got := indexIDs(t, store); !equalIDs(got, []string{"ws-first", "ws-out-of-band"}) {
		t.Fatalf("expected the index to include the out-of-band workspace, got %v", got)
	}
	if got := store.BasePath(); !sameWorkspaceRoot(got, root) {
		t.Fatalf("expected the root to be unchanged, got %s", got)
	}
}

// TestFileStore_SetBasePath_PreparationFailureKeepsLiveState proves a target the
// store cannot use leaves the previous root fully operational.
func TestFileStore_SetBasePath_PreparationFailureKeepsLiveState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Root A")
	folderA := seedRootWorkspace(t, root, "ws-a-only", "A Only")

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// A file, not a directory, occupies the requested root.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	if _, err := store.SetBasePath(blocked); err == nil {
		t.Fatal("expected SetBasePath to fail against a file")
	}

	if got := store.BasePath(); !sameWorkspaceRoot(got, root) {
		t.Fatalf("expected the previous root to remain live, got %s", got)
	}
	if got := sortedIDs(t, store); !equalIDs(got, []string{"ws-a-only"}) {
		t.Fatalf("expected the previous cache to remain intact, got %v", got)
	}
	if got, err := store.GetFolderPath("ws-a-only"); err != nil || !sameWorkspaceRoot(got, folderA) {
		t.Fatalf("expected folder reads to keep working, got %s err=%v", got, err)
	}
	if _, err := store.Get("ws-a-only"); err != nil {
		t.Fatalf("expected workspace reads to keep working: %v", err)
	}
	if got := indexIDs(t, store); !equalIDs(got, []string{"ws-a-only"}) {
		t.Fatalf("expected the previous index to remain usable, got %v", got)
	}

	// An empty path is rejected the same way, without disturbing live state.
	if _, err := store.SetBasePath("   "); err == nil {
		t.Fatal("expected SetBasePath to reject an empty root")
	}
	if got := store.BasePath(); !sameWorkspaceRoot(got, root) {
		t.Fatalf("expected the root to survive a rejected empty path, got %s", got)
	}
}

// TestFileStore_SetBasePath_ConcurrentReadsAndSwitches runs bounded concurrent
// reads, reloads, and root switches. Run with -race, it proves no deadlock, no
// mixed-root path, and no use of a closed index handle.
func TestFileStore_SetBasePath_ConcurrentReadsAndSwitches(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "Root A")
	rootB := filepath.Join(t.TempDir(), "Root B")
	folderA := seedRootWorkspace(t, rootA, "ws-a-only", "A Only")
	folderB := seedRootWorkspace(t, rootB, "ws-b-only", "B Only")

	store, err := NewFileStore(rootA)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	roots := map[string]string{
		"ws-a-only": folderA,
		"ws-b-only": folderB,
	}

	var wg sync.WaitGroup
	const rounds = 12

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			target := rootB
			if i%2 == 1 {
				target = rootA
			}
			if _, err := store.SetBasePath(target); err != nil {
				t.Errorf("SetBasePath: %v", err)
				return
			}
		}
	}()

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds*4; i++ {
				base := store.BasePath()
				if base != "" && !sameWorkspaceRoot(base, rootA) && !sameWorkspaceRoot(base, rootB) {
					t.Errorf("observed an unexpected root %q", base)
					return
				}
				for _, id := range []string{"ws-a-only", "ws-b-only"} {
					folder, err := store.GetFolderPath(id)
					if err != nil {
						continue // not registered under the currently active root
					}
					// A folder path must never mix one root's cache entry with
					// the other root's base path.
					if !sameWorkspaceRoot(folder, roots[id]) {
						t.Errorf("mixed-root path for %s: %s", id, folder)
						return
					}
				}
				_ = store.CachedWorkspaces()
				// A handle captured before a switch may legitimately be closed
				// by it, so the error is not asserted; the call is here so the
				// race detector covers the index swap itself.
				if idx := store.GetIndex(); idx != nil {
					_, _ = idx.List()
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := store.Reload(); err != nil {
				t.Errorf("Reload: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	// The store settles on exactly one coherent root.
	base := store.BasePath()
	wantID := "ws-a-only"
	if sameWorkspaceRoot(base, rootB) {
		wantID = "ws-b-only"
	}
	if got := sortedIDs(t, store); !equalIDs(got, []string{wantID}) {
		t.Fatalf("expected a coherent final state for root %s, got %v", base, got)
	}
}
