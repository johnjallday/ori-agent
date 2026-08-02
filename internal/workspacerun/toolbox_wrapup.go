package workspacerun

import (
	"fmt"
	"sort"
	"strings"
)

// The Wrap-up: what a run actually did with the capabilities it was given
// (PRD FR-114–FR-122).
//
// The distinction this file exists to protect is MEASURED versus INFERRED. A
// tool call leaves a trace event, so "read_note was invoked 4 times" is a fact.
// A skill contributes prompt text, so nothing observes whether the model
// followed it — "this skill went unused" is a guess, and presenting a guess as
// a measurement would lead users to delete skills that were working (FR-116,
// FR-117).
//
// So: operations get counted from traces, and skills get reported as
// "no direct evidence either way" rather than as unused.
//
// Nothing here changes anything. Every suggestion is a description of an edit
// the user may choose to make, and **Create variant** prefills a draft that
// goes through the normal preview flow rather than rewriting the toolbox the
// completed run used (FR-119, FR-120).

// ToolboxWrapUp is the post-run report tied to one immutable snapshot.
type ToolboxWrapUp struct {
	RunID string `json:"run_id"`
	// SnapshotHash ties the report to the exact capabilities measured. If the
	// toolbox has been edited since, this is how a reader knows the report
	// describes something else.
	SnapshotHash   string `json:"snapshot_hash,omitempty"`
	ToolboxID      string `json:"toolbox_id,omitempty"`
	ToolboxName    string `json:"toolbox_name,omitempty"`
	ToolboxVersion int64  `json:"toolbox_version,omitempty"`

	// --- Measured (FR-115) ---
	Operations []WrapUpOperation `json:"operations,omitempty"`
	// UnusedOperations are allowlisted operations with no invocation in the
	// trace. Concrete and countable, unlike skills.
	UnusedOperations []string `json:"unused_operations,omitempty"`
	BlockedCalls     int      `json:"blocked_calls"`
	Retries          int      `json:"retries"`
	ApprovalRequests int      `json:"approval_requests"`
	// ConnectionFailures names bindings whose connection failed mid-run. The
	// run fails or asks rather than substituting another connector (FR-113).
	ConnectionFailures []string `json:"connection_failures,omitempty"`
	TotalToolCalls     int      `json:"total_tool_calls"`

	// TokensUsed / CostUSD / DurationMs are recorded when the executor
	// reported them, and omitted rather than estimated when it did not.
	TokensUsed int64   `json:"tokens_used,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`

	// --- Inferred, and labeled as such (FR-117) ---
	SkillObservations []WrapUpSkillObservation `json:"skill_observations,omitempty"`

	// --- Optional, evidence-based suggestions (FR-118) ---
	Suggestions []WrapUpSuggestion `json:"suggestions,omitempty"`
}

// WrapUpOperation is one operation the run actually invoked.
type WrapUpOperation struct {
	Tool string `json:"tool"`
	// Binding / Server identify where it came from, resolved through the
	// snapshot rather than guessed from the tool name.
	Binding string `json:"binding,omitempty"`
	Server  string `json:"server,omitempty"`
	// SideEffect is the classification the binding declared.
	SideEffect string `json:"side_effect,omitempty"`
	Calls      int    `json:"calls"`
	Failures   int    `json:"failures,omitempty"`
}

// WrapUpSkillObservation is what can honestly be said about one active skill.
type WrapUpSkillObservation struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name,omitempty"`
	Source       string `json:"source"`
	// Evidence is "measured" when something observable ties back to the skill,
	// and "none" when nothing does. It is never "unused": a prompt-only skill
	// leaves no trace whether it worked or not, and claiming otherwise would
	// invite deleting something load-bearing (FR-116).
	Evidence string `json:"evidence"`
	// Note is the plain-language version of the same thing.
	Note string `json:"note,omitempty"`
	// PromptChars is the context it cost, which IS measurable and is the honest
	// basis for a "consider removing" conversation.
	PromptChars int `json:"prompt_chars,omitempty"`
}

