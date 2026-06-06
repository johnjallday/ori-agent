package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"
)

// DelegationCaps bounds the adaptive delegation loop so it can never run away.
// The default values are a conservative starting point; final tuning is tracked
// as an open question in the PRD.
type DelegationCaps struct {
	MaxIterations int           // coordinator adapt iterations per failed task
	MaxSubtasks   int           // total delegated subtasks per failed task
	Timeout       time.Duration // wall-clock budget for the whole loop
}

// DefaultDelegationCaps returns conservative defaults for the delegation loop.
func DefaultDelegationCaps() DelegationCaps {
	return DelegationCaps{MaxIterations: 3, MaxSubtasks: 8, Timeout: 10 * time.Minute}
}

func (c DelegationCaps) normalized() DelegationCaps {
	d := DefaultDelegationCaps()
	if c.MaxIterations <= 0 {
		c.MaxIterations = d.MaxIterations
	}
	if c.MaxSubtasks <= 0 {
		c.MaxSubtasks = d.MaxSubtasks
	}
	if c.Timeout <= 0 {
		c.Timeout = d.Timeout
	}
	return c
}

// CoordinatorAdaptRequest is the input to one coordinator reasoning step.
type CoordinatorAdaptRequest struct {
	WorkspaceID   string
	Coordinator   string
	FailedTask    Task
	Trigger       DelegationTrigger
	Iteration     int               // 1-based attempt number within the loop
	MaxIterations int               // attempt budget, so the coordinator knows how many remain
	PriorResults  map[string]string // delegated subtask id -> result text
}

// CoordinatorAdaptResult is the output of one coordinator reasoning step.
type CoordinatorAdaptResult struct {
	DelegatedTaskIDs []string // subtasks the coordinator created this step
	DirectResult     string   // set when the coordinator did the work itself
	Resolved         bool     // coordinator considers the failed task resolved

	// NeedsInput requests a pause-to-ask: the coordinator is unsure how to
	// proceed (low confidence / no viable specialist) and wants the user.
	NeedsInput       bool
	Question         string
	SuggestedActions []string
}

// CoordinatorAdapter is the LLM-reasoning seam of the delegation loop: it runs
// the coordinator (entry agent) with the delegate_task tool and reports what it
// did. It is injected so the loop's control flow is testable without an LLM.
type CoordinatorAdapter interface {
	Adapt(ctx context.Context, req CoordinatorAdaptRequest) (CoordinatorAdaptResult, error)
}

// DelegationLoop drives "adapt on failure": it asks the coordinator to adapt,
// executes any delegated subtasks, and repeats until the coordinator resolves
// the task or a cap is hit. Single-level is enforced upstream (only the
// coordinator holds delegate_task), so the subtasks executed here are leaves.
type DelegationLoop struct {
	store     Store
	executor  taskExecutor
	adapter   CoordinatorAdapter
	caps      DelegationCaps
	eventBus  *EventBus
	telemetry DelegationTelemetry
}

// DelegationTelemetry records per-delegation telemetry. utilitytelemetry.Tracker
// satisfies it; it is optional and injected so the workspace package does not
// depend on the telemetry package.
type DelegationTelemetry interface {
	RecordDelegationEvent(mode, reason, target string)
}

// NewDelegationLoop builds a loop. caps is normalized so zero values fall back to
// DefaultDelegationCaps.
func NewDelegationLoop(store Store, executor taskExecutor, adapter CoordinatorAdapter, caps DelegationCaps) *DelegationLoop {
	return &DelegationLoop{store: store, executor: executor, adapter: adapter, caps: caps.normalized()}
}

// SetEventBus wires delegation lifecycle events (delegation.*).
func (l *DelegationLoop) SetEventBus(bus *EventBus) { l.eventBus = bus }

// SetTelemetry wires per-delegation telemetry recording.
func (l *DelegationLoop) SetTelemetry(t DelegationTelemetry) { l.telemetry = t }

func (l *DelegationLoop) emit(eventType EventType, workspaceID, parentTaskID, coordinator string, data map[string]any) {
	if l.eventBus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["parent_task_id"] = parentTaskID
	data["coordinator"] = coordinator
	l.eventBus.Publish(NewTaskEvent(eventType, workspaceID, parentTaskID, coordinator, data))
}

