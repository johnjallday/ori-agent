package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

const workspaceEntryAgentNameKey = "entry_agent_name"

func workspaceHasAgentName(workspace *session.Workspace, agentName string) bool {
	if workspace == nil {
		return false
	}

	target := strings.TrimSpace(agentName)
	if target == "" {
		return false
	}

	for _, inst := range workspace.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), target) {
			return true
		}
	}

	for _, name := range workspace.Agents {
		if strings.EqualFold(strings.TrimSpace(name), target) {
			return true
		}
	}

	return false
}

func currentWorkspaceEntryAgentName(workspace *session.Workspace) string {
	if workspace == nil {
		return ""
	}

	if workspace.SharedData != nil {
		if raw, ok := workspace.SharedData[workspaceEntryAgentNameKey]; ok {
			if name := strings.TrimSpace(fmt.Sprint(raw)); name != "" && workspaceHasAgentName(workspace, name) {
				return name
			}
		}
	}

	for _, inst := range workspace.AgentInstances {
		if inst.EntryPoint && strings.TrimSpace(inst.Name) != "" {
			return strings.TrimSpace(inst.Name)
		}
	}

	for _, inst := range workspace.AgentInstances {
		if name := strings.TrimSpace(inst.Name); name != "" {
			return name
		}
	}

	for _, name := range workspace.Agents {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func availableWorkspaceEntryAgentName(workspace *session.Workspace, agentStore store.Store) string {
	name := strings.TrimSpace(currentWorkspaceEntryAgentName(workspace))
	if name == "" || agentStore == nil {
		return name
	}

	ag, ok := agentStore.GetAgent(name)
	if !ok || ag == nil {
		return ""
	}

	return name
}

func setWorkspaceEntryAgent(workspace *session.Workspace, agentName string) {
	if workspace == nil {
		return
	}

	trimmed := strings.TrimSpace(agentName)
	if workspace.SharedData == nil {
		workspace.SharedData = make(map[string]interface{})
	}

	if trimmed == "" {
		delete(workspace.SharedData, workspaceEntryAgentNameKey)
		for i := range workspace.AgentInstances {
			workspace.AgentInstances[i].EntryPoint = false
		}
		return
	}

	workspace.SharedData[workspaceEntryAgentNameKey] = trimmed
	ensureLegacyWorkspaceAgentName(workspace, trimmed)

	found := false
	for i := range workspace.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(workspace.AgentInstances[i].Name), trimmed) && !found {
			workspace.AgentInstances[i].EntryPoint = true
			found = true
		} else {
			workspace.AgentInstances[i].EntryPoint = false
		}
	}

	if !found {
		workspace.AgentInstances = append(workspace.AgentInstances, session.AgentInstance{
			ID:             uuid.New().String(),
			Name:           trimmed,
			InstanceNumber: 1,
			NodeID:         canonicalWorkspaceEntryNodeID(trimmed),
			EntryPoint:     true,
			CreatedAt:      time.Now(),
		})
	}
}

func ensureLegacyWorkspaceAgentName(workspace *session.Workspace, agentName string) {
	trimmed := strings.TrimSpace(agentName)
	if workspace == nil || trimmed == "" {
		return
	}

	for _, existing := range workspace.Agents {
		if strings.EqualFold(strings.TrimSpace(existing), trimmed) {
			return
		}
	}
	workspace.Agents = append(workspace.Agents, trimmed)
}

func canonicalWorkspaceEntryNodeID(agentName string) string {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("%s-node-1", name)
}

func toWorkspaceAgentInstances(items []session.AgentInstance) []agentworkspace.AgentInstance {
	if len(items) == 0 {
		return nil
	}

	out := make([]agentworkspace.AgentInstance, len(items))
	for i, item := range items {
		out[i] = agentworkspace.AgentInstance{
			ID:             item.ID,
			Name:           item.Name,
			InstanceNumber: item.InstanceNumber,
			NodeID:         item.NodeID,
			Role:           item.Role,
			Description:    item.Description,
			EntryPoint:     item.EntryPoint,
			CreatedAt:      item.CreatedAt,
		}
	}
	return out
}

