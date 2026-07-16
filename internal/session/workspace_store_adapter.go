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
	"github.com/johnjallday/ori-agent/internal/agent"
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

// ConvertAgentWorkspace converts a workspace.Workspace into a session.Workspace
// without requiring a backing store. This is used by HTTP handlers that need to
// preserve workspace.json metadata when importing from the file store.
func ConvertAgentWorkspace(ws *workspace.Workspace) *Workspace {
	adapter := &WorkspaceStoreAdapter{}
	return adapter.toSessionWorkspace(ws)
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

	return a.toAgentWorkspace(sessionWS), nil
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
			active = append(active, a.toAgentWorkspace(&ws))
		}
	}
	return active, nil
}

// ListActiveForScheduling returns active workspaces for the task scheduler with
// chat history omitted. The scheduler reads only scheduling state (tasks, scheduled
// tasks, mission fields, status) and routes writes through Update (which re-fetches
// the full record), so dropping Messages avoids deserializing chat history for every
// workspace each tick. Falls back to the full ListActive when the backing store does
// not support the lighter query.
func (a *WorkspaceStoreAdapter) ListActiveForScheduling() ([]*workspace.Workspace, error) {
	ctx := context.Background()

	type schedulingStore interface {
		ListWorkspacesForScheduling(ctx context.Context) ([]Workspace, error)
	}
	ss, ok := a.store.(schedulingStore)
	if !ok {
		return a.ListActive()
	}

	workspaces, err := ss.ListWorkspacesForScheduling(ctx)
	if err != nil {
		return nil, err
	}

	active := make([]*workspace.Workspace, 0, len(workspaces))
	for i := range workspaces {
		ws := workspaces[i]
		if ws.Status != WorkspaceStatusActive && ws.Status != "" {
			continue
		}
		active = append(active, a.toAgentWorkspace(&ws))
	}
	return active, nil
}

