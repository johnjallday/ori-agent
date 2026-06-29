package workspace

import "strings"

// CoordinatorSource describes how a workspace's coordinator agent was resolved.
type CoordinatorSource string

const (
	// CoordinatorSourceExplicitEntryAgent — an explicit entry agent is configured
	// (via shared_data.entry_agent_name or an AgentInstance.EntryPoint).
	CoordinatorSourceExplicitEntryAgent CoordinatorSource = "explicit_entry_agent"
	// CoordinatorSourceSingleAgentDefault — no explicit entry agent is set, but the
	// workspace has exactly one runnable agent, used as a documented fallback.
	CoordinatorSourceSingleAgentDefault CoordinatorSource = "single_agent_default"
	// CoordinatorSourceMissing — a multi-agent workspace has no explicit entry agent.
	// Coordinator-driven assignment must block in this case rather than guess.
	CoordinatorSourceMissing CoordinatorSource = "missing"
)

// ResolveCoordinator returns the workspace coordinator agent name and how it was
// resolved.
//
// Unlike EntryAgentName(), it deliberately does NOT fall back to "first available
// agent" for multi-agent workspaces: coordinator-driven assignment must block on a
// missing entry agent (CoordinatorSourceMissing) instead of silently picking one.
// The returned name is empty when the source is CoordinatorSourceMissing.
func (w *Workspace) ResolveCoordinator() (string, CoordinatorSource) {
	if w == nil {
		return "", CoordinatorSourceMissing
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.resolveCoordinatorLocked()
}

func (w *Workspace) resolveCoordinatorLocked() (string, CoordinatorSource) {
	// 1. Explicit entry agent from shared data (must still be a workspace member).
	if name := entryAgentNameFromSharedData(w.SharedData); name != "" && w.hasAgent(name) {
		return name, CoordinatorSourceExplicitEntryAgent
	}

	// 2. Explicit entry agent marked on an AgentInstance.
	for _, inst := range w.AgentInstances {
		if inst.EntryPoint {
			if name := strings.TrimSpace(inst.Name); name != "" {
				return name, CoordinatorSourceExplicitEntryAgent
			}
		}
	}

	// 3. No explicit entry agent — fall back only when exactly one runnable agent
	//    exists. This is the single-agent-workspace convenience case.
	if names := w.runnableAgentNamesLocked(); len(names) == 1 {
		return names[0], CoordinatorSourceSingleAgentDefault
	}

	// 4. Multi-agent (or empty) workspace with no explicit entry agent.
	return "", CoordinatorSourceMissing
}

// runnableAgentNamesLocked returns the distinct agent names in the workspace.
// The caller must hold at least a read lock.
func (w *Workspace) runnableAgentNamesLocked() []string {
	seen := make(map[string]struct{})
	var names []string
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		if name == "" {
			return
		}
		key := normalizeAgentNameKey(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	for _, inst := range w.AgentInstances {
		add(inst.Name)
	}
	return names
}
