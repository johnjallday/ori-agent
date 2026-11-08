package workspace

import (
	"fmt"
	"strings"
)

// Card suggestion tools the closing step names (PRD #792 FR19, FR20).
const (
	keeperFieldsToolName  = "project_card_propose_fields"
	managerFieldsToolName = "home_library_propose_fields"
)

// taskClosingStep is the one instruction a task prompt's protected tail gains
// in exactly two cases (FR27): the task's workspace is a linked song and the
// executing agent is its keeper, or the task's workspace is a Home and the
// executing agent is its bound Manager. Every other task gets "". It reads
// only workspace records, worded from the Home's words snapshot (neutral
// without one), and adds no run or schedule. The tools recheck their full
// policy at the call, so this is a hint, never an authority.
func (h *LLMTaskHandler) taskClosingStep(task Task) string {
	if h == nil || h.workspaceStore == nil || strings.TrimSpace(task.WorkspaceID) == "" || strings.TrimSpace(task.To) == "" {
		return ""
	}
	ws, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil || ws == nil {
		return ""
	}
	if state := ws.GetAssistantProgramState(); state != nil {
		if !taskRunByBoundManager(ws, state, task) {
			return ""
		}
		noun, _ := state.ProjectCardWords.PromptNoun()
		return closingStepText(noun, managerFieldsToolName,
			fmt.Sprintf("for that %s (find its entry_id and fields_revision with home_library_search)", noun))
	}
	station, ok := h.keeperStation(ws, task)
	if !ok {
		return ""
	}
	noun, _ := station.GetAssistantProgramState().ProjectCardWords.PromptNoun()
	return closingStepText(noun, keeperFieldsToolName, "")
}

// closingStepText is claim-free by construction: it asks for one call only
// when the work changed something, and says the suggestion is the owner's to
// accept.
func closingStepText(noun, tool, target string) string {
	if target != "" {
		target = " " + target
	}
	return fmt.Sprintf("## Before You Finish\n\n"+
		"If this work changed what the %[1]s needs next, what blocks it, or what it will deliver, call `%[2]s` once%[3]s "+
		"with only those fields (next_action, blockers, deliverables, milestones, or the package's declared fields). "+
		"Do not call it when nothing changed. The call saves a suggestion only: do not say that the %[1]s card changed, "+
		"because the owner accepts or dismisses suggestions.\n\n", noun, tool, target)
}

// taskRunByBoundManager reports whether a Home's task runs as its bound,
// required, primary Home Manager.
func taskRunByBoundManager(home *Workspace, state *AssistantProgramState, task Task) bool {
	if state.Declaration == nil || state.HomeBindings.StateRevision < 1 {
		return false
	}
	name := strings.TrimSpace(task.To)
	for _, role := range state.Declaration.Roles {
		if role.Scope != AssistantRoleScopeHome || !role.Primary || !role.Required {
			continue
		}
		for _, binding := range state.HomeBindings.Bindings {
			if binding.RoleID == role.ID && strings.TrimSpace(binding.AgentName) == name {
				return taskInstance(home, name, task.AssignedNodeID, binding.AgentInstanceID)
			}
		}
	}
	return false
}

// keeperStation returns the song's Home when the task's workspace is a live,
// non-resource song whose link names a Home whose mirrors agree, and the task
// runs as the song's entry agent.
func (h *LLMTaskHandler) keeperStation(song *Workspace, task Task) (*Workspace, bool) {
	link := song.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID == "" || AssistantProjectLinkIsResource(link) ||
		song.Status == StatusTrashed || song.Status == StatusMissing {
		return nil, false
	}
	name := strings.TrimSpace(task.To)
	if name == "" || strings.TrimSpace(song.EntryAgentName()) != name || !taskInstance(song, name, task.AssignedNodeID, "") {
		return nil, false
	}
	if !AssistantProjectLinkMirrorsAgree(h.workspaceStore, song) {
		return nil, false
	}
	station, err := h.workspaceStore.Get(link.StationWorkspaceID)
	if err != nil || station == nil || station.OwnerUserID != song.OwnerUserID || station.GetAssistantProgramState() == nil ||
		station.GetAssistantProgramState().Key.Normalize() != link.Key.Normalize() {
		return nil, false
	}
	return station, true
}

// taskInstance reports whether exactly one instance carries this agent name
// (and, when the task or binding names one, that node or instance ID).
func taskInstance(ws *Workspace, name, nodeID, instanceID string) bool {
	matched := 0
	ok := false
	for _, instance := range ws.AgentInstances {
		if strings.TrimSpace(instance.Name) != name {
			continue
		}
		matched++
		ok = (nodeID == "" || instance.NodeID == nodeID) && (instanceID == "" || instance.ID == instanceID)
	}
	return matched == 1 && ok
}
