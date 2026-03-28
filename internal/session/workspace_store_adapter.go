// Package session provides the WorkspaceStoreAdapter that implements workspace.Store
// using the session.HybridStore as the underlying storage. This allows orchestration
// handlers to use SQLite storage through a unified interface.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceStoreAdapter implements workspace.Store using session.HybridStore.
// This adapter bridges the session storage system with the orchestration system,
// allowing both to share the same SQLite-backed workspace data.
type WorkspaceStoreAdapter struct {
	store HybridStore
}

// NewWorkspaceStoreAdapter creates a new adapter wrapping the given HybridStore.
func NewWorkspaceStoreAdapter(store HybridStore) *WorkspaceStoreAdapter {
	return &WorkspaceStoreAdapter{store: store}
}

// Save persists a workspace to storage by converting from workspace.Workspace to session.Workspace.
func (a *WorkspaceStoreAdapter) Save(ws *workspace.Workspace) error {
	ctx := context.Background()

	// Convert workspace.Workspace to session.Workspace
	sessionWS := a.toSessionWorkspace(ws)

	// Check if workspace exists
	existing, err := a.store.GetWorkspace(ctx, ws.ID)
	if err == ErrWorkspaceNotFound {
		// Create new workspace
		return a.store.CreateWorkspace(ctx, sessionWS)
	}
	if err != nil {
		return fmt.Errorf("failed to check existing workspace: %w", err)
	}

	// Update existing workspace
	sessionWS.SessionCount = existing.SessionCount // Preserve session count
	sessionWS.ParentID = existing.ParentID         // Preserve parent relationship
	sessionWS.Color = existing.Color               // Preserve color
	return a.store.UpdateWorkspace(ctx, sessionWS)
}

// Get retrieves a workspace by ID and converts to workspace.Workspace.
func (a *WorkspaceStoreAdapter) Get(id string) (*workspace.Workspace, error) {
	ctx := context.Background()

	sessionWS, err := a.store.GetWorkspace(ctx, id)
	if err == ErrWorkspaceNotFound {
		return nil, fmt.Errorf("workspace %s not found", id)
	}
	if err != nil {
		return nil, err
	}

	return a.toAgentStudioWorkspace(sessionWS), nil
}

// List returns all workspace IDs.
func (a *WorkspaceStoreAdapter) List() ([]string, error) {
	ctx := context.Background()

	workspaces, err := a.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(workspaces))
	for i, ws := range workspaces {
		ids[i] = ws.ID
	}
	return ids, nil
}

// Delete removes a workspace from storage.
func (a *WorkspaceStoreAdapter) Delete(id string) error {
	ctx := context.Background()
	return a.store.DeleteWorkspace(ctx, id)
}

// ListActive returns all active workspaces.
func (a *WorkspaceStoreAdapter) ListActive() ([]*workspace.Workspace, error) {
	ctx := context.Background()

	workspaces, err := a.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	active := make([]*workspace.Workspace, 0)
	for _, ws := range workspaces {
		if ws.Status == WorkspaceStatusActive || ws.Status == "" {
			active = append(active, a.toAgentStudioWorkspace(&ws))
		}
	}
	return active, nil
}