// Evidence values.
const (
	WrapUpEvidenceMeasured = "measured"
	WrapUpEvidenceNone     = "none"
)

// Suggestion kinds (FR-118).
const (
	SuggestRemoveUnusedOperations = "remove_unused_operations"
	SuggestNarrowAllowlist        = "narrow_allowlist"
	SuggestAddCapability          = "add_capability"
	SuggestRemoveOverlap          = "remove_overlap"
	SuggestSaveVariant            = "save_variant"
)

// WrapUpSuggestion is an evidence-backed thing the user MAY choose to do.
type WrapUpSuggestion struct {
	Kind string `json:"kind"`
	// Message states the suggestion in plain language.
	Message string `json:"message"`
	// Evidence names what was observed to justify it. A suggestion without
	// evidence is an opinion, and this report does not make those.
	Evidence string `json:"evidence,omitempty"`
	// Tools / Bindings identify what the suggestion is about, so a variant can
	// be prefilled from it without re-deriving anything.
	Tools    []string `json:"tools,omitempty"`
	Bindings []string `json:"bindings,omitempty"`
}

// BuildToolboxWrapUp measures one finished run against its snapshot.
//
// It reads only the snapshot and the trace, so it can be recomputed from
// persisted state at any time and cannot be influenced by later changes to the
// workspace.
func BuildToolboxWrapUp(runID string, snapshot *RunToolboxSnapshot, trace []TraceEvent, cost *CostSummary, durationMs int64) *ToolboxWrapUp {
	if snapshot == nil {
		return nil
	}

	wrapUp := &ToolboxWrapUp{
		RunID:          runID,
		SnapshotHash:   snapshot.Hash,
		ToolboxID:      snapshot.ToolboxID,
		ToolboxName:    snapshot.ToolboxName,
		ToolboxVersion: snapshot.ToolboxVersion,
		DurationMs:     durationMs,
	}
	if cost != nil {
		wrapUp.TokensUsed = int64(cost.TotalTokens)
		wrapUp.CostUSD = cost.USD
	}

	// Index the snapshot so every measured call resolves to the binding that
	// actually provided it — never to a name-based guess.
	type toolOrigin struct {
		binding    string
		server     string
		sideEffect string
	}
	origins := make(map[string]toolOrigin)
	allowlisted := make(map[string]struct{})
	for _, binding := range snapshot.MCPBindings {
		for _, tool := range binding.AllowedTools {
			key := strings.ToLower(strings.TrimSpace(tool))
			effect := binding.DefaultSideEffect
			if override, ok := binding.ToolRisks[tool]; ok {
				effect = override
			}
			origins[key] = toolOrigin{
				binding:    binding.BindingID,
				server:     firstNonEmptyString(binding.Alias, binding.ServerName),
				sideEffect: effect,
			}
			allowlisted[key] = struct{}{}
		}
	}

	invoked := make(map[string]*WrapUpOperation)
	for _, event := range trace {
		switch event.Kind {
		case TraceToolCall:
			tool := strings.TrimSpace(event.ToolName)
			if tool == "" {
				continue
			}
			wrapUp.TotalToolCalls++
			key := strings.ToLower(tool)
			operation, seen := invoked[key]
			if !seen {
				origin := origins[key]
				operation = &WrapUpOperation{
					Tool:       tool,
					Binding:    origin.binding,
					Server:     origin.server,
					SideEffect: origin.sideEffect,
				}
				invoked[key] = operation
			}
			operation.Calls++
		case TraceToolResult:
			if isBlockedStatus(event.Status) {
				wrapUp.BlockedCalls++
			}
			if tool := strings.ToLower(strings.TrimSpace(event.ToolName)); tool != "" {
				if operation, seen := invoked[tool]; seen && isFailureStatus(event.Status) {
					operation.Failures++
				}
			}
		case TraceError:
			if isRetryMessage(event.Message) {
				wrapUp.Retries++
			}
			if binding := connectionFailureBinding(event); binding != "" {
				wrapUp.ConnectionFailures = appendUniqueString(wrapUp.ConnectionFailures, binding)
			}
		case TraceMessage:
			if isApprovalRequest(event) {
				wrapUp.ApprovalRequests++
			}
		}
	}

	for _, operation := range invoked {
		wrapUp.Operations = append(wrapUp.Operations, *operation)
	}
	sort.SliceStable(wrapUp.Operations, func(i, j int) bool {
		if wrapUp.Operations[i].Calls != wrapUp.Operations[j].Calls {
			return wrapUp.Operations[i].Calls > wrapUp.Operations[j].Calls
		}
		return wrapUp.Operations[i].Tool < wrapUp.Operations[j].Tool
	})

	// FR-116: an allowlisted operation with no invocation is genuinely unused.
	// This is the concrete half of the report.
	for tool := range allowlisted {
		if _, called := invoked[tool]; !called {
			wrapUp.UnusedOperations = append(wrapUp.UnusedOperations, tool)
		}
	}
	sort.Strings(wrapUp.UnusedOperations)

	wrapUp.SkillObservations = observeSkills(snapshot, invoked)
	wrapUp.Suggestions = suggestFromEvidence(snapshot, wrapUp)
	return wrapUp
}

