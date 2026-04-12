package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectPath(t *testing.T) {
	dir := t.TempDir()

	// Create a workspace folder with a project subdirectory
	wsDir := filepath.Join(dir, "workspaces", "my-workspace")
	_ = os.MkdirAll(wsDir, 0755)
	projectDir := filepath.Join(wsDir, "my-project")
	_ = os.MkdirAll(projectDir, 0755)

	// Relative path within the workspace folder
	absPath, resolved := ResolveProjectPath(wsDir, "my-project")
	if !resolved {
		t.Errorf("expected resolved=true for existing project dir")
	}
	if filepath.Base(absPath) != "my-project" {
		t.Errorf("absPath=%q, expected to end with my-project", absPath)
	}
}

func TestResolveProjectPath_NotFound(t *testing.T) {
	dir := t.TempDir()

	absPath, resolved := ResolveProjectPath(dir, "nonexistent")
	if resolved {
		t.Errorf("expected resolved=false for nonexistent path")
	}
	if absPath == "" {
		t.Error("absPath should still be set even when not resolved")
	}
}

func TestResolveProjectPath_Traversal(t *testing.T) {
	dir := t.TempDir()

	// Create a target outside the workspace folder
	outsideDir := filepath.Join(dir, "outside")
	_ = os.MkdirAll(outsideDir, 0755)

	wsDir := filepath.Join(dir, "workspaces", "my-workspace")
	_ = os.MkdirAll(wsDir, 0755)

	// Attempt to traverse outside the workspace folder
	absPath, resolved := ResolveProjectPath(wsDir, "../../outside")
	if resolved {
		t.Error("expected resolved=false for path traversal")
	}
	if absPath != "" {
		t.Errorf("expected empty absPath for traversal attempt, got %q", absPath)
	}
}

func TestResolveProjectPath_Empty(t *testing.T) {
	absPath, resolved := ResolveProjectPath("/some/path", "")
	if resolved {
		t.Error("expected resolved=false for empty project_path")
	}
	if absPath != "" {
		t.Error("expected empty absPath for empty project_path")
	}
}

func TestFileStore_GetProjectPathInfo(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "My Workspace")
	ws.ProjectPath = "src"
	_ = store.Save(ws)

	// Create the project directory inside the workspace folder
	wsFolder, _ := store.GetFolderPath("ws-1")
	_ = os.MkdirAll(filepath.Join(wsFolder, "src"), 0755)

	info, err := store.GetProjectPathInfo("ws-1")
	if err != nil {
		t.Fatalf("GetProjectPathInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RelativePath != "src" {
		t.Errorf("RelativePath=%q, want %q", info.RelativePath, "src")
	}
}

func TestFileStore_GetProjectPathInfo_NoPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "No Path")
	_ = store.Save(ws)

	info, err := store.GetProjectPathInfo("ws-1")
	if err != nil {
		t.Fatalf("GetProjectPathInfo: %v", err)
	}
	if info != nil {
		t.Error("expected nil info for workspace without project_path")
	}
}
