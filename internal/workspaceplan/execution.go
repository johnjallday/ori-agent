package workspaceplan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Plan execution.
//
// The Plan does not execute anything itself. It decides WHICH approved Task is
// eligible next and asks the existing Task-to-Run machinery to run it; Runs
// remain the record of what happened, and Tasks remain the record of what is
// left (FR-100).
//
// That division is why this file has no status of its own to keep in sync. Plan
// status answers "what is this plan doing" — executing, paused, completed — and
// everything more specific is derived from the Tasks each time it is asked.

// TaskDispatcher starts one Task and returns the Run it created.
//
// It is an interface so the planning domain does not depend on the run package,
// and so tests can dispatch without standing up an executor.
type TaskDispatcher interface {
	// DispatchTask executes a Task, returning the ID of the Run it created.
	// The Run record is the authority on how that execution went.
	DispatchTask(ctx context.Context, workspaceID string, task workspace.Task) (runID string, err error)
}

// Executor supervises an approved Plan's work.
type Executor struct {
	service    *Service
	tasks      TaskReader
	dispatcher TaskDispatcher
	// mutate applies a change to one Task and persists it, reusing the
	// workspace's own safe transition rules rather than writing status
	// directly (FR-112).
	mutate TaskMutator
	// slots arbitrates which Plan may execute in this workspace. Optional:
	// without it a Plan starts whenever asked (FR-106).
	slots *SlotCoordinator
	// checker and availability decide the gates in front of a dispatch. They
	// live here rather than on the automatic runner so manual and automatic
	// dispatch cannot drift into two different opinions about what is allowed
	// to run (FR-105, FR-118).
	checker      PreconditionChecker
	availability func(ctx context.Context, workspaceID string) ValidationContext
}

// TaskMutator applies a change to one Task through the workspace's own
// validated mutation path.
type TaskMutator interface {
	MutateTask(workspaceID, taskID string, fn func(*workspace.Task) error) error
}

