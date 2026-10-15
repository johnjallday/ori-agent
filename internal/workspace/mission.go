package workspace

import (
	"context"
	"fmt"
	"sort"
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
	// AutonomyPolicy decides what the run may do with its workspace tools
	// while it looks; it never changes how suggestions are stored.
	AutonomyPolicy AutonomyPolicy
	// AgentNames are the workspace's agents, the candidates for a
	// suggestion's suggested_agent.
	AgentNames []string
	// OpenWork are the workspace's unfinished tickets (Backlog, Ready and
	// active), listed with their state so the model does not suggest them
	// again and can see what is already planned (FR12, FR18).
	OpenWork []MissionWorkItem
	// RecentlyCompleted are the last few finished tasks with a one-line
	// result, so suggestions build on what was done (FR12).
	RecentlyCompleted []MissionWorkItem
	// DismissedTitles are suggestions the user marked Not useful (FR20).
	DismissedTitles []string
}

// MissionWorkItem is one ticket as a goal run sees it.
type MissionWorkItem struct {
	Title     string
	State     string
	Suggested bool
	Result    string
}

// missionRecentlyCompletedLimit is how many finished tasks a goal run sees.
const missionRecentlyCompletedLimit = 5

// missionResultLineLimit bounds a finished task's one-line result.
const missionResultLineLimit = 160

// MissionWorkSnapshot lists a workspace's unfinished top-level tickets and its
// most recently finished tasks for the goal-run prompt.
func MissionWorkSnapshot(ws *Workspace) (open, completed []MissionWorkItem) {
	if ws == nil {
		return nil, nil
	}
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	var done []Task
	for _, task := range ws.Tasks {
		if task.ParentTaskID != "" {
			continue
		}
		state := task.CanonicalState()
		switch state {
		case TicketStateCancelled:
			continue
		case TicketStateDone:
			done = append(done, task)
			continue
		}
		open = append(open, MissionWorkItem{
			Title: task.Description, State: string(state),
			Suggested: task.SourceType == TicketSourceGoalSuggestion,
		})
	}
	sort.SliceStable(done, func(i, j int) bool { return missionFinishedAt(done[i]).After(missionFinishedAt(done[j])) })
	if len(done) > missionRecentlyCompletedLimit {
		done = done[:missionRecentlyCompletedLimit]
	}
	for _, task := range done {
		completed = append(completed, MissionWorkItem{
			Title: task.Description, State: string(TicketStateDone),
			Result: sanitizeTaskPromptText(task.Result, missionResultLineLimit),
		})
	}
	return open, completed
}

func missionFinishedAt(task Task) time.Time {
	if task.CompletedAt != nil {
		return *task.CompletedAt
	}
	return task.UpdatedAt
}

// missionPromptListLimit bounds each title list in the mission prompt.
const missionPromptListLimit = 50

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
	// Origin names what started the run for its run record (MissionOrigin*).
	// Empty means cadence, or event when Event is set.
	Origin string
}

// Goal-run origins recorded on MissionRunRecord.Origin.
const (
	MissionOriginCadence   = "cadence"
	MissionOriginManual    = "manual"
	MissionOriginFirstGoal = "first_goal"
	MissionOriginEvent     = "event"
)

// MissionOptionsTrigger is a MissionTrigger that also takes run options. The
// scheduler uses it, when available, to label manual runs in run history.
type MissionOptionsTrigger interface {
	TriggerMissionRunOpts(ctx context.Context, workspaceID string, cycleOrdinal int, opts MissionRunOptions) (string, error)
}

