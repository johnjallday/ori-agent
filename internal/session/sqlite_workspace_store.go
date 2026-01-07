package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/database"
)

// ============================================================================
// Workspace Operations
// ============================================================================

// CreateWorkspace creates a new workspace.
func (s *SQLiteStore) CreateWorkspace(ctx context.Context, workspace *Workspace) error {
	if workspace.ID == "" {
		return ErrInvalidID
	}

	// Serialize orchestration fields to JSON
	agentsJSON, err := json.Marshal(workspace.Agents)
	if err != nil {
		agentsJSON = []byte("[]")
	}
	if workspace.Agents == nil {
		agentsJSON = []byte("[]")
	}

	agentInstancesJSON, err := json.Marshal(workspace.AgentInstances)
	if err != nil {
		agentInstancesJSON = []byte("[]")
	}
	if workspace.AgentInstances == nil {
		agentInstancesJSON = []byte("[]")
	}

	sharedDataJSON, err := json.Marshal(workspace.SharedData)
	if err != nil {
		sharedDataJSON = []byte("{}")
	}
	if workspace.SharedData == nil {
		sharedDataJSON = []byte("{}")
	}

	var layoutJSON []byte
	if workspace.Layout != nil {
		layoutJSON, err = json.Marshal(workspace.Layout)
		if err != nil {
			layoutJSON = nil
		}
	}

	status := workspace.Status
	if status == "" {
		status = WorkspaceStatusActive
	}

	// Serialize orchestration data JSON fields
	messagesJSON := workspace.MessagesJSON
	if messagesJSON == nil {
		messagesJSON = []byte("[]")
	}
	tasksJSON := workspace.TasksJSON
	if tasksJSON == nil {
		tasksJSON = []byte("[]")
	}
	attachmentsJSON := workspace.AttachmentsJSON
	if attachmentsJSON == nil {
		attachmentsJSON = []byte("[]")
	}
	scheduledTasksJSON := workspace.ScheduledTasksJSON
	if scheduledTasksJSON == nil {
		scheduledTasksJSON = []byte("[]")
	}
	storeNodesJSON := workspace.StoreNodesJSON
	if storeNodesJSON == nil {
		storeNodesJSON = []byte("[]")
	}
	workflowsJSON := workspace.WorkflowsJSON
	if workflowsJSON == nil {
		workflowsJSON = []byte("{}")
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, description, parent_id, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workspace.ID, workspace.Name, workspace.Description, workspace.ParentID, workspace.Color,
		workspace.SessionCount, workspace.CreatedAt, workspace.UpdatedAt,
		string(agentsJSON), string(agentInstancesJSON), string(sharedDataJSON), string(status), layoutJSON,
		string(messagesJSON), string(tasksJSON), string(attachmentsJSON), string(scheduledTasksJSON), string(storeNodesJSON), string(workflowsJSON))

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	return nil
}

