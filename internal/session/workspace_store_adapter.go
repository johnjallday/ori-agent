// Package session provides the WorkspaceStoreAdapter that implements agentstudio.Store
// using the session.HybridStore as the underlying storage. This allows orchestration
// handlers to use SQLite storage through a unified interface.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
)

// WorkspaceStoreAdapter implements agentstudio.Store using session.HybridStore.
// This adapter bridges the session storage system with the orchestration system,
// allowing both to share the same SQLite-backed workspace data.
type WorkspaceStoreAdapter struct {
	store HybridStore
}

// NewWorkspaceStoreAdapter creates a new adapter wrapping the given HybridStore.
func NewWorkspaceStoreAdapter(store HybridStore) *WorkspaceStoreAdapter {
	return &WorkspaceStoreAdapter{store: store}
}

// Save persists a workspace to storage by converting from agentstudio.Workspace to session.Workspace.
func (a *WorkspaceStoreAdapter) Save(ws *agentstudio.Workspace) error {
	ctx := context.Background()

	// Convert agentstudio.Workspace to session.Workspace
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

// Get retrieves a workspace by ID and converts to agentstudio.Workspace.
func (a *WorkspaceStoreAdapter) Get(id string) (*agentstudio.Workspace, error) {
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
func (a *WorkspaceStoreAdapter) ListActive() ([]*agentstudio.Workspace, error) {
	ctx := context.Background()

	workspaces, err := a.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}

	active := make([]*agentstudio.Workspace, 0)
	for _, ws := range workspaces {
		if ws.Status == WorkspaceStatusActive || ws.Status == "" {
			active = append(active, a.toAgentStudioWorkspace(&ws))
		}
	}
	return active, nil
}

// toSessionWorkspace converts agentstudio.Workspace to session.Workspace.
func (a *WorkspaceStoreAdapter) toSessionWorkspace(ws *agentstudio.Workspace) *Workspace {
	sessionWS := &Workspace{
		ID:          ws.ID,
		Name:        ws.Name,
		Description: ws.Description,
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
				CreatedAt:      ai.CreatedAt,
			}
		}
	}

	// Convert Layout
	if ws.Layout != nil {
		sessionWS.Layout = convertToSessionLayout(ws.Layout)
	}

	// Serialize orchestration data as JSON
	if len(ws.Messages) > 0 {
		sessionWS.MessagesJSON, _ = json.Marshal(ws.Messages)
	}
	if len(ws.Tasks) > 0 {
		sessionWS.TasksJSON, _ = json.Marshal(ws.Tasks)
	}
	if len(ws.Attachments) > 0 {
		sessionWS.AttachmentsJSON, _ = json.Marshal(ws.Attachments)
	}
	if len(ws.ScheduledTasks) > 0 {
		sessionWS.ScheduledTasksJSON, _ = json.Marshal(ws.ScheduledTasks)
	}
	if len(ws.StoreNodes) > 0 {
		sessionWS.StoreNodesJSON, _ = json.Marshal(ws.StoreNodes)
	}
	if len(ws.Workflows) > 0 {
		sessionWS.WorkflowsJSON, _ = json.Marshal(ws.Workflows)
	}

	return sessionWS
}

