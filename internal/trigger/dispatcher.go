package trigger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// MissionRunner is the slice of the mission bridge the dispatcher needs.
// *workspacerun.MissionBridge satisfies it.
type MissionRunner interface {
	TriggerMissionRunOpts(ctx context.Context, workspaceID string, cycleOrdinal int, opts workspace.MissionRunOptions) (string, error)
}

// Task.Context keys set on tasks created by trigger fires, so the UI and
// debugging tools can attribute the task to its trigger.
const (
	TaskContextTriggerIDKey  = "trigger_id"
	TaskContextFireIDKey     = "trigger_fire_id"
	TaskContextEventKey      = "trigger_event"
	taskFromTrigger          = "system:trigger"
	defaultDispatchTimeout   = 30 * time.Minute
	triggerFindingPriority   = "medium"
	triggerFindingConfidence = "high"
)

// Dispatcher executes coalesced fires: mission_run through the mission
// bridge, task_prompt as a queued workspace task. It records every fire on
// the trigger's history and surfaces failures as Action Center findings
// (PRD #22–24).
type Dispatcher struct {
	store          *Store
	workspaceStore workspace.Store
	mission        MissionRunner              // nil when no bridge is wired (some test paths)
	opportunities  workspace.OpportunityStore // nil disables failure findings
	timeout        time.Duration
}

// NewDispatcher constructs a Dispatcher. mission and opportunities may be nil;
// the corresponding behaviors degrade gracefully (mission fires fail with a
// recorded error; findings are skipped).
func NewDispatcher(store *Store, wsStore workspace.Store, mission MissionRunner, opps workspace.OpportunityStore) *Dispatcher {
	return &Dispatcher{
		store:          store,
		workspaceStore: wsStore,
		mission:        mission,
		opportunities:  opps,
		timeout:        defaultDispatchTimeout,
	}
}

// Dispatch executes one fire for a trigger. Implements DispatchFunc.
func (d *Dispatcher) Dispatch(t Trigger, fire PendingFire) {
	firedAt := time.Now()
	evCtx := buildEventContext(t, fire, firedAt)

	rec := FireRecord{
		FireID:     fire.FireID,
		FiredAt:    firedAt,
		EventCount: fire.EventCount(),
		Summary:    evCtx.Summary,
	}

	switch t.Action.Kind {
	case ActionMissionRun:
		runID, err := d.fireMissionRun(t, evCtx)
		rec.RunID = runID
		if err != nil {
			rec.Error = err.Error()
		}
	case ActionTaskPrompt:
		taskID, err := d.fireTaskPrompt(t, fire, evCtx)
		rec.TaskID = taskID
		if err != nil {
			rec.Error = err.Error()
		}
	default:
		rec.Error = fmt.Sprintf("unknown action kind %q", t.Action.Kind)
	}

	if err := d.store.RecordFire(t.WorkspaceID, t.ID, rec); err != nil && err != ErrNotFound {
		logger.Warn("trigger dispatcher: record fire", logger.Fields{
			"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "error": err,
		})
	}

	if rec.Error != "" {
		logger.Warn("trigger fire failed", logger.Fields{
			"trigger_id": t.ID, "trigger_name": t.Name, "workspace_id": t.WorkspaceID, "error": rec.Error,
		})
		d.fileFailureFinding(t, rec.Error)
	} else {
		logger.Info("trigger fired", logger.Fields{
			"trigger_id": t.ID, "trigger_name": t.Name, "workspace_id": t.WorkspaceID,
			"run_id": rec.RunID, "task_id": rec.TaskID, "events": rec.EventCount,
		})
	}
}

