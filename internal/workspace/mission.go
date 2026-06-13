package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Context keys the mission bridge writes onto a synthetic mission task so the
// task handler can detect mission runs at the tool-call boundary and apply
// the autonomy gate. The bridge is in a different package (workspacerun) so
// these need to be exported strings, not unexported package-private values.
const (
	// MissionTaskContextOriginKey is set on Task.Context when the task is a
	// mission run. Presence + matching value is the runner's signal to apply
	// the autonomy gate.
	MissionTaskContextOriginKey   = "mission_origin"
	MissionTaskContextOriginValue = "workspace_mission"
	// MissionTaskContextPolicyKey carries the workspace's AutonomyPolicy at
	// the moment the run was created (as a string). Read at the gate to
	// decide which tool classes to allow.
	MissionTaskContextPolicyKey = "mission_autonomy_policy"
	// MissionTaskContextOrdinalKey carries the cycle ordinal (1 = baseline).
	// Useful for logging and prompt distinction; the gate itself does not
	// use it.
	MissionTaskContextOrdinalKey = "mission_cycle_ordinal"
)

// IsMissionTask reports whether a task's Context marks it as a mission run.
// Centralized so multiple call sites read the same signal in lockstep.
func IsMissionTask(ctx map[string]any) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx[MissionTaskContextOriginKey].(string)
	return v == MissionTaskContextOriginValue
}

// MissionAutonomyFromContext extracts the autonomy policy stored on a mission
// task's Context. Returns the empty policy if the task isn't a mission task or
// the value is missing/malformed; callers must treat empty as "deny everything"
// because the gate cannot make a decision without a policy.
func MissionAutonomyFromContext(ctx map[string]any) AutonomyPolicy {
	if ctx == nil {
		return ""
	}
	raw, _ := ctx[MissionTaskContextPolicyKey].(string)
	return AutonomyPolicy(raw)
}

// MissionPromptInputs gathers everything the mission-run system prompt builder
// needs from the workspace. Kept as a struct rather than positional args so
// future inputs (e.g. recent run summaries) can be added without churning
// every call site.
type MissionPromptInputs struct {
	// WorkspaceManagerPrompt is the existing Workspace Manager system prompt —
	// the entry agent's base personality and rules. Mission framing wraps
	// around it; we don't replace it.
	WorkspaceManagerPrompt string
	// Mission is the user-authored persistent goal text from Workspace.Mission.
	Mission string
	// CycleOrdinal is 1 for the first run (baseline), 2+ for subsequent runs.
	// Baseline runs get a "report on current state and strategy" framing;
	// recurring runs get a "compare against backlog, update findings" framing.
	CycleOrdinal int
	// OpenOpportunities is the slice of currently-open opportunities (new or
	// snoozed-but-due). Passed back into the run so the agent updates existing
	// records rather than rediscovering them every cycle.
	OpenOpportunities []Opportunity
	// TriggeringEvent is set when this run was started by an event trigger
	// (webhook or file watch) rather than cadence. Rendered as its own prompt
	// section so the agent knows why it was woken. nil for cadence/manual runs.
	TriggeringEvent *TriggerEventContext
}

// TriggerEventContext describes the external event that started a mission run.
// Lives in this package (not internal/trigger) because BuildMissionSystemPrompt
// renders it and internal/trigger already imports workspace — the reverse
// import would cycle.
type TriggerEventContext struct {
	// TriggerName is the user-given name of the trigger that fired.
	TriggerName string `json:"trigger_name"`
	// TriggerType is "webhook", "file_watch", or "test" (manual test-fire).
	TriggerType string `json:"trigger_type"`
	// FiredAt is when the (coalesced) fire decision was made.
	FiredAt time.Time `json:"fired_at"`
	// EventCount is how many raw events were coalesced into this fire (≥ 1).
	EventCount int `json:"event_count"`
	// Summary is a one-line human-readable description of the event(s),
	// e.g. `create: invoice-2026-06.pdf (+2 more)` or `POST 1.2 KB from 192.168.1.10`.
	Summary string `json:"summary"`
	// Payload carries the size-capped event detail: the webhook body, or the
	// list of file events. Already truncated/summarized by the trigger layer.
	Payload string `json:"payload,omitempty"`
}