// GetWorkspace retrieves a workspace by ID.
func (s *SQLiteStore) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	workspace := &Workspace{}

	var parentID sql.NullString
	var color sql.NullString
	var description sql.NullString
	var agentsJSON sql.NullString
	var agentInstancesJSON sql.NullString
	var sharedDataJSON sql.NullString
	var status sql.NullString
	var layoutJSON sql.NullString
	var messagesJSON sql.NullString
	var tasksJSON sql.NullString
	var attachmentsJSON sql.NullString
	var scheduledTasksJSON sql.NullString
	var storeNodesJSON sql.NullString
	var workflowsJSON sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, parent_id, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json
		FROM workspaces WHERE id = ?
	`, id).Scan(&workspace.ID, &workspace.Name, &description, &parentID, &color,
		&workspace.SessionCount, &workspace.CreatedAt, &workspace.UpdatedAt,
		&agentsJSON, &agentInstancesJSON, &sharedDataJSON, &status, &layoutJSON,
		&messagesJSON, &tasksJSON, &attachmentsJSON, &scheduledTasksJSON, &storeNodesJSON, &workflowsJSON)

	if err == sql.ErrNoRows {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	workspace.Description = description.String
	workspace.ParentID = parentID.String
	workspace.Color = color.String

	// Deserialize orchestration fields
	if agentsJSON.Valid && agentsJSON.String != "" {
		_ = json.Unmarshal([]byte(agentsJSON.String), &workspace.Agents)
	}
	if agentInstancesJSON.Valid && agentInstancesJSON.String != "" {
		_ = json.Unmarshal([]byte(agentInstancesJSON.String), &workspace.AgentInstances)
	}
	if sharedDataJSON.Valid && sharedDataJSON.String != "" {
		_ = json.Unmarshal([]byte(sharedDataJSON.String), &workspace.SharedData)
	}
	if status.Valid && status.String != "" {
		workspace.Status = WorkspaceStatus(status.String)
	}
	if layoutJSON.Valid && layoutJSON.String != "" {
		var layout CanvasLayout
		if err := json.Unmarshal([]byte(layoutJSON.String), &layout); err == nil {
			workspace.Layout = &layout
		}
	}

	// Store raw JSON for orchestration data fields
	if messagesJSON.Valid && messagesJSON.String != "" {
		workspace.MessagesJSON = json.RawMessage(messagesJSON.String)
	}
	if tasksJSON.Valid && tasksJSON.String != "" {
		workspace.TasksJSON = json.RawMessage(tasksJSON.String)
	}
	if attachmentsJSON.Valid && attachmentsJSON.String != "" {
		workspace.AttachmentsJSON = json.RawMessage(attachmentsJSON.String)
	}
	if scheduledTasksJSON.Valid && scheduledTasksJSON.String != "" {
		workspace.ScheduledTasksJSON = json.RawMessage(scheduledTasksJSON.String)
	}
	if storeNodesJSON.Valid && storeNodesJSON.String != "" {
		workspace.StoreNodesJSON = json.RawMessage(storeNodesJSON.String)
	}
	if workflowsJSON.Valid && workflowsJSON.String != "" {
		workspace.WorkflowsJSON = json.RawMessage(workflowsJSON.String)
	}

	return workspace, nil
}

// UpdateWorkspace updates workspace metadata.
func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspace *Workspace) error {
	// Serialize orchestration fields to JSON
	agentsJSON, err := json.Marshal(workspace.Agents)
	if err != nil {
		agentsJSON = []byte("[]")
	}
	if workspace.Agents == nil {
		agentsJSON = []byte("[]")
	}

	agentInstancesJSON, err := json.Marshal(workspace.AgentInstances)
	if err != nil {
		agentInstancesJSON = []byte("[]")
	}
	if workspace.AgentInstances == nil {
		agentInstancesJSON = []byte("[]")
	}

	sharedDataJSON, err := json.Marshal(workspace.SharedData)
	if err != nil {
		sharedDataJSON = []byte("{}")
	}
	if workspace.SharedData == nil {
		sharedDataJSON = []byte("{}")
	}

	var layoutJSON []byte
	if workspace.Layout != nil {
		layoutJSON, err = json.Marshal(workspace.Layout)
		if err != nil {
			layoutJSON = nil
		}
	}

	status := workspace.Status
	if status == "" {
		status = WorkspaceStatusActive
	}

	// Serialize orchestration data JSON fields
	messagesJSON := workspace.MessagesJSON
	if messagesJSON == nil {
		messagesJSON = []byte("[]")
	}
	tasksJSON := workspace.TasksJSON
	if tasksJSON == nil {
		tasksJSON = []byte("[]")
	}
	attachmentsJSON := workspace.AttachmentsJSON
	if attachmentsJSON == nil {
		attachmentsJSON = []byte("[]")
	}
	scheduledTasksJSON := workspace.ScheduledTasksJSON
	if scheduledTasksJSON == nil {
		scheduledTasksJSON = []byte("[]")
	}
	storeNodesJSON := workspace.StoreNodesJSON
	if storeNodesJSON == nil {
		storeNodesJSON = []byte("[]")
	}
	workflowsJSON := workspace.WorkflowsJSON
	if workflowsJSON == nil {
		workflowsJSON = []byte("{}")
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE workspaces
		SET name = ?, description = ?, parent_id = NULLIF(?, ''), color = ?, updated_at = ?,
			agents = ?, agent_instances = ?, shared_data = ?, status = ?, layout = ?,
			messages_json = ?, tasks_json = ?, attachments_json = ?, scheduled_tasks_json = ?, store_nodes_json = ?, workflows_json = ?
		WHERE id = ?
	`, workspace.Name, workspace.Description, workspace.ParentID, workspace.Color, workspace.UpdatedAt,
		string(agentsJSON), string(agentInstancesJSON), string(sharedDataJSON), string(status), layoutJSON,
		string(messagesJSON), string(tasksJSON), string(attachmentsJSON), string(scheduledTasksJSON), string(storeNodesJSON), string(workflowsJSON),
		workspace.ID)

	if err != nil {
		return fmt.Errorf("failed to update workspace: %w", err)
	}

	if err := database.CheckRowsAffectedWithError(result, "workspace", ErrWorkspaceNotFound); err != nil {
		return err
	}

	return nil
}

