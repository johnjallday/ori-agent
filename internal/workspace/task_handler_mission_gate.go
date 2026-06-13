package workspace

import "fmt"

// evaluateMissionGate is the LLMTaskHandler's per-tool-call autonomy check for
// mission runs. Called only after IsMissionTask returns true. Returns nil if
// the call should proceed; returns a non-nil error if the call must be blocked.
//
// v1 implementation:
//  1. Read the workspace's AutonomyPolicy from the task's mission context.
//  2. Resolve the tool's SideEffect via resolveMissionToolSideEffect
//     (per-tool override → binding default → name heuristic).
//  3. Apply IsAllowedUnderPolicy. Unclassified always denies.
//
// Known limitations called out in the PRD's Open Questions:
//   - Tool→binding attribution does not introspect MCP server tool listings, so
//     a tool without an explicit override inherits the most restrictive default
//     across enabled bindings rather than its owning binding's default. Precise
//     per-binding resolution via the MCP registry's tool list is a follow-up.
//   - "Workspace-internal write" detection isn't precise — Propose currently
//     allows ALL SideEffectWrite tools. Differentiating internal vs. external
//     write is what the SideEffectExternal classification is for; that's why
//     external defaults to deny under both Watch and Propose.
func (h *LLMTaskHandler) evaluateMissionGate(task Task, toolName string) error {
	policy := MissionAutonomyFromContext(task.Context)
	if policy == "" {
		return fmt.Errorf("mission gate: workspace autonomy policy missing from task context")
	}

	classification := h.resolveMissionToolSideEffect(task.WorkspaceID, toolName)
	dec := EvaluateMissionToolCallDecision(policy, classification, toolName)
	if dec.Allowed {
		return nil
	}
	return fmt.Errorf("mission autonomy gate blocked tool %q: %s (classification=%q, policy=%q)",
		toolName, dec.Reason, dec.Classification, dec.Policy)
}

// delegatedSubtaskAutonomyPolicy returns the workspace autonomy policy that
// applies to a delegated subtask, or "" when the task isn't a delegated subtask
// or the workspace has no policy configured (then the gate does not apply, so
// delegation in non-policy workspaces behaves like ordinary task execution).
func (h *LLMTaskHandler) delegatedSubtaskAutonomyPolicy(task Task) AutonomyPolicy {
	if task.AssignmentMode != TaskAssignmentModeDynamicDelegation || h.workspaceStore == nil {
		return ""
	}
	ws, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil || ws == nil {
		return ""
	}
	return ws.AutonomyPolicy
}

// evaluateExecutionAutonomyGate applies the autonomy gate to a tool call. Mission
// runs use the policy from the mission context; delegated subtasks use the
// workspace policy (FR27c). Other tasks are not gated. Returns nil to proceed
// and a non-nil error to block the call.
func (h *LLMTaskHandler) evaluateExecutionAutonomyGate(task Task, toolName string) error {
	if IsMissionTask(task.Context) {
		return h.evaluateMissionGate(task, toolName)
	}
	policy := h.delegatedSubtaskAutonomyPolicy(task)
	if policy == "" {
		return nil
	}
	classification := h.resolveMissionToolSideEffect(task.WorkspaceID, toolName)
	dec := EvaluateMissionToolCallDecision(policy, classification, toolName)
	if dec.Allowed {
		return nil
	}
	return fmt.Errorf("delegation autonomy gate blocked tool %q: %s (classification=%q, policy=%q)",
		toolName, dec.Reason, dec.Classification, dec.Policy)
}