// fireMissionRun starts the workspace mission with the event injected,
// mirroring the cadence path (same ordinal math, same bridge, PRD #4).
func (d *Dispatcher) fireMissionRun(t Trigger, evCtx *workspace.TriggerEventContext) (string, error) {
	if d.mission == nil {
		return "", fmt.Errorf("mission trigger not configured on this server")
	}
	ws, err := d.workspaceStore.Get(t.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("load workspace: %w", err)
	}
	if !ws.MissionEnabled {
		return "", fmt.Errorf("workspace mission is disabled; enable it or change the trigger action")
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	return d.mission.TriggerMissionRunOpts(ctx, t.WorkspaceID, ws.MissionExecutionCount+1, workspace.MissionRunOptions{
		Event:       evCtx,
		HoldCadence: ws.MissionCadenceHeartbeat,
	})
}

// fireTaskPrompt creates a workspace task from the trigger's stored prompt
// and queues it for the task executor (status assigned → picked up on the
// next executor poll).
func (d *Dispatcher) fireTaskPrompt(t Trigger, fire PendingFire, evCtx *workspace.TriggerEventContext) (string, error) {
	// Assign the ID up front so we report the right one regardless of where
	// AddTask inserts the task (it preserves a non-empty ID). Avoids relying
	// on positional lookup of the "last" task, which breaks under any future
	// insertion-order change.
	taskID := "trg-task-" + uuid.NewString()
	task := workspace.Task{
		ID:          taskID,
		From:        taskFromTrigger,
		To:          t.Action.Agent,
		Description: buildTaskDescription(t.Action.Prompt, evCtx),
		Priority:    t.Action.Priority,
		Status:      workspace.TaskStatusAssigned,
		Context: map[string]any{
			TaskContextTriggerIDKey: t.ID,
			TaskContextFireIDKey:    fire.FireID,
			TaskContextEventKey:     evCtx.Summary,
		},
		CreatedAt: time.Now(),
	}

	if err := d.workspaceStore.Update(t.WorkspaceID, func(ws *workspace.Workspace) error {
		return ws.AddTask(task)
	}); err != nil {
		return "", fmt.Errorf("create task from trigger: %w", err)
	}
	return taskID, nil
}

// fileFailureFinding surfaces a fire failure in the Action Center (PRD #24).
// The title is stable per trigger so repeat failures dedup-merge into one
// finding instead of flooding the inbox.
func (d *Dispatcher) fileFailureFinding(t Trigger, errMsg string) {
	if d.opportunities == nil {
		return
	}
	now := time.Now()
	_, _, err := d.opportunities.Upsert(workspace.Opportunity{
		WorkspaceID:       t.WorkspaceID,
		Title:             fmt.Sprintf("Event trigger %q is failing", t.Name),
		Summary:           fmt.Sprintf("The %s trigger %q could not complete a fire.", t.Type, t.Name),
		Evidence:          errMsg,
		Priority:          triggerFindingPriority,
		Confidence:        triggerFindingConfidence,
		Status:            workspace.OpportunityNew,
		RecommendedAction: "Open the workspace's Triggers section to inspect the fire history and fix the trigger configuration.",
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		logger.Warn("trigger dispatcher: file failure finding", logger.Fields{
			"trigger_id": t.ID, "workspace_id": t.WorkspaceID, "error": err,
		})
	}
}

// buildEventContext renders a fire into the prompt-facing event context.
func buildEventContext(t Trigger, fire PendingFire, firedAt time.Time) *workspace.TriggerEventContext {
	triggerType := string(t.Type)
	if len(fire.Events) > 0 && fire.Events[0].Kind == "test" {
		triggerType = "test"
	}
	return &workspace.TriggerEventContext{
		TriggerName: t.Name,
		TriggerType: triggerType,
		FiredAt:     firedAt,
		EventCount:  fire.EventCount(),
		Summary:     Summary(fire.Events, fire.DroppedEvents),
		Payload:     renderPayload(fire),
	}
}

// renderPayload formats a fire's events as prompt-ready text, bounded by
// MaxPayloadBytes (PRD #7).
func renderPayload(fire PendingFire) string {
	var b strings.Builder
	for _, ev := range fire.Events {
		if b.Len() >= MaxPayloadBytes {
			break
		}
		switch ev.Kind {
		case "webhook":
			fmt.Fprintf(&b, "- webhook POST (%s) from %s at %s\n", ev.ContentType, ev.RemoteAddr, ev.Timestamp.Format(time.RFC3339))
			if ev.Body != "" {
				body := ev.Body
				if remaining := MaxPayloadBytes - b.Len(); len(body) > remaining {
					body = body[:remaining]
					ev.Truncated = true
				}
				b.WriteString("  body: ")
				b.WriteString(body)
				b.WriteString("\n")
			}
			if ev.Truncated {
				b.WriteString("  [payload truncated]\n")
			}
		case "file":
			fmt.Fprintf(&b, "- file %s: %s at %s\n", ev.FileEvent, ev.FilePath, ev.Timestamp.Format(time.RFC3339))
		case "test":
			fmt.Fprintf(&b, "- manual test fire at %s\n", ev.Timestamp.Format(time.RFC3339))
		}
	}
	if fire.DroppedEvents > 0 {
		fmt.Fprintf(&b, "- [+%d more events omitted]\n", fire.DroppedEvents)
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildTaskDescription appends the structured event block to the trigger's
// stored prompt (PRD #6).
func buildTaskDescription(prompt string, evCtx *workspace.TriggerEventContext) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n--- TRIGGERING EVENT ---\n")
	fmt.Fprintf(&b, "Trigger: %s (%s)\n", evCtx.TriggerName, evCtx.TriggerType)
	fmt.Fprintf(&b, "Fired at: %s\n", evCtx.FiredAt.Format(time.RFC3339))
	if evCtx.EventCount > 1 {
		fmt.Fprintf(&b, "Coalesced events: %d\n", evCtx.EventCount)
	}
	fmt.Fprintf(&b, "Event: %s\n", evCtx.Summary)
	if evCtx.Payload != "" {
		b.WriteString("Event detail:\n")
		b.WriteString(evCtx.Payload)
		b.WriteString("\n")
	}
	return b.String()
}
