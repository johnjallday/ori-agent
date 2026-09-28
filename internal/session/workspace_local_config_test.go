package session

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestWorkspaceLocalConfigAdapterUsesCanonicalOwnerWithoutShadowCopy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("WORKSPACE_DIR", filepath.Join(root, "forbidden-shadow"))
	t.Setenv("ORI_SECRET_STORE_BACKEND", "memory")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(root, "data", "sessions.db")})
	must(err)
	t.Cleanup(func() { _ = db.Close() })
	hybrid := NewHybridStoreWithDB(db, 10)
	adapter := NewWorkspaceStoreAdapter(hybrid)
	legacy, err := workspace.NewFileStore(filepath.Join(root, "canonical"))
	must(err)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Synthetic", Agents: []string{"Guide"}})
	ws.OwnerUserID = "local"
	must(legacy.Save(ws))
	must(adapter.Save(ws))
	must(workspacecontinuity.NewLocalStore(db).RegisterNative(t.Context(), ws.ID))
	must(legacy.Close())
	files, err := workspace.NewFileStoreWithLocalConfig(filepath.Join(root, "canonical"), workspace.NewLocalConfigStore(db, vault.NewMemorySecretStore()))
	must(err)
	t.Cleanup(func() { _ = files.Close() })
	composed := workspace.NewSyncStore(adapter, files)
	profile := &agent.Agent{Role: types.RoleOrchestrator, Settings: types.Settings{APIKey: "synthetic-scoped-key", Model: "offline-model"}}
	must(composed.SaveWorkspaceAgent(ws.ID, "Guide", profile))
	got, found, err := adapter.GetWorkspaceAgent(ws.ID, "Guide")
	must(err)
	if !found || got.Settings.APIKey != profile.Settings.APIKey || got.Appearance == nil {
		t.Fatal("direct SQLite adapter consumer lost the native profile")
	}
	got.Settings.Model = "updated-offline-model"
	must(adapter.SaveWorkspaceAgent(ws.ID, "Guide", got))
	got, found, err = composed.GetWorkspaceAgent(ws.ID, "Guide")
	must(err)
	if !found || got.Settings.Model != "updated-offline-model" || got.Settings.APIKey != profile.Settings.APIKey {
		t.Fatal("direct adapter edit did not reach the canonical private owner")
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden-shadow")); !os.IsNotExist(err) {
		t.Fatal("adapter created or consulted a second workspace snapshot directory")
	}
	folder, err := files.GetFolderPath(ws.ID)
	must(err)
	data, err := os.ReadFile(filepath.Join(folder, "agents", "guide", "config.json"))
	must(err)
	if bytes.Contains(data, []byte(profile.Settings.APIKey)) {
		t.Fatal("canonical scoped edit published a private API key")
	}
	var slots int
	must(db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_local_config WHERE workspace_id=? AND kind='agent'`, ws.ID).Scan(&slots))
	if slots != 1 {
		t.Fatal("canonical edit retained duplicate private snapshots")
	}
	// Exercise the real hybrid/adapter/file composition rather than constructing
	// matching SQL/file timestamps by hand. Native saves must be capturable.
	must(composed.Save(ws))
	must(files.PrepareNativeLocalConfig(t.Context(), ws.ID))
	canonical, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	must(err)
	must(db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := NewSQLiteStore(db).SnapshotContinuityWorkspace(t.Context(), tx, canonical)
		return err
	}))
}

func TestWorkspaceLocalConfigLegacyAdapterRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DIR", filepath.Join(root, "workspaces"))
	adapter := NewWorkspaceStoreAdapter(nil)
	for _, id := range []string{"../outside", "nested/workspace", `nested\workspace`, ".", "..", ""} {
		if workspaceFolderForAdapter(id) != "" {
			t.Fatal("untrusted workspace identity became a filesystem path")
		}
		if err := adapter.SaveWorkspaceAgent(id, "Guide", &agent.Agent{}); err == nil {
			t.Fatal("legacy adapter wrote an invalid workspace identity")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("rejected snapshot write created directories")
	}
}
