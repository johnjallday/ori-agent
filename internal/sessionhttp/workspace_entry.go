package sessionhttp

import (
	"context"
	"errors"
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

// errNoTasksClaimed is an internal sentinel returned from the claim sweep's
// Update closure to skip the otherwise-unconditional folder-store Save (and its
// task-markdown rewrite) when there is nothing to claim. The caller treats it as
// success.
var errNoTasksClaimed = errors.New("no unassigned tasks to claim")

// taskMutationStore returns the store the claim sweep should write through:
// the primary (SyncStore-wrapped) store when wired, so changes reach the store
// orchestration reads from, falling back to the raw folder store (e.g. in tests).
func (h *Handler) taskMutationStore() agentworkspace.Store {
	if h.workspaceTaskStore != nil {
		return h.workspaceTaskStore
	}
	if h.workspaceStore != nil {
		return h.workspaceStore
	}
	return nil
}

// claimUnassignedTasksForEntryAgent hands every currently-unassigned task in the
// workspace to its resolved coordinator (entry agent). Tasks live in the folder
// store, so the sweep runs inside workspaceStore.Update for lost-update safety.
// It is best-effort and self-gating: it returns 0 when there is no folder
// workspace, no resolvable coordinator, or no unassigned task.
//
// Call it after the workspace's entry-agent state has been synced to the folder
// store, so the coordinator it resolves reflects the just-applied change.
func (h *Handler) claimUnassignedTasksForEntryAgent(workspaceID string) (int, error) {
	if h == nil {
		return 0, nil
	}
	store := h.taskMutationStore()
	if store == nil {
		return 0, nil
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return 0, nil
	}

	claimed := 0
	err := store.Update(id, func(ws *agentworkspace.Workspace) error {
		claimed = ws.ClaimUnassignedTasksForCoordinator()
		if claimed == 0 {
			return errNoTasksClaimed
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoTasksClaimed) {
		return 0, err
	}
	return claimed, nil
}

// claimUnassignedTasksForEntryAgentLogged runs the claim sweep as a best-effort
// side effect of an entry-agent lifecycle change, logging the outcome and
// returning the number of tasks claimed (0 on failure, so a failed sweep is
// never reported as success). Callers may surface the count in their response.
func (h *Handler) claimUnassignedTasksForEntryAgentLogged(workspaceID string) int {
	claimed, err := h.claimUnassignedTasksForEntryAgent(workspaceID)
	if err != nil {
		logger.Warn("Failed to claim unassigned tasks for entry agent", logger.Fields{"workspace_id": workspaceID, "error": err})
		return 0
	}
	if claimed > 0 {
		logger.Info("Claimed unassigned tasks for entry agent", logger.Fields{"workspace_id": workspaceID, "claimed": claimed})
	}
	return claimed
}

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