// MissionRunOptions carries optional inputs for an event-initiated mission run.
// The zero value reproduces plain cadence semantics, so existing callers of
// TriggerMissionRun are unaffected.
type MissionRunOptions struct {
	// Event, when set, is injected into the mission system prompt.
	Event *TriggerEventContext
	// HoldCadence, when true, prevents the run outcome from advancing
	// NextMissionRunAt (the workspace's cadence-heartbeat setting).
	HoldCadence bool
}

// BuildMissionSystemPrompt composes the system prompt for a mission run.
//
// The composition layers:
//  1. The Workspace Manager's existing system prompt (its personality + rules).
//  2. Mission framing — what the agent is responsible for, what the cadence is.
//  3. Cycle-mode framing — baseline (first run) vs. recurring review.
//  4. The structured-output contract — what JSON shape findings must take.
//  5. The current opportunity backlog so the agent can update instead of duplicate.
//
// Layering rather than replacing keeps interactive chat with the same manager
// unaffected — that path doesn't call this function.
func BuildMissionSystemPrompt(in MissionPromptInputs) string {
	var b strings.Builder
	if strings.TrimSpace(in.WorkspaceManagerPrompt) != "" {
		b.WriteString(strings.TrimRight(in.WorkspaceManagerPrompt, "\n"))
		b.WriteString("\n\n")
	}

	b.WriteString("--- MISSION CONTEXT ---\n\n")
	if strings.TrimSpace(in.Mission) != "" {
		b.WriteString("Mission: ")
		b.WriteString(strings.TrimSpace(in.Mission))
		b.WriteString("\n\n")
	}

	if in.CycleOrdinal <= 1 {
		b.WriteString("This is the BASELINE run for this mission. Produce a concise current-state assessment of the workspace and identify the most impactful 1-5 opportunities to address. Do NOT take action on them — only report.\n\n")
	} else {
		b.WriteString(fmt.Sprintf("This is recurring mission run #%d. Compare current workspace state against the prior opportunity backlog. Update existing opportunities when the same issue persists, mark resolved when fixed, and add new ones only when they are genuinely new findings. Do NOT take action — only report.\n\n", in.CycleOrdinal))
	}

	if ev := in.TriggeringEvent; ev != nil {
		b.WriteString("--- TRIGGERING EVENT ---\n")
		b.WriteString(fmt.Sprintf("This run was started by the %q %s trigger (not the regular cadence).\n", ev.TriggerName, ev.TriggerType))
		if !ev.FiredAt.IsZero() {
			b.WriteString("Fired at: " + ev.FiredAt.Format(time.RFC3339) + "\n")
		}
		if ev.EventCount > 1 {
			b.WriteString(fmt.Sprintf("Coalesced events: %d\n", ev.EventCount))
		}
		if strings.TrimSpace(ev.Summary) != "" {
			b.WriteString("Event: " + strings.TrimSpace(ev.Summary) + "\n")
		}
		if strings.TrimSpace(ev.Payload) != "" {
			b.WriteString("Event detail:\n")
			b.WriteString(strings.TrimSpace(ev.Payload))
			b.WriteString("\n")
		}
		b.WriteString("Focus this run on what the event implies for the mission; fall back to a normal review only if the event turns out to be irrelevant.\n\n")
	}

	if len(in.OpenOpportunities) > 0 {
		b.WriteString("Existing open opportunities (use these titles verbatim when the same issue recurs so they merge correctly):\n")
		for _, o := range in.OpenOpportunities {
			if !o.IsOpen() {
				continue
			}
			b.WriteString(fmt.Sprintf("  - [%s] %s\n", o.Priority, o.Title))
		}
		b.WriteString("\n")
	}

	b.WriteString("Return a JSON object with this shape:\n")
	b.WriteString(`  {"findings": [{"title": string, "summary": string, "evidence": string, "priority": "low"|"medium"|"high"|"critical", "confidence": "low"|"medium"|"high", "recommended_action": string}]}` + "\n")
	b.WriteString("If you have no findings, return {\"findings\": []}.\n")

	return b.String()
}

