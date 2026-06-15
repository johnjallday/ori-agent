package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileStore_CacheIsMetadataOnly guards item 2.0: the resident cache holds
// metadata only (no chat history / tasks), while Get reads through to disk and
// still returns the complete record.
func TestFileStore_CacheIsMetadataOnly(t *testing.T) {
	dir := t.TempDir()

	ws := newTestWorkspace("ws-1", "Alpha")
	ws.FolderSlug = "ws-1"
	ws.Messages = []AgentMessage{{ID: "m1", Content: "hello"}, {ID: "m2", Content: "world"}}
	ws.Tasks = []Task{{ID: "t1"}}
	folder := filepath.Join(dir, "ws-1")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, WorkspaceConfigFile), data, 0644); err != nil {
		t.Fatal(err)
	}

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// The resident cache entry must be metadata-only but keep metadata fields.
	store.mu.RLock()
	cached := store.cache["ws-1"]
	store.mu.RUnlock()
	if cached == nil {
		t.Fatal("workspace not cached after load")
	}
	if len(cached.Messages) != 0 || len(cached.Tasks) != 0 {
		t.Errorf("cache should be metadata-only, got %d messages / %d tasks", len(cached.Messages), len(cached.Tasks))
	}
	if cached.Name != "Alpha" {
		t.Errorf("cache lost metadata: Name=%q, want Alpha", cached.Name)
	}

	// Get reads through to disk and returns the full record.
	got, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 2 || len(got.Tasks) != 1 {
		t.Errorf("Get should read through to full history, got %d messages / %d tasks", len(got.Messages), len(got.Tasks))
	}

	// The cache entry stays metadata-only even after a read-through Get.
	store.mu.RLock()
	cachedAfter := store.cache["ws-1"]
	store.mu.RUnlock()
	if cachedAfter == nil || len(cachedAfter.Messages) != 0 || len(cachedAfter.Tasks) != 0 {
		t.Errorf("cache must remain metadata-only after Get")
	}

	// metadataCacheCopy must not mutate its source.
	src := newTestWorkspace("ws-2", "Beta")
	src.Messages = []AgentMessage{{ID: "x"}}
	src.Tasks = []Task{{ID: "y"}}
	if _, err := metadataCacheCopy(src); err != nil {
		t.Fatalf("metadataCacheCopy: %v", err)
	}
	if len(src.Messages) != 1 || len(src.Tasks) != 1 {
		t.Errorf("metadataCacheCopy mutated its source: %d messages / %d tasks", len(src.Messages), len(src.Tasks))
	}
}
