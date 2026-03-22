package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectPath(t *testing.T) {
	dir := t.TempDir()

	// Create a project directory
	projectDir := filepath.Join(dir, "my-project")
	os.MkdirAll(projectDir, 0755)

	// Workspace folder
	wsDir := filepath.Join(dir, "workspaces", "my-workspace")
	os.MkdirAll(wsDir, 0755)

	// Relative path from workspace to project
	absPath, resolved := ResolveProjectPath(wsDir, "../../my-project")
	if !resolved {
		t.Errorf("expected resolved=true for existing project dir")
	}
	if filepath.Base(absPath) != "my-project" {
		t.Errorf("absPath=%q, expected to end with my-project", absPath)
	}
}

func TestResolveProjectPath_NotFound(t *testing.T) {
	dir := t.TempDir()

	absPath, resolved := ResolveProjectPath(dir, "../../nonexistent")
	if resolved {
		t.Errorf("expected resolved=false for nonexistent path")
	}
	if absPath == "" {
		t.Error("absPath should still be set even when not resolved")
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

	// Create a project directory alongside the workspace
	projectDir := filepath.Join(dir, "..", "my-project")
	os.MkdirAll(projectDir, 0755)

	ws := newTestWorkspace("ws-1", "My Workspace")
	ws.ProjectPath = "../../my-project"
	store.Save(ws)

	// The path won't resolve because the relative path from the workspace folder
	// doesn't match our temp dir layout, but it should still return info
	info, err := store.GetProjectPathInfo("ws-1")
	if err != nil {
		t.Fatalf("GetProjectPathInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RelativePath != "../../my-project" {
		t.Errorf("RelativePath=%q, want %q", info.RelativePath, "../../my-project")
	}
}

func TestFileStore_GetProjectPathInfo_NoPath(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ws := newTestWorkspace("ws-1", "No Path")
	store.Save(ws)

	info, err := store.GetProjectPathInfo("ws-1")
	if err != nil {
		t.Fatalf("GetProjectPathInfo: %v", err)
	}
	if info != nil {
		t.Error("expected nil info for workspace without project_path")
	}
}
