package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalConfigLegacyStagingMoveRefusesBeforeMovingSeparatedWorkspace(t *testing.T) {
	local, files, ws, folder := localConfigFixture(t)
	localConfigMust(t, local.MigrateBindingsFile(t.Context(), folder, ws.ID))
	before, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	localConfigMust(t, err)
	destination := filepath.Join(t.TempDir(), "confirmed-root")
	result := MoveStagedContent(files.BasePath(), destination)
	if len(result.Moved) != 0 || len(result.Warnings) != 1 {
		t.Fatal("legacy mover accepted a separated workspace")
	}
	after, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	localConfigMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("refusal changed canonical bytes")
	}
	if _, err := os.Stat(filepath.Join(destination, filepath.Base(folder))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("workspace physically moved before refusal")
	}
	root, err := os.OpenRoot(folder)
	localConfigMust(t, err)
	defer func() { _ = root.Close() }()
	value, err := FromJSON(before)
	localConfigMust(t, err)
	if err := rewriteInRoot(root, WorkspaceConfigFile, value); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("raw writer accepted private marker")
	}
	loaded, err := local.ReadBindingsFile(t.Context(), folder, ws.ID)
	localConfigMust(t, err)
	if loaded.MCPBindings[0].Config["arbitrary_name"] != "synthetic-connector-secret" {
		t.Fatal("refusal lost native settings")
	}
}
