package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteToStoreForWorkspaceUsesWorkspaceFolderTarget(t *testing.T) {
	store, ws := newStoreOperationTestWorkspace(t, "ws-store-folder", "Store Folder")

	node := &StoreNode{
		ID:              "store-1",
		WorkspaceID:     ws.ID,
		Name:            "Reports",
		StorageTarget:   StorageTargetWorkspaceFolder,
		WorkspaceFolder: filepath.Join("reports", "daily"),
		Format:          "text",
		WriteMode:       "overwrite",
		AutoCreateDir:   true,
	}

	if err := WriteToStoreForWorkspace(node, store, ws.ID, filepath.Join("runs", "summary.txt"), "workspace data"); err != nil {
		t.Fatalf("WriteToStoreForWorkspace: %v", err)
	}

	targetPath := filepath.Join(store.GetFilesPath(ws.ID), "reports", "daily", "runs", "summary.txt")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "workspace data" {
		t.Fatalf("expected stored workspace data, got %q", string(data))
	}
	if node.WriteCount != 1 {
		t.Fatalf("expected write count 1, got %d", node.WriteCount)
	}
	if node.LastFilePath != filepath.Join("runs", "summary.txt") {
		t.Fatalf("expected last file path to be relative file path, got %q", node.LastFilePath)
	}
}

func TestWriteToStoreForWorkspacePreservesExternalBaseDir(t *testing.T) {
	baseDir := t.TempDir()
	node := &StoreNode{
		ID:            "store-external",
		Name:          "External",
		BaseDir:       baseDir,
		Format:        "text",
		WriteMode:     "overwrite",
		AutoCreateDir: true,
	}

	if err := WriteToStoreForWorkspace(node, nil, "", "summary.txt", "external data"); err != nil {
		t.Fatalf("WriteToStoreForWorkspace: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "external data" {
		t.Fatalf("expected external data, got %q", string(data))
	}
}

func TestWriteToStoreForWorkspaceRejectsTraversalAndSymlinkEscape(t *testing.T) {
	store, ws := newStoreOperationTestWorkspace(t, "ws-store-secure", "Store Secure")
	node := &StoreNode{
		ID:              "store-secure",
		WorkspaceID:     ws.ID,
		Name:            "Secure",
		StorageTarget:   StorageTargetWorkspaceFolder,
		WorkspaceFolder: "",
		Format:          "text",
		WriteMode:       "overwrite",
		AutoCreateDir:   true,
	}

	if err := WriteToStoreForWorkspace(node, store, ws.ID, "../outside.txt", "nope"); err == nil {
		t.Fatal("expected traversal file path to be rejected")
	}

	outsideDir := t.TempDir()
	linkPath := filepath.Join(store.GetFilesPath(ws.ID), "escape")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	err := WriteToStoreForWorkspace(node, store, ws.ID, filepath.Join("escape", "leak.txt"), "leak")
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "leak.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to be written outside workspace, stat err=%v", statErr)
	}
}

func TestCSVWithoutHeaderForExistingStoreStrictInWorkspaceResolvesFolderTarget(t *testing.T) {
	store, ws := newStoreOperationTestWorkspace(t, "ws-store-csv", "Store CSV")
	if err := os.MkdirAll(filepath.Join(store.GetFilesPath(ws.ID), "reports"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	existingPath := filepath.Join(store.GetFilesPath(ws.ID), "reports", "runs.csv")
	if err := os.WriteFile(existingPath, []byte("date,value\n2026-05-25,low"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	node := &StoreNode{
		ID:              "store-csv",
		WorkspaceID:     ws.ID,
		Name:            "CSV",
		StorageTarget:   StorageTargetWorkspaceFolder,
		WorkspaceFolder: "reports",
		Format:          "csv",
		WriteMode:       "append",
		AutoCreateDir:   true,
	}

	payload, err := CSVWithoutHeaderForExistingStoreStrictInWorkspace(node, store, ws.ID, "runs.csv", "date,value\n2026-05-26,high")
	if err != nil {
		t.Fatalf("CSVWithoutHeaderForExistingStoreStrictInWorkspace: %v", err)
	}
	if strings.TrimSpace(payload) != "2026-05-26,high" {
		t.Fatalf("expected headerless append payload, got %q", payload)
	}

	if _, err := CSVWithoutHeaderForExistingStoreStrictInWorkspace(node, store, ws.ID, "runs.csv", "date,status\n2026-05-26,high"); err == nil {
		t.Fatal("expected mismatched CSV header to be rejected")
	}
}

func newStoreOperationTestWorkspace(t *testing.T, id, name string) (*FileStore, *Workspace) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ws := newTestWorkspace(id, name)
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	return store, ws
}