// NewExecutor returns a Plan executor.
func NewExecutor(service *Service, tasks TaskReader, opts ...ExecutorOption) *Executor {
	executor := &Executor{service: service, tasks: tasks}
	for _, opt := range opts {
		opt(executor)
	}
	return executor
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithDispatcher attaches the Task-to-Run dispatcher.
func WithDispatcher(dispatcher TaskDispatcher) ExecutorOption {
	return func(e *Executor) { e.dispatcher = dispatcher }
}

// WithTaskMutator attaches the Task mutation path.
func WithTaskMutator(mutator TaskMutator) ExecutorOption {
	return func(e *Executor) { e.mutate = mutator }
}

// WithSlots attaches the workspace execution slot.
//
// Without it a Plan starts whenever asked, which is the right behavior for a
// build with no arbitration wired — the slot restricts concurrency, it is not
// a prerequisite for running at all.
func WithSlots(slots *SlotCoordinator) ExecutorOption {
	return func(e *Executor) { e.slots = slots }
}

// WithGates attaches gate evaluation: the compiled enforcement adapters and the
// live agent/capability lookup.
//
// A nil checker is not "no preconditions" — it makes every enforced
// precondition fail closed, because a Plan approved on the promise of a check
// must not run where nothing can perform it (FR-105).
func WithGates(
	checker PreconditionChecker,
	availability func(context.Context, string) ValidationContext,
) ExecutorOption {
	return func(e *Executor) {
		e.checker = checker
		e.availability = availability
	}
}

// Gates returns everything standing between one Task and its dispatch.
//
// Gates come from the version the Task was approved under. A Plan with no
// approved version has no gates to read, and no Task either — nothing was
// materialized — so the empty result is the correct one rather than a hole.
func (e *Executor) Gates(ctx context.Context, plan *Plan, task workspace.Task) ([]Gate, error) {
	if plan.ApprovedVersion <= 0 {
		return nil, nil
	}
	version, err := e.service.Store().GetVersion(
		ctx, plan.WorkspaceID, plan.ID, plan.ApprovedVersion)
	if err != nil {
		return nil, err
	}

	availability := ValidationContext{}
	if e.availability != nil {
		availability = e.availability(ctx, plan.WorkspaceID)
	}

	return EvaluateGates(ctx, e.checker, GateInput{
		WorkspaceID:  plan.WorkspaceID,
		Plan:         plan,
		Content:      version.Content,
		ItemID:       itemIDForTask(plan, task.ID),
		TaskID:       task.ID,
		Availability: availability,
	})
}

// StartInput asks a Plan to begin, or to take its next step.
type StartInput struct {
	Actor string
	// TaskID optionally starts one specific eligible Task. Empty starts the
	// first eligible Task in Plan order, which is what stepping through means.
	TaskID string
}

// StartResult reports what a start did.
type StartResult struct {
	PlanID string `json:"plan_id"`
	// TaskID and RunID identify the work that was started, empty when nothing
	// was eligible.
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	// Started is false when there was nothing to start, which is not an error:
	// a plan whose work is all running or finished has simply nothing to do.
	Started  bool     `json:"started"`
	Progress Progress `json:"progress"`
	// Reason explains a no-op start, so the UI can say why nothing happened.
	Reason string `json:"reason,omitempty"`
}

// Start dispatches the next eligible Task of an approved Plan (FR-102).
//
// In step_through this is how work moves at all: approval created the Tasks and
// started nothing, so every step is a deliberate user action.
func (e *Executor) Start(ctx context.Context, workspaceID, planID string, input StartInput) (*StartResult, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if e.dispatcher == nil {
		return nil, fmt.Errorf("%w: no task dispatcher is configured", ErrValidation)
	}
	// Only an approved or already-executing Plan may dispatch. A draft has
	// nothing approved to run, and a paused Plan resumes rather than starts.
	if plan.Status != StatusApproved && plan.Status != StatusExecuting {
		return nil, fmt.Errorf("%w: a %s plan cannot start work", ErrInvalidTransition, plan.Status)
	}

	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	task, found := e.selectTask(plan, ws.Tasks, input.TaskID)
	if !found {
		progress := DeriveProgress(plan, ws.Tasks)
		return &StartResult{
			PlanID:   planID,
			Progress: progress,
			Reason:   noStartReason(progress),
		}, nil
	}

	// A blocking gate stops a manual start too. This is a deliberate user
	// action, but no click makes an absent agent exist: dispatching anyway
	// would move the same failure into the Run record and cost a slot claim on
	// the way (FR-118).
	gates, err := e.Gates(ctx, plan, task)
	if err != nil {
		return nil, err
	}
	if blocking := blockingGates(gates); len(blocking) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnavailableCapability, GateSummary(blocking))
	}

	// Take the workspace's execution slot before dispatching. The claim
	// happens here rather than at approval because approving does not start
	// work — holding the slot from approval would block other plans on
	// something that may never run (FR-106).
	if e.slots != nil {
		claim, err := e.slots.Claim(ctx, workspaceID, planID)
		if err != nil {
			return nil, err
		}
		if !claim.Owned() {
			// Waiting is visible, not an error: the plan is queued and the
			// caller is told what it waits behind (FR-107).
			progress := DeriveProgress(plan, ws.Tasks)
			progress.WaitingForSlot = progress.Ready
			progress.Ready = 0
			return &StartResult{
				PlanID:   planID,
				Progress: progress,
				Reason:   waitingReason(claim),
			}, nil
		}
	}

	// The Plan moves to executing before the Run starts, so a Plan is never
	// running work while claiming to be merely approved.
	if plan.Status == StatusApproved {
		if _, err := e.service.Transition(ctx, workspaceID, planID, TransitionInput{
			To: StatusExecuting, Source: SourceUser, Actor: input.Actor,
			Reason: fmt.Sprintf("started %q", task.Description),
			TaskID: task.ID,
		}); err != nil {
			return nil, err
		}
	}

	runID, err := e.dispatcher.DispatchTask(ctx, workspaceID, task)
	if err != nil {
		return nil, fmt.Errorf("dispatch task %s: %w", task.ID, err)
	}

	// Link the Run to the Plan. The Run record stays authoritative for status,
	// traces, and results; this only records that it belongs to this Plan
	// (FR-9, FR-100).
	if runID != "" {
		if err := e.linkRun(ctx, plan, task, runID); err != nil {
			return nil, err
		}
	}

	refreshed, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	updated, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	return &StartResult{
		PlanID:   planID,
		TaskID:   task.ID,
		RunID:    runID,
		Started:  true,
		Progress: DeriveProgress(refreshed, updated.Tasks),
	}, nil
}

