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

	// Serialize all JSON fields using helper
	f := serializeWorkspaceFields(workspace)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspaces (id, name, description, parent_id, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json, directory_references_json)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, workspace.ID, workspace.Name, workspace.Description, workspace.ParentID, workspace.Color,
		workspace.SessionCount, workspace.CreatedAt, workspace.UpdatedAt,
		string(f.agents), string(f.agentInstances), string(f.sharedData), string(f.status), f.layout,
		string(f.messages), string(f.tasks), string(f.attachments), string(f.scheduledTasks), string(f.storeNodes), string(f.workflows), string(f.directoryReferences))

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
	var directoryReferencesJSON sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, parent_id, color, session_count, created_at, updated_at,
			agents, agent_instances, shared_data, status, layout,
			messages_json, tasks_json, attachments_json, scheduled_tasks_json, store_nodes_json, workflows_json, directory_references_json
		FROM workspaces WHERE id = ?
	`, id).Scan(&workspace.ID, &workspace.Name, &description, &parentID, &color,
		&workspace.SessionCount, &workspace.CreatedAt, &workspace.UpdatedAt,
		&agentsJSON, &agentInstancesJSON, &sharedDataJSON, &status, &layoutJSON,
		&messagesJSON, &tasksJSON, &attachmentsJSON, &scheduledTasksJSON, &storeNodesJSON, &workflowsJSON, &directoryReferencesJSON)

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
	if directoryReferencesJSON.Valid && directoryReferencesJSON.String != "" {
		workspace.DirectoryReferencesJSON = json.RawMessage(directoryReferencesJSON.String)
	}

	return workspace, nil
}

// UpdateWorkspace updates workspace metadata.
func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspace *Workspace) error {
	// Serialize all JSON fields using helper
	f := serializeWorkspaceFields(workspace)

	result, err := s.db.ExecContext(ctx, `
		UPDATE workspaces
		SET name = ?, description = ?, parent_id = NULLIF(?, ''), color = ?, updated_at = ?,
			agents = ?, agent_instances = ?, shared_data = ?, status = ?, layout = ?,
			messages_json = ?, tasks_json = ?, attachments_json = ?, scheduled_tasks_json = ?, store_nodes_json = ?, workflows_json = ?, directory_references_json = ?
		WHERE id = ?
	`, workspace.Name, workspace.Description, workspace.ParentID, workspace.Color, workspace.UpdatedAt,
		string(f.agents), string(f.agentInstances), string(f.sharedData), string(f.status), f.layout,
		string(f.messages), string(f.tasks), string(f.attachments), string(f.scheduledTasks), string(f.storeNodes), string(f.workflows), string(f.directoryReferences),
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
