package workspace

import "fmt"

// evaluateMissionGate is the LLMTaskHandler's per-tool-call autonomy check for
// mission runs. Called only after IsMissionTask returns true. Returns nil if
// the call should proceed; returns a non-nil error if the call must be blocked.
//
// v1 implementation (heuristic-based):
//  1. Read the workspace's AutonomyPolicy from the task's mission context.
//  2. Look up the tool's binding (MCP/skill) on the workspace to resolve
//     SideEffect via DefaultSideEffect + ToolOverrides.
//  3. If no binding owns the tool, fall back to SuggestSideEffect's name
//     heuristic — a read-prefixed name gets SideEffectRead, everything else
//     stays unclassified.
//  4. Apply IsAllowedUnderPolicy. Unclassified always denies.
//
// Known limitations called out in the PRD's Open Questions:
//   - The binding-by-tool-name lookup uses ToolOverrides keys + binding default;
//     it does not introspect MCP server tool listings. If a binding has a tool
//     that isn't in ToolOverrides and the binding default itself is unclassified,
//     we fall back to the heuristic. Per-tool binding-driven resolution that
//     consults the MCP registry's actual tool list is a follow-up.
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

// resolveMissionToolSideEffect walks workspace bindings looking for one whose
// ToolOverrides map names this tool, or whose DefaultSideEffect should apply.
// Returns the first definitive classification found; falls back to the
// heuristic (SuggestSideEffect) if nothing matches.
//
// Iteration order: ToolOverrides take precedence (most specific). Then we
// scan bindings for a non-empty DefaultSideEffect — but only when we can
// reasonably attribute this tool to that binding. v1 attribution is naive:
// we have no MCP-registry lookup here, so a DefaultSideEffect only applies
// when no other binding has classified the tool. This biases toward the
// heuristic, which is the safer choice during initial rollout.
func (h *LLMTaskHandler) resolveMissionToolSideEffect(workspaceID, toolName string) SideEffect {
	if h.workspaceStore == nil {
		return SuggestSideEffect(toolName)
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return SuggestSideEffect(toolName)
	}

	// 1) Exact per-tool override on any enabled binding.
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

	// 2) Heuristic suggestion from the tool name. Returns SideEffectRead
	// for read-prefixed names; empty for everything else.
	if se := SuggestSideEffect(toolName); se != "" {
		return se
	}

	// 3) No override and no heuristic hit — unclassified. The gate will
	// deny. (We deliberately do NOT fall back to DefaultSideEffect across
	// the whole workspace because that could allow a tool from binding A
	// to inherit binding B's permissive default.)
	return ""
}

// EvaluateMissionToolCallDecision is a thin wrapper around the package-level
// EvaluateMissionToolCall so the task handler can pass a pre-resolved
// classification (avoiding re-resolving). Kept here rather than in mission.go
// because the resolution step lives on the handler.
func EvaluateMissionToolCallDecision(policy AutonomyPolicy, classification SideEffect, toolName string) GateDecision {
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