// toSessionWorkspace converts workspace.Workspace to session.Workspace.
func (a *WorkspaceStoreAdapter) toSessionWorkspace(ws *workspace.Workspace) *Workspace {
	sessionWS := &Workspace{
		ID:                ws.ID,
		Name:              ws.Name,
		Kind:              NormalizeWorkspaceKind(ws.Kind),
		Description:       ws.Description,
		FolderSlug:        ws.FolderSlug,
		ProjectPath:       ws.ProjectPath,
		Designation:       NormalizeWorkspaceDesignation(ws.Designation),
		AllowNativeMCPCLI: ws.AllowNativeMCPCLI,
		Tags:              append([]string(nil), ws.Tags...),
		OwnerUserID:       normalizeOwnerUserID(ws.OwnerUserID),
		ParentID:          ws.ParentID,
		OrderIndex:        ws.OrderIndex,
		CreatedAt:         ws.CreatedAt,
		UpdatedAt:         ws.UpdatedAt,
		SharedData:        ws.SharedData,
		Status:            WorkspaceStatus(ws.Status),
		Version:           ws.Version,
	}

	// Convert AgentInstances
	if len(ws.AgentInstances) > 0 {
		sessionWS.AgentInstances = make([]AgentInstance, len(ws.AgentInstances))
		for i, ai := range ws.AgentInstances {
			sessionWS.AgentInstances[i] = AgentInstance{
				ID:                 ai.ID,
				Name:               ai.Name,
				InstanceNumber:     ai.InstanceNumber,
				NodeID:             ai.NodeID,
				Role:               ai.Role,
				Description:        ai.Description,
				CustomInstructions: ai.CustomInstructions,
				EntryPoint:         ai.EntryPoint,
				CreatedAt:          ai.CreatedAt,
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
	if len(ws.Folders) > 0 {
		if data, err := json.Marshal(ws.Folders); err != nil {
			logger.Warn("Failed to marshal workspace folders", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.FoldersJSON = data
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
	if len(ws.SkillBindings) > 0 {
		if data, err := json.Marshal(ws.SkillBindings); err != nil {
			logger.Warn("Failed to marshal workspace skill bindings", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.SkillBindingsJSON = data
		}
	}
	if len(ws.AgentSkillAccess) > 0 {
		if data, err := json.Marshal(ws.AgentSkillAccess); err != nil {
			logger.Warn("Failed to marshal workspace agent skill access", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.AgentSkillAccessJSON = data
		}
	}
	if len(ws.Opportunities) > 0 {
		if data, err := json.Marshal(ws.Opportunities); err != nil {
			logger.Warn("Failed to marshal workspace opportunities", logger.Fields{"workspace_id": ws.ID, "error": err})
		} else {
			sessionWS.OpportunitiesJSON = data
		}
	}

	return sessionWS
}

// toAgentWorkspace converts session.Workspace to workspace.Workspace.
func (a *WorkspaceStoreAdapter) toAgentWorkspace(ws *Workspace) *workspace.Workspace {
	agentWS := &workspace.Workspace{
		ID:                ws.ID,
		Name:              ws.Name,
		Kind:              string(NormalizeWorkspaceKind(string(ws.Kind))),
		Description:       ws.Description,
		FolderSlug:        ws.FolderSlug,
		ProjectPath:       ws.ProjectPath,
		Designation:       string(NormalizeWorkspaceDesignation(string(ws.Designation))),
		AllowNativeMCPCLI: ws.AllowNativeMCPCLI,
		Tags:              append([]string(nil), ws.Tags...),
		OwnerUserID:       normalizeOwnerUserID(ws.OwnerUserID),
		ParentID:          ws.ParentID,
		OrderIndex:        ws.OrderIndex,
		CreatedAt:         ws.CreatedAt,
		UpdatedAt:         ws.UpdatedAt,
		SharedData:        ws.SharedData,
		Status:            workspace.WorkspaceStatus(ws.Status),
		Version:           ws.Version,
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
				ID:                 ai.ID,
				Name:               ai.Name,
				InstanceNumber:     ai.InstanceNumber,
				NodeID:             ai.NodeID,
				Role:               ai.Role,
				Description:        ai.Description,
				CustomInstructions: ai.CustomInstructions,
				EntryPoint:         ai.EntryPoint,
				CreatedAt:          ai.CreatedAt,
			}
		}
	}

	// Convert Layout
	if ws.Layout != nil {
		agentWS.Layout = convertToAgentWorkspaceLayout(ws.Layout)
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

	if len(ws.FoldersJSON) > 0 {
		if err := json.Unmarshal(ws.FoldersJSON, &agentWS.Folders); err != nil {
			logger.Warn("Failed to unmarshal workspace folders", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.Folders == nil {
		agentWS.Folders = []workspace.Folder{}
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
		agentWS.MCPBindings = []workspace.MCPBinding{}
	}

	if len(ws.AgentMCPAccessJSON) > 0 {
		if err := json.Unmarshal(ws.AgentMCPAccessJSON, &agentWS.AgentMCPAccess); err != nil {
			logger.Warn("Failed to unmarshal workspace agent MCP access", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.AgentMCPAccess == nil {
		agentWS.AgentMCPAccess = []workspace.AgentMCPAccess{}
	}

	if len(ws.SkillBindingsJSON) > 0 {
		if err := json.Unmarshal(ws.SkillBindingsJSON, &agentWS.SkillBindings); err != nil {
			logger.Warn("Failed to unmarshal workspace skill bindings", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.SkillBindings == nil {
		agentWS.SkillBindings = []workspace.SkillBinding{}
	}

	if len(ws.AgentSkillAccessJSON) > 0 {
		if err := json.Unmarshal(ws.AgentSkillAccessJSON, &agentWS.AgentSkillAccess); err != nil {
			logger.Warn("Failed to unmarshal workspace agent skill access", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}
	if agentWS.AgentSkillAccess == nil {
		agentWS.AgentSkillAccess = []workspace.AgentSkillAccess{}
	}

	if len(ws.OpportunitiesJSON) > 0 {
		if err := json.Unmarshal(ws.OpportunitiesJSON, &agentWS.Opportunities); err != nil {
			logger.Warn("Failed to unmarshal workspace opportunities", logger.Fields{"workspace_id": ws.ID, "error": err})
		}
	}

	if agentWS.SharedData == nil {
		agentWS.SharedData = make(map[string]any)
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

	if layout.DirectoryPositions != nil {
		sessionLayout.DirectoryPositions = make(map[string]Position)
		for k, v := range layout.DirectoryPositions {
			sessionLayout.DirectoryPositions[k] = Position{X: v.X, Y: v.Y}
		}
	}

	if layout.FolderPositions != nil {
		sessionLayout.FolderPositions = make(map[string]Position)
		for k, v := range layout.FolderPositions {
			sessionLayout.FolderPositions[k] = Position{X: v.X, Y: v.Y}
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

// convertToAgentWorkspaceLayout converts session.CanvasLayout to workspace.CanvasLayout.
func convertToAgentWorkspaceLayout(layout *CanvasLayout) *workspace.CanvasLayout {
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

	if layout.DirectoryPositions != nil {
		agentLayout.DirectoryPositions = make(map[string]workspace.Position)
		for k, v := range layout.DirectoryPositions {
			agentLayout.DirectoryPositions[k] = workspace.Position{X: v.X, Y: v.Y}
		}
	}

	if layout.FolderPositions != nil {
		agentLayout.FolderPositions = make(map[string]workspace.Position)
		for k, v := range layout.FolderPositions {
			agentLayout.FolderPositions[k] = workspace.Position{X: v.X, Y: v.Y}
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

// adapterLocks serializes Update calls per workspace inside a single
// adapter instance. The session-backed store has its own concurrency
// strategy at the SQL layer; the lock here protects the
// load → mutate → save read-modify-write window inside this process.
var adapterLocks workspace.LockTable

// Lock acquires the per-workspace mutex used by Update. Save itself does not
// acquire the lock — callers that bypass Update can still race.
func (a *WorkspaceStoreAdapter) Lock(wsID string) func() {
	return adapterLocks.Lock(wsID)
}

// Update applies fn to the workspace and persists the result, atomic against
// other Update calls on the same workspace within this process.
func (a *WorkspaceStoreAdapter) Update(wsID string, fn func(*workspace.Workspace) error) error {
	return workspace.CanonicalUpdate(a, wsID, fn)
}

// CreateWorkspaceViaAdapter creates a new workspace through the adapter interface.
// This is a helper for creating workspaces with proper defaults.
func (a *WorkspaceStoreAdapter) CreateWorkspaceViaAdapter(name, description string, agents []string) (*workspace.Workspace, error) {
	now := time.Now()
	ws := &workspace.Workspace{
		ID:          generateID(),
		Name:        name,
		Description: description,
		Status:      workspace.StatusActive,
		SharedData:  make(map[string]any),
		Messages:    []workspace.AgentMessage{},
		Tasks:       []workspace.Task{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for _, agentName := range agents {
		if err := ws.AddAgent(agentName); err != nil {
			return nil, err
		}
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

// GetOutputsPath returns the path for auto-saved task results for a workspace.
// Uses a workspaces directory under the current working directory.
func (a *WorkspaceStoreAdapter) GetOutputsPath(workspaceID string) string {
	// Default to "workspaces" directory, similar to FileStore
	baseDir := "workspaces"
	if p := os.Getenv("WORKSPACE_DIR"); p != "" {
		baseDir = p
	}
	return filepath.Join(baseDir, workspaceID, "outputs")
}

// GetWorkspaceAgent returns a workspace-local agent snapshot from disk.
// The session-backed adapter does not store snapshots itself; it reads from
// the workspace folder when one is available via WORKSPACE_DIR.
func (a *WorkspaceStoreAdapter) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	folder := workspaceFolderForAdapter(workspaceID)
	if folder == "" {
		return nil, false, nil
	}
	return workspace.ReadWorkspaceAgentFromFolder(folder, agentName)
}

// SaveWorkspaceAgent writes a workspace-local agent snapshot to disk.
func (a *WorkspaceStoreAdapter) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	folder := workspaceFolderForAdapter(workspaceID)
	if folder == "" {
		return fmt.Errorf("workspace folder for %s not found", workspaceID)
	}
	return workspace.WriteWorkspaceAgentToFolder(folder, agentName, ag)
}

func workspaceFolderForAdapter(workspaceID string) string {
	baseDir := "workspaces"
	if p := os.Getenv("WORKSPACE_DIR"); p != "" {
		baseDir = p
	}
	return filepath.Join(baseDir, workspaceID)
}

// generateID creates a new UUID for workspaces.
func generateID() string {
	return uuid.New().String()
}
