package agenthttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// detectHomeMutationRequest inspects a prompt for an explicit, supported
// state-changing request and returns the confirmation to surface (FR #24). It
// recognizes three mutations: create a workspace, create a task in an existing
// workspace, and start an existing task. Nothing is executed here — execution
// happens only on a follow-up request carrying the matching ConfirmedAction.
//
// Detection is intentionally conservative: a mutation is proposed only when the
// verb and target are unambiguous (and, for task mutations, when the referenced
// workspace and task actually resolve against real state). Anything short of a
// confident match falls through to the normal answer path.
func (h *HomeAssistantAskHandler) detectHomeMutationRequest(prompt string) *HomeActionConfirmation {
	if conf := detectWorkspaceCreationRequest(prompt); conf != nil {
		return conf
	}
	// Agent assignment is checked before task creation: "add agent X to Y" and
	// "add a task to Y" share the "add" verb but differ by the agent/task keyword.
	if conf := h.detectAssignAgentRequest(prompt); conf != nil {
		return conf
	}
	return h.detectTaskMutationRequest(prompt)
}

// detectAssignAgentRequest matches "add/assign agent X to <workspace>" and
// proposes an assign_agent confirmation. Both the agent (against the roster) and
// the workspace must resolve to real state, otherwise it falls through.
func (h *HomeAssistantAskHandler) detectAssignAgentRequest(prompt string) *HomeActionConfirmation {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" {
		return nil
	}
	assignVerb := strings.HasPrefix(lower, "assign ")
	addVerb := strings.HasPrefix(lower, "add ") || strings.HasPrefix(lower, "put ")
	if !assignVerb && !addVerb {
		return nil
	}
	// "assign X to Y" is unambiguous; "add/put" share the "add" verb with task
	// creation, so require the explicit "agent" keyword to disambiguate.
	if addVerb && !strings.Contains(lower, "agent") {
		return nil
	}
	if h.Sources.Workspaces == nil || h.Sources.Agents == nil {
		return nil
	}
	wsID, wsName, ok := matchWorkspaceInPrompt(h.Sources.Workspaces, prompt)
	if !ok {
		return nil
	}
	agentName, ok := matchAgentInPrompt(h.Sources.Agents, prompt)
	if !ok {
		return nil
	}
	return &HomeActionConfirmation{
		ActionID:   "assign-agent",
		ActionType: HomeActionAssignAgent,
		Summary:    fmt.Sprintf("Add agent %q to workspace %q?", agentName, wsName),
		Arguments:  map[string]any{"workspace_id": wsID, "agent_name": agentName},
	}
}

// matchAgentInPrompt returns the name of a roster agent that appears in the
// prompt (longest match wins), so assignment resolves to a real agent.
func matchAgentInPrompt(reader homeAgentsReader, prompt string) (string, bool) {
	if reader == nil {
		return "", false
	}
	roster, ok := reader.AgentRoster()
	if !ok {
		return "", false
	}
	p := strings.ToLower(prompt)
	best := ""
	bestLen := 0
	for _, a := range roster {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		if len(name) >= 2 && strings.Contains(p, name) && len(name) > bestLen {
			best, bestLen = a.Name, len(name)
		}
	}
	return best, bestLen > 0
}

// detectWorkspaceCreationRequest matches "create/new/make/add a workspace
// called/named X" and proposes a create_workspace confirmation.
func detectWorkspaceCreationRequest(prompt string) *HomeActionConfirmation {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" {
		return nil
	}
	if !hasCreateVerb(lower) {
		return nil
	}
	for _, marker := range []string{"workspace called ", "workspace named "} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[idx+len(marker):])
		name = strings.Trim(name, " .\"'")
		if name == "" {
			continue
		}
		return &HomeActionConfirmation{
			ActionID:   "create-workspace",
			ActionType: HomeActionCreateWorkspace,
			Summary:    fmt.Sprintf("Create a new workspace named %q?", name),
			Arguments:  map[string]any{"name": name},
		}
	}
	return nil
}

// detectTaskMutationRequest matches task creation ("create a task to X in
// <workspace>") and task execution ("start/run the <task> task in
// <workspace>"). Both require the named workspace to resolve to real state;
// start additionally requires the task to resolve.
func (h *HomeAssistantAskHandler) detectTaskMutationRequest(prompt string) *HomeActionConfirmation {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" || !strings.Contains(lower, "task") {
		return nil
	}
	store := h.Sources.Workspaces
	if store == nil {
		return nil
	}

	createVerb := hasCreateVerb(lower)
	startVerb := hasStartVerb(lower)
	if !createVerb && !startVerb {
		return nil
	}

	wsID, wsName, ok := matchWorkspaceInPrompt(store, prompt)
	if !ok {
		return nil
	}

	// "start" takes precedence: "run" / "start" are unambiguous execution verbs,
	// whereas "add"/"create" only ever mean creation.
	if startVerb {
		return h.buildStartTaskConfirmation(store, wsID, wsName, trimmed)
	}

	desc := extractTaskDescription(trimmed, wsName)
	if desc == "" {
		return nil
	}
	return &HomeActionConfirmation{
		ActionID:   "create-task",
		ActionType: HomeActionCreateTask,
		Summary:    fmt.Sprintf("Create a task in %q: %q?", wsName, desc),
		Arguments:  map[string]any{"workspace_id": wsID, "description": desc},
	}
}

