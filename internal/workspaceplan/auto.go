package workspaceplan

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Automatic execution (FR-101, FR-103, FR-105).
//
// Auto mode is a loop, not a fan-out. The Task-to-Run bridge dispatches
// synchronously, so "run the plan" means: pick the next eligible Task, run it,
// look again. Looking again each time is what makes dependency order and gates
// hold — every decision is made against the state that exists at the moment of
// dispatch rather than a plan of record drawn up at the start.
//
// Nothing here starts by itself. A Plan reaches this code only by way of an
// Approve and Start approval for one exact version, spent through a successful
// materialization (FR-103).

// AutoRunner drives approved automatic Plans.
type AutoRunner struct {
	// executor owns dispatch, the slot, and gate evaluation. The runner only
	// decides when to ask it for the next step.
	executor *Executor
	logger   *log.Logger

	// base bounds every run's lifetime, so a server shutdown stops the loops
	// rather than leaving them dispatching into a closing process.
	base   context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	// running tracks the Plans with a live loop, keyed by workspace and plan.
	// A second start for a Plan already running is a no-op rather than a
	// second loop racing the first for the same Tasks.
	running map[string]struct{}
	// wg lets tests and shutdown wait for the loops to finish.
	wg sync.WaitGroup
}

// AutoRunnerOption configures an AutoRunner.
type AutoRunnerOption func(*AutoRunner)

// WithAutoLogger sets where a run reports failures it cannot return to a
// caller. A background loop has no request to fail; silence would make a stuck
// Plan indistinguishable from a finished one.
func WithAutoLogger(logger *log.Logger) AutoRunnerOption {
	return func(r *AutoRunner) { r.logger = logger }
}

// NewAutoRunner builds a runner over an executor.
func NewAutoRunner(executor *Executor, opts ...AutoRunnerOption) *AutoRunner {
	base, cancel := context.WithCancel(context.Background())
	runner := &AutoRunner{
		executor: executor,
		base:     base,
		cancel:   cancel,
		running:  make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(runner)
	}
	return runner
}

// Stop cancels every in-flight run and waits for the loops to return. In-flight
// Tasks are not killed: cancelling stops the loop from dispatching the NEXT
// one, which is the same safe stop a pause performs (FR-108).
func (r *AutoRunner) Stop() {
	if r == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
}

// Wait blocks until every started run has finished. Tests use it; production
// code does not need it because runs are fire-and-forget by design.
func (r *AutoRunner) Wait() {
	if r != nil {
		r.wg.Wait()
	}
}

// AutoStartResult reports what launching automatic execution did.
type AutoStartResult struct {
	PlanID string `json:"plan_id"`
	// Launched is false when the Plan was not eligible to run automatically —
	// already running, not in auto mode, or not approved.
	Launched bool   `json:"launched"`
	Reason   string `json:"reason,omitempty"`
}

// Launch begins automatic execution of a Plan whose approval authorized it.
//
// It returns as soon as the loop is running rather than when the Plan finishes.
// A Plan is minutes or hours of work; holding an HTTP request open for it would
// tie the run's survival to a browser tab.
func (r *AutoRunner) Launch(ctx context.Context, workspaceID, planID, actor string) (*AutoStartResult, error) {
	if r == nil || r.executor == nil {
		return nil, fmt.Errorf("%w: automatic execution is not configured", ErrValidation)
	}

	plan, err := r.executor.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	// Only an approved Plan runs. In particular a draft cannot: the approval is
	// what authorized this, and there is nothing else that could have.
	if plan.Status != StatusApproved && plan.Status != StatusExecuting {
		return &AutoStartResult{PlanID: planID,
			Reason: fmt.Sprintf("a %s plan does not run automatically", plan.Status)}, nil
	}

	mode, err := r.approvedMode(ctx, plan)
	if err != nil {
		return nil, err
	}
	// A step-through Plan is never driven automatically, even by an explicit
	// launch. The mode is part of what the user approved (FR-101).
	if mode != ExecutionAuto {
		return &AutoStartResult{PlanID: planID,
			Reason: "this plan was approved to step through, so it starts one task at a time"}, nil
	}

	key := workspaceID + "\x00" + planID
	r.mu.Lock()
	if _, active := r.running[key]; active {
		r.mu.Unlock()
		return &AutoStartResult{PlanID: planID, Reason: "this plan is already running"}, nil
	}
	r.running[key] = struct{}{}
	r.mu.Unlock()

	r.wg.Go(func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, key)
			r.mu.Unlock()
		}()
		r.run(r.base, workspaceID, planID, actor)
	})

	return &AutoStartResult{PlanID: planID, Launched: true}, nil
}

// approvedMode reads the execution mode from the version the Plan was approved
// under, not from its working draft.
//
// The draft may have moved on since approval. Reading the mode from it would
// let an edit made after approval change how already-approved work runs, which
// is precisely the authority a draft must not have (FR-68, FR-103).
func (r *AutoRunner) approvedMode(ctx context.Context, plan *Plan) (ExecutionMode, error) {
	content, err := r.approvedContent(ctx, plan)
	if err != nil {
		return "", err
	}
	return content.Execution.Mode, nil
}

// approvedContent returns the content of the Plan's approved version.
func (r *AutoRunner) approvedContent(ctx context.Context, plan *Plan) (PlanContent, error) {
	if plan.ApprovedVersion <= 0 {
		return PlanContent{}, fmt.Errorf("%w: this plan has no approved version", ErrInvalidTransition)
	}
	version, err := r.executor.service.Store().GetVersion(
		ctx, plan.WorkspaceID, plan.ID, plan.ApprovedVersion)
	if err != nil {
		return PlanContent{}, err
	}
	return version.Content, nil
}