func (h *Handler) ensureWorkspaceEntryAgent(workspaceName, requestedAgentName string) (string, bool, error) {
	if h == nil || h.agentStore == nil {
		return "", false, fmt.Errorf("agent store is unavailable")
	}

	requested := strings.TrimSpace(requestedAgentName)
	if requested != "" {
		if existing, ok := h.agentStore.GetAgent(requested); !ok || existing == nil {
			return "", false, fmt.Errorf("entry agent %q does not exist", requested)
		}
		return requested, false, nil
	}

	baseName := defaultWorkspaceEntryAgentName(workspaceName)
	agentName := uniqueWorkspaceEntryAgentName(h.agentStore, baseName)
	config := &store.CreateAgentConfig{
		Type:         "workspace-manager",
		SystemPrompt: workspaceEntryAgentSystemPrompt(workspaceName),
	}
	if err := h.agentStore.CreateAgent(agentName, config); err != nil {
		return "", false, err
	}

	if ag, ok := h.agentStore.GetAgent(agentName); ok && ag != nil {
		ag.Type = "workspace-manager"
		ag.Role = types.RoleOrchestrator
		if ag.Metadata == nil {
			ag.Metadata = &types.AgentMetadata{}
		}
		ag.Metadata.Description = fmt.Sprintf("Workspace manager for %q. Coordinate workspace tasks, notes, files, directories, and delegate to specialists when appropriate.", strings.TrimSpace(workspaceName))
		ag.Metadata.Tags = dedupeAgentMetadataTags(append(ag.Metadata.Tags, "workspace-entry", "workspace-manager", "orchestrator"))
		_ = h.agentStore.SetAgent(agentName, ag)
	}

	return agentName, true, nil
}

func (h *Handler) defaultSessionAgentNameForWorkspace(ctx context.Context, workspaceID string) string {
	if h == nil || h.store == nil {
		return ""
	}

	trimmedWorkspaceID := strings.TrimSpace(workspaceID)
	if trimmedWorkspaceID == "" {
		return ""
	}

	ws, err := h.store.GetWorkspace(ctx, trimmedWorkspaceID)
	if err != nil || ws == nil {
		return ""
	}

	if _, _, err := h.ensureWorkspaceManagerForWorkspace(ctx, ws); err != nil {
		logger.Warn("Failed to auto-provision workspace manager for session", logger.Fields{
			"workspace_id": trimmedWorkspaceID,
			"error":        err,
		})
	}

	return availableWorkspaceEntryAgentName(ws, h.agentStore)
}

func (h *Handler) ensureWorkspaceManagerForWorkspace(ctx context.Context, workspace *session.Workspace) (string, bool, error) {
	if h == nil || workspace == nil || workspace.IsGroup() {
		return "", false, nil
	}

	if existing := availableWorkspaceEntryAgentName(workspace, h.agentStore); existing != "" {
		return existing, false, nil
	}

	if h.store == nil || h.agentStore == nil {
		return "", false, nil
	}

	agentName, created, err := h.ensureWorkspaceEntryAgent(workspace.Name, "")
	if err != nil {
		return "", false, fmt.Errorf("create workspace manager: %w", err)
	}

	setWorkspaceEntryAgent(workspace, agentName)
	workspace.UpdatedAt = time.Now()

	if err := h.store.UpdateWorkspace(ctx, workspace); err != nil {
		h.rollbackWorkspaceEntryAgent(agentName, created)
		return "", false, fmt.Errorf("persist workspace manager for %s: %w", workspace.ID, err)
	}

	if h.workspaceStore != nil {
		folderWS, buildErr := buildFileStoreWorkspace(workspace)
		if buildErr != nil {
			logger.Warn("Failed to build workspace manager file-store snapshot", logger.Fields{
				"workspace_id": workspace.ID,
				"error":        buildErr,
			})
		} else if saveErr := h.workspaceStore.Save(folderWS); saveErr != nil {
			logger.Warn("Failed to sync auto-provisioned workspace manager to file store", logger.Fields{
				"workspace_id": workspace.ID,
				"error":        saveErr,
			})
		}
	}

	logger.Info("Auto-provisioned workspace manager for workspace", logger.Fields{
		"workspace_id": workspace.ID,
		"workspace":    workspace.Name,
		"agent":        agentName,
	})

	return agentName, true, nil
}