// selectTask picks the Task to start: a named one if the caller asked for it
// and it is eligible, otherwise the first eligible in Plan order.
func (e *Executor) selectTask(plan *Plan, tasks []workspace.Task, taskID string) (workspace.Task, bool) {
	eligible := EligibleTasks(plan, tasks)
	if taskID == "" {
		if len(eligible) == 0 {
			return workspace.Task{}, false
		}
		return eligible[0], true
	}
	for _, task := range eligible {
		if task.ID == taskID {
			return task, true
		}
	}
	return workspace.Task{}, false
}

// waitingReason says what a queued Plan is waiting behind and where it stands,
// rather than only that it is waiting (FR-107).
func waitingReason(claim *ClaimResult) string {
	position := 0
	if claim.Waiting != nil {
		position = claim.Waiting.Position
	}
	if claim.HolderPlanID != "" {
		return fmt.Sprintf(
			"another plan is executing in this workspace; this plan is %s in line",
			ordinal(position))
	}
	return fmt.Sprintf("waiting for the workspace execution slot (%s in line)", ordinal(position))
}

func ordinal(position int) string {
	switch position {
	case 0:
		return "next"
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	default:
		return fmt.Sprintf("%dth", position)
	}
}

// noStartReason explains why a start did nothing, so the UI can say something
// more useful than "nothing happened".
func noStartReason(progress Progress) string {
	switch {
	case progress.Total == 0:
		return "this plan has no tasks yet"
	case progress.Running > 0:
		return "every eligible task is already running"
	case progress.Failed > 0:
		return "the remaining work is blocked behind a failed task"
	case progress.Blocked > 0:
		return "the remaining work is waiting on unfinished dependencies"
	case progress.Remaining == 0:
		return "all of this plan's work has finished"
	default:
		return "there is nothing eligible to start"
	}
}

func (e *Executor) linkRun(ctx context.Context, plan *Plan, task workspace.Task, runID string) error {
	provenance, _ := ProvenanceFromTaskContext(task.Context)
	return e.service.Store().LinkRun(ctx, plan.WorkspaceID, plan.ID, RunLink{
		PlanID:      plan.ID,
		WorkspaceID: plan.WorkspaceID,
		Version:     provenance.Version,
		GroupID:     provenance.GroupID,
		ItemID:      provenance.ItemID,
		TaskID:      task.ID,
		RunID:       runID,
		CreatedAt:   e.service.Now(),
	})
}

// PauseInput describes a requested pause.
type PauseInput struct {
	Actor  string
	Reason string
}

// PauseResult reports what a pause did, including work still in flight.
type PauseResult struct {
	PlanID string `json:"plan_id"`
	// StillRunning names the Tasks that were already executing. Pausing stops
	// FUTURE dispatch; it does not kill work that is mid-flight, because
	// terminating an agent mid-action is not a safe stop (FR-108).
	StillRunning []TaskRef `json:"still_running,omitempty"`
	// SlotReleased is false while work is still in flight: the workspace
	// execution slot is only released once the plan has actually stopped
	// (FR-108).
	SlotReleased bool     `json:"slot_released"`
	Progress     Progress `json:"progress"`
}

// TaskRef identifies a Task in a user-facing preview.
type TaskRef struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	Status      string `json:"status,omitempty"`
}

