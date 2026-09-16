package server

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Guided setup reaches the data directory's allowlist through this helper, so
// a Home or project it creates keeps its staffed agents across a restart.
func TestAllowlistLocallyCreatedWorkspaceRecordsIntoTheBuilderAllowlist(t *testing.T) {
	allowlist := workspace.NewAllowlist(filepath.Join(t.TempDir(), workspace.DefaultAllowlistFilename))
	builder := &ServerBuilder{workspaceAllowlist: allowlist}

	builder.allowlistLocallyCreatedWorkspace("home-1")
	builder.allowlistLocallyCreatedWorkspace("home-1")
	builder.allowlistLocallyCreatedWorkspace("   ")

	if !allowlist.Contains("home-1") {
		t.Fatal("created workspace was not recorded")
	}
	reloaded, err := workspace.LoadAllowlist(allowlist.PersistencePath())
	if err != nil || !reloaded.Contains("home-1") {
		t.Fatalf("recorded workspace did not persist: err=%v", err)
	}
}

func TestAllowlistLocallyCreatedWorkspaceWithoutAnAllowlistIsANoOp(t *testing.T) {
	var nilBuilder *ServerBuilder
	nilBuilder.allowlistLocallyCreatedWorkspace("home-1")
	(&ServerBuilder{}).allowlistLocallyCreatedWorkspace("home-1")
}
