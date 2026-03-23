package sessionhttp

import (
	"os"
	"path/filepath"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCreateWorkspaceUsesResolvedWorkspaceRoot(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	baseDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("failed to create workspace file store: %v", err)
	}

	customRoot := t.TempDir()
	handler.SetWorkspaceStore(fileStore)
	handler.SetWorkspaceRootResolver(func() string {
		return customRoot
	})

	createTestWorkspace(t, handler, "Roadmap")

	customConfig := filepath.Join(customRoot, "roadmap", agentworkspace.WorkspaceConfigFile)
	if _, err := os.Stat(customConfig); err != nil {
		t.Fatalf("expected workspace folder in resolved custom root: %v", err)
	}

	defaultConfig := filepath.Join(baseDir, "roadmap", agentworkspace.WorkspaceConfigFile)
	if _, err := os.Stat(defaultConfig); !os.IsNotExist(err) {
		t.Fatalf("did not expect workspace folder in file store base path, stat err=%v", err)
	}
}