// Pause stops future dispatch without terminating in-flight work (FR-108).
//
// The distinction matters: a user pausing a plan wants it to stop taking new
// steps, not to have an agent killed halfway through writing a file.
func (e *Executor) Pause(ctx context.Context, workspaceID, planID string, input PauseInput) (*PauseResult, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.Status != StatusExecuting && plan.Status != StatusApproved {
		return nil, fmt.Errorf("%w: a %s plan is not running", ErrInvalidTransition, plan.Status)
	}

	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	running := RunningTasks(plan, ws.Tasks)

	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "paused by the user"
	}
	// The reason is retained on the Plan's history so a paused plan can always
	// say why it stopped (FR-109).
	paused, err := e.service.Transition(ctx, workspaceID, planID, TransitionInput{
		To: StatusPaused, Source: SourceUser, Actor: input.Actor, Reason: reason,
	})
	if err != nil {
		return nil, err
	}

	// The slot is released only once nothing is in flight. Releasing while an
	// agent is mid-action would let a second Plan start beside it, which is
	// exactly the overlap the slot exists to prevent (FR-108).
	released := len(running) == 0
	if released {
		if err := e.releaseSlot(ctx, workspaceID, planID); err != nil {
			return nil, err
		}
	}

	return &PauseResult{
		PlanID:       planID,
		StillRunning: taskRefs(running),
		SlotReleased: released,
		Progress:     DeriveProgress(paused, ws.Tasks),
	}, nil
}

// releaseSlot gives up the workspace slot and, when another Plan is waiting,
// leaves it free for that Plan's next start.
func (e *Executor) releaseSlot(ctx context.Context, workspaceID, planID string) error {
	if e.slots == nil {
		return nil
	}
	lease, err := e.slots.store.CurrentLease(ctx, workspaceID)
	if err != nil {
		return err
	}
	// Releasing a slot this Plan does not hold is a no-op, not an error: a
	// Plan that never started has nothing to give up.
	if lease == nil || lease.PlanID != planID {
		return nil
	}
	if _, err := e.slots.Release(ctx, workspaceID, planID, lease.Generation); err != nil {
		return err
	}
	return nil
}

// Resume returns a paused Plan to executing (FR-110).
//
// It does not itself dispatch. Resuming makes the Plan eligible to run again;
// group 6's execution slot decides when it actually does, and a resumed Plan
// rejoins the queue rather than displacing whoever holds the slot.
func (e *Executor) Resume(ctx context.Context, workspaceID, planID, actor string) (*Plan, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if plan.Status != StatusPaused {
		return nil, fmt.Errorf("%w: only a paused plan can resume, this one is %s",
			ErrInvalidTransition, plan.Status)
	}

	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	// A Plan whose work is all failed has nothing to resume into; it needs a
	// retry or a revision first, and saying so beats silently resuming into a
	// stall (FR-113).
	if failed := FailedTasks(plan, ws.Tasks); len(failed) > 0 {
		if len(EligibleTasks(plan, ws.Tasks)) == 0 {
			return nil, fmt.Errorf(
				"%w: %d task(s) failed and nothing else can start. Retry, reassign, skip, or revise first",
				ErrInvalidTransition, len(failed))
		}
	}

	// Resuming rejoins the queue rather than displacing the current holder: a
	// paused plan gave up its turn, and taking it back by force would stop
	// whoever started in the meantime (FR-110).
	if e.slots != nil {
		if _, err := e.slots.Enqueue(ctx, workspaceID, planID); err != nil {
			return nil, err
		}
	}

	return e.service.Transition(ctx, workspaceID, planID, TransitionInput{
		To: StatusExecuting, Source: SourceUser, Actor: actor, Reason: "resumed",
	})
}

