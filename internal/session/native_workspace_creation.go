package session

import (
	"context"
	"database/sql"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func (a *WorkspaceStoreAdapter) NativeWorkspaceDatabase() *database.DB { return a.store.DB() }

// InsertNativeWorkspace is used only by workspace.CreateNativeWorkspace, never
// by ordinary Save/import/discovery. It leaves commit and provisional admission
// with the explicit creator. New-work order defaults are applied in this view.
func (a *WorkspaceStoreAdapter) InsertNativeWorkspace(ctx context.Context, tx *sql.Tx, ws *workspace.Workspace) error {
	if tx == nil || ws == nil || !workspacecontinuity.ValidID(ws.ID) || ws.OwnerUserID != "local" || ws.CreatedAt.IsZero() || ws.UpdatedAt.IsZero() {
		return workspacecontinuity.ErrInvalid
	}
	if ws.OrderIndex == 0 {
		var next int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(order_index),0)+1 FROM workspaces WHERE COALESCE(parent_id,'')=?`, ws.ParentID).Scan(&next); err != nil {
			return err
		}
		ws.OrderIndex = next
	}
	return insertWorkspace(ctx, tx, a.toSessionWorkspace(ws))
}

// ValidateNativeWorkspace verifies the exact native representation written by
// the SyncStore, not a checkpoint-sized DTO. SQL compares the bounded expected
// serialized fields in place: it does not materialize another large history or
// normalize malformed/stale rows to apparent agreement. All column identifiers
// below are compiled constants, never portable records or request field names.
func (a *WorkspaceStoreAdapter) ValidateNativeWorkspace(ctx context.Context, q workspacecontinuity.Queryer, ws *workspace.Workspace) error {
	if q == nil || ws == nil || ws.OwnerUserID != "local" || !workspacecontinuity.ValidID(ws.ID) {
		return workspacecontinuity.ErrInvalid
	}
	expected := a.toSessionWorkspace(ws)
	f := serializeWorkspaceFields(expected)
	columns := []struct {
		name  string
		value []byte
	}{
		{"agent_instances", f.agentInstances}, {"tags", f.tags}, {"shared_data", f.sharedData}, {"layout", f.layout},
		{"messages_json", f.messages}, {"tasks_json", f.tasks}, {"attachments_json", f.attachments},
		{"folders_json", f.folders}, {"scheduled_tasks_json", f.scheduledTasks}, {"store_nodes_json", f.storeNodes},
		{"workflows_json", f.workflows}, {"directory_references_json", f.directoryReferences},
		{"mcp_bindings_json", f.mcpBindings}, {"agent_mcp_access_json", f.agentMCPAccess},
		{"skill_bindings_json", f.skillBindings}, {"agent_skill_access_json", f.agentSkillAccess},
		{"opportunities_json", f.opportunities}, {"installed_capabilities_json", f.installedCapabilities},
		{"toolbox_state_json", f.toolboxState}, {"mission_state_json", f.missionState}, {"assistant_program_json", f.assistantProgram},
	}
	where := []string{"id=?", "owner_user_id='local'", "deleted_at IS NULL", "name=?", "folder_slug=?", "kind=?", "COALESCE(description,'')=?",
		"COALESCE(parent_id,'')=?", "order_index=?", "status=?", "ticket_sequence=?", "ticket_migration_version=?", "version=?", "allow_native_mcp_cli=?"}
	args := []any{ws.ID, expected.Name, expected.FolderSlug, string(NormalizeWorkspaceKind(string(expected.Kind))), expected.Description,
		expected.ParentID, expected.OrderIndex, string(f.status), expected.TicketSequence, expected.TicketMigrationVersion, expected.Version, expected.AllowNativeMCPCLI}
	for _, column := range columns {
		where = append(where, "COALESCE(CAST("+column.name+" AS TEXT),'null')=?")
		value := string(column.value)
		if value == "" {
			value = "null"
		}
		args = append(args, value)
	}
	var createdRaw, updatedRaw any
	err := q.QueryRowContext(ctx, "SELECT created_at,updated_at FROM workspaces WHERE "+strings.Join(where, " AND "), args...).Scan(&createdRaw, &updatedRaw)
	if err == sql.ErrNoRows {
		return workspacecontinuity.ErrChanged
	}
	if err != nil {
		return err
	}
	created, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return workspacecontinuity.ErrInvalid
	}
	updated, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return workspacecontinuity.ErrInvalid
	}
	if !created.Equal(ws.CreatedAt) || !updated.Equal(ws.UpdatedAt) {
		return workspacecontinuity.ErrChanged
	}
	return nil
}

func (a *WorkspaceStoreAdapter) CheckWorkspaceExecution(ctx context.Context, workspaceID string, automatic bool) error {
	return workspace.RequireWorkspaceExecution(ctx, a.agentSnapshots, workspaceID, automatic)
}

var _ workspace.NativeWorkspaceRecords = (*WorkspaceStoreAdapter)(nil)