// deleteWorkspaceManagerAgent removes the auto-created workspace manager agent
// when a workspace is deleted. Only deletes agents tagged as "workspace-manager"
// to avoid removing user-chosen entry agents.
func (h *Handler) deleteWorkspaceManagerAgent(ws *session.Workspace) {
	if h == nil || h.agentStore == nil || ws == nil {
		return
	}

	entryName := currentWorkspaceEntryAgentName(ws)
	if entryName == "" {
		return
	}

	ag, ok := h.agentStore.GetAgent(entryName)
	if !ok || ag == nil || ag.Metadata == nil {
		return
	}

	for _, tag := range ag.Metadata.Tags {
		if tag == "workspace-manager" {
			if err := h.agentStore.DeleteAgent(entryName); err != nil {
				logger.Warn("Failed to delete workspace manager agent", logger.Fields{
					"agent": entryName,
					"error": err,
				})
			} else {
				logger.Info("Deleted workspace manager agent", logger.Fields{"agent": entryName})
			}
			return
		}
	}
}

func (h *Handler) rollbackWorkspaceEntryAgent(agentName string, created bool) {
	if !created || h == nil || h.agentStore == nil || strings.TrimSpace(agentName) == "" {
		return
	}
	_ = h.agentStore.DeleteAgent(strings.TrimSpace(agentName))
}

func defaultWorkspaceEntryAgentName(workspaceName string) string {
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		return "Workspace Manager"
	}
	if strings.HasSuffix(strings.ToLower(name), " manager") {
		return name
	}
	return name + " Manager"
}

func uniqueWorkspaceEntryAgentName(agentStore store.Store, baseName string) string {
	if agentStore == nil {
		return strings.TrimSpace(baseName)
	}

	base := strings.TrimSpace(baseName)
	if base == "" {
		base = "Workspace Manager"
	}

	existing := make(map[string]struct{})
	for _, name := range agentStore.ListAgents() {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			existing[trimmed] = struct{}{}
		}
	}

	if _, ok := existing[strings.ToLower(base)]; !ok {
		return base
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", base, i)
		if _, ok := existing[strings.ToLower(candidate)]; !ok {
			return candidate
		}
	}
}

func workspaceEntryAgentSystemPrompt(workspaceName string) string {
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		name = "this workspace"
	}
	return fmt.Sprintf(
		"You are the workspace manager for %q. Stay focused on this workspace: tasks, notes, files, directories, sessions, and agent coordination. Act as the workspace front door: first understand the user's goal, ask only the minimum clarifying questions, and propose or confirm a short plan when the request is ambiguous or multi-step. Answer directly when shared workspace context is enough. When specialist help might be useful, do not hand off immediately just because a matching agent exists. Decide whether specialist help is actually needed, inspect current or available workspace agents, and ask the user before adding or switching to a specialist unless the user already explicitly requested that handoff. Use workspace agent management tools to invite an existing specialist into the workspace when appropriate. For travel, itinerary, booking, or trip-planning requests, always do an intake pass before planning: do not generate an itinerary or recommendations on the first reply, even if dates and cities are already present. Instead, ask for missing information such as travel dates, flight status, hotel status, preferences, pace, budget, constraints, and must-do activities. If the user says flights or hotels are already booked, ask them to attach the confirmation or travel file to this workspace before planning around it. Prefer 2-4 short multiple-choice questions when helpful. Once the intake is sufficient, default to orchestration for full travel-planning work: for itinerary, day-by-day, or multi-city planning, do not keep acting like the planner by default. Summarize the intake, recommend the right specialist, and ask permission to invite or create specialists such as itinerary, hotel, or flight agents. Only continue yourself for narrow follow-up questions, lightweight clarifications, or when the user explicitly asks to keep planning with the workspace manager. Do not behave like a generic global assistant outside this workspace.",
		name,
	)
}

func dedupeAgentMetadataTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