// run is the dispatch loop.
//
// Each iteration re-reads the Plan and the workspace. That re-read is not
// defensive coding — it is how a pause takes effect, how a cancelled Plan
// stops, and how a Task that failed changes what is eligible next.
func (r *AutoRunner) run(ctx context.Context, workspaceID, planID, actor string) {
	for {
		if ctx.Err() != nil {
			return
		}

		plan, err := r.executor.service.Get(ctx, workspaceID, planID)
		if err != nil {
			r.report(planID, "read plan: %v", err)
			return
		}
		// A pause, a cancel, or a failure recorded elsewhere ends the loop. The
		// Plan's status is the authority on whether it should still be running,
		// and this loop never overrides it.
		if plan.Status != StatusApproved && plan.Status != StatusExecuting {
			return
		}

		ws, err := r.executor.tasks.Get(workspaceID)
		if err != nil {
			r.report(planID, "read workspace: %v", err)
			return
		}

		eligible := EligibleTasks(plan, ws.Tasks)
		if len(eligible) == 0 {
			r.finish(ctx, plan, ws.Tasks, actor)
			return
		}

		task, gates, err := r.nextDispatchable(ctx, plan, eligible)
		if err != nil {
			r.report(planID, "evaluate gates: %v", err)
			return
		}
		if task == nil {
			// Every eligible Task is behind a gate. That is a safe stop, not a
			// failure: the Plan pauses, says which gate held it, and gives up
			// the slot so a queued Plan can use it (FR-105, FR-108).
			r.pauseAtGates(ctx, workspaceID, planID, actor, gates)
			return
		}

		if _, err := r.executor.Start(ctx, workspaceID, planID, StartInput{
			Actor: actor, TaskID: task.ID,
		}); err != nil {
			// A dispatch failure pauses rather than fails the Plan: most are
			// retryable, and a Plan marked failed needs a revision to leave that
			// state (FR-120).
			r.pauseOnError(ctx, workspaceID, planID, actor, task.Description, err)
			return
		}
	}
}

// nextDispatchable returns the first eligible Task no gate holds, along with
// the gates seen when nothing can run.
func (r *AutoRunner) nextDispatchable(
	ctx context.Context,
	plan *Plan,
	eligible []workspace.Task,
) (*workspace.Task, []Gate, error) {
	var held []Gate
	for i := range eligible {
		task := eligible[i]
		gates, err := r.executor.Gates(ctx, plan, task)
		if err != nil {
			return nil, nil, err
		}
		// Automatic dispatch is stopped by every gate, not only the blocking
		// ones: an automation gate exists precisely to hand this decision back
		// to a person.
		if len(gates) == 0 {
			return &task, nil, nil
		}
		held = append(held, gates...)
	}
	return nil, held, nil
}

// itemIDForTask maps a Task back to the Plan item it was materialized from.
func itemIDForTask(plan *Plan, taskID string) string {
	for _, link := range plan.TaskLinks {
		if link.TaskID == taskID && link.RetiredAt == nil {
			return link.ItemID
		}
	}
	return ""
}

// pauseAtGates stops the Plan at its gates and records which ones held it.
func (r *AutoRunner) pauseAtGates(ctx context.Context, workspaceID, planID, actor string, gates []Gate) {
	reason := GateSummary(gates)
	if reason == "" {
		reason = "stopped at an execution gate"
	} else {
		reason = "stopped at a gate: " + reason
	}
	if _, err := r.executor.Pause(ctx, workspaceID, planID, PauseInput{
		Actor: actor, Reason: reason,
	}); err != nil {
		r.report(planID, "pause at gate: %v", err)
	}
}

// pauseOnError stops the Plan after a dispatch failure, naming the step.
func (r *AutoRunner) pauseOnError(ctx context.Context, workspaceID, planID, actor, description string, cause error) {
	reason := fmt.Sprintf("could not start %q: %v", description, cause)
	if _, err := r.executor.Pause(ctx, workspaceID, planID, PauseInput{
		Actor: actor, Reason: reason,
	}); err != nil {
		r.report(planID, "pause after dispatch failure (%v): %v", cause, err)
	}
}

// finish ends a run that has nothing left to dispatch.
//
// Nothing eligible does not mean done: work may be blocked behind a failure, or
// still in flight. Completion is attempted only when the Plan genuinely has
// nothing outstanding; otherwise the Plan pauses so a person can see why it
// stopped rather than finding it quietly parked in executing (FR-113, FR-119).
func (r *AutoRunner) finish(ctx context.Context, plan *Plan, tasks []workspace.Task, actor string) {
	progress := DeriveProgress(plan, tasks)
	if progress.Running > 0 {
		// Another dispatch is still in flight — its own loop will carry on.
		return
	}

	if progress.Completed == progress.Total {
		if _, err := r.executor.Complete(ctx, plan.WorkspaceID, plan.ID, actor); err != nil {
			r.report(plan.ID, "complete plan: %v", err)
		}
		return
	}

	reason := noStartReason(progress)
	if reason == "" {
		reason = "nothing else can start"
	}
	if _, err := r.executor.Pause(ctx, plan.WorkspaceID, plan.ID, PauseInput{
		Actor: actor, Reason: reason,
	}); err != nil {
		r.report(plan.ID, "pause after exhausting work: %v", err)
	}
}

// report logs a failure a background loop cannot return to anyone.
func (r *AutoRunner) report(planID, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if r.logger != nil {
		r.logger.Printf("workspaceplan auto %s: %s", planID, message)
		return
	}
	log.Printf("workspaceplan auto %s: %s", planID, strings.TrimSpace(message))
}
