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
	if conf := detectCreateAgentRequest(prompt); conf != nil {
		return conf
	}
	if conf := h.detectRemoveAgentRequest(prompt); conf != nil {
		return conf
	}
	// Agent assignment is checked before task creation: "add agent X to Y" and
	// "add a task to Y" share the "add" verb but differ by the agent/task keyword.
	if conf := h.detectAssignAgentRequest(prompt); conf != nil {
		return conf
	}
	return h.detectTaskMutationRequest(prompt)
}

// detectCreateAgentRequest matches "create/new/make an agent called/named X" and
// proposes a create_agent confirmation. Parallels workspace creation.
func detectCreateAgentRequest(prompt string) *HomeActionConfirmation {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" || !hasCreateVerb(lower) {
		return nil
	}
	for _, marker := range []string{"agent called ", "agent named "} {
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
			ActionID:   "create-agent",
			ActionType: HomeActionCreateAgent,
			Summary:    fmt.Sprintf("Create a new agent named %q?", name),
			Arguments:  map[string]any{"name": name},
		}
	}
	return nil
}

// detectRemoveAgentRequest matches "remove/unassign agent X from <workspace>" and
// proposes a remove_agent confirmation. The agent (against the roster) and the
// workspace must both resolve, otherwise it falls through.
func (h *HomeAssistantAskHandler) detectRemoveAgentRequest(prompt string) *HomeActionConfirmation {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" {
		return nil
	}
	if !strings.HasPrefix(lower, "remove ") && !strings.HasPrefix(lower, "unassign ") {
		return nil
	}
	if !strings.Contains(lower, " from ") {
		return nil
	}
	if h.Sources.Workspaces == nil || h.Sources.Agents == nil {
		return nil
	}
	agentName, ok := matchAgentInPrompt(h.Sources.Agents, prompt)
	if !ok {
		return nil
	}
	wsID, wsName, ok := matchWorkspaceInPrompt(h.Sources.Workspaces, prompt)
	if !ok {
		return nil
	}
	return &HomeActionConfirmation{
		ActionID:   "remove-agent",
		ActionType: HomeActionRemoveAgent,
		Summary:    fmt.Sprintf("Remove agent %q from workspace %q?", agentName, wsName),
		Arguments:  map[string]any{"workspace_id": wsID, "agent_name": agentName},
	}
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
		// Require >= 3 chars (matching matchWorkspaceInPrompt) so very short agent
		// names don't substring-match unrelated words; longest match wins.
		if len(name) >= 3 && strings.Contains(p, name) && len(name) > bestLen {
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

// homeBacklogConnectivePhrases are the connective phrases that distinguish an
// actual "add to the backlog" capture request from a task whose description
// merely mentions the word "backlog" (e.g. "new task that reviews the
// backlog in Roadmap" is a normal task-creation request, not backlog
// capture — it contains "backlog" but not one of these more specific
// phrases). Shared by the detection guard and extractBacklogDescription so
// the two can never drift out of sync.
var homeBacklogConnectivePhrases = []string{"to the backlog", "to backlog", "in the backlog", "in backlog"}

// detectBacklogCaptureRequest matches "add X to the backlog in <workspace>"
// and proposes a create_backlog_item confirmation (PRD workspace-backlog
// FR23-25). Unlike the other detectors it returns a second, non-empty decline
// message when the "backlog" phrasing is confidently recognized but the named
// workspace can't be resolved unambiguously — the caller uses that to give a
// direct, user-readable answer instead of silently falling through to a
// normal chat response (FR23-25, 6.6). A nil confirmation with an empty
// decline means "not a backlog request at all", identical in effect to every
// other detector's plain nil.
func (h *HomeAssistantAskHandler) detectBacklogCaptureRequest(prompt string) (*HomeActionConfirmation, string) {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)
	if lower == "" || !hasCreateVerb(lower) || !containsAny(lower, homeBacklogConnectivePhrases) {
		return nil, ""
	}
	store := h.Sources.Workspaces
	if store == nil {
		return nil, ""
	}

	wsID, wsName, ok, ambiguous := matchRoutableWorkspaceInPrompt(store, prompt)
	if ambiguous {
		return nil, "More than one workspace matches that name — try naming it more specifically."
	}
	if !ok {
		return nil, "I couldn't find a workspace matching that name for the backlog item. Try naming it exactly, e.g. \"add X to the backlog in Website Redesign\"."
	}

	desc := extractBacklogDescription(trimmed, wsName)
	if desc == "" {
		return nil, "I need a short description of what to add to the backlog."
	}

	return &HomeActionConfirmation{
		ActionID:   "create-backlog-item",
		ActionType: HomeActionCreateBacklogItem,
		Summary:    fmt.Sprintf("Add %q to the backlog in %q? It stays uncommitted — no agent or schedule — until you promote it to Ready.", desc, wsName),
		Arguments:  map[string]any{"workspace_id": wsID, "description": desc},
	}, ""
}

