package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// llmCoordinatorAdapter is the concrete CoordinatorAdapter: it runs the workspace
// coordinator (entry agent) with the delegate_task tool to adapt a failed task.
// It detects the subtasks the coordinator created during its run via a
// before/after snapshot of task IDs, which is robust to prompt wording and does
// not depend on the coordinator setting parent links correctly.
type llmCoordinatorAdapter struct {
	store    Store
	executor taskExecutor
}

// NewCoordinatorAdapter builds the concrete adapter used by the delegation loop.
func NewCoordinatorAdapter(store Store, executor taskExecutor) CoordinatorAdapter {
	return &llmCoordinatorAdapter{store: store, executor: executor}
}

func (a *llmCoordinatorAdapter) Adapt(ctx context.Context, req CoordinatorAdaptRequest) (CoordinatorAdaptResult, error) {
	if a.executor == nil {
		return CoordinatorAdaptResult{}, fmt.Errorf("coordinator adapter: no task executor")
	}

	before, err := a.taskIDSet(req.WorkspaceID)
	if err != nil {
		return CoordinatorAdaptResult{}, err
	}

	// The synthetic coordinator task is executed but not persisted; its assignee
	// is the coordinator, so the execution tool factory exposes delegate_task.
	synthetic := Task{
		ID:          "coordinator-adapt-" + uuid.NewString(),
		WorkspaceID: req.WorkspaceID,
		From:        "delegation_loop",
		To:          req.Coordinator,
		Description: buildCoordinatorAdaptPrompt(req),
		Status:      TaskStatusInProgress,
		Context:     map[string]any{"delegation_adapt": true, "failed_task_id": req.FailedTask.ID},
	}

	result, runErr := a.executor.ExecuteTask(ctx, req.Coordinator, synthetic)

	delegated, err := a.newTaskIDsInOrder(req.WorkspaceID, before)
	if err != nil {
		return CoordinatorAdaptResult{}, err
	}

	if len(delegated) == 0 {
		// The coordinator delegated nothing this step: it either did the work
		// itself (result) or could not proceed. With no result and a run error,
		// surface the error so the loop records a failure rather than resolving
		// with an empty answer.
		trimmed := strings.TrimSpace(result)
		if trimmed == "" && runErr != nil {
			return CoordinatorAdaptResult{}, fmt.Errorf("coordinator adaptation failed: %w", runErr)
		}
		return CoordinatorAdaptResult{DirectResult: trimmed, Resolved: true}, nil
	}

	// Subtasks were created; the loop executes them and calls back with results.
	return CoordinatorAdaptResult{DelegatedTaskIDs: delegated}, nil
}

func (a *llmCoordinatorAdapter) taskIDSet(workspaceID string) (map[string]bool, error) {
	ws, err := a.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ws.Tasks))
	for i := range ws.Tasks {
		set[ws.Tasks[i].ID] = true
	}
	return set, nil
}

// newTaskIDsInOrder returns the IDs of tasks present now but absent from before,
// in workspace insertion order so the loop combines results deterministically.
func (a *llmCoordinatorAdapter) newTaskIDsInOrder(workspaceID string, before map[string]bool) ([]string, error) {
	ws, err := a.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for i := range ws.Tasks {
		if id := ws.Tasks[i].ID; !before[id] {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func buildCoordinatorAdaptPrompt(req CoordinatorAdaptRequest) string {
	var b strings.Builder
	b.WriteString("A task you are coordinating did not succeed and needs adaptation.\n\n")
	if req.Iteration > 0 && req.MaxIterations > 0 {
		fmt.Fprintf(&b, "Attempt %d of %d. Avoid repeating a delegation decision that has already failed in an earlier attempt.\n", req.Iteration, req.MaxIterations)
	}
	fmt.Fprintf(&b, "Failed task: %s\n", strings.TrimSpace(req.FailedTask.Description))
	if d := strings.TrimSpace(req.FailedTask.Details); d != "" {
		fmt.Fprintf(&b, "Details: %s\n", d)
	}
	fmt.Fprintf(&b, "Outcome (%s): %s\n\n", req.Trigger.Code, strings.TrimSpace(req.Trigger.Reason))

	if len(req.PriorResults) > 0 {
		b.WriteString("Results from subtasks you already delegated:\n")
		for id, res := range req.PriorResults {
			fmt.Fprintf(&b, "- %s: %s\n", id, truncateForPrompt(res, 500))
		}
		b.WriteString("\n")
	}

	if len(req.Specialists) > 0 {
		fmt.Fprintf(&b, "Specialists you can delegate to: %s\n\n", strings.Join(req.Specialists, ", "))
	} else {
		b.WriteString("This workspace has no other agents, so there is nobody to delegate to — resolve it yourself or ask for input.\n\n")
	}

	b.WriteString("Decide how to resolve this:\n")
	fmt.Fprintf(&b, "- To hand work to a specialist, call delegate_task with parent_task_id=%q.\n", req.FailedTask.ID)
	b.WriteString("- Delegate only to an agent named above; any other name will be rejected.\n")
	b.WriteString("- If you can resolve it yourself, do so and reply with the final result.\n")
	b.WriteString("- A specialist cannot re-delegate, so delegate only from here when a different specialist is genuinely needed.\n")
	return b.String()
}

func truncateForPrompt(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