// CancelPreview describes what cancelling would affect, before it happens
// (FR-111, FR-154).
type CancelPreview struct {
	PlanID string `json:"plan_id"`
	// Running work is stopped through the existing safe cancellation path;
	// Queued work is simply never dispatched.
	Running []TaskRef `json:"running,omitempty"`
	Queued  []TaskRef `json:"queued,omitempty"`
	// Completed work is untouched. Cancelling a plan does not erase what it
	// already achieved (FR-112).
	CompletedCount int `json:"completed_count"`
}

// PreviewCancel reports what cancelling this Plan would affect.
func (e *Executor) PreviewCancel(ctx context.Context, workspaceID, planID string) (*CancelPreview, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]workspace.Task, len(ws.Tasks))
	for _, task := range ws.Tasks {
		byID[task.ID] = task
	}

	preview := &CancelPreview{PlanID: planID}
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup {
			continue
		}
		task, exists := byID[link.TaskID]
		if !exists {
			continue
		}
		switch classifyTask(task, byID) {
		case taskRunning:
			preview.Running = append(preview.Running, taskRef(task))
		case taskCompleted:
			preview.CompletedCount++
		case taskFailed:
			// Already terminal; cancelling changes nothing about it.
		default:
			preview.Queued = append(preview.Queued, taskRef(task))
		}
	}
	return preview, nil
}

// Cancel stops a Plan and its unstarted work (FR-111, FR-112).
//
// Completed history is never deleted, and running work is stopped through the
// workspace's own cancellation path rather than by writing a status directly.
func (e *Executor) Cancel(ctx context.Context, workspaceID, planID, reason, actor string) (*Plan, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]workspace.Task, len(ws.Tasks))
	for _, task := range ws.Tasks {
		byID[task.ID] = task
	}

	// Cancel the work that has not finished. A completed Task is left exactly
	// as it is: the plan is being stopped, not undone.
	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil {
			continue
		}
		task, exists := byID[link.TaskID]
		if !exists {
			continue
		}
		if isTerminalTaskStatus(task.Status) {
			continue
		}
		if err := e.cancelTask(workspaceID, task.ID); err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(reason) == "" {
		reason = "cancelled by the user"
	}
	cancelled, err := e.service.Transition(ctx, workspaceID, planID, TransitionInput{
		To: StatusCancelled, Source: SourceUser, Actor: actor, Reason: reason,
	})
	if err != nil {
		return nil, err
	}

	// A cancelled Plan holds nothing and waits for nothing.
	if err := e.releaseSlot(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	if e.slots != nil {
		if err := e.slots.Dequeue(ctx, workspaceID, planID); err != nil {
			return nil, err
		}
	}
	return cancelled, nil
}

func (e *Executor) cancelTask(workspaceID, taskID string) error {
	if e.mutate == nil {
		return nil
	}
	return e.mutate.MutateTask(workspaceID, taskID, func(task *workspace.Task) error {
		if isTerminalTaskStatus(task.Status) {
			return nil
		}
		task.Status = workspace.TaskStatusCancelled
		return nil
	})
}

func isTerminalTaskStatus(status workspace.TaskStatus) bool {
	switch status {
	case workspace.TaskStatusCompleted, workspace.TaskStatusFailed,
		workspace.TaskStatusCancelled, workspace.TaskStatusTimeout:
		return true
	default:
		return false
	}
}

// Retry re-dispatches a failed Task as a NEW Run attempt (FR-114).
//
// The earlier Run keeps its trace, result, and artifacts. A retry is another
// attempt at the same work, not a rewrite of what already happened.
func (e *Executor) Retry(ctx context.Context, workspaceID, planID, taskID, actor string) (*StartResult, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	var target *workspace.Task
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == taskID {
			target = &ws.Tasks[i]
			break
		}
	}
	if target == nil || !planOwnsTask(plan, taskID) {
		return nil, fmt.Errorf("%w: task %s is not part of this plan", ErrPlanNotFound, taskID)
	}
	if e.mutate == nil {
		return nil, fmt.Errorf("%w: no task mutator is configured", ErrValidation)
	}

	// Return the Task to pending so the existing dispatch path can pick it up
	// again. Its previous Run is untouched.
	if err := e.mutate.MutateTask(workspaceID, taskID, func(task *workspace.Task) error {
		task.Status = workspace.TaskStatusPending
		task.Error = ""
		return nil
	}); err != nil {
		return nil, err
	}

	// A paused Plan returns to executing when the user retries: retrying is a
	// decision to keep going.
	if plan.Status == StatusPaused {
		if _, err := e.service.Transition(ctx, workspaceID, planID, TransitionInput{
			To: StatusExecuting, Source: SourceUser, Actor: actor,
			Reason: "retried a failed task", TaskID: taskID,
		}); err != nil {
			return nil, err
		}
	}
	return e.Start(ctx, workspaceID, planID, StartInput{Actor: actor, TaskID: taskID})
}