// buildStartTaskConfirmation resolves which pending task the user wants to start
// within the given workspace. It matches the prompt against the descriptions of
// runnable tasks; if exactly one runnable task exists it is used directly, and
// if several match ambiguously it declines (returns nil) so the assistant can
// fall back to listing them instead of guessing.
func (h *HomeAssistantAskHandler) buildStartTaskConfirmation(store workspace.Store, wsID, wsName, prompt string) *HomeActionConfirmation {
	ws, err := store.Get(wsID)
	if err != nil || ws == nil {
		return nil
	}

	runnable := make([]workspace.Task, 0, len(ws.Tasks))
	for _, t := range ws.Tasks {
		if isRunnableTaskStatus(t.Status) {
			runnable = append(runnable, t)
		}
	}
	if len(runnable) == 0 {
		return nil
	}

	promptTokens := tokenizePrompt(prompt)
	bestIdx := -1
	bestScore := 0
	tie := false
	for i := range runnable {
		descTokens := tokenizePrompt(runnable[i].Description)
		score := signalTokenOverlap(promptTokens, descTokens)
		switch {
		case score > bestScore:
			bestScore, bestIdx, tie = score, i, false
		case score == bestScore && score > 0:
			tie = true
		}
	}

	switch {
	case bestScore > 0 && !tie:
		// Confident description match.
	case len(runnable) == 1:
		// Only one candidate — "start the task in <workspace>" is unambiguous.
		bestIdx = 0
	default:
		// No signal match, or an ambiguous tie among several tasks.
		return nil
	}

	target := runnable[bestIdx]
	return &HomeActionConfirmation{
		ActionID:   "start-task",
		ActionType: HomeActionStartTask,
		Summary:    fmt.Sprintf("Start the task %q in %q?", truncateTaskLabel(target.Description, 80), wsName),
		Arguments:  map[string]any{"workspace_id": wsID, "task_id": target.ID},
	}
}

// isRunnableTaskStatus reports whether a task in this status can be (re)started
// from the home assistant. In-progress and completed tasks are excluded; failed,
// cancelled, and timed-out tasks may be retried.
func isRunnableTaskStatus(status workspace.TaskStatus) bool {
	switch status {
	case workspace.TaskStatusInProgress, workspace.TaskStatusCompleted:
		return false
	default:
		return true
	}
}

func hasCreateVerb(lower string) bool {
	return strings.HasPrefix(lower, "create ") ||
		strings.HasPrefix(lower, "add ") ||
		strings.HasPrefix(lower, "new ") ||
		strings.HasPrefix(lower, "make ")
}

func hasStartVerb(lower string) bool {
	return strings.HasPrefix(lower, "start ") ||
		strings.HasPrefix(lower, "run ") ||
		strings.HasPrefix(lower, "execute ") ||
		strings.HasPrefix(lower, "begin ") ||
		strings.HasPrefix(lower, "kick off ") ||
		strings.HasPrefix(lower, "launch ")
}

// extractTaskDescription pulls the free-text task description out of a creation
// prompt by removing the workspace mention and the leading "create a task …"
// scaffolding. Returns "" when no description remains.
func extractTaskDescription(prompt, wsName string) string {
	s := stripWorkspaceMention(prompt, wsName)

	lower := strings.ToLower(s)
	idx := strings.Index(lower, "task")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(s[idx+len("task"):])

	// Drop a single leading connector that links "task" to its description.
	for _, connector := range []string{"to ", "that ", "which ", "for ", "of ", "named ", "called ", "titled ", "about "} {
		if strings.HasPrefix(strings.ToLower(rest), connector) {
			rest = strings.TrimSpace(rest[len(connector):])
			break
		}
	}
	rest = strings.Trim(rest, " :\"'.-")
	return strings.TrimSpace(rest)
}

// stripWorkspaceMention removes the first reference to wsName (and the
// surrounding "in [the] … [workspace]" connective words) from the prompt so the
// remaining text is just the task description.
func stripWorkspaceMention(prompt, wsName string) string {
	wsName = strings.TrimSpace(wsName)
	if wsName == "" {
		return prompt
	}
	lower := strings.ToLower(prompt)
	lowerName := strings.ToLower(wsName)

	// Longest, most specific phrasings first so we strip the connectives too.
	candidates := []string{
		"in the " + lowerName + " workspace",
		"in " + lowerName + " workspace",
		"to the " + lowerName + " workspace",
		"in the " + lowerName,
		"in " + lowerName,
		"to the " + lowerName,
		"to " + lowerName,
		lowerName + " workspace",
		lowerName,
	}
	for _, phrase := range candidates {
		if idx := strings.Index(lower, phrase); idx >= 0 {
			out := prompt[:idx] + " " + prompt[idx+len(phrase):]
			return strings.Join(strings.Fields(out), " ")
		}
	}
	return prompt
}

// truncateTaskLabel shortens a task description for display in a confirmation
// summary, appending an ellipsis when cut.
func truncateTaskLabel(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := strings.TrimSpace(s[:max])
	return cut + "…"
}