// DeleteWorkspace removes a workspace, moving sessions and subworkspaces to root.
func (s *SQLiteStore) DeleteWorkspace(ctx context.Context, id string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Check workspace exists
		var exists bool
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE id = ?", id).Scan(&exists)
		if err == sql.ErrNoRows {
			return ErrWorkspaceNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to check workspace: %w", err)
		}

		// Move sessions to root
		_, err = tx.ExecContext(ctx, "UPDATE sessions SET workspace_id = NULL WHERE workspace_id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to move sessions: %w", err)
		}

		// Move subworkspaces to root
		_, err = tx.ExecContext(ctx, "UPDATE workspaces SET parent_id = NULL WHERE parent_id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to move subworkspaces: %w", err)
		}

		// Delete the workspace
		_, err = tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("failed to delete workspace: %w", err)
		}

		return nil
	})
}

// ListWorkspaces returns all workspaces as a flat list.
func (s *SQLiteStore) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, parent_id, color, session_count, created_at, updated_at,
			agents, agent_instances, status
		FROM workspaces
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	workspaces := make([]Workspace, 0)
	for rows.Next() {
		var workspace Workspace
		var parentID, color, description sql.NullString
		var agentsJSON, agentInstancesJSON, status sql.NullString

		if err := rows.Scan(&workspace.ID, &workspace.Name, &description, &parentID, &color,
			&workspace.SessionCount, &workspace.CreatedAt, &workspace.UpdatedAt,
			&agentsJSON, &agentInstancesJSON, &status); err != nil {
			return nil, fmt.Errorf("failed to scan workspace: %w", err)
		}

		workspace.Description = description.String
		workspace.ParentID = parentID.String
		workspace.Color = color.String

		// Deserialize orchestration fields for list views
		if agentsJSON.Valid && agentsJSON.String != "" {
			_ = json.Unmarshal([]byte(agentsJSON.String), &workspace.Agents)
		}
		if agentInstancesJSON.Valid && agentInstancesJSON.String != "" {
			_ = json.Unmarshal([]byte(agentInstancesJSON.String), &workspace.AgentInstances)
		}
		if status.Valid && status.String != "" {
			workspace.Status = WorkspaceStatus(status.String)
		}

		workspaces = append(workspaces, workspace)
	}

	return workspaces, nil
}

// GetWorkspaceTree returns workspaces organized as a tree structure.
func (s *SQLiteStore) GetWorkspaceTree(ctx context.Context) ([]Workspace, error) {
	workspaces, err := s.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	// Build lookup map
	workspaceMap := make(map[string]*Workspace)
	for i := range workspaces {
		workspaces[i].Children = []Workspace{} // Initialize children slice
		workspaceMap[workspaces[i].ID] = &workspaces[i]
	}

	// Build tree
	roots := make([]Workspace, 0)
	for i := range workspaces {
		workspace := &workspaces[i]
		if workspace.ParentID == "" {
			roots = append(roots, *workspace)
		} else if parent, ok := workspaceMap[workspace.ParentID]; ok {
			parent.Children = append(parent.Children, *workspace)
		} else {
			// Orphaned workspace - treat as root
			roots = append(roots, *workspace)
		}
	}

	return roots, nil
}

// GetSubworkspaceIDs returns all descendant workspace IDs.
func (s *SQLiteStore) GetSubworkspaceIDs(ctx context.Context, workspaceID string) ([]string, error) {
	// Use recursive CTE to get all descendants
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT id FROM workspaces WHERE parent_id = ?
			UNION ALL
			SELECT w.id FROM workspaces w
			INNER JOIN descendants d ON w.parent_id = d.id
		)
		SELECT id FROM descendants
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subworkspace IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan subworkspace ID: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, nil
}
