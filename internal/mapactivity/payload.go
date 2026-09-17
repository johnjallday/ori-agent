package mapactivity

import (
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Kind names what sort of work an activity is (task-run-show FR4).
type Kind string

const (
	KindTask        Kind = "task"
	KindDailyBrief  Kind = "daily_brief"
	KindFileJanitor Kind = "file_janitor"
)

// Phase is where an activity is in its life (FR4).
type Phase string

const (
	PhaseStarted  Phase = "started"
	PhaseStep     Phase = "step"
	PhaseBlocked  Phase = "blocked"
	PhaseResumed  Phase = "resumed"
	PhaseFinished Phase = "finished"
)

// Outcome is how a finished activity ended. Empty means the tracker never saw
// an ending: the run went silent and was dropped, or it was cancelled or
// deleted. An empty outcome never produces a parcel (FR6).
type Outcome string

const (
	OutcomeNone      Outcome = ""
	OutcomeSucceeded Outcome = "succeeded"
	OutcomePartial   Outcome = "partial"
	OutcomeFailed    Outcome = "failed"
	OutcomeTimeout   Outcome = "timeout"
)

// StepType is the kind of step inside a running activity (FR4).
type StepType string

const (
	StepThinking   StepType = "thinking"
	StepToolCall   StepType = "tool_call"
	StepToolResult StepType = "tool_result"
	StepDelegation StepType = "delegation"
)

// Step is one step inside an activity. It carries the tool's NAME and whether a
// tool call worked — never its arguments or its output (FR61).
type Step struct {
	Type     StepType `json:"type"`
	ToolName string   `json:"tool_name,omitempty"`
	OK       *bool    `json:"ok,omitempty"`
}

// ActivityEvent is one `activity` message on the stream (FR4).
//
// Every field is built from an allowlist in this file. Nothing here is ever
// copied wholesale from a bus event's Data, which carries tool arguments, task
// descriptions, full results and error text (FR61).
type ActivityEvent struct {
	Kind        Kind      `json:"kind"`
	Phase       Phase     `json:"phase"`
	ActivityID  string    `json:"activity_id"`
	WorkspaceID string    `json:"workspace_id"`
	AgentName   string    `json:"agent_name"`
	TaskID      string    `json:"task_id,omitempty"`
	Step        *Step     `json:"step,omitempty"`
	Outcome     Outcome   `json:"outcome"`
	ParcelID    string    `json:"parcel_id"`
	Count       *int      `json:"count,omitempty"`
	At          time.Time `json:"at"`
}

// RunningActivity is one entry in the snapshot's `running` list: enough for a
// freshly loaded map to light the building and show the latest step.
type RunningActivity struct {
	Kind        Kind      `json:"kind"`
	ActivityID  string    `json:"activity_id"`
	WorkspaceID string    `json:"workspace_id"`
	AgentName   string    `json:"agent_name"`
	TaskID      string    `json:"task_id,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Blocked     bool      `json:"blocked"`
	Step        *Step     `json:"step,omitempty"`
	At          time.Time `json:"at"`

	lastSeen time.Time // tracker clock; heartbeats move it, nothing else sees it
}

// ParcelSummary is one entry in the snapshot's `parcels` list. It is the pile's
// row, not the result card: the card's summary, failure reason and rewards are
// served only by the open endpoints (FR37, FR62).
type ParcelSummary struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Kind        Kind      `json:"kind"`
	Title       string    `json:"title"`
	AgentName   string    `json:"agent_name"`
	Outcome     Outcome   `json:"outcome"`
	ProducedAt  time.Time `json:"produced_at"`
}

// Snapshot is the body of GET /api/workspace-map/activity and of the stream's
// first `snapshot` message (FR2).
type Snapshot struct {
	Running []RunningActivity `json:"running"`
	Parcels []ParcelSummary   `json:"parcels"`
}

// StreamMessage is one message fanned out to stream subscribers. Name is the SSE
// event name (`activity` or `parcel`); Payload is marshalled as its data.
type StreamMessage struct {
	Name    string
	Payload any
}

// TaskActivityID is the activity id of a task run (FR4).
func TaskActivityID(workspaceID, taskID string) string {
	return "task:" + workspaceID + ":" + taskID
}

// taskPhaseFor maps a bus event type to an activity phase (FR5). The second
// result is false for event types that are not part of an activity.
func taskPhaseFor(eventType workspace.EventType) (Phase, bool) {
	switch eventType {
	case workspace.EventTaskStarted:
		return PhaseStarted, true
	case workspace.EventTaskThinking, workspace.EventTaskToolCall, workspace.EventTaskToolResult, workspace.EventDelegationStarted:
		return PhaseStep, true
	case workspace.EventTaskBlocked:
		return PhaseBlocked, true
	case workspace.EventTaskResumed:
		return PhaseResumed, true
	case workspace.EventTaskCompleted, workspace.EventTaskFailed, workspace.EventTaskTimeout:
		return PhaseFinished, true
	}
	return "", false
}

// taskOutcomeFor is the outcome a finishing task event reports.
func taskOutcomeFor(eventType workspace.EventType) Outcome {
	switch eventType {
	case workspace.EventTaskCompleted:
		return OutcomeSucceeded
	case workspace.EventTaskFailed:
		return OutcomeFailed
	case workspace.EventTaskTimeout:
		return OutcomeTimeout
	}
	return OutcomeNone
}

// stepFor builds the allowlisted step of a step event: its type, the tool's
// name, and for a tool result whether it worked.
func stepFor(ev workspace.Event) *Step {
	switch ev.Type {
	case workspace.EventTaskThinking:
		return &Step{Type: StepThinking}
	case workspace.EventTaskToolCall:
		return &Step{Type: StepToolCall, ToolName: stringField(ev.Data, "tool_name")}
	case workspace.EventTaskToolResult:
		step := &Step{Type: StepToolResult, ToolName: stringField(ev.Data, "tool_name")}
		if ok, present := ev.Data["success"].(bool); present {
			step.OK = &ok
		}
		return step
	case workspace.EventDelegationStarted:
		return &Step{Type: StepDelegation}
	}
	return nil
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func cloneStep(step *Step) *Step {
	if step == nil {
		return nil
	}
	out := *step
	if step.OK != nil {
		ok := *step.OK
		out.OK = &ok
	}
	return &out
}