// toAgentStudioWorkspace converts session.Workspace to agentstudio.Workspace.
func (a *WorkspaceStoreAdapter) toAgentStudioWorkspace(ws *Workspace) *agentstudio.Workspace {
	agentWS := &agentstudio.Workspace{
		ID:          ws.ID,
		Name:        ws.Name,
		Description: ws.Description,
		CreatedAt:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
		Agents:      ws.Agents,
		SharedData:  ws.SharedData,
		Status:      agentstudio.WorkspaceStatus(ws.Status),
	}

	// Default status to active if not set
	if agentWS.Status == "" {
		agentWS.Status = agentstudio.StatusActive
	}

	// Convert AgentInstances
	if len(ws.AgentInstances) > 0 {
		agentWS.AgentInstances = make([]agentstudio.AgentInstance, len(ws.AgentInstances))
		for i, ai := range ws.AgentInstances {
			agentWS.AgentInstances[i] = agentstudio.AgentInstance{
				ID:             ai.ID,
				Name:           ai.Name,
				InstanceNumber: ai.InstanceNumber,
				NodeID:         ai.NodeID,
				CreatedAt:      ai.CreatedAt,
			}
		}
	}

	// Convert Layout
	if ws.Layout != nil {
		agentWS.Layout = convertToAgentStudioLayout(ws.Layout)
	}

	// Deserialize orchestration data from JSON
	if len(ws.MessagesJSON) > 0 {
		_ = json.Unmarshal(ws.MessagesJSON, &agentWS.Messages)
	}
	if agentWS.Messages == nil {
		agentWS.Messages = []agentstudio.AgentMessage{}
	}

	if len(ws.TasksJSON) > 0 {
		_ = json.Unmarshal(ws.TasksJSON, &agentWS.Tasks)
	}
	if agentWS.Tasks == nil {
		agentWS.Tasks = []agentstudio.Task{}
	}

	if len(ws.AttachmentsJSON) > 0 {
		_ = json.Unmarshal(ws.AttachmentsJSON, &agentWS.Attachments)
	}

	if len(ws.ScheduledTasksJSON) > 0 {
		_ = json.Unmarshal(ws.ScheduledTasksJSON, &agentWS.ScheduledTasks)
	}

	if len(ws.StoreNodesJSON) > 0 {
		_ = json.Unmarshal(ws.StoreNodesJSON, &agentWS.StoreNodes)
	}

	if len(ws.WorkflowsJSON) > 0 {
		_ = json.Unmarshal(ws.WorkflowsJSON, &agentWS.Workflows)
	}
	if agentWS.Workflows == nil {
		agentWS.Workflows = make(map[string]agentstudio.Workflow)
	}

	if agentWS.SharedData == nil {
		agentWS.SharedData = make(map[string]interface{})
	}

	return agentWS
}

// convertToSessionLayout converts agentstudio.CanvasLayout to session.CanvasLayout.
func convertToSessionLayout(layout *agentstudio.CanvasLayout) *CanvasLayout {
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

// convertToAgentStudioLayout converts session.CanvasLayout to agentstudio.CanvasLayout.
func convertToAgentStudioLayout(layout *CanvasLayout) *agentstudio.CanvasLayout {
	if layout == nil {
		return nil
	}

	agentLayout := &agentstudio.CanvasLayout{
		Scale:   layout.Scale,
		OffsetX: layout.OffsetX,
		OffsetY: layout.OffsetY,
	}

	if layout.TaskPositions != nil {
		agentLayout.TaskPositions = make(map[string]agentstudio.Position)
		for k, v := range layout.TaskPositions {
			agentLayout.TaskPositions[k] = agentstudio.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AgentPositions != nil {
		agentLayout.AgentPositions = make(map[string]agentstudio.Position)
		for k, v := range layout.AgentPositions {
			agentLayout.AgentPositions[k] = agentstudio.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.AttachmentPositions != nil {
		agentLayout.AttachmentPositions = make(map[string]agentstudio.Position)
		for k, v := range layout.AttachmentPositions {
			agentLayout.AttachmentPositions[k] = agentstudio.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.SchedulerPositions != nil {
		agentLayout.SchedulerPositions = make(map[string]agentstudio.Position)
		for k, v := range layout.SchedulerPositions {
			agentLayout.SchedulerPositions[k] = agentstudio.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.StorePositions != nil {
		agentLayout.StorePositions = make(map[string]agentstudio.Position)
		for k, v := range layout.StorePositions {
			agentLayout.StorePositions[k] = agentstudio.Position{X: v.X, Y: v.Y}
		}
	}

	if len(layout.WorkflowConnections) > 0 {
		agentLayout.WorkflowConnections = make([]agentstudio.WorkflowConnectionLayout, len(layout.WorkflowConnections))
		for i, conn := range layout.WorkflowConnections {
			agentLayout.WorkflowConnections[i] = agentstudio.WorkflowConnectionLayout{
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

// Ensure WorkspaceStoreAdapter implements agentstudio.Store
var _ agentstudio.Store = (*WorkspaceStoreAdapter)(nil)

// CreateWorkspaceViaAdapter creates a new workspace through the adapter interface.
// This is a helper for creating workspaces with proper defaults.
func (a *WorkspaceStoreAdapter) CreateWorkspaceViaAdapter(name, description string, agents []string) (*agentstudio.Workspace, error) {
	now := time.Now()
	ws := &agentstudio.Workspace{
		ID:          generateID(),
		Name:        name,
		Description: description,
		Agents:      agents,
		Status:      agentstudio.StatusActive,
		SharedData:  make(map[string]interface{}),
		Messages:    []agentstudio.AgentMessage{},
		Tasks:       []agentstudio.Task{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := a.Save(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// generateID creates a new UUID for workspaces.
func generateID() string {
	// Use the same ID generation as elsewhere in the codebase
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
