package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIndex_RegisterAndList(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	now := time.Now().Truncate(time.Second)

	err = idx.Register(IndexEntry{
		ID: "ws-1", Name: "Alpha", FolderPath: "alpha", UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	err = idx.Register(IndexEntry{
		ID: "ws-2", Name: "Beta", FolderPath: "beta", UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	// Should be sorted by name
	if entries[0].Name != "Alpha" {
		t.Errorf("first entry Name=%q, want Alpha", entries[0].Name)
	}
}

func TestIndex_Get(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_ = idx.Register(IndexEntry{
		ID: "ws-1", Name: "Alpha", FolderPath: "alpha", UpdatedAt: time.Now(),
	})

	entry, err := idx.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Name != "Alpha" {
		t.Errorf("Name=%q, want Alpha", entry.Name)
	}

	_, err = idx.Get("nonexistent")
	if err == nil {
		t.Error("Get nonexistent should fail")
	}
}

func TestIndex_Unregister(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_ = idx.Register(IndexEntry{
		ID: "ws-1", Name: "Alpha", FolderPath: "alpha", UpdatedAt: time.Now(),
	})

	if err := idx.Unregister("ws-1"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List returned %d entries after unregister, want 0", len(entries))
	}
}

func TestIndex_RegisterUpsert(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_ = idx.Register(IndexEntry{
		ID: "ws-1", Name: "Old Name", FolderPath: "old-name", UpdatedAt: time.Now(),
	})

	// Re-register with updated name
	_ = idx.Register(IndexEntry{
		ID: "ws-1", Name: "New Name", FolderPath: "new-name", UpdatedAt: time.Now(),
	})

	entry, err := idx.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Name != "New Name" {
		t.Errorf("Name=%q after upsert, want %q", entry.Name, "New Name")
	}
	if entry.FolderPath != "new-name" {
		t.Errorf("FolderPath=%q after upsert, want %q", entry.FolderPath, "new-name")
	}
}

func TestIndex_Rebuild(t *testing.T) {
	dir := t.TempDir()

	// Create workspace folders on disk manually
	ws1Dir := filepath.Join(dir, "alpha")
	_ = os.MkdirAll(ws1Dir, 0755)
	ws1 := newTestWorkspace("ws-1", "Alpha")
	ws1.FolderSlug = "alpha"
	data1, _ := ws1.ToJSON()
	_ = os.WriteFile(filepath.Join(ws1Dir, WorkspaceConfigFile), data1, 0644)

	ws2Dir := filepath.Join(dir, "beta")
	_ = os.MkdirAll(ws2Dir, 0755)
	ws2 := newTestWorkspace("ws-2", "Beta")
	ws2.FolderSlug = "beta"
	data2, _ := ws2.ToJSON()
	_ = os.WriteFile(filepath.Join(ws2Dir, WorkspaceConfigFile), data2, 0644)

	// Create index (starts empty)
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	// Rebuild should discover both workspaces
	if err := idx.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries after rebuild, want 2", len(entries))
	}
}

func TestIndex_RebuildWithSubWorkspaces(t *testing.T) {
	dir := t.TempDir()

	// Create parent workspace
	parentDir := filepath.Join(dir, "parent")
	_ = os.MkdirAll(parentDir, 0755)
	parent := newTestWorkspace("ws-parent", "Parent")
	parent.FolderSlug = "parent"
	parentData, _ := parent.ToJSON()
	_ = os.WriteFile(filepath.Join(parentDir, WorkspaceConfigFile), parentData, 0644)

	// Create child workspace inside sub-workspaces/
	childDir := filepath.Join(parentDir, SubWorkspacesDir, "child")
	_ = os.MkdirAll(childDir, 0755)
	child := newTestWorkspace("ws-child", "Child")
	child.FolderSlug = "child"
	childData, _ := child.ToJSON()
	_ = os.WriteFile(filepath.Join(childDir, WorkspaceConfigFile), childData, 0644)

	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2 (parent + child)", len(entries))
	}

	// Child should have parent_id set
	childEntry, err := idx.Get("ws-child")
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if childEntry.ParentID != "ws-parent" {
		t.Errorf("child ParentID=%q, want %q", childEntry.ParentID, "ws-parent")
	}
}

func TestIndex_RebuildFromEntries(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	// Seed a stale entry that a rebuild must drop.
	_ = idx.Register(IndexEntry{ID: "stale", Name: "Stale", FolderPath: "stale", UpdatedAt: time.Now()})

	now := time.Now().Truncate(time.Second)
	err = idx.RebuildFromEntries([]IndexEntry{
		{ID: "ws-1", Name: "Alpha", FolderPath: "alpha", UpdatedAt: now},
		{ID: "ws-2", Name: "Beta", FolderPath: "alpha/sub-workspaces/beta", ParentID: "ws-1", UpdatedAt: now},
	})
	if err != nil {
		t.Fatalf("RebuildFromEntries: %v", err)
	}

	entries, err := idx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2 (stale should be gone)", len(entries))
	}
	if _, err := idx.Get("stale"); err == nil {
		t.Error("stale entry should have been removed by RebuildFromEntries")
	}
	child, err := idx.Get("ws-2")
	if err != nil {
		t.Fatalf("Get ws-2: %v", err)
	}
	if child.ParentID != "ws-1" {
		t.Errorf("ParentID=%q, want ws-1", child.ParentID)
	}
}

// TestFileStore_IndexFromCacheMatchesDiskRebuild guards the 1.3 optimization: the
// index that NewFileStore builds from the cache (rebuildIndexFromCache) must be
// identical to the one a full disk-scan Rebuild produces.
func TestFileStore_IndexFromCacheMatchesDiskRebuild(t *testing.T) {
	dir := t.TempDir()

	parentDir := filepath.Join(dir, "parent")
	_ = os.MkdirAll(parentDir, 0755)
	parent := newTestWorkspace("ws-parent", "Parent")
	parent.FolderSlug = "parent"
	pData, _ := parent.ToJSON()
	_ = os.WriteFile(filepath.Join(parentDir, WorkspaceConfigFile), pData, 0644)

	childDir := filepath.Join(parentDir, SubWorkspacesDir, "child")
	_ = os.MkdirAll(childDir, 0755)
	child := newTestWorkspace("ws-child", "Child")
	child.FolderSlug = "child"
	cData, _ := child.ToJSON()
	_ = os.WriteFile(filepath.Join(childDir, WorkspaceConfigFile), cData, 0644)

	// Construction populates the index from the loaded cache.
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	fromCache := indexEntriesByID(t, store.index)
	if len(fromCache) != 2 {
		t.Fatalf("cache-built index has %d entries, want 2", len(fromCache))
	}
	if fromCache["ws-child"].ParentID != "ws-parent" {
		t.Errorf("child ParentID=%q, want ws-parent", fromCache["ws-child"].ParentID)
	}

	// A full disk-scan rebuild must yield the same entries.
	if err := store.index.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	fromDisk := indexEntriesByID(t, store.index)

	if !reflect.DeepEqual(fromCache, fromDisk) {
		t.Fatalf("index from cache != index from disk rebuild\n cache=%+v\n disk=%+v", fromCache, fromDisk)
	}
}

func indexEntriesByID(t *testing.T, idx *Index) map[string]IndexEntry {
	t.Helper()
	list, err := idx.List()
	if err != nil {
		t.Fatalf("index List: %v", err)
	}
	m := make(map[string]IndexEntry, len(list))
	for _, e := range list {
		m[e.ID] = e
	}
	return m
}

func TestIndex_ParentID(t *testing.T) {
	dir := t.TempDir()
	idx, err := NewIndex(dir)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_ = idx.Register(IndexEntry{
		ID: "ws-parent", Name: "Parent", FolderPath: "parent", UpdatedAt: time.Now(),
	})
	_ = idx.Register(IndexEntry{
		ID: "ws-child", Name: "Child", FolderPath: "parent/sub-workspaces/child",
		ParentID: "ws-parent", UpdatedAt: time.Now(),
	})

	child, err := idx.Get("ws-child")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if child.ParentID != "ws-parent" {
		t.Errorf("ParentID=%q, want %q", child.ParentID, "ws-parent")
	}
}
