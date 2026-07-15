package sessionhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// handleWorkspaceAgents handles requests to /api/workspaces/{id}/agents.
func (h *Handler) handleWorkspaceAgents(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	switch r.Method {
	case http.MethodPost:
		h.addWorkspaceAgent(w, r, workspaceID)
	case http.MethodDelete:
		// Extract agent name or instance ID from path
		var agentIdentifier string
		if len(parts) > 2 {
			agentIdentifier = parts[2]
		}
		h.removeWorkspaceAgent(w, r, workspaceID, agentIdentifier)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// addWorkspaceAgent handles POST /api/workspaces/{id}/agents.
func (h *Handler) addWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req struct {
		AgentName string `json:"agent_name"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.AgentName) == "" {
		_ = orihttp.RespondBadRequest(w, "agent_name is required")
		return
	}
	// This long-standing endpoint can attach a workspace-local definition that
	// has not yet been persisted in the global store. Create-time composition is
	// stricter and validates saved definitions before persistence; keep this
	// endpoint backward compatible while sharing its idempotent instance helper.
	agentName := strings.TrimSpace(req.AgentName)

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	// Add to workspace. Capture the prior agent count first: adding the first
	// agent to an otherwise-agentless workspace makes it the coordinator (via the
	// single-agent default), which is when pre-existing unassigned tasks should
	// be claimed.
	firstAgentAdded := len(workspace.AgentInstances) == 0
	newInstance, added := attachWorkspaceSpecialist(workspace, agentName)

	if strings.TrimSpace(currentWorkspaceEntryAgentName(workspace)) == "" {
		setWorkspaceEntryAgent(workspace, agentName)
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to add agent")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after adding workspace agent", logger.Fields{"id": workspaceID, "error": err})
	}

	// When this add established the workspace's coordinator (its first agent),
	// claim any tasks that were created before a coordinator existed (e.g.
	// template starter tasks) so they are owned, not orphaned. Runs after the
	// folder-store sync above so the sweep resolves the now-coordinator agent.
	tasksClaimed := 0
	if firstAgentAdded && added {
		tasksClaimed = h.claimUnassignedTasksForEntryAgentLogged(workspaceID)
	}

	logger.Info("Agent added to workspace", logger.Fields{
		"workspace_id":    workspaceID,
		"agent_name":      agentName,
		"instance_id":     newInstance.ID,
		"instance_number": newInstance.InstanceNumber,
	})

	response := map[string]any{
		"success":          true,
		"agent_instance":   newInstance,
		"workspace":        workspace,
		"already_attached": !added,
	}
	if tasksClaimed > 0 {
		response["tasks_claimed"] = tasksClaimed
	}
	_ = orihttp.RespondCreated(w, response)
}

// attachWorkspaceSpecialist attaches a saved definition to a workspace once.
// It is shared by normal workspace-agent mutation, template roster seeding,
// and create-time existing-agent composition. Existing definitions can belong
// to many workspaces, but a single workspace must never receive two instances
// of the same canonical name.
func attachWorkspaceSpecialist(ws *session.Workspace, name string) (session.AgentInstance, bool) {
	name = strings.TrimSpace(name)
	if ws == nil || name == "" {
		return session.AgentInstance{}, false
	}
	for _, inst := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), name) {
			return inst, false
		}
	}

	instance := session.AgentInstance{
		ID:             uuid.New().String(),
		Name:           name,
		InstanceNumber: 1,
		NodeID:         name + "-1-node-" + uuid.New().String()[:8],
		CreatedAt:      time.Now(),
	}
	ws.AgentInstances = append(ws.AgentInstances, instance)
	return instance, true
}

// removeWorkspaceAgent handles DELETE /api/workspaces/{id}/agents/{name}.
func (h *Handler) removeWorkspaceAgent(w http.ResponseWriter, r *http.Request, workspaceID, agentIdentifier string) {
	if agentIdentifier == "" {
		_ = orihttp.RespondBadRequest(w, "agent name or instance ID is required")
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	agentIdentifier = resolveWorkspaceAgentIdentifier(workspace, agentIdentifier)

	// Try to find and remove by instance ID first, then by node ID, then by name.
	removed := false
	removedNames := make(map[string]struct{})
	removedNodeIDs := make(map[string]struct{})
	removedInstanceIDs := make(map[string]struct{})
	newInstances := make([]session.AgentInstance, 0, len(workspace.AgentInstances))
	for _, inst := range workspace.AgentInstances {
		if inst.ID == agentIdentifier || inst.NodeID == agentIdentifier || inst.Name == agentIdentifier {
			removed = true
			if name := strings.TrimSpace(inst.Name); name != "" {
				removedNames[name] = struct{}{}
			}
			if nodeID := strings.TrimSpace(inst.NodeID); nodeID != "" {
				removedNodeIDs[nodeID] = struct{}{}
			}
			if instanceID := strings.TrimSpace(inst.ID); instanceID != "" {
				removedInstanceIDs[instanceID] = struct{}{}
			}
		} else {
			newInstances = append(newInstances, inst)
		}
	}

	if !removed {
		_ = orihttp.RespondNotFound(w, "Agent not found in workspace")
		return
	}

	entryAgentName := strings.TrimSpace(currentWorkspaceEntryAgentName(workspace))
	entryAgentRemoved := false
	for removedName := range removedNames {
		if strings.EqualFold(strings.TrimSpace(removedName), entryAgentName) {
			entryAgentRemoved = true
			break
		}
	}
	if entryAgentRemoved && len(newInstances) == 0 {
		_ = orihttp.RespondBadRequest(w, "workspace must keep an entry agent")
		return
	}

	workspace.AgentInstances = newInstances

	if entryAgentRemoved && len(newInstances) > 0 {
		setWorkspaceEntryAgent(workspace, newInstances[0].Name)
	} else if entryAgentName != "" {
		setWorkspaceEntryAgent(workspace, entryAgentName)
	}

	if err := cleanupRemovedAgentWorkspaceState(workspace, removedNames, removedNodeIDs, removedInstanceIDs); err != nil {
		logger.Error("Failed to cleanup workspace state after removing agent", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to remove agent")
		return
	}

	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to remove agent")
		return
	}
	if err := h.syncWorkspacePortableStateToFileStore(workspace); err != nil {
		logger.Warn("Failed to sync workspace.json after removing workspace agent", logger.Fields{"id": workspaceID, "error": err})
	}

	// If removing the entry agent promoted a different member to coordinator,
	// hand any now-unassigned tasks to the new entry agent.
	if entryAgentRemoved && len(newInstances) > 0 {
		h.claimUnassignedTasksForEntryAgentLogged(workspaceID)
	}

	logger.Info("Agent removed from workspace", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentIdentifier,
	})

	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"workspace": workspace,
	})
}

func (h *Handler) buildWorkspaceDetailResponse(workspace *session.Workspace) map[string]any {
	if workspace == nil {
		return map[string]any{}
	}

	payload := make(map[string]any)
	if data, err := json.Marshal(workspace); err == nil {
		if err := json.Unmarshal(data, &payload); err != nil {
			logger.Warn("Failed to decode workspace payload for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		}
	} else {
		logger.Warn("Failed to encode workspace payload for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
	}

	analyticsWorkspace := buildWorkspaceAnalyticsView(workspace)
	settings := workspacesettings.Extract(workspace.SharedData)
	payload["attachments"] = h.buildWorkspaceResponseAttachments(workspace)
	payload["primary_directory_id"] = workspacePrimaryDirectoryID(workspace)
	payload["entry_agent_name"] = availableWorkspaceEntryAgentName(workspace, h.agentStore)
	payload["agent_stats"] = analyticsWorkspace.GetAgentStats()
	payload["workspace_progress"] = analyticsWorkspace.GetWorkspaceProgress()
	payload["workspace_settings"] = settings
	payload["workspace_settings_effective_behavior"] = workspacesettings.BuildEffectiveBehavior(settings)

	return payload
}

func (h *Handler) buildWorkspaceResponseAttachments(workspace *session.Workspace) []agentworkspace.Attachment {
	if workspace == nil || len(workspace.AttachmentsJSON) == 0 {
		return []agentworkspace.Attachment{}
	}

	var attachments []agentworkspace.Attachment
	if err := json.Unmarshal(workspace.AttachmentsJSON, &attachments); err != nil {
		logger.Warn("Failed to decode workspace attachments for response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		return []agentworkspace.Attachment{}
	}

	for i := range attachments {
		attachments[i] = agentworkspace.HydrateAttachment(attachments[i], h.workspaceStore)
	}

	return attachments
}

func buildWorkspaceAnalyticsView(workspace *session.Workspace) *agentworkspace.Workspace {
	analyticsWorkspace := &agentworkspace.Workspace{
		ID:          workspace.ID,
		Name:        workspace.Name,
		Kind:        string(workspace.Kind),
		Description: workspace.Description,
		FolderSlug:  workspace.FolderSlug,
		ProjectPath: workspace.ProjectPath,
		SharedData:  workspace.SharedData,
		Status:      agentworkspace.WorkspaceStatus(workspace.Status),
		CreatedAt:   workspace.CreatedAt,
		UpdatedAt:   workspace.UpdatedAt,
		Layout:      toWorkspaceAnalyticsLayout(workspace.Layout),
	}

	if analyticsWorkspace.Status == "" {
		analyticsWorkspace.Status = agentworkspace.StatusActive
	}

	if len(workspace.AgentInstances) > 0 {
		analyticsWorkspace.AgentInstances = make([]agentworkspace.AgentInstance, len(workspace.AgentInstances))
		for i, inst := range workspace.AgentInstances {
			analyticsWorkspace.AgentInstances[i] = agentworkspace.AgentInstance{
				ID:                 inst.ID,
				Name:               inst.Name,
				InstanceNumber:     inst.InstanceNumber,
				NodeID:             inst.NodeID,
				Role:               inst.Role,
				Description:        inst.Description,
				CustomInstructions: inst.CustomInstructions,
				EntryPoint:         inst.EntryPoint,
				CreatedAt:          inst.CreatedAt,
			}
		}
	}

	if len(workspace.TasksJSON) > 0 {
		if err := json.Unmarshal(workspace.TasksJSON, &analyticsWorkspace.Tasks); err != nil {
			logger.Warn("Failed to decode workspace tasks for analytics response", logger.Fields{"workspace_id": workspace.ID, "error": err})
		}
	}
	if analyticsWorkspace.Tasks == nil {
		analyticsWorkspace.Tasks = []agentworkspace.Task{}
	}

	return analyticsWorkspace
}

func toWorkspaceAnalyticsLayout(layout *session.CanvasLayout) *agentworkspace.CanvasLayout {
	if layout == nil {
		return nil
	}

	converted := &agentworkspace.CanvasLayout{
		Scale:   layout.Scale,
		OffsetX: layout.OffsetX,
		OffsetY: layout.OffsetY,
	}

	if len(layout.TaskPositions) > 0 {
		converted.TaskPositions = make(map[string]agentworkspace.Position, len(layout.TaskPositions))
		for key, value := range layout.TaskPositions {
			converted.TaskPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.AgentPositions) > 0 {
		converted.AgentPositions = make(map[string]agentworkspace.Position, len(layout.AgentPositions))
		for key, value := range layout.AgentPositions {
			converted.AgentPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.AttachmentPositions) > 0 {
		converted.AttachmentPositions = make(map[string]agentworkspace.Position, len(layout.AttachmentPositions))
		for key, value := range layout.AttachmentPositions {
			converted.AttachmentPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.SchedulerPositions) > 0 {
		converted.SchedulerPositions = make(map[string]agentworkspace.Position, len(layout.SchedulerPositions))
		for key, value := range layout.SchedulerPositions {
			converted.SchedulerPositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.StorePositions) > 0 {
		converted.StorePositions = make(map[string]agentworkspace.Position, len(layout.StorePositions))
		for key, value := range layout.StorePositions {
			converted.StorePositions[key] = agentworkspace.Position{X: value.X, Y: value.Y}
		}
	}
	if len(layout.WorkflowConnections) > 0 {
		converted.WorkflowConnections = make([]agentworkspace.WorkflowConnectionLayout, len(layout.WorkflowConnections))
		for i, connection := range layout.WorkflowConnections {
			converted.WorkflowConnections[i] = agentworkspace.WorkflowConnectionLayout{
				ID:       connection.ID,
				From:     connection.From,
				FromPort: connection.FromPort,
				To:       connection.To,
				ToPort:   connection.ToPort,
				Color:    connection.Color,
				Animated: connection.Animated,
			}
		}
	}

	return converted
}

func resolveWorkspaceAgentIdentifier(workspace *session.Workspace, agentIdentifier string) string {
	trimmed := strings.TrimSpace(agentIdentifier)
	if workspace == nil || trimmed == "" || !strings.Contains(trimmed, ":") {
		return trimmed
	}

	parts := strings.SplitN(trimmed, ":", 2)
	agentName := strings.TrimSpace(parts[0])
	instanceNumber, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return trimmed
	}

	for _, inst := range workspace.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), agentName) && inst.InstanceNumber == instanceNumber {
			if id := strings.TrimSpace(inst.ID); id != "" {
				return id
			}
			if nodeID := strings.TrimSpace(inst.NodeID); nodeID != "" {
				return nodeID
			}
		}
	}

	return trimmed
}

func cleanupRemovedAgentWorkspaceState(
	workspace *session.Workspace,
	removedNames map[string]struct{},
	removedNodeIDs map[string]struct{},
	removedInstanceIDs map[string]struct{},
) error {
	if workspace == nil {
		return nil
	}

	if workspace.Layout != nil {
		if len(removedNodeIDs) > 0 && workspace.Layout.AgentPositions != nil {
			for nodeID := range removedNodeIDs {
				delete(workspace.Layout.AgentPositions, nodeID)
			}
		}
		if len(removedNodeIDs) > 0 && len(workspace.Layout.WorkflowConnections) > 0 {
			filteredConnections := workspace.Layout.WorkflowConnections[:0]
			for _, connection := range workspace.Layout.WorkflowConnections {
				if _, removedFrom := removedNodeIDs[connection.From]; removedFrom {
					continue
				}
				if _, removedTo := removedNodeIDs[connection.To]; removedTo {
					continue
				}
				filteredConnections = append(filteredConnections, connection)
			}
			workspace.Layout.WorkflowConnections = filteredConnections
		}
	}

	if len(workspace.TasksJSON) > 0 && (len(removedNames) > 0 || len(removedNodeIDs) > 0) {
		var tasks []agentworkspace.Task
		if err := json.Unmarshal(workspace.TasksJSON, &tasks); err != nil {
			return fmt.Errorf("decode tasks: %w", err)
		}

		changed := false
		for i := range tasks {
			if _, removedNode := removedNodeIDs[strings.TrimSpace(tasks[i].AssignedNodeID)]; removedNode {
				tasks[i].To = "unassigned"
				tasks[i].AssignedNodeID = ""
				tasks[i].InputTaskIDs = nil
				changed = true
			} else if tasks[i].AssignedNodeID == "" {
				if _, removedName := removedNames[strings.TrimSpace(tasks[i].To)]; removedName {
					tasks[i].To = "unassigned"
					changed = true
				}
			}

			if _, removedName := removedNames[strings.TrimSpace(tasks[i].From)]; removedName {
				tasks[i].From = ""
				changed = true
			}
		}

		if changed {
			data, err := json.Marshal(tasks)
			if err != nil {
				return fmt.Errorf("encode tasks: %w", err)
			}
			workspace.TasksJSON = data
		}
	}

	if len(workspace.AgentMCPAccessJSON) > 0 && len(removedInstanceIDs) > 0 {
		var accessEntries []agentworkspace.AgentMCPAccess
		if err := json.Unmarshal(workspace.AgentMCPAccessJSON, &accessEntries); err != nil {
			return fmt.Errorf("decode agent mcp access: %w", err)
		}

		filteredAccess := accessEntries[:0]
		for _, entry := range accessEntries {
			if _, removed := removedInstanceIDs[strings.TrimSpace(entry.AgentInstanceID)]; removed {
				continue
			}
			filteredAccess = append(filteredAccess, entry)
		}

		data, err := json.Marshal(filteredAccess)
		if err != nil {
			return fmt.Errorf("encode agent mcp access: %w", err)
		}
		workspace.AgentMCPAccessJSON = data
	}

	return nil
}

// handleWorkspaceLayout handles requests to /api/workspaces/{id}/layout.
func (h *Handler) handleWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkspaceLayout(w, r, workspaceID)
	case http.MethodPut:
		h.saveWorkspaceLayout(w, r, workspaceID)
	case http.MethodPatch:
		h.saveWorkspaceLayout(w, r, workspaceID)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

// getWorkspaceLayout handles GET /api/workspaces/{id}/layout.
func (h *Handler) getWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	layout := workspace.Layout
	if layout == nil {
		layout = &session.CanvasLayout{}
	}

	orihttp.WriteJSON(w, map[string]any{
		"layout": layout,
	})
}

// saveWorkspaceLayout handles PUT/PATCH /api/workspaces/{id}/layout.
func (h *Handler) saveWorkspaceLayout(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var layout session.CanvasLayout
	if !orihttp.ParseJSONBody(w, r, &layout) {
		return
	}

	// Get the workspace
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}

	workspace.Layout = &layout
	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(r.Context(), workspace); err != nil {
		logger.Error("Failed to update workspace layout", logger.Fields{"id": workspaceID, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to save layout")
		return
	}

	logger.Info("Workspace layout saved", logger.Fields{"workspace_id": workspaceID})

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"layout":  layout,
	})
}