// DelegationLoopResult reports the loop outcome on success.
type DelegationLoopResult struct {
	Resolved     bool
	Result       string
	Iterations   int
	SubtaskCount int
}

// Run executes the adaptive loop for a failed/blocked task. When a cap is hit it
// returns a *TaskBlockedError so the failure surfaces to the user (or, later, a
// pause-to-ask) rather than looping forever.
func (l *DelegationLoop) Run(ctx context.Context, workspaceID string, failed Task, trigger DelegationTrigger) (DelegationLoopResult, error) {
	if l == nil || l.adapter == nil {
		return DelegationLoopResult{}, fmt.Errorf("delegation loop: no coordinator adapter configured")
	}
	if l.executor == nil {
		return DelegationLoopResult{}, fmt.Errorf("delegation loop: no task executor configured")
	}

	coordinator, err := l.resolveCoordinator(workspaceID)
	if err != nil {
		return DelegationLoopResult{}, err
	}

	l.emit(EventDelegationStarted, workspaceID, failed.ID, coordinator, map[string]any{
		"trigger_code":   trigger.Code,
		"trigger_reason": trigger.Reason,
	})

	ctx, cancel := context.WithTimeout(ctx, l.caps.Timeout)
	defer cancel()

	results := map[string]string{}
	var order []string
	subtaskCount := 0

	for iter := 1; iter <= l.caps.MaxIterations; iter++ {
		if ctx.Err() != nil {
			l.emit(EventDelegationCapHit, workspaceID, failed.ID, coordinator, map[string]any{"cap": "timeout"})
			return DelegationLoopResult{Iterations: iter - 1, SubtaskCount: subtaskCount},
				l.capHit("delegation timed out", failed, trigger)
		}

		adapt, aerr := l.adapter.Adapt(ctx, CoordinatorAdaptRequest{
			WorkspaceID:   workspaceID,
			Coordinator:   coordinator,
			FailedTask:    failed,
			Trigger:       trigger,
			Iteration:     iter,
			MaxIterations: l.caps.MaxIterations,
			PriorResults:  cloneStringMap(results),
		})
		if aerr != nil {
			l.emit(EventDelegationFailed, workspaceID, failed.ID, coordinator, map[string]any{"error": aerr.Error()})
			return DelegationLoopResult{Iterations: iter, SubtaskCount: subtaskCount}, aerr
		}

		// The coordinator asked for input. Surface a blocked error; the caller
		// turns it into a pause-to-ask when interactive, or a failure when not.
		if adapt.NeedsInput {
			return DelegationLoopResult{Iterations: iter, SubtaskCount: subtaskCount},
				l.needsInputBlock(adapt, failed)
		}

		for _, id := range adapt.DelegatedTaskIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			subtaskCount++
			if subtaskCount > l.caps.MaxSubtasks {
				return DelegationLoopResult{Iterations: iter, SubtaskCount: subtaskCount - 1},
					l.capHit("exceeded the maximum number of delegated subtasks", failed, trigger)
			}
			order = append(order, id)
			if res, eerr := l.executeSubtask(ctx, workspaceID, id); eerr != nil {
				results[id] = "error: " + eerr.Error()
			} else {
				results[id] = res
			}
		}

		if adapt.Resolved {
			finalResult := combineLoopResult(adapt.DirectResult, failed.ResultCombinationMode, order, results)
			l.emit(EventDelegationCompleted, workspaceID, failed.ID, coordinator, map[string]any{
				"iterations":   iter,
				"subtasks":     subtaskCount,
				"output_valid": delegationResultHonorsSpec(failed, finalResult),
			})
			return DelegationLoopResult{
				Resolved:     true,
				Result:       finalResult,
				Iterations:   iter,
				SubtaskCount: subtaskCount,
			}, nil
		}
	}

	l.emit(EventDelegationCapHit, workspaceID, failed.ID, coordinator, map[string]any{"cap": "max_iterations"})
	return DelegationLoopResult{Iterations: l.caps.MaxIterations, SubtaskCount: subtaskCount},
		l.capHit("exceeded the maximum number of delegation iterations", failed, trigger)
}

func (l *DelegationLoop) resolveCoordinator(workspaceID string) (string, error) {
	ws, err := l.store.Get(workspaceID)
	if err != nil {
		return "", fmt.Errorf("delegation loop: workspace not found: %w", err)
	}
	name, source := ws.ResolveCoordinator()
	if source == CoordinatorSourceMissing {
		return "", ErrCoordinatorMissing
	}
	return name, nil
}

