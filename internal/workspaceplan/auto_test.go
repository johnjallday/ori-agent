package workspaceplan

import (
	"context"
	"log"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// autoContent is reviewableContent approved to run automatically.
func autoContent() PlanContent {
	content := reviewableContent()
	content.Execution.Mode = ExecutionAuto
	// Both items need an assignee: automatic dispatch will not invent one, and
	// an unassigned item is a capability gate rather than work to run.
	content.Groups[0].Items[1].Assignee = "builder"
	return content
}

// autoExecutable materializes an auto-mode plan and returns everything needed
// to drive it. The dispatcher completes each task the way a real run would, so
// the loop can make progress.
func autoExecutable(t *testing.T, ctx context.Context, content PlanContent) (
	*Service, *Executor, *AutoRunner, *fakeTaskWriter, *fakeDispatcher, *Plan) {
	t.Helper()

	service := reviewService(t)
	writer := newFakeTaskWriter()
	materializer := NewMaterializer(service, writer)

	plan := newReviewablePlan(t, ctx, service, content)
	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectFor(content.Execution.Mode),
		UserName:       "jj",
		IdempotencyKey: "auto-key",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	mutator := &fakeMutator{writer: writer}
	dispatcher := &fakeDispatcher{}
	// A dispatched task completes, which is what makes its dependents eligible
	// on the loop's next pass.
	dispatcher.onDispatch = func(taskID string) {
		_ = mutator.MutateTask("ws-1", taskID, func(task *workspace.Task) error {
			task.Status = workspace.TaskStatusCompleted
			return nil
		})
	}

	executor := NewExecutor(service, writer,
		WithDispatcher(dispatcher),
		WithTaskMutator(mutator),
		WithGates(nil, func(context.Context, string) ValidationContext {
			return ValidationContext{AvailableAgents: []string{"builder"}}
		}))
	service.progress = NewTaskProgressSource(writer)

	refreshed, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	return service, executor, NewAutoRunner(executor), writer, dispatcher, refreshed
}

// --- Only Approve and Start runs (FR-101, FR-103) --------------------------

// A step-through plan is never driven automatically, even when something asks
// it to. The mode is part of what the user approved.
func TestAutoRefusesAStepThroughPlan(t *testing.T) {
	ctx := context.Background()
	_, _, auto, _, dispatcher, plan := autoExecutable(t, ctx, reviewableContent())

	result, err := auto.Launch(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	if result.Launched {
		t.Fatal("a step_through plan was driven automatically")
	}
	if !strings.Contains(result.Reason, "step through") {
		t.Errorf("reason = %q, want it to say the plan steps through", result.Reason)
	}
	if dispatcher.count() != 0 {
		t.Errorf("dispatched %d task(s) for a step_through plan", dispatcher.count())
	}
}

// A draft has no approval, so nothing authorized automatic execution.
func TestAutoRefusesAnUnapprovedPlan(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	writer := newFakeTaskWriter()
	plan := newReviewablePlan(t, ctx, service, autoContent())

	executor := NewExecutor(service, writer, WithDispatcher(&fakeDispatcher{}))
	auto := NewAutoRunner(executor)

	result, err := auto.Launch(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.Launched {
		t.Fatal("an unapproved plan was driven automatically")
	}
}

// --- Dependency fan-out (FR-104) -------------------------------------------

// Auto mode runs the whole plan, and runs it in dependency order: the second
// item depends on the first, so it can only be dispatched after it completed.
func TestAutoRunsEveryTaskInDependencyOrder(t *testing.T) {
	ctx := context.Background()
	service, _, auto, writer, dispatcher, plan := autoExecutable(t, ctx, autoContent())
	first := taskIDByDescription(t, writer, "Snapshot staging")
	second := taskIDByDescription(t, writer, "Verify checksums")

	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	dispatcher.mu.Lock()
	order := append([]string(nil), dispatcher.dispatched...)
	dispatcher.mu.Unlock()

	if len(order) != 2 {
		t.Fatalf("dispatched %d task(s), want 2: %v", len(order), order)
	}
	if order[0] != first || order[1] != second {
		t.Errorf("dispatch order = %v, want the snapshot before the verification", order)
	}

	finished, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if finished.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", finished.Status)
	}
}

// A predecessor that failed does not satisfy its dependent's condition, so the
// run stops rather than running work against a result that never arrived.
func TestAutoStopsWhenAPredecessorFails(t *testing.T) {
	ctx := context.Background()
	service, _, auto, writer, dispatcher, plan := autoExecutable(t, ctx, autoContent())

	first := taskIDByDescription(t, writer, "Snapshot staging")
	dispatcher.onDispatch = func(taskID string) {
		status := workspace.TaskStatusCompleted
		if taskID == first {
			status = workspace.TaskStatusFailed
		}
		writer.mu.Lock()
		defer writer.mu.Unlock()
		for i := range writer.workspace.Tasks {
			if writer.workspace.Tasks[i].ID == taskID {
				writer.workspace.Tasks[i].Status = status
			}
		}
	}

	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	if dispatcher.count() != 1 {
		t.Errorf("dispatched %d task(s); the dependent must not run after a failure", dispatcher.count())
	}
	stopped, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	// A retryable failure pauses rather than fails: reaching failed would need
	// a revision to leave (FR-120).
	if stopped.Status != StatusPaused {
		t.Errorf("status = %q, want paused", stopped.Status)
	}
}

// --- Gate stops (FR-105, FR-118) -------------------------------------------

// A required validation checkpoint stops automatic dispatch and says so.
func TestAutoStopsAtARequiredValidationCheckpoint(t *testing.T) {
	ctx := context.Background()
	content := autoContent()
	content.Validations = []ValidationCheckpoint{{
		ID: "chk-1", Title: "A human reviews the snapshot",
		AppliesTo: []string{"itm-1"}, Required: true,
	}}

	service, _, auto, _, dispatcher, plan := autoExecutable(t, ctx, content)

	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	if dispatcher.count() != 0 {
		t.Fatalf("dispatched %d task(s) past a required checkpoint", dispatcher.count())
	}
	stopped, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stopped.Status != StatusPaused {
		t.Fatalf("status = %q, want paused at the gate", stopped.Status)
	}

	activity, err := service.Activity(ctx, "ws-1", plan.ID, 20)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !mentions(activity, "A human reviews the snapshot") {
		t.Errorf("the gate that stopped the plan is not in its history: %+v", activity)
	}
}

// The same checkpoint does NOT stop a deliberate user start. That is the whole
// distinction: the gate asked for a human, and a human starting the step is the
// human it asked for.
func TestAValidationGateDoesNotBlockADeliberateStart(t *testing.T) {
	ctx := context.Background()
	content := autoContent()
	content.Validations = []ValidationCheckpoint{{
		ID: "chk-1", Title: "A human reviews the snapshot", Required: true,
	}}

	_, executor, _, _, dispatcher, plan := autoExecutable(t, ctx, content)

	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Started {
		t.Fatalf("a deliberate start was refused: %s", result.Reason)
	}
	if dispatcher.count() != 1 {
		t.Errorf("dispatched %d task(s), want 1", dispatcher.count())
	}
}

// An unavailable agent blocks every dispatch, including a deliberate one. No
// click makes an absent agent exist.
func TestAnUnavailableAgentBlocksEvenADeliberateStart(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	writer := newFakeTaskWriter()
	materializer := NewMaterializer(service, writer)

	plan := newReviewablePlan(t, ctx, service, autoContent())
	_, approval := approveVersion(t, ctx, service, plan, EffectCreateTasksAndStart)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	dispatcher := &fakeDispatcher{}
	executor := NewExecutor(service, writer,
		WithDispatcher(dispatcher),
		WithTaskMutator(&fakeMutator{writer: writer}),
		// The workspace has agents, but not the one this plan assigned.
		WithGates(nil, func(context.Context, string) ValidationContext {
			return ValidationContext{AvailableAgents: []string{"someone-else"}}
		}))
	service.progress = NewTaskProgressSource(writer)

	_, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{Actor: "jj"})
	if err == nil {
		t.Fatal("a start against a missing agent was allowed")
	}
	if !strings.Contains(err.Error(), "builder") {
		t.Errorf("error does not name the missing agent: %v", err)
	}
	if dispatcher.count() != 0 {
		t.Errorf("dispatched %d task(s) with no agent to run them", dispatcher.count())
	}
}

// An enforced precondition with nothing configured to check it fails closed:
// automatic dispatch stops rather than running on an unverified promise.
func TestAnUncheckablePreconditionStopsAutomaticDispatch(t *testing.T) {
	ctx := context.Background()
	content := autoContent()
	content.Execution.Preconditions = []string{"safe_branch"}

	service, _, auto, _, dispatcher, plan := autoExecutable(t, ctx, content)

	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	if dispatcher.count() != 0 {
		t.Fatalf("dispatched %d task(s) past an unverifiable precondition", dispatcher.count())
	}
	stopped, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stopped.Status != StatusPaused {
		t.Errorf("status = %q, want paused", stopped.Status)
	}
}

// A satisfied precondition lets the run proceed.
func TestASatisfiedPreconditionAllowsDispatch(t *testing.T) {
	ctx := context.Background()
	content := autoContent()
	content.Execution.Preconditions = []string{"safe_branch"}

	service := reviewService(t)
	writer := newFakeTaskWriter()
	materializer := NewMaterializer(service, writer)
	plan := newReviewablePlan(t, ctx, service, content)
	_, approval := approveVersion(t, ctx, service, plan, EffectCreateTasksAndStart)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	mutator := &fakeMutator{writer: writer}
	dispatcher := &fakeDispatcher{onDispatch: func(taskID string) {
		_ = mutator.MutateTask("ws-1", taskID, func(task *workspace.Task) error {
			task.Status = workspace.TaskStatusCompleted
			return nil
		})
	}}
	executor := NewExecutor(service, writer,
		WithDispatcher(dispatcher),
		WithTaskMutator(mutator),
		WithGates(passingChecker{}, func(context.Context, string) ValidationContext {
			return ValidationContext{AvailableAgents: []string{"builder"}}
		}))
	service.progress = NewTaskProgressSource(writer)

	auto := NewAutoRunner(executor)
	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	auto.Wait()

	if dispatcher.count() != 2 {
		t.Errorf("dispatched %d task(s), want 2", dispatcher.count())
	}
}

// passingChecker satisfies every precondition it is asked about.
type passingChecker struct{}

func (passingChecker) CheckPrecondition(context.Context, string, string, string) (*Gate, error) {
	return nil, nil
}

// --- One run per plan ------------------------------------------------------

// Launching a plan that is already running does not start a second loop racing
// the first for the same tasks.
func TestLaunchingTwiceDoesNotStartTwoRuns(t *testing.T) {
	ctx := context.Background()
	_, _, auto, _, dispatcher, plan := autoExecutable(t, ctx, autoContent())

	if _, err := auto.Launch(ctx, "ws-1", plan.ID, "jj"); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	auto.Wait()
	first := dispatcher.count()

	// The plan is finished now, so a second launch has nothing to do and says
	// so rather than dispatching again.
	result, err := auto.Launch(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	auto.Wait()

	if result.Launched {
		t.Error("a completed plan was launched again")
	}
	if dispatcher.count() != first {
		t.Errorf("dispatched %d task(s) after relaunch, want %d", dispatcher.count(), first)
	}
}

// mentions reports whether any activity entry contains the text.
func mentions(activity []Activity, text string) bool {
	for _, entry := range activity {
		if strings.Contains(entry.Reason, text) {
			return true
		}
	}
	return false
}

// approveVersion snapshots and approves the current draft with a chosen effect.
func approveVersion(
	t *testing.T, ctx context.Context, service *Service, plan *Plan, effect ApprovalEffect,
) (*Version, *Approval) {
	t.Helper()
	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         effect,
		UserName:       "jj",
		IdempotencyKey: "effect-key",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return version, approval
}

// A background runner logs plan content the user wrote. A title carrying a
// newline must not be able to forge a second log entry, which is what a bare
// TrimSpace allowed: it only strips the ends, never the interior.
func TestReportedValuesCannotForgeALogLine(t *testing.T) {
	logged := &strings.Builder{}
	runner := &AutoRunner{logger: log.New(logged, "", 0)}

	runner.report(
		"plan-1\nworkspaceplan auto plan-1: forged plan id",
		"draft %q failed",
		"Ship it\nworkspaceplan auto plan-1: forged message",
	)

	output := strings.TrimSuffix(logged.String(), "\n")
	if strings.Contains(output, "\n") {
		t.Errorf("report emitted more than one line:\n%s", output)
	}
	// The content is still reported, just flattened — sanitizing must not
	// silently swallow the diagnostic.
	if !strings.Contains(output, "forged message") {
		t.Errorf("report dropped the message content: %q", output)
	}
}
