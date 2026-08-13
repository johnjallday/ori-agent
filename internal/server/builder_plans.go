package server

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspaceplan"
)

// Wiring for the Workspace Planning Workflow's two lookups: which model to
// plan with, and what the workspace actually has to plan around.
//
// Both are resolved per call rather than captured at build time. Planning
// settings and the agent roster change while the server runs, and a Plan
// drafted after such a change should reflect it without a restart.

// resolvePlanningProvider returns the provider and model to plan with.
//
// Planning uses the configured system model: it is a structured-output task
// with no conversation, so it should not consume a workspace agent's model
// choice. Any failure here means generation is unavailable right now, which
// the caller reports distinctly from a failure — everything that does not need
// a model keeps working (FR-58, FR-177).
func (b *ServerBuilder) resolvePlanningProvider(context.Context) (llm.Provider, string, error) {
	if b.llmFactory == nil || b.configManager == nil {
		return nil, "", fmt.Errorf("no planning model is configured")
	}

	providerName, modelName := b.configManager.GetSystemModel()
	result, err := b.llmFactory.GetSystemModelProvider(providerName, modelName)
	if err != nil {
		return nil, "", err
	}
	if result == nil || result.Provider == nil {
		return nil, "", fmt.Errorf("no planning model is configured")
	}
	return result.Provider, result.Model, nil
}

// resolvePlanAvailability reports the agents a workspace can assign work to.
//
// The distinction between a nil slice and an empty one is load-bearing:
// validation treats nil as "not checked" and empty as "nothing is available".
// Returning empty for a workspace we failed to load would invalidate every
// assignment in it, so an unreadable workspace returns nil instead (FR-46,
// FR-48).
func (b *ServerBuilder) resolvePlanAvailability(_ context.Context, workspaceID string) workspaceplan.ValidationContext {
	if b.workspaceStore == nil {
		return workspaceplan.ValidationContext{}
	}
	workspace, err := b.workspaceStore.Get(workspaceID)
	if err != nil || workspace == nil {
		return workspaceplan.ValidationContext{}
	}

	// An empty (non-nil) slice is the honest answer for a workspace that
	// genuinely has no agents: the planner is then told to leave every
	// assignee empty rather than inventing one.
	//
	// Names are deduplicated because a workspace may hold several instances of
	// one agent type, and the planner assigns by name.
	seen := make(map[string]struct{}, len(workspace.AgentInstances))
	agents := make([]string, 0, len(workspace.AgentInstances))
	for _, instance := range workspace.AgentInstances {
		if instance.Name == "" {
			continue
		}
		if _, exists := seen[instance.Name]; exists {
			continue
		}
		seen[instance.Name] = struct{}{}
		agents = append(agents, instance.Name)
	}
	return workspaceplan.ValidationContext{AvailableAgents: agents}
}