// RunPolicyHints describes how an AutonomyPolicy translates into the
// workspacerun.Policy shape, without importing workspacerun (which would
// create a cycle — workspacerun already imports workspace). The bridge layer
// that creates runs from missions reads these hints and constructs the
// concrete workspacerun.Policy.
//
// Field values use the same string constants workspacerun exposes
// (PolicyMutation*, PolicyApproval*, PolicyExternalEffects*) so the bridge
// is a one-line lookup.
type RunPolicyHints struct {
	Mutation        string // "allowed" | "dry_run" | "denied"
	Approval        string // "none" | "final_only" | "per_tool"
	ExternalEffects string // "allowed" | "denied"
}

// AutonomyPolicyRunHints returns the RunPolicyHints that the bridge layer
// should apply when constructing a workspacerun.Run for a mission cycle.
//
// Watch  → mutation denied, approval none, external effects denied
// Propose → mutation allowed (workspace-internal), approval none, external denied
//
// Unknown autonomy values fall back to Watch semantics so a half-configured
// workspace cannot accidentally perform external work.
func AutonomyPolicyRunHints(p AutonomyPolicy) RunPolicyHints {
	switch p {
	case AutonomyPropose:
		return RunPolicyHints{Mutation: "allowed", Approval: "none", ExternalEffects: "denied"}
	}
	return RunPolicyHints{Mutation: "denied", Approval: "none", ExternalEffects: "denied"}
}

// missionFindings is the expected JSON shape returned by the agent during a
// mission run (see BuildMissionSystemPrompt). Kept private so callers can
// only get at the parsed Opportunity slice via ParseMissionOutput.
type missionFindings struct {
	Findings []missionFinding `json:"findings"`
}

type missionFinding struct {
	Title             string `json:"title"`
	Summary           string `json:"summary,omitempty"`
	Evidence          string `json:"evidence,omitempty"`
	Priority          string `json:"priority,omitempty"`
	Confidence        string `json:"confidence,omitempty"`
	RecommendedAction string `json:"recommended_action,omitempty"`
}

// ParseMissionOutput extracts findings from a mission run's structured output
// and converts them into Opportunity records. The caller is expected to feed
// these through OpportunityStore.Upsert so dedup-key merging is applied;
// this function does NOT touch persistence.
//
// raw is permissive — it accepts either the strict object form or a single
// JSON array of findings. Anything outside that contract is reported as an
// error so the run can be marked failed with a clear message.
func ParseMissionOutput(workspaceID, sourceRunID, raw string) ([]Opportunity, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	// Some models wrap JSON in fenced code blocks; strip a single fence pair
	// if present so we don't have to instruct the agent twice about format.
	trimmed = stripJSONFence(trimmed)

	var findings []missionFinding
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &findings); err != nil {
			return nil, fmt.Errorf("mission output: failed to parse findings array: %w", err)
		}
	} else {
		var wrapper missionFindings
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
			return nil, fmt.Errorf("mission output: failed to parse findings object: %w", err)
		}
		findings = wrapper.Findings
	}

	now := time.Now()
	opps := make([]Opportunity, 0, len(findings))
	for _, f := range findings {
		title := strings.TrimSpace(f.Title)
		if title == "" {
			// A finding without a title can't be deduped — skip rather than
			// flood the backlog with anonymous rows.
			continue
		}
		opps = append(opps, Opportunity{
			WorkspaceID:       workspaceID,
			SourceRunID:       sourceRunID,
			Title:             title,
			Summary:           strings.TrimSpace(f.Summary),
			Evidence:          strings.TrimSpace(f.Evidence),
			Priority:          strings.ToLower(strings.TrimSpace(f.Priority)),
			Confidence:        strings.ToLower(strings.TrimSpace(f.Confidence)),
			Status:            OpportunityNew,
			RecommendedAction: strings.TrimSpace(f.RecommendedAction),
			CreatedAt:         now,
			UpdatedAt:         now,
		})
	}
	return opps, nil
}

