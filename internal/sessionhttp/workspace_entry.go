package sessionhttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
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