// toSessionWorkspace converts workspace.Workspace to session.Workspace.
func (a *WorkspaceStoreAdapter) toSessionWorkspace(ws *workspace.Workspace) *Workspace {
	sessionWS := &Workspace{
		ID:          ws.ID,
		Name:        ws.Name,
		Kind:        NormalizeWorkspaceKind(ws.Kind),
		Description: ws.Description,
		FolderSlug:  ws.FolderSlug,
		ProjectPath: ws.ProjectPath,
		CreatedAt:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
		Agents:      ws.Agents,
		SharedData:  ws.SharedData,
		Status:      WorkspaceStatus(ws.Status),
	}

	// Convert AgentInstances
	if len(ws.AgentInstances) > 0 {
		sessionWS.AgentInstances = make([]AgentInstance, len(ws.AgentInstances))
		for i, ai := range ws.AgentInstances {
			sessionWS.AgentInstances[i] = AgentInstance{
				ID:             ai.ID,
				Name:           ai.Name,
				InstanceNumber: ai.InstanceNumber,
				NodeID:         ai.NodeID,
				Role:           ai.Role,
				Description:    ai.Description,
				EntryPoint:     ai.EntryPoint,
				CreatedAt:      ai.CreatedAt,
			}
		}
	}

	// Convert Layout
	if ws.Layout != nil {
		sessionWS.Layout = convertToSessionLayout(ws.Layout)
	}

	// Serialize orchestration data as JSON with error logging
	if len(ws.Messages) > 0 {
		if data, err := json.Marshal(ws.Messages); err != nil {
			logger.Warn("Failed to marshal workspace messages", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.MessagesJSON = data
		}
	}
	if len(ws.Tasks) > 0 {
		if data, err := json.Marshal(ws.Tasks); err != nil {
			logger.Warn("Failed to marshal workspace tasks", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.TasksJSON = data
		}
	}
	if len(ws.Attachments) > 0 {
		if data, err := json.Marshal(ws.Attachments); err != nil {
			logger.Warn("Failed to marshal workspace attachments", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.AttachmentsJSON = data
		}
	}
	if len(ws.ScheduledTasks) > 0 {
		if data, err := json.Marshal(ws.ScheduledTasks); err != nil {
			logger.Warn("Failed to marshal workspace scheduled tasks", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.ScheduledTasksJSON = data
		}
	}
	if len(ws.StoreNodes) > 0 {
		if data, err := json.Marshal(ws.StoreNodes); err != nil {
			logger.Warn("Failed to marshal workspace store nodes", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.StoreNodesJSON = data
		}
	}
	if len(ws.Workflows) > 0 {
		if data, err := json.Marshal(ws.Workflows); err != nil {
			logger.Warn("Failed to marshal workspace workflows", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.WorkflowsJSON = data
		}
	}
	if len(ws.DirectoryReferences) > 0 {
		if data, err := json.Marshal(ws.DirectoryReferences); err != nil {
			logger.Warn("Failed to marshal workspace directory references", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.DirectoryReferencesJSON = data
		}
	}
	if len(ws.MCPBindings) > 0 {
		if data, err := json.Marshal(ws.MCPBindings); err != nil {
			logger.Warn("Failed to marshal workspace MCP bindings", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.MCPBindingsJSON = data
		}
	}
	if len(ws.AgentMCPAccess) > 0 {
		if data, err := json.Marshal(ws.AgentMCPAccess); err != nil {
			logger.Warn("Failed to marshal workspace agent MCP access", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.AgentMCPAccessJSON = data
		}
	}

	return sessionWS
}

// toAgentStudioWorkspace converts session.Workspace to workspace.Workspace.
func (a *WorkspaceStoreAdapter) toAgentStudioWorkspace(ws *Workspace) *workspace.Workspace {
	agentWS := &workspace.Workspace{
		ID:          ws.ID,
		Name:        ws.Name,
		Kind:        string(NormalizeWorkspaceKind(string(ws.Kind))),
		Description: ws.Description,
		FolderSlug:  ws.FolderSlug,
		ProjectPath: ws.ProjectPath,
		CreatedAt:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
		Agents:      ws.Agents,
		SharedData:  ws.SharedData,
		Status:      workspace.WorkspaceStatus(ws.Status),
	}

	// Default status to active if not set
	if agentWS.Status == "" {
		agentWS.Status = workspace.StatusActive
	}

	// Convert AgentInstances
	if len(ws.AgentInstances) > 0 {
		agentWS.AgentInstances = make([]workspace.AgentInstance, len(ws.AgentInstances))
		for i, ai := range ws.AgentInstances {
			agentWS.AgentInstances[i] = workspace.AgentInstance{
				ID:             ai.ID,
				Name:           ai.Name,
				InstanceNumber: ai.InstanceNumber,
				NodeID:         ai.NodeID,
				Role:           ai.Role,
				Description:    ai.Description,
				EntryPoint:     ai.EntryPoint,
				CreatedAt:      ai.CreatedAt,
			}
		}
	}

	// Convert Layout
	if ws.Layout != nil {
		agentWS.Layout = convertToAgentStudioLayout(ws.Layout)
	}

	// Deserialize orchestration data from JSON with error logging
	if len(ws.MessagesJSON) > 0 {
		if err := json.Unmarshal(ws.MessagesJSON, &agentWS.Messages); err != nil {
			logger.Warn("Failed to unmarshal workspace messages", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.Messages == nil {
		agentWS.Messages = []workspace.AgentMessage{}
	}

	if len(ws.TasksJSON) > 0 {
		if err := json.Unmarshal(ws.TasksJSON, &agentWS.Tasks); err != nil {
			logger.Warn("Failed to unmarshal workspace tasks", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.Tasks == nil {
		agentWS.Tasks = []workspace.Task{}
	}

	if len(ws.AttachmentsJSON) > 0 {
		if err := json.Unmarshal(ws.AttachmentsJSON, &agentWS.Attachments); err != nil {
			logger.Warn("Failed to unmarshal workspace attachments", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}

	if len(ws.ScheduledTasksJSON) > 0 {
		if err := json.Unmarshal(ws.ScheduledTasksJSON, &agentWS.ScheduledTasks); err != nil {
			logger.Warn("Failed to unmarshal workspace scheduled tasks", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}

	if len(ws.StoreNodesJSON) > 0 {
		if err := json.Unmarshal(ws.StoreNodesJSON, &agentWS.StoreNodes); err != nil {
			logger.Warn("Failed to unmarshal workspace store nodes", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}

	if len(ws.WorkflowsJSON) > 0 {
		if err := json.Unmarshal(ws.WorkflowsJSON, &agentWS.Workflows); err != nil {
			logger.Warn("Failed to unmarshal workspace workflows", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.Workflows == nil {
		agentWS.Workflows = make(map[string]workspace.Workflow)
	}

	if len(ws.DirectoryReferencesJSON) > 0 {
		if err := json.Unmarshal(ws.DirectoryReferencesJSON, &agentWS.DirectoryReferences); err != nil {
			logger.Warn("Failed to unmarshal workspace directory references", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.DirectoryReferences == nil {
		agentWS.DirectoryReferences = []workspace.DirectoryReference{}
	}

	if len(ws.MCPBindingsJSON) > 0 {
		if err := json.Unmarshal(ws.MCPBindingsJSON, &agentWS.MCPBindings); err != nil {
			logger.Warn("Failed to unmarshal workspace MCP bindings", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.MCPBindings == nil {
		agentWS.MCPBindings = []workspace.WorkspaceMCPBinding{}
	}

	if len(ws.AgentMCPAccessJSON) > 0 {
		if err := json.Unmarshal(ws.AgentMCPAccessJSON, &agentWS.AgentMCPAccess); err != nil {
			logger.Warn("Failed to unmarshal workspace agent MCP access", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.AgentMCPAccess == nil {
		agentWS.AgentMCPAccess = []workspace.WorkspaceAgentMCPAccess{}
	}

	if agentWS.SharedData == nil {
		agentWS.SharedData = make(map[string]interface{})
	}

	return agentWS
}

// convertToSessionLayout converts workspace.CanvasLayout to session.CanvasLayout.
func convertToSessionLayout(layout *workspace.CanvasLayout) *CanvasLayout {
	if layout == nil {
		return nil
	}

	sessionLayout := &CanvasLayout{
		Scale:   layout.Scale,
		OffsetX: layout.OffsetX,
		OffsetY: layout.OffsetY,
	}

	if layout.TaskPositions != nil {
		sessionLayout.TaskPositions = make(map[string]Position)
		for k, v := range layout.TaskPositions {
			sessionLayout.TaskPositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AgentPositions != nil {
		sessionLayout.AgentPositions = make(map[string]Position)
		for k, v := range layout.AgentPositions {
			sessionLayout.AgentPositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AttachmentPositions != nil {
		sessionLayout.AttachmentPositions = make(map[string]Position)
		for k, v := range layout.AttachmentPositions {
			sessionLayout.AttachmentPositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if layout.SchedulerPositions != nil {
		sessionLayout.SchedulerPositions = make(map[string]Position)
		for k, v := range layout.SchedulerPositions {
			sessionLayout.SchedulerPositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if layout.StorePositions != nil {
		sessionLayout.StorePositions = make(map[string]Position)
		for k, v := range layout.StorePositions {
			sessionLayout.StorePositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if len(layout.WorkflowConnections) > 0 {
		sessionLayout.WorkflowConnections = make([]WorkflowConnectionLayout, len(layout.WorkflowConnections))
		for i, conn := range layout.WorkflowConnections {
			sessionLayout.WorkflowConnections[i] = WorkflowConnectionLayout{
				ID:       conn.ID,
				From:     conn.From,
				FromPort: conn.FromPort,
				To:       conn.To,
				ToPort:   conn.ToPort,
				Color:    conn.Color,
				Animated: conn.Animated,
			}
		}
	}

	return sessionLayout
}

// convertToAgentStudioLayout converts session.CanvasLayout to workspace.CanvasLayout.
func convertToAgentStudioLayout(layout *CanvasLayout) *workspace.CanvasLayout {
	if layout == nil {
		return nil
	}

	agentLayout := &workspace.CanvasLayout{
		Scale:   layout.Scale,
		OffsetX: layout.OffsetX,
		OffsetY: layout.OffsetY,
	}

	if layout.TaskPositions != nil {
		agentLayout.TaskPositions = make(map[string]workspace.Position)
		for k, v := range layout.TaskPositions {
			agentLayout.TaskPositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AgentPositions != nil {
		agentLayout.AgentPositions = make(map[string]workspace.Position)
		for k, v := range layout.AgentPositions {
			agentLayout.AgentPositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AttachmentPositions != nil {
		agentLayout.AttachmentPositions = make(map[string]workspace.Position)
		for k, v := range layout.AttachmentPositions {
			agentLayout.AttachmentPositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.SchedulerPositions != nil {
		agentLayout.SchedulerPositions = make(map[string]workspace.Position)
		for k, v := range layout.SchedulerPositions {
			agentLayout.SchedulerPositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.StorePositions != nil {
		agentLayout.StorePositions = make(map[string]workspace.Position)
		for k, v := range layout.StorePositions {
			agentLayout.StorePositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if len(layout.WorkflowConnections) > 0 {
		agentLayout.WorkflowConnections = make([]workspace.WorkflowConnectionLayout, len(layout.WorkflowConnections))
		for i, conn := range layout.WorkflowConnections {
			agentLayout.WorkflowConnections[i] = workspace.WorkflowConnectionLayout{
				ID:       conn.ID,
				From:     conn.From,
				FromPort: conn.FromPort,
				To:       conn.To,
				ToPort:   conn.ToPort,
				Color:    conn.Color,
				Animated: conn.Animated,
			}
		}
	}

	return agentLayout
}

// Ensure WorkspaceStoreAdapter implements workspace.Store
var _ workspace.Store = (*WorkspaceStoreAdapter)(nil)

// CreateWorkspaceViaAdapter creates a new workspace through the adapter interface.
// This is a helper for creating workspaces with proper defaults.
func (a *WorkspaceStoreAdapter) CreateWorkspaceViaAdapter(name, description string, agents []string) (*workspace.Workspace, error) {
	now := time.Now()
	ws := &workspace.Workspace{
		ID:          generateID(),
		Name:        name,
		Description: description,
		Agents:      agents,
		Status:      workspace.StatusActive,
		SharedData:  make(map[string]interface{}),
		Messages:    []workspace.AgentMessage{},
		Tasks:       []workspace.Task{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := a.Save(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// GetFilesPath returns the path for storing files for a workspace.
// Uses a workspaces directory under the current working directory.
func (a *WorkspaceStoreAdapter) GetFilesPath(workspaceID string) string {
	// Default to "workspaces" directory, similar to FileStore
	baseDir := "workspaces"
	if p := os.Getenv("WORKSPACE_DIR"); p != "" {
		baseDir = p
	}
	return filepath.Join(baseDir, workspaceID, "files")
}

// generateID creates a new UUID for workspaces.
func generateID() string {
	return uuid.New().String()
}