// stripJSONFence removes a leading and trailing ``` fence (optionally
// followed by "json") if present. Only strips one pair — if the agent
// returns a nested fence the inner one stays and parsing will surface
// the issue clearly.
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line.
	if newlineIdx := strings.Index(s, "\n"); newlineIdx != -1 {
		s = s[newlineIdx+1:]
	} else {
		// No newline after opening fence — malformed; return as-is.
		return s
	}
	// Drop a trailing fence.
	s = strings.TrimRight(s, " \n\t")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// MissionRunOutcome describes how a single mission run completed, used by
// ApplyMissionRunOutcome to update the workspace's mission tracking fields.
type MissionRunOutcome struct {
	StartedAt time.Time
	Succeeded bool
	// HoldCadence leaves NextMissionRunAt untouched: the run still counts
	// (LastMissionRunAt, counters) but does not push the cadence back. Set for
	// event-triggered runs when the workspace's MissionCadenceHeartbeat is on.
	HoldCadence bool
}

// ApplyMissionRunOutcome updates the workspace's mission tracking fields
// after a run. Mutates ws in place — callers are expected to have already
// loaded the workspace via Store.Update and to save the result.
//
// LastMissionRunAt is set unconditionally to the run's start time. The next
// run time is recomputed from the cadence when present; if no cadence is set
// it stays nil (manual-only). MissionExecutionCount always increments;
// MissionFailureCount increments only on failure so success rates can be
// derived as (Execution-Failure)/Execution.
func ApplyMissionRunOutcome(ws *Workspace, outcome MissionRunOutcome) {
	if ws == nil {
		return
	}
	started := outcome.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	ws.LastMissionRunAt = &started
	ws.MissionExecutionCount++
	if !outcome.Succeeded {
		ws.MissionFailureCount++
	}
	if outcome.HoldCadence {
		return
	}
	if ws.Cadence != nil {
		ws.NextMissionRunAt = CalculateNextRun(*ws.Cadence, started)
	} else {
		ws.NextMissionRunAt = nil
	}
}

// GateDecision captures the autonomy gate's verdict for a single tool call.
// Used to produce TraceEvent entries when a call is blocked.
type GateDecision struct {
	Allowed        bool
	Reason         string     // human-readable explanation; empty when Allowed
	Classification SideEffect // resolved SideEffect at decision time
	Policy         AutonomyPolicy
	ToolName       string
}

// EvaluateMissionToolCall is the centralized autonomy gate for mission runs.
// Returns a GateDecision the caller can both act on (allow/block the call)
// and convert into a trace entry when blocking.
//
// The function is intentionally pure — no logging, no trace writes. The
// caller decides how to surface the verdict so this is trivially unit-testable
// and the same function works in tests, the executor, and the readiness
// preview UI.
func EvaluateMissionToolCall(policy AutonomyPolicy, defaultSE SideEffect, overrides map[string]SideEffect, toolName string) GateDecision {
	// Native workspace-memory tools bypass binding classification entirely:
	// they are always workspace-internal writes and are allowed under every
	// policy, including Watch (see IsWorkspaceMemoryTool for the rationale).
	// Checked first so binding overrides cannot re-classify or block them.
	if IsWorkspaceMemoryTool(toolName) {
		return GateDecision{Allowed: true, Classification: SideEffectWrite, Policy: policy, ToolName: toolName}
	}
	resolved := ResolveSideEffect(defaultSE, overrides, toolName)
	dec := GateDecision{
		Classification: resolved,
		Policy:         policy,
		ToolName:       toolName,
	}
	if resolved == "" {
		dec.Reason = "tool is unclassified — classify the binding before enabling missions"
		return dec
	}
	if !IsAllowedUnderPolicy(policy, resolved) {
		dec.Reason = fmt.Sprintf("%s policy denies %s tools", policy, resolved)
		return dec
	}
	dec.Allowed = true
	return dec
}