// SkipInput records a decision to proceed without a required Task.
type SkipInput struct {
	Actor string
	// Reason is required. Skipping approved work is a judgement call, and an
	// unexplained one is not auditable (FR-115).
	Reason string
}

// Skip marks an approved Task as deliberately not done (FR-115).
//
// The Plan can still complete afterwards, but as completed-with-exceptions:
// the difference between "everything approved was done" and "we decided to
// leave something out" must survive into the report.
func (e *Executor) Skip(ctx context.Context, workspaceID, planID, taskID string, input SkipInput) (*Plan, error) {
	if strings.TrimSpace(input.Reason) == "" {
		return nil, fmt.Errorf("%w: skipping approved work requires a reason", ErrValidation)
	}
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	if !planOwnsTask(plan, taskID) {
		return nil, fmt.Errorf("%w: task %s is not part of this plan", ErrPlanNotFound, taskID)
	}
	if e.mutate == nil {
		return nil, fmt.Errorf("%w: no task mutator is configured", ErrValidation)
	}

	if err := e.mutate.MutateTask(workspaceID, taskID, func(task *workspace.Task) error {
		task.Status = workspace.TaskStatusCancelled
		task.Result = fmt.Sprintf("Skipped: %s", input.Reason)
		return nil
	}); err != nil {
		return nil, err
	}

	entry := NewActivity(plan, ActivityTaskSkipped, SourceUser, input.Actor, input.Reason)
	entry.TaskID = taskID
	entry.CreatedAt = e.service.Now()
	if _, err := e.service.Store().AppendActivity(ctx, entry); err != nil {
		return nil, err
	}
	return e.service.Get(ctx, workspaceID, planID)
}

func planOwnsTask(plan *Plan, taskID string) bool {
	for _, link := range plan.TaskLinks {
		if link.TaskID == taskID {
			return true
		}
	}
	return false
}

func taskRef(task workspace.Task) TaskRef {
	return TaskRef{TaskID: task.ID, Description: task.Description, Status: string(task.Status)}
}

func taskRefs(tasks []workspace.Task) []TaskRef {
	if len(tasks) == 0 {
		return nil
	}
	refs := make([]TaskRef, 0, len(tasks))
	for _, task := range tasks {
		refs = append(refs, taskRef(task))
	}
	return refs
}

// --- Completion (FR-119 through FR-121) ------------------------------------

// CompletionReport is the durable summary a finished Plan leaves behind
// (FR-121).
type CompletionReport struct {
	PlanID      string    `json:"plan_id"`
	Version     int       `json:"plan_version"`
	Objective   string    `json:"objective"`
	CompletedAt time.Time `json:"completed_at"`

	TotalTasks     int `json:"total_tasks"`
	CompletedTasks int `json:"completed_tasks"`
	FailedTasks    int `json:"failed_tasks"`
	SkippedTasks   int `json:"skipped_tasks"`

	// Exceptions are the approved items that did not happen as approved. Their
	// presence is what makes this completed-with-exceptions rather than simply
	// completed (FR-115).
	Exceptions []CompletionException `json:"exceptions,omitempty"`
	// Artifacts and Runs point at what the plan produced; their records remain
	// authoritative.
	Artifacts []string `json:"artifacts,omitempty"`
	RunIDs    []string `json:"run_ids,omitempty"`
	// FollowUps are suggestions, not commitments. New executable scope needs a
	// new draft and a new approval (FR-116).
	FollowUps []string `json:"follow_ups,omitempty"`
}