// observeSkills says only what can be said about each active skill.
//
// A skill that shares a name with an invoked tool has real evidence; everything
// else has none. "None" is explicitly NOT "unused" — the report has no way to
// see whether a prompt shaped the model's behavior, and pretending otherwise
// would make its advice actively harmful (FR-116, FR-117).
func observeSkills(snapshot *RunToolboxSnapshot, invoked map[string]*WrapUpOperation) []WrapUpSkillObservation {
	observations := make([]WrapUpSkillObservation, 0, len(snapshot.Skills))
	for _, skill := range snapshot.Skills {
		observation := WrapUpSkillObservation{
			CapabilityID: skill.CapabilityID,
			DisplayName:  firstNonEmptyString(skill.DisplayName, skill.CapabilityID),
			Source:       skill.Source,
			PromptChars:  skill.PromptChars,
			Evidence:     WrapUpEvidenceNone,
			Note:         "No direct evidence either way — skills shape how the agent works, and that leaves no trace.",
		}
		if _, called := invoked[strings.ToLower(skill.CapabilityID)]; called {
			observation.Evidence = WrapUpEvidenceMeasured
			observation.Note = "A tool with this name was invoked during the run."
		}
		observations = append(observations, observation)
	}
	sort.SliceStable(observations, func(i, j int) bool {
		return observations[i].CapabilityID < observations[j].CapabilityID
	})
	return observations
}

