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

// workspaceJSONFields holds all serialized JSON fields for workspace storage.
type workspaceJSONFields struct {
	agents              []byte
	agentInstances      []byte
	sharedData          []byte
	layout              []byte
	messages            []byte
	tasks               []byte
	attachments         []byte
	scheduledTasks      []byte
	storeNodes          []byte
	workflows           []byte
	directoryReferences []byte
	mcpBindings         []byte
	agentMCPAccess      []byte
	status              WorkspaceStatus
}

// serializeWorkspaceFields converts workspace fields to JSON for database storage.
// This centralizes the serialization logic used by both Create and Update operations.
func serializeWorkspaceFields(workspace *Workspace) workspaceJSONFields {
	fields := workspaceJSONFields{}

	// Serialize agents array
	if workspace.Agents == nil {
		fields.agents = []byte("[]")
	} else if data, err := json.Marshal(workspace.Agents); err != nil {
		fields.agents = []byte("[]")
	} else {
		fields.agents = data
	}

	// Serialize agent instances array
	if workspace.AgentInstances == nil {
		fields.agentInstances = []byte("[]")
	} else if data, err := json.Marshal(workspace.AgentInstances); err != nil {
		fields.agentInstances = []byte("[]")
	} else {
		fields.agentInstances = data
	}

	// Serialize shared data map
	if workspace.SharedData == nil {
		fields.sharedData = []byte("{}")
	} else if data, err := json.Marshal(workspace.SharedData); err != nil {
		fields.sharedData = []byte("{}")
	} else {
		fields.sharedData = data
	}

	// Serialize layout (optional)
	if workspace.Layout != nil {
		if data, err := json.Marshal(workspace.Layout); err == nil {
			fields.layout = data
		}
	}

	// Use existing JSON fields or defaults
	fields.messages = workspace.MessagesJSON
	if fields.messages == nil {
		fields.messages = []byte("[]")
	}
	fields.tasks = workspace.TasksJSON
	if fields.tasks == nil {
		fields.tasks = []byte("[]")
	}
	fields.attachments = workspace.AttachmentsJSON
	if fields.attachments == nil {
		fields.attachments = []byte("[]")
	}
	fields.scheduledTasks = workspace.ScheduledTasksJSON
	if fields.scheduledTasks == nil {
		fields.scheduledTasks = []byte("[]")
	}
	fields.storeNodes = workspace.StoreNodesJSON
	if fields.storeNodes == nil {
		fields.storeNodes = []byte("[]")
	}
	fields.workflows = workspace.WorkflowsJSON
	if fields.workflows == nil {
		fields.workflows = []byte("{}")
	}
	fields.directoryReferences = workspace.DirectoryReferencesJSON
	if fields.directoryReferences == nil {
		fields.directoryReferences = []byte("[]")
	}
	fields.mcpBindings = workspace.MCPBindingsJSON
	if fields.mcpBindings == nil {
		fields.mcpBindings = []byte("[]")
	}
	fields.agentMCPAccess = workspace.AgentMCPAccessJSON
	if fields.agentMCPAccess == nil {
		fields.agentMCPAccess = []byte("[]")
	}

	// Default status
	fields.status = workspace.Status
	if fields.status == "" {
		fields.status = WorkspaceStatusActive
	}

	return fields
}

// CreateWorkspace creates a new workspace.
func (s *SQLiteStore) CreateWorkspace(ctx context.Context, workspace *Workspace) error {
	if workspace.ID == "" {
		return ErrInvalidID
	}

	if workspace.OrderIndex == 0 {
		nextIndex, err := s.nextWorkspaceOrderIndex(ctx, workspace.ParentID)
		if err != nil {
			return err
		}
		workspace.OrderIndex = nextIndex
	}

	// Serialize all JSON fields using helper
	f := serializeWorkspaceFields(workspace)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, kind, description, parent_id, order_index, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json, directory_references_json,
			mcp_bindings_json, agent_mcp_access_json)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workspace.ID, workspace.Name, NormalizeWorkspaceKind(string(workspace.Kind)), workspace.Description, workspace.ParentID, workspace.OrderIndex, workspace.Color,
		workspace.SessionCount, workspace.CreatedAt, workspace.UpdatedAt,
		string(f.agents), string(f.agentInstances), string(f.sharedData), string(f.status), f.layout,
		string(f.messages), string(f.tasks), string(f.attachments), string(f.scheduledTasks), string(f.storeNodes), string(f.workflows), string(f.directoryReferences),
		string(f.mcpBindings), string(f.agentMCPAccess))

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicateID
		}
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	return nil
}