// resolveMissionToolSideEffect classifies a tool for the autonomy gate.
// Precedence: per-tool override → binding default → name heuristic → empty.
//
// The binding default is the primary classifier for tools without an explicit
// override: MissionBindingsReady guarantees every enabled binding has a valid
// DefaultSideEffect before a mission can start, and ResolveSideEffect documents
// "override → default → empty" as the resolution contract. We honor that here
// (an earlier version skipped the default and denied every non-read tool, which
// blocked Propose missions from using the writes their bindings authorized).
//
// We can't yet attribute a tool to its owning binding (no MCP-registry lookup —
// a follow-up), so we apply the most restrictive default across enabled
// bindings. That fails closed (never grants more than the strictest binding
// allows) and is deliberately checked BEFORE the name heuristic so an external
// binding's read-prefixed tool (e.g. "fetch_url") isn't mis-allowed as a read.
func (h *LLMTaskHandler) resolveMissionToolSideEffect(workspaceID, toolName string) SideEffect {
	// Native workspace-memory tools have a fixed classification; binding
	// overrides and defaults must not be able to re-classify them.
	if IsWorkspaceMemoryTool(toolName) {
		return SideEffectWrite
	}
	if h.workspaceStore == nil {
		return SuggestSideEffect(toolName)
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return SuggestSideEffect(toolName)
	}

	// 1) Exact per-tool override on any enabled binding (most specific).
	for _, b := range ws.MCPBindings {
		if !b.Enabled {
			continue
		}
		if se, ok := b.ToolOverrides[toolName]; ok && se != "" {
			return se
		}
	}
	for _, b := range ws.SkillBindings {
		if !b.Enabled {
			continue
		}
		if se, ok := b.ToolOverrides[toolName]; ok && se != "" {
			return se
		}
	}

	// 2) Most restrictive DefaultSideEffect among enabled bindings.
	if def := missionDefaultSideEffect(ws); def != "" {
		return def
	}

	// 3) Heuristic from the tool name — only reached when no enabled binding
	// has a classified default (e.g. an as-yet-unclassified workspace). Returns
	// SideEffectRead for read-prefixed names; empty for everything else.
	if se := SuggestSideEffect(toolName); se != "" {
		return se
	}

	// 4) No override, no binding default, no heuristic hit — unclassified; the
	// gate will deny.
	return ""
}

// missionDefaultSideEffect returns the most restrictive DefaultSideEffect among
// the workspace's enabled bindings, or "" when none carry a valid default.
// "Most restrictive" is the classification the gate is least likely to allow
// (external > write > read), so when a tool can't be attributed to a specific
// binding we inherit the strictest default rather than the most permissive one.
func missionDefaultSideEffect(ws *Workspace) SideEffect {
	rank := func(se SideEffect) int {
		switch se {
		case SideEffectExternal:
			return 3
		case SideEffectWrite:
			return 2
		case SideEffectRead:
			return 1
		default:
			return 0
		}
	}
	var best SideEffect
	consider := func(se SideEffect) {
		if isValidSideEffect(se) && rank(se) > rank(best) {
			best = se
		}
	}
	for _, b := range ws.MCPBindings {
		if b.Enabled {
			consider(b.DefaultSideEffect)
		}
	}
	for _, b := range ws.SkillBindings {
		if b.Enabled {
			consider(b.DefaultSideEffect)
		}
	}
	return best
}

// EvaluateMissionToolCallDecision is a thin wrapper around the package-level
// EvaluateMissionToolCall so the task handler can pass a pre-resolved
// classification (avoiding re-resolving). Kept here rather than in mission.go
// because the resolution step lives on the handler.
func EvaluateMissionToolCallDecision(policy AutonomyPolicy, classification SideEffect, toolName string) GateDecision {
	// Memory tools are allowed under every policy regardless of the resolved
	// classification (see IsWorkspaceMemoryTool); mirror EvaluateMissionToolCall.
	if IsWorkspaceMemoryTool(toolName) {
		return GateDecision{Allowed: true, Classification: SideEffectWrite, Policy: policy, ToolName: toolName}
	}
	dec := GateDecision{
		Classification: classification,
		Policy:         policy,
		ToolName:       toolName,
	}
	if classification == "" {
		dec.Reason = "tool is unclassified — classify the binding or update the heuristic before enabling missions"
		return dec
	}
	if !IsAllowedUnderPolicy(policy, classification) {
		dec.Reason = fmt.Sprintf("%s policy denies %s tools", policy, classification)
		return dec
	}
	dec.Allowed = true
	return dec
}