// CompletionException is one approved item that did not complete as approved.
type CompletionException struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason,omitempty"`
}

// Complete moves a Plan to completed, if its work actually finished (FR-119).
//
// It refuses while anything is still outstanding. A plan that reports itself
// complete with work left is worse than one that reports itself unfinished.
func (e *Executor) Complete(ctx context.Context, workspaceID, planID, actor string) (*CompletionReport, error) {
	plan, err := e.service.Get(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	ws, err := e.tasks.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	progress := DeriveProgress(plan, ws.Tasks)
	if progress.Running > 0 || progress.Ready > 0 || progress.Blocked > 0 {
		return nil, fmt.Errorf(
			"%w: %d task(s) are still outstanding; the plan cannot complete yet",
			ErrInvalidTransition, progress.Running+progress.Ready+progress.Blocked)
	}
	if progress.Failed > 0 {
		return nil, fmt.Errorf(
			"%w: %d task(s) failed. Retry, skip with a reason, or revise the plan before completing",
			ErrInvalidTransition, progress.Failed)
	}

	report := e.buildReport(plan, ws.Tasks, progress)
	if _, err := e.service.Transition(ctx, workspaceID, planID, TransitionInput{
		To: StatusCompleted, Source: SourceUser, Actor: actor,
		Reason: completionReason(report),
	}); err != nil {
		return nil, err
	}

	// A finished Plan gives the workspace back, so whatever is queued can run.
	if err := e.releaseSlot(ctx, workspaceID, planID); err != nil {
		return nil, err
	}
	return report, nil
}

func completionReason(report *CompletionReport) string {
	if len(report.Exceptions) == 0 {
		return "all approved work completed"
	}
	return fmt.Sprintf("completed with %d exception(s)", len(report.Exceptions))
}

func (e *Executor) buildReport(plan *Plan, tasks []workspace.Task, progress Progress) *CompletionReport {
	byID := make(map[string]workspace.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	report := &CompletionReport{
		PlanID:         plan.ID,
		Version:        plan.ApprovedVersion,
		Objective:      plan.Objective,
		CompletedAt:    e.service.Now(),
		TotalTasks:     progress.Total,
		CompletedTasks: progress.Completed,
		FailedTasks:    progress.Failed,
	}

	for _, link := range plan.TaskLinks {
		if link.RetiredAt != nil || link.Role == LinkRoleGroup {
			continue
		}
		task, exists := byID[link.TaskID]
		if !exists {
			continue
		}
		switch task.Status {
		case workspace.TaskStatusCancelled:
			report.SkippedTasks++
			report.Exceptions = append(report.Exceptions, CompletionException{
				TaskID: task.ID, Description: task.Description,
				Outcome: "skipped", Reason: task.Result,
			})
		case workspace.TaskStatusFailed, workspace.TaskStatusTimeout:
			report.Exceptions = append(report.Exceptions, CompletionException{
				TaskID: task.ID, Description: task.Description,
				Outcome: string(task.Status), Reason: task.Error,
			})
		}
	}

	for _, link := range plan.RunLinks {
		report.RunIDs = append(report.RunIDs, link.RunID)
	}
	return report
}

// Fail moves a Plan to failed (FR-120).
//
// It is deliberately narrow: ordinary retryable errors pause instead. A Plan
// reaches failed only when it cannot continue without revision or user
// intervention, so "failed" keeps meaning something.
func (e *Executor) Fail(ctx context.Context, workspaceID, planID, reason, actor string) (*Plan, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: failing a plan requires a reason", ErrValidation)
	}
	return e.service.Transition(ctx, workspaceID, planID, TransitionInput{
		To: StatusFailed, Source: SourceUser, Actor: actor, Reason: reason,
	})
}