// matchRoutableWorkspaceInPrompt is matchWorkspaceInPrompt (home_nav_catalog.go)
// narrowed to workspaces a new item could actually be captured into: active
// only (excludes trashed/completed/failed/cancelled/missing — FR23-25, 6.6
// "deleted... workspace targets") and never a group roll-up (mirrors
// isHomeAssistantRoutableWorkspace's "unauthorized" carve-out, since a group
// has no content root of its own to capture into). It additionally reports a
// tie between two distinct, equally-specific name matches as ambiguous rather
// than silently picking one, which the simpler matchWorkspaceInPrompt does not.
func matchRoutableWorkspaceInPrompt(store workspace.Store, prompt string) (id, name string, ok, ambiguous bool) {
	if store == nil {
		return "", "", false, false
	}
	active, err := store.ListActive()
	if err != nil {
		return "", "", false, false
	}
	p := strings.ToLower(prompt)
	bestLen := 0
	for _, ws := range active {
		if !isHomeAssistantRoutableWorkspace(ws) {
			continue
		}
		n := strings.ToLower(strings.TrimSpace(ws.Name))
		if len(n) < 3 || !strings.Contains(p, n) {
			continue
		}
		switch {
		case len(n) > bestLen:
			id, name, bestLen, ambiguous = ws.ID, ws.Name, len(n), false
		case len(n) == bestLen && !strings.EqualFold(ws.Name, name):
			ambiguous = true
		}
	}
	return id, name, bestLen > 0, ambiguous
}

// extractBacklogDescription pulls the free-text idea out of a backlog-capture
// prompt by removing the workspace mention, the leading "add/create/new/make"
// verb, and the trailing "to/in [the] backlog" phrase. Returns "" when no
// description remains. The description sits BETWEEN the verb and "backlog"
// ("add X to the backlog in Y"), unlike extractTaskDescription's task
// phrasing where the description follows the "task" keyword.
func extractBacklogDescription(prompt, wsName string) string {
	s := stripWorkspaceMention(prompt, wsName)
	lower := strings.ToLower(s)

	for _, verb := range []string{"add ", "create ", "new ", "make "} {
		if strings.HasPrefix(lower, verb) {
			s = s[len(verb):]
			lower = lower[len(verb):]
			break
		}
	}

	for _, phrase := range homeBacklogConnectivePhrases {
		if idx := strings.Index(lower, phrase); idx >= 0 {
			s = s[:idx]
			lower = lower[:idx]
			break
		}
	}

	s = strings.Trim(strings.TrimSpace(s), " :\"'.-")
	return strings.TrimSpace(s)
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, substrings []string) bool {
	for _, sub := range substrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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
	case workspace.TaskStatusInProgress, workspace.TaskStatusCompleted, workspace.TaskStatusBacklog:
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
