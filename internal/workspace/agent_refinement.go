package workspace

import "strings"

// AgentInstanceByName returns the workspace's AgentInstance for the given agent
// name (case-insensitive). When several instances share the name, the
// entry-point instance is preferred, else the first match. The bool is false
// when the agent is not attached to the workspace.
func AgentInstanceByName(ws *Workspace, name string) (AgentInstance, bool) {
	if ws == nil {
		return AgentInstance{}, false
	}
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return AgentInstance{}, false
	}
	var match AgentInstance
	found := false
	for _, inst := range ws.AgentInstances {
		if strings.ToLower(strings.TrimSpace(inst.Name)) != target {
			continue
		}
		if inst.EntryPoint {
			return inst, true
		}
		if !found {
			match, found = inst, true
		}
	}
	return match, found
}

// RenderAgentRefinement renders a workspace instance's per-attachment refinement
// of a shared agent definition — its role, description, and custom_instructions —
// as a directive prompt section layered onto the shared base configuration. It
// returns "" when the instance carries no refinement.
//
// Unlike notes/memory (untrusted data that is fenced as reference), refinement is
// first-party guidance the workspace owner authored to steer the agent, so it is
// rendered as directives. It is shared by the chat and task prompt paths so the
// same refinement reaches both (PRD FR16/FR19).
func RenderAgentRefinement(inst AgentInstance) string {
	role := strings.TrimSpace(inst.Role)
	desc := strings.TrimSpace(inst.Description)
	custom := strings.TrimSpace(inst.CustomInstructions)
	if role == "" && desc == "" && custom == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("Workspace-specific guidance (applies only in this workspace, layered on your base configuration):")
	if role != "" {
		b.WriteString("\n- Your role here: ")
		b.WriteString(role)
	}
	if desc != "" {
		b.WriteString("\n- ")
		b.WriteString(desc)
	}
	if custom != "" {
		b.WriteString("\n\n")
		b.WriteString(custom)
	}
	return b.String()
}

// AppendAgentRefinement appends the named agent's refinement section (if any) to
// base, resolving the instance from the workspace store. It is a no-op that
// returns base unchanged when the store/workspace/instance is unavailable or the
// instance carries no refinement. Convenience for prompt-assembly call sites.
func AppendAgentRefinement(base string, store Store, workspaceID, agentName string) string {
	if store == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(agentName) == "" {
		return base
	}
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil {
		return base
	}
	inst, ok := AgentInstanceByName(ws, agentName)
	if !ok {
		return base
	}
	section := RenderAgentRefinement(inst)
	if section == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return section
	}
	return base + "\n\n---\n" + section
}