func (s *SQLiteStore) nextWorkspaceOrderIndex(ctx context.Context, parentID string) (int, error) {
	var maxIndex sql.NullInt64
	var err error
	if parentID == "" {
		err = s.db.QueryRowContext(ctx, "SELECT MAX(order_index) FROM workspaces WHERE parent_id IS NULL").Scan(&maxIndex)
	} else {
		err = s.db.QueryRowContext(ctx, "SELECT MAX(order_index) FROM workspaces WHERE parent_id = ?", parentID).Scan(&maxIndex)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get workspace order index: %w", err)
	}

	if !maxIndex.Valid {
		return 1, nil
	}

	return int(maxIndex.Int64) + 1, nil
}

// GetWorkspace retrieves a workspace by ID.
func (s *SQLiteStore) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	workspace := &Workspace{}

	var parentID sql.NullString
	var color sql.NullString
	var description sql.NullString
	var kind sql.NullString
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
	var directoryReferencesJSON sql.NullString
	var mcpBindingsJSON sql.NullString
	var agentMCPAccessJSON sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, description, parent_id, order_index, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json, directory_references_json,
			mcp_bindings_json, agent_mcp_access_json
		FROM workspaces WHERE id = ?
	`, id).Scan(&workspace.ID, &workspace.Name, &kind, &description, &parentID, &workspace.OrderIndex, &color,
		&workspace.SessionCount, &workspace.CreatedAt, &workspace.UpdatedAt,
		&agentsJSON, &agentInstancesJSON, &sharedDataJSON, &status, &layoutJSON,
		&messagesJSON, &tasksJSON, &attachmentsJSON, &scheduledTasksJSON, &storeNodesJSON, &workflowsJSON, &directoryReferencesJSON,
		&mcpBindingsJSON, &agentMCPAccessJSON)

	if err == sql.ErrNoRows {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	workspace.Description = description.String
	workspace.Kind = NormalizeWorkspaceKind(kind.String)
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
	if directoryReferencesJSON.Valid && directoryReferencesJSON.String != "" {
		workspace.DirectoryReferencesJSON = json.RawMessage(directoryReferencesJSON.String)
	}
	if mcpBindingsJSON.Valid && mcpBindingsJSON.String != "" {
		workspace.MCPBindingsJSON = json.RawMessage(mcpBindingsJSON.String)
	}
	if agentMCPAccessJSON.Valid && agentMCPAccessJSON.String != "" {
		workspace.AgentMCPAccessJSON = json.RawMessage(agentMCPAccessJSON.String)
	}

	return workspace, nil
}

// UpdateWorkspace updates workspace metadata.
func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspace *Workspace) error {
	// Serialize all JSON fields using helper
	f := serializeWorkspaceFields(workspace)

	result, err := s.db.ExecContext(ctx, `
		UPDATE workspaces
		SET name = ?, kind = ?, description = ?, parent_id = NULLIF(?, ''), order_index = ?, color = ?, updated_at = ?,
			agents = ?, agent_instances = ?, shared_data = ?, status = ?, layout = ?,
			messages_json = ?, tasks_json = ?, attachments_json = ?, scheduled_tasks_json = ?, store_nodes_json = ?, workflows_json = ?, directory_references_json = ?,
			mcp_bindings_json = ?, agent_mcp_access_json = ?
		WHERE id = ?
	`, workspace.Name, NormalizeWorkspaceKind(string(workspace.Kind)), workspace.Description, workspace.ParentID, workspace.OrderIndex, workspace.Color, workspace.UpdatedAt,
		string(f.agents), string(f.agentInstances), string(f.sharedData), string(f.status), f.layout,
		string(f.messages), string(f.tasks), string(f.attachments), string(f.scheduledTasks), string(f.storeNodes), string(f.workflows), string(f.directoryReferences),
		string(f.mcpBindings), string(f.agentMCPAccess),
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

// DeleteSessionsByWorkspace deletes all sessions (and their messages/tool_calls) belonging to a workspace.
func (s *SQLiteStore) DeleteSessionsByWorkspace(ctx context.Context, workspaceID string) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		// Delete tool_calls for sessions in this workspace
		_, err := tx.ExecContext(ctx,
			"DELETE FROM tool_calls WHERE session_id IN (SELECT id FROM sessions WHERE workspace_id = ?)", workspaceID)
		if err != nil {
			return fmt.Errorf("failed to delete tool calls: %w", err)
		}

		// Delete messages for sessions in this workspace
		_, err = tx.ExecContext(ctx,
			"DELETE FROM messages WHERE session_id IN (SELECT id FROM sessions WHERE workspace_id = ?)", workspaceID)
		if err != nil {
			return fmt.Errorf("failed to delete messages: %w", err)
		}

		// Delete session tags
		_, err = tx.ExecContext(ctx,
			"DELETE FROM session_tags WHERE session_id IN (SELECT id FROM sessions WHERE workspace_id = ?)", workspaceID)
		if err != nil {
			return fmt.Errorf("failed to delete session tags: %w", err)
		}

		// Delete sessions
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE workspace_id = ?", workspaceID)
		if err != nil {
			return fmt.Errorf("failed to delete sessions: %w", err)
		}

		return nil
	})
}

// UnlinkSessionsFromWorkspace sets workspace_id to NULL for all sessions in a workspace.
func (s *SQLiteStore) UnlinkSessionsFromWorkspace(ctx context.Context, workspaceID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET workspace_id = NULL WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return fmt.Errorf("failed to unlink sessions: %w", err)
	}
	return nil
}

// ListWorkspaces returns all workspaces as a flat list.
func (s *SQLiteStore) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, description, parent_id, order_index, color, session_count, created_at, updated_at,
			agents, agent_instances, status
		FROM workspaces
		ORDER BY COALESCE(parent_id, ''), order_index ASC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	workspaces := make([]Workspace, 0)
	for rows.Next() {
		var workspace Workspace
		var parentID, color, description, kind sql.NullString
		var agentsJSON, agentInstancesJSON, status sql.NullString

		if err := rows.Scan(&workspace.ID, &workspace.Name, &kind, &description, &parentID, &workspace.OrderIndex, &color,
			&workspace.SessionCount, &workspace.CreatedAt, &workspace.UpdatedAt,
			&agentsJSON, &agentInstancesJSON, &status); err != nil {
			return nil, fmt.Errorf("failed to scan workspace: %w", err)
		}

		workspace.Kind = NormalizeWorkspaceKind(kind.String)
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

	// Build lookup map and parent -> children map.
	workspaceMap := make(map[string]*Workspace, len(workspaces))
	childrenMap := make(map[string][]*Workspace, len(workspaces))
	roots := make([]*Workspace, 0)

	for i := range workspaces {
		ws := &workspaces[i]
		ws.Children = nil
		workspaceMap[ws.ID] = ws
	}

	for i := range workspaces {
		ws := &workspaces[i]
		if ws.ParentID == "" {
			roots = append(roots, ws)
			continue
		}
		if _, ok := workspaceMap[ws.ParentID]; ok {
			childrenMap[ws.ParentID] = append(childrenMap[ws.ParentID], ws)
			continue
		}
		// Orphaned workspace - treat as root
		roots = append(roots, ws)
	}

	var buildNode func(*Workspace) Workspace
	buildNode = func(ws *Workspace) Workspace {
		node := *ws
		children := childrenMap[ws.ID]
		if len(children) > 0 {
			node.Children = make([]Workspace, 0, len(children))
			for _, child := range children {
				node.Children = append(node.Children, buildNode(child))
			}
		} else {
			node.Children = []Workspace{}
		}
		return node
	}

	result := make([]Workspace, 0, len(roots))
	for _, root := range roots {
		result = append(result, buildNode(root))
	}

	return result, nil
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
