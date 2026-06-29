package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
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
		workspace.SharedData = make(map[string]any)
	}

	if trimmed == "" {
		delete(workspace.SharedData, workspaceEntryAgentNameKey)
		for i := range workspace.AgentInstances {
			workspace.AgentInstances[i].EntryPoint = false
		}
		return
	}

	workspace.SharedData[workspaceEntryAgentNameKey] = trimmed

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

func canonicalWorkspaceEntryNodeID(agentName string) string {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("%s-node-1", name)
}

// defaultGroupEntryAgentName returns the manager-agent name seeded for a
// group, matching the detail page's entry-agent defaults ("<Name> Manager").
func defaultGroupEntryAgentName(workspaceName string) string {
	name := strings.TrimSpace(workspaceName)
	if name == "" {
		return "Group Manager"
	}
	if strings.HasSuffix(strings.ToLower(name), " manager") {
		return name
	}
	return name + " Manager"
}

// autoCreateGroupEntryAgent creates the default "<Name> Manager" agent for a
// newly created group and returns its name, so groups are chat-ready the
// moment they exist. A fresh agent is always created — name collisions get a
// numeric suffix rather than adopting an unrelated existing agent, because a
// workspace's entry agent is deleted along with the workspace. Failures are
// non-fatal and return "": the detail page then falls back to its standard
// missing-entry-agent prompt.
func (h *Handler) autoCreateGroupEntryAgent(ws *session.Workspace) string {
	if h == nil || h.agentStore == nil || ws == nil {
		return ""
	}

	base := defaultGroupEntryAgentName(ws.Name)
	name := base
	for i := 2; ; i++ {
		if _, exists := h.agentStore.GetAgent(name); !exists {
			break
		}
		if i > 50 {
			logger.Warn("Could not find a free group entry agent name", logger.Fields{"base": base})
			return ""
		}
		name = fmt.Sprintf("%s %d", base, i)
	}

	systemPrompt := fmt.Sprintf(
		"You are the workspace manager for %q. Act as the default front door for the workspace: "+
			"clarify user intent, answer directly when the request only needs shared context, and "+
			"break work into tasks for specialists when needed.",
		strings.TrimSpace(ws.Name),
	)
	if err := h.agentStore.CreateAgent(name, &store.CreateAgentConfig{
		Type:         agent.TypeGeneral,
		Role:         types.RoleOrchestrator,
		SystemPrompt: systemPrompt,
	}); err != nil {
		logger.Warn("Failed to auto-create group entry agent", logger.Fields{"agent": name, "error": err})
		return ""
	}

	logger.Info("Auto-created group entry agent", logger.Fields{"agent": name, "workspace": ws.Name})
	return name
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

func (h *Handler) validateWorkspaceEntryAgent(requestedAgentName string) (string, error) {
	if h == nil || h.agentStore == nil {
		return "", fmt.Errorf("agent store is unavailable")
	}

	requested := strings.TrimSpace(requestedAgentName)
	if requested == "" {
		return "", nil
	}

	if existing, ok := h.agentStore.GetAgent(requested); !ok || existing == nil {
		return "", fmt.Errorf("entry agent %q does not exist", requested)
	}
	return requested, nil
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

	return availableWorkspaceEntryAgentName(ws, h.agentStore)
}
