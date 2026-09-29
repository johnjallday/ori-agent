package workspace

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestLocalConfigNativeCreationStagesPrivateFilesBeforeAdmission(t *testing.T) {
	local, original, _, _ := localConfigFixture(t)
	gate := &resetstate.WorkGate{}
	tracked, err := NewLocalConfigStoreWithWorkGate(local.db, local.secrets, gate)
	localConfigMust(t, err)
	files, err := NewFileStoreWithLocalConfig(original.BasePath(), tracked)
	localConfigMust(t, err)
	t.Cleanup(func() { _ = files.Close() })
	// The host creation owner, not generic Save or discovery, owns this permit.
	release, err := gate.Enter()
	localConfigMust(t, err)
	defer release()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Explicit native creation", Agents: []string{"Guide"}})
	ws.OwnerUserID = "local"
	ws.MCPBindings = []MCPBinding{{ID: "connector", ServerName: "synthetic", Config: map[string]any{"key": "creation-secret"}}}
	coordinator := workspacecontinuity.NewLocalStore(local.db)
	creation, err := coordinator.BeginNativeCreation(t.Context(), ws.ID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?,?,?,?)`, ws.ID, ws.Name, ws.CreatedAt, ws.UpdatedAt)
		return err
	})
	localConfigMust(t, err)
	localConfigMust(t, files.Save(ws))
	source := syntheticPrivateAgent()
	source.Appearance.SetUpload("guide.png")
	localConfigMust(t, files.seedNativeAgent(t.Context(), ws.ID, "Guide", source,
		ownedAppearanceFunc(func(context.Context, string, string) ([]byte, error) { return tinyContinuityImage(t), nil })))
	localConfigMust(t, files.PrepareNativeLocalConfig(t.Context(), ws.ID))
	ag, found, err := files.GetWorkspaceAgent(ws.ID, "Guide")
	localConfigMust(t, err)
	if !found || ag.Settings.APIKey != source.Settings.APIKey {
		t.Fatal("native provisioning stripped the new user's key")
	}
	a, err := coordinator.Attachment(t.Context(), ws.ID)
	localConfigMust(t, err)
	if a.AllowsManual() || a.AllowsAutomatic() || a.AllowsPreparation() {
		t.Fatal("staging granted execution or readiness")
	}
	if _, err := files.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png"); !errors.Is(err, ErrLocalConfigUnavailable) {
		t.Fatal("provisioning was exposed as completed display", err)
	}
	if err := gate.TryFence(t.Context()); err == nil {
		t.Fatal("reset fenced through the native creation owner")
	}
	folder, err := files.GetFolderPath(ws.ID)
	localConfigMust(t, err)
	localConfigMust(t, creation.Complete(t.Context(), func(q workspacecontinuity.Queryer) error {
		// Production owners additionally validate all mirrored domain fields.
		// This isolated owner proves exact canonical identity and denied files.
		var id, name string
		if err := q.QueryRowContext(t.Context(), `SELECT id,name FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, ws.ID).Scan(&id, &name); err != nil {
			return err
		}
		data, err := workspacecontinuity.ReadCanonicalFile(t.Context(), folder, WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
		if err != nil {
			return err
		}
		canonical, err := decodeLocalWorkspace(data, id)
		if err != nil {
			return err
		}
		if canonical.Name != name || canonical.WorkspaceLocalConfigID == "" || len(canonical.MCPBindings[0].Config) != 0 {
			return workspacecontinuity.ErrChanged
		}
		return nil
	}))
	if _, err := files.ReadWorkspaceAppearance(t.Context(), ws.ID, "Guide", "guide.png"); err != nil {
		t.Fatal("completed native files remained unreadable", err)
	}
	a, err = coordinator.Attachment(t.Context(), ws.ID)
	localConfigMust(t, err)
	if a.Provisioning || !a.AllowsAutomatic() {
		t.Fatal("completed creation did not admit native work")
	}
	p, err := coordinator.Preparation(t.Context(), ws.ID)
	localConfigMust(t, err)
	if p.Ready() {
		t.Fatal("native creation pretended to prepare a checkpoint")
	}
}