// BuildMissionSystemPrompt composes the prompt for a goal (mission) run.
//
// The composition layers:
//  1. The Workspace Manager's existing system prompt (its personality + rules).
//  2. The goal, and the run's framing — first run vs. a later review.
//  3. What the run may do while it looks, by autonomy level (FR19).
//  4. Work that already exists or was dismissed, so it is not suggested again.
//  5. Open findings, so recurring ones merge instead of duplicating.
//  6. The output contract: {"items": [...]} of tasks and findings (FR14).
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
		b.WriteString("Goal: ")
		b.WriteString(strings.TrimSpace(in.Mission))
		b.WriteString("\n\n")
	}

	if in.CycleOrdinal <= 1 {
		b.WriteString("This is the first goal run for this workspace (the BASELINE run). Look at what the workspace holds, then suggest the few pieces of work that would move the goal forward most, and note anything the user should know.\n\n")
	} else {
		b.WriteString(fmt.Sprintf("This is goal run #%d. Compare the workspace with the goal and with the work and findings listed below; suggest only work that is genuinely new, and keep a recurring finding's title so it merges.\n\n", in.CycleOrdinal))
	}

	switch in.AutonomyPolicy {
	case AutonomyPropose:
		b.WriteString("While you look you may read this workspace and write notes or drafts inside it with your workspace tools. Do not change anything outside this workspace, and do not start, assign or complete any task: your suggestions reach the user's Backlog after you answer.\n\n")
	default:
		b.WriteString("While you look you may read this workspace with your tools, but do not change anything: no notes, drafts, tasks or files. Your suggestions reach the user's Backlog after you answer.\n\n")
	}

	if names := missionPromptList(in.AgentNames); len(names) > 0 {
		b.WriteString("Agents in this workspace (use one of these names for suggested_agent, or leave it empty): ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n\n")
	}
	if len(in.OpenWork) > 0 {
		b.WriteString("Work that already exists in this workspace (do not suggest it again):\n")
		for i, item := range in.OpenWork {
			if i >= missionPromptListLimit {
				break
			}
			marker := ""
			if item.Suggested {
				marker = ", suggested"
			}
			fmt.Fprintf(&b, "  - [%s%s] %s\n", item.State, marker, missionPromptLine(item.Title))
		}
		b.WriteString("\n")
	}
	if len(in.RecentlyCompleted) > 0 {
		b.WriteString("Recently finished work (build on it; do not repeat it):\n")
		for _, item := range in.RecentlyCompleted {
			line := "  - " + missionPromptLine(item.Title)
			if item.Result != "" {
				line += " — " + item.Result
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if titles := missionPromptList(in.DismissedTitles); len(titles) > 0 {
		b.WriteString("Suggestions the user dismissed as not useful (never suggest them again):\n")
		for _, title := range titles {
			b.WriteString("  - " + title + "\n")
		}
		b.WriteString("\n")
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
		b.WriteString("Open findings (use these titles verbatim when the same issue recurs so they merge correctly):\n")
		for _, o := range in.OpenOpportunities {
			if !o.IsOpen() {
				continue
			}
			b.WriteString(fmt.Sprintf("  - [%s] %s\n", o.Priority, o.Title))
		}
		b.WriteString("\n")
	}

	b.WriteString("--- OUTPUT ---\n\n")
	b.WriteString("Answer with ONE JSON object and nothing else, no prose before or after it:\n")
	b.WriteString(`  {"items": [ ... ]}` + "\n")
	b.WriteString("Every item has a \"kind\":\n")
	b.WriteString("- \"task\": work that moves the goal forward and that an agent in this workspace could do.\n")
	b.WriteString(`    {"kind": "task", "title": string, "why": string, "evidence": string, "priority": "low"|"medium"|"high"|"critical", "confidence": "low"|"medium"|"high", "effort": "small"|"medium"|"large", "suggested_agent": string, "first_step": string}` + "\n")
	b.WriteString("- \"finding\": something the user should know that is not itself a task, such as a risk, an anomaly or a decision to make.\n")
	b.WriteString(`    {"kind": "finding", "title": string, "why": string, "evidence": string, "priority": "low"|"medium"|"high"|"critical", "confidence": "low"|"medium"|"high", "recommended_action": string}` + "\n")
	fmt.Fprintf(&b, "Suggest at most %d tasks and %d findings. Ground each item in evidence from this workspace and give a short title that says what to do.\n", MissionMaxTaskSuggestions, MissionMaxFindings)
	b.WriteString("If nothing is worth suggesting, answer {\"items\": []}.\n")

	return b.String()
}

// missionPromptLine flattens a title to one line.
func missionPromptLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// missionPromptList trims, drops empties and bounds a title list.
func missionPromptList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= missionPromptListLimit {
			break
		}
	}
	return out
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
	if native, ok := NativeToolSideEffect(toolName); ok {
		resolved = native
	}
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