// suggestFromEvidence produces suggestions that each cite what was observed.
func suggestFromEvidence(snapshot *RunToolboxSnapshot, wrapUp *ToolboxWrapUp) []WrapUpSuggestion {
	var suggestions []WrapUpSuggestion

	// Unused operations are the one thing this report can recommend removing
	// with confidence, because "never called" is measured.
	if len(wrapUp.UnusedOperations) > 0 && wrapUp.TotalToolCalls > 0 {
		suggestions = append(suggestions, WrapUpSuggestion{
			Kind: SuggestRemoveUnusedOperations,
			Message: fmt.Sprintf("%d operation(s) were available but never used. A smaller toolbox is easier for the agent to choose from.",
				len(wrapUp.UnusedOperations)),
			Evidence: fmt.Sprintf("%d tool calls were made, none of them to these.", wrapUp.TotalToolCalls),
			Tools:    wrapUp.UnusedOperations,
		})
	}

	// A blocked call is evidence of a real gap: the agent tried and was
	// refused, which is different from never trying.
	if wrapUp.BlockedCalls > 0 {
		suggestions = append(suggestions, WrapUpSuggestion{
			Kind:     SuggestAddCapability,
			Message:  "Some calls were blocked. The toolbox may be missing something this goal needs, or an operation may need classifying.",
			Evidence: fmt.Sprintf("%d blocked call(s) during this run.", wrapUp.BlockedCalls),
		})
	}

	if len(wrapUp.ConnectionFailures) > 0 {
		suggestions = append(suggestions, WrapUpSuggestion{
			Kind:     SuggestAddCapability,
			Message:  "A connection this toolbox depends on failed during the run.",
			Evidence: "Connection failure on " + strings.Join(wrapUp.ConnectionFailures, ", ") + ".",
			Bindings: wrapUp.ConnectionFailures,
		})
	}

	// Two bindings whose invoked operations do the same thing is overlap the
	// run actually exercised — not a theoretical one.
	if overlapping := invokedOverlap(snapshot, wrapUp); len(overlapping) > 1 {
		suggestions = append(suggestions, WrapUpSuggestion{
			Kind:     SuggestRemoveOverlap,
			Message:  "More than one connection provided the same kind of operation. Keeping one makes tool choice more predictable.",
			Evidence: "Both " + strings.Join(overlapping, " and ") + " were used for similar work.",
			Bindings: overlapping,
		})
	}

	// Offer the variant only when there is something concrete to change.
	if len(suggestions) > 0 {
		suggestions = append(suggestions, WrapUpSuggestion{
			Kind:     SuggestSaveVariant,
			Message:  "Save a leaner variant of this toolbox. Nothing changes until you review and use it.",
			Evidence: "Based on what this run actually used.",
		})
	}
	return suggestions
}

// invokedOverlap names bindings that both served invoked operations of the same
// semantic shape.
func invokedOverlap(snapshot *RunToolboxSnapshot, wrapUp *ToolboxWrapUp) []string {
	byShape := make(map[string]map[string]struct{})
	for _, operation := range wrapUp.Operations {
		if operation.Binding == "" || operation.Calls == 0 {
			continue
		}
		shape := toolShape(operation.Tool)
		if shape == "" {
			continue
		}
		if byShape[shape] == nil {
			byShape[shape] = make(map[string]struct{})
		}
		byShape[shape][firstNonEmptyString(operation.Server, operation.Binding)] = struct{}{}
	}

	for _, providers := range byShape {
		if len(providers) < 2 {
			continue
		}
		names := make([]string, 0, len(providers))
		for provider := range providers {
			names = append(names, provider)
		}
		sort.Strings(names)
		return names
	}
	return nil
}

// toolShape reduces a tool name to the kind of thing it does, for overlap
// detection only. It is a heuristic, and it is used only to phrase a
// suggestion — never to claim a measurement.
func toolShape(tool string) string {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	for _, shape := range []string{"search", "read", "write", "list", "create", "delete", "send"} {
		if strings.Contains(normalized, shape) {
			return shape
		}
	}
	return ""
}

func isBlockedStatus(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	return normalized == "blocked" || normalized == "denied" || normalized == "refused"
}

func isFailureStatus(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	return normalized == "error" || normalized == "failed" || normalized == "failure"
}

func isRetryMessage(message string) bool {
	return strings.Contains(strings.ToLower(message), "retry")
}

func isApprovalRequest(event TraceEvent) bool {
	if strings.Contains(strings.ToLower(event.Message), "approval") {
		return true
	}
	_, ok := event.Data["approval_request"]
	return ok
}

// connectionFailureBinding extracts the binding a connection error concerned,
// preferring structured trace data over parsing the message.
func connectionFailureBinding(event TraceEvent) string {
	if binding, ok := event.Data["binding_id"].(string); ok && strings.TrimSpace(binding) != "" {
		return strings.TrimSpace(binding)
	}
	if server, ok := event.Data["server"].(string); ok && strings.TrimSpace(server) != "" {
		return strings.TrimSpace(server)
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