func (l *DelegationLoop) executeSubtask(ctx context.Context, workspaceID, taskID string) (string, error) {
	ws, err := l.store.Get(workspaceID)
	if err != nil {
		return "", err
	}
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			sub := ws.Tasks[i]
			if l.telemetry != nil {
				l.telemetry.RecordDelegationEvent(
					string(TaskAssignmentModeDynamicDelegation), "delegated subtask", sub.To)
			}
			if l.eventBus != nil {
				l.eventBus.Publish(NewTaskEvent(EventTaskAssigned, workspaceID, sub.ID, sub.To, map[string]any{
					"delegated_task_id": sub.ID,
					"target_agent":      sub.To,
					"assigned_by":       sub.AssignedBy,
					"assignment_mode":   string(sub.AssignmentMode),
					"assignment_reason": sub.AssignmentReason,
					"parent_task_id":    sub.ParentTaskID,
				}))
			}
			return l.executor.ExecuteTask(ctx, sub.To, sub)
		}
	}
	return "", fmt.Errorf("delegated subtask %s not found", taskID)
}

// capHit builds the terminal blocked error when the loop is stopped by a cap.
// Phase 5 layers pause-to-ask on top of this same signal.
func (l *DelegationLoop) capHit(reason string, failed Task, trigger DelegationTrigger) error {
	question := fmt.Sprintf(
		"The coordinator could not resolve %q after adapting (%s). Review and retry, adjust the task, or assign it manually.",
		strings.TrimSpace(failed.Description), strings.TrimSpace(trigger.Reason),
	)
	return &TaskBlockedError{
		ReasonCode:       "delegation_cap_exceeded",
		Reason:           reason,
		Question:         question,
		SuggestedActions: []string{"switch_agent_retry", "mark_failed"},
	}
}

// needsInputBlock builds the blocked error for a coordinator pause request,
// reusing the coordinator's question/actions when provided.
func (l *DelegationLoop) needsInputBlock(adapt CoordinatorAdaptResult, failed Task) error {
	question := strings.TrimSpace(adapt.Question)
	if question == "" {
		question = fmt.Sprintf(
			"The coordinator needs your input to continue %q. Provide guidance, or assign the task manually.",
			strings.TrimSpace(failed.Description),
		)
	}
	actions := adapt.SuggestedActions
	if len(actions) == 0 {
		actions = []string{"switch_agent_retry", "mark_failed"}
	}
	return &TaskBlockedError{
		ReasonCode:       "delegation_needs_input",
		Reason:           "the coordinator needs input to proceed",
		Question:         question,
		SuggestedActions: actions,
	}
}

// combineLoopResult produces the parent task's final result. The coordinator's
// own answer is the primary synthesis (it saw the subtask results and
// reinterpreted them, honoring the parent's output spec); only when it resolves
// without an answer do we fall back to combining subtask outputs by the parent's
// ResultCombinationMode.
// delegationResultHonorsSpec reports whether the synthesized result satisfies the
// parent's output spec/contract (true when there is no spec to honor). It reuses
// the trigger's validator so "honoring the output spec" (FR32) is observable on
// the delegation.completed event without failing the loop on a fallback combine.
func delegationResultHonorsSpec(parent Task, result string) bool {
	_, invalid := classifyOutputValidation(parent, result)
	return !invalid
}

func combineLoopResult(direct string, mode TaskResultCombinationMode, order []string, results map[string]string) string {
	if d := strings.TrimSpace(direct); d != "" {
		return d
	}
	switch mode {
	case TaskResultCombinationLastResult:
		for i := len(order) - 1; i >= 0; i-- {
			if r := strings.TrimSpace(results[order[i]]); r != "" {
				return r
			}
		}
		return ""
	case TaskResultCombinationJSONMap:
		m := make(map[string]string, len(order))
		for _, id := range order {
			m[id] = results[id]
		}
		if b, err := json.Marshal(m); err == nil {
			return string(b)
		}
		fallthrough
	default: // concat; structured_outputs relies on the coordinator's DirectResult
		var b strings.Builder
		for _, id := range order {
			res := strings.TrimSpace(results[id])
			if res == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(res)
		}
		return b.String()
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
