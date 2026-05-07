package workspace

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Agent resolution. Given an agent name attached to a task, decide which
// concrete agent.Agent to drive the task with: first ask the runtime
// resolver (workspace-aware), then fall back to a workspace-local snapshot,
// then to the global agent store. When the assigned agent is gone entirely,
// surface a structured TaskBlockedError so the UI can offer a switch-agent
// retry rather than failing the task silently.

func (h *LLMTaskHandler) resolveExecutionAgent(agentName string, task Task) (*resolvedTaskAgent, error) {
	normalizedAgentName := strings.TrimSpace(agentName)

	if h.runtimeResolver != nil {
		resolved, err := h.runtimeResolver.ResolveAgentForTask(normalizedAgentName, task)
		if err != nil {
			if blockedErr := h.buildMissingAssignedAgentBlockedError(normalizedAgentName, task); blockedErr != nil {
				return nil, blockedErr
			}
			return nil, err
		}
		if resolved != nil && resolved.Agent != nil {
			return &resolvedTaskAgent{
				Agent:      resolved.Agent,
				MCPServers: append([]string{}, resolved.MCPServers...),
			}, nil
		}
	}

	if localAgent, ok := h.getWorkspaceLocalAgentSnapshot(task.WorkspaceID, normalizedAgentName); ok {
		return &resolvedTaskAgent{Agent: localAgent}, nil
	}

	ag, ok := h.agentStore.GetAgent(normalizedAgentName)
	if !ok {
		if blockedErr := h.buildMissingAssignedAgentBlockedError(normalizedAgentName, task); blockedErr != nil {
			return nil, blockedErr
		}
		return nil, fmt.Errorf("agent %s not found", normalizedAgentName)
	}
	return &resolvedTaskAgent{Agent: ag}, nil
}

// getWorkspaceLocalAgentSnapshot returns the per-workspace agent override
// for (workspaceID, agentName) when one exists. Lookup errors are logged
// and treated as "no override," matching the prior fallthrough semantics.
func (h *LLMTaskHandler) getWorkspaceLocalAgentSnapshot(workspaceID, agentName string) (*agent.Agent, bool) {
	if h == nil || h.workspaceStore == nil {
		return nil, false
	}
	workspaceID = strings.TrimSpace(workspaceID)
	agentName = strings.TrimSpace(agentName)
	if workspaceID == "" || agentName == "" {
		return nil, false
	}

	local, ok, err := h.workspaceStore.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		logger.Warn("workspace-local task agent lookup failed", logger.Fields{
			"workspace_id": workspaceID,
			"agent":        agentName,
			"error":        err.Error(),
		})
		return nil, false
	}
	return local, ok && local != nil
}

// buildMissingAssignedAgentBlockedError constructs the user-facing blocked
// state when the task's assigned agent has been deleted or has otherwise
// disappeared. Returns nil when the agent does in fact resolve (defensive)
// or when prerequisites for building a useful message are missing.
func (h *LLMTaskHandler) buildMissingAssignedAgentBlockedError(agentName string, task Task) *TaskBlockedError {
	if h == nil || h.agentStore == nil {
		return nil
	}

	normalizedAgentName := strings.TrimSpace(agentName)
	if normalizedAgentName == "" {
		return nil
	}

	if existing, ok := h.agentStore.GetAgent(normalizedAgentName); ok && existing != nil {
		return nil
	}

	if _, ok := h.getWorkspaceLocalAgentSnapshot(task.WorkspaceID, normalizedAgentName); ok {
		return nil
	}

	isWorkspaceAgent := false
	if strings.TrimSpace(task.WorkspaceID) != "" && h.workspaceStore != nil {
		if ws, err := h.workspaceStore.Get(task.WorkspaceID); err == nil && ws != nil {
			isWorkspaceAgent = ws.HasAgent(normalizedAgentName)
		}
	}

	reason := fmt.Sprintf("Assigned agent %s is no longer available.", normalizedAgentName)
	question := fmt.Sprintf(
		"This task is assigned to %s, but that agent no longer exists. Switch to another agent or recreate it, then retry.",
		normalizedAgentName,
	)
	if isWorkspaceAgent {
		reason = fmt.Sprintf("Assigned workspace agent %s is no longer available.", normalizedAgentName)
		question = fmt.Sprintf(
			"This task still points at workspace agent %s, but that agent no longer exists as a runnable definition. Switch to another agent or recreate it, then retry.",
			normalizedAgentName,
		)
	}

	if availableAgents := h.listAvailableExecutionAgents(normalizedAgentName); len(availableAgents) > 0 {
		question = fmt.Sprintf(
			"%s %d other runnable agent%s %s currently available.",
			question,
			len(availableAgents),
			map[bool]string{true: "", false: "s"}[len(availableAgents) == 1],
			map[bool]string{true: "is", false: "are"}[len(availableAgents) == 1],
		)
	}

	return &TaskBlockedError{
		ReasonCode: "assigned_agent_missing",
		Reason:     reason,
		Question:   question,
		SuggestedActions: []string{
			"switch_agent_retry",
			"mark_failed",
		},
	}
}

// listAvailableExecutionAgents returns the deduplicated, sorted list of
// agent names registered in the global store (workspace-local overrides
// aren't included; the caller's question text just needs a count of
// switch-agent candidates).
func (h *LLMTaskHandler) listAvailableExecutionAgents(excludeAgent string) []string {
	if h == nil || h.agentStore == nil {
		return nil
	}

	names := append([]string(nil), h.agentStore.ListAgents()...)
	sort.Strings(names)

	excludedKey := strings.ToLower(strings.TrimSpace(excludeAgent))
	seen := make(map[string]struct{}, len(names))
	available := make([]string, 0, len(names))
	for _, candidate := range names {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if key == excludedKey {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		available = append(available, trimmed)
	}

	return available
}
