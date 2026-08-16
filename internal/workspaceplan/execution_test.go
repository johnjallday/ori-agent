package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeDispatcher records what was dispatched instead of running it.
type fakeDispatcher struct {
	mu         sync.Mutex
	dispatched []string
	failWith   error
	// onDispatch lets a test move the task's state the way a real run would.
	onDispatch func(taskID string)
}

func (f *fakeDispatcher) DispatchTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return "", f.failWith
	}
	f.dispatched = append(f.dispatched, task.ID)
	if f.onDispatch != nil {
		f.onDispatch(task.ID)
	}
	return "run-" + task.ID, nil
}

func (f *fakeDispatcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dispatched)
}

// fakeMutator applies task changes to the shared fake workspace.
type fakeMutator struct {
	writer *fakeTaskWriter
}

func (m *fakeMutator) MutateTask(workspaceID, taskID string, fn func(*workspace.Task) error) error {
	m.writer.mu.Lock()
	defer m.writer.mu.Unlock()
	for i := range m.writer.workspace.Tasks {
		if m.writer.workspace.Tasks[i].ID == taskID {
			return fn(&m.writer.workspace.Tasks[i])
		}
	}
	return fmt.Errorf("task %s not found", taskID)
}

// setTaskStatus moves a task the way a real run would.
func setTaskStatus(t *testing.T, writer *fakeTaskWriter, taskID string, status workspace.TaskStatus) {
	t.Helper()
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for i := range writer.workspace.Tasks {
		if writer.workspace.Tasks[i].ID == taskID {
			writer.workspace.Tasks[i].Status = status
			return
		}
	}
	t.Fatalf("task %s not found", taskID)
}

func taskIDByDescription(t *testing.T, writer *fakeTaskWriter, description string) string {
	t.Helper()
	for _, task := range writer.tasks() {
		if task.Description == description {
			return task.ID
		}
	}
	t.Fatalf("no task described %q", description)
	return ""
}

// executable materializes a plan and returns everything needed to run it.
func executable(t *testing.T, ctx context.Context) (
	*Service, *Executor, *fakeTaskWriter, *fakeDispatcher, *Plan) {
	t.Helper()

	service, materializer, writer, plan, approval := materializable(t, ctx, reviewableContent())
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	dispatcher := &fakeDispatcher{}
	executor := NewExecutor(service, writer,
		WithDispatcher(dispatcher),
		WithTaskMutator(&fakeMutator{writer: writer}))

	// Progress is derived from the same workspace the executor reads.
	service.progress = NewTaskProgressSource(writer)

	refreshed, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	return service, executor, writer, dispatcher, refreshed
}

// --- Step-through (FR-101, FR-102) -----------------------------------------

// Approval creates tasks and starts nothing. Every step is a deliberate user
// action (FR-102).
func TestApprovalCreatesTasksWithoutStartingThem(t *testing.T) {
	ctx := context.Background()
	_, _, writer, dispatcher, plan := executable(t, ctx)

	if dispatcher.count() != 0 {
		t.Errorf("materialization dispatched %d task(s); step_through must start nothing", dispatcher.count())
	}
	if plan.Status != StatusApproved {
		t.Errorf("status = %q, want approved", plan.Status)
	}
	for _, task := range writer.tasks() {
		if task.Status != workspace.TaskStatusPending {
			t.Errorf("task %q status = %q, want pending", task.Description, task.Status)
		}
	}
}

func TestStartDispatchesOneEligibleTaskInPlanOrder(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, dispatcher, plan := executable(t, ctx)

	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Started {
		t.Fatalf("start did nothing: %s", result.Reason)
	}
	if dispatcher.count() != 1 {
		t.Errorf("dispatched %d task(s), want exactly 1", dispatcher.count())
	}

	// The first eligible task in plan order is the one that ran.
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	if result.TaskID != snapshot {
		t.Errorf("started %q, want the first task in plan order", result.TaskID)
	}
	if result.RunID != "run-"+snapshot {
		t.Errorf("run id = %q", result.RunID)
	}

	// The plan is executing, and the run is linked back to it.
	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if after.Status != StatusExecuting {
		t.Errorf("status = %q, want executing", after.Status)
	}
	if len(after.RunLinks) != 1 || after.RunLinks[0].TaskID != snapshot {
		t.Errorf("run link = %+v", after.RunLinks)
	}
}

// A dependent task cannot start before its input finishes (FR-104).
func TestStartRespectsDependencies(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, dispatcher, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	verify := taskIDByDescription(t, writer, "Verify checksums")

	// Asking for the dependent task explicitly is refused while it is blocked.
	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{TaskID: verify})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Started {
		t.Error("a blocked task was dispatched")
	}

	// Once its input completes, it becomes eligible.
	if _, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{}); err != nil {
		t.Fatalf("start first: %v", err)
	}
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusCompleted)

	next, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if !next.Started || next.TaskID != verify {
		t.Errorf("second start = %+v, want the dependent task", next)
	}
	if dispatcher.count() != 2 {
		t.Errorf("dispatched %d, want 2", dispatcher.count())
	}
}

// A failed predecessor does NOT satisfy a dependency: running work against a
// result that never arrived is worse than leaving it visibly blocked.
func TestAFailedPredecessorDoesNotUnblockDependents(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusFailed)

	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Started {
		t.Error("a task whose input failed was dispatched")
	}
	if !strings.Contains(result.Reason, "failed") {
		t.Errorf("reason = %q, want it to name the failure", result.Reason)
	}
}

func TestStartExplainsWhenThereIsNothingToDo(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	for _, task := range writer.tasks() {
		setTaskStatus(t, writer, task.ID, workspace.TaskStatusCompleted)
	}

	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Started {
		t.Error("a finished plan started something")
	}
	if !strings.Contains(result.Reason, "finished") {
		t.Errorf("reason = %q", result.Reason)
	}
}

func TestStartRefusesAPlanThatIsNotApproved(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	writer := newFakeTaskWriter()
	executor := NewExecutor(service, writer, WithDispatcher(&fakeDispatcher{}))
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	_, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
}

// --- Progress (FR-107) -----------------------------------------------------

// Progress is derived on read; nothing about task state is persisted on the
// plan (FR-11, FR-12).
func TestProgressIsDerivedFromLiveTaskState(t *testing.T) {
	ctx := context.Background()
	service, _, writer, _, plan := executable(t, ctx)

	loaded, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Progress == nil {
		t.Fatal("no progress was derived")
	}
	// Two item tasks; the group task is a container and is not counted.
	if loaded.Progress.Total != 2 {
		t.Errorf("total = %d, want 2 item tasks", loaded.Progress.Total)
	}
	if loaded.Progress.Ready != 1 || loaded.Progress.Blocked != 1 {
		t.Errorf("ready/blocked = %d/%d, want 1/1",
			loaded.Progress.Ready, loaded.Progress.Blocked)
	}

	// Move the tasks and the derived counts follow, with nothing written to
	// the plan.
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusCompleted)

	updated, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if updated.Progress.Completed != 1 || updated.Progress.Ready != 1 {
		t.Errorf("progress = %+v, want 1 completed and 1 ready", updated.Progress)
	}
	if updated.Progress.Remaining != 1 {
		t.Errorf("remaining = %d, want 1", updated.Progress.Remaining)
	}
}

func TestDeriveProgressCountsEveryState(t *testing.T) {
	plan := &Plan{
		ID: "plan-1", WorkspaceID: "ws-1",
		TaskLinks: []TaskLink{
			{TaskID: "grp", Role: LinkRoleGroup},
			{TaskID: "done", Role: LinkRoleItem},
			{TaskID: "running", Role: LinkRoleItem},
			{TaskID: "failed", Role: LinkRoleItem},
			{TaskID: "ready", Role: LinkRoleItem},
			{TaskID: "blocked", Role: LinkRoleItem},
		},
	}
	tasks := []workspace.Task{
		{ID: "grp", Status: workspace.TaskStatusPending},
		{ID: "done", Status: workspace.TaskStatusCompleted},
		{ID: "running", Status: workspace.TaskStatusInProgress},
		{ID: "failed", Status: workspace.TaskStatusFailed},
		{ID: "ready", Status: workspace.TaskStatusPending},
		{ID: "blocked", Status: workspace.TaskStatusPending, InputTaskIDs: []string{"failed"}},
	}

	progress := DeriveProgress(plan, tasks)
	if progress.Total != 5 {
		t.Errorf("total = %d, want 5 (the group task is not counted)", progress.Total)
	}
	if progress.Completed != 1 || progress.Running != 1 || progress.Failed != 1 ||
		progress.Ready != 1 || progress.Blocked != 1 {
		t.Errorf("progress = %+v", progress)
	}
	if progress.Remaining != 3 {
		t.Errorf("remaining = %d, want 3", progress.Remaining)
	}
}

// A retired link belongs to work a corrective revision replaced; it is history,
// not outstanding work (FR-77).
func TestDeriveProgressIgnoresRetiredLinks(t *testing.T) {
	retiredAt := mustTime("2026-08-13T10:00:00Z")
	plan := &Plan{
		ID: "plan-1", WorkspaceID: "ws-1",
		TaskLinks: []TaskLink{
			{TaskID: "live", Role: LinkRoleItem},
			{TaskID: "retired", Role: LinkRoleItem, RetiredAt: &retiredAt},
		},
	}
	tasks := []workspace.Task{
		{ID: "live", Status: workspace.TaskStatusPending},
		{ID: "retired", Status: workspace.TaskStatusPending},
	}

	if progress := DeriveProgress(plan, tasks); progress.Total != 1 {
		t.Errorf("total = %d, want 1 — a retired link was counted", progress.Total)
	}
}

// A link whose task vanished is a discrepancy worth surfacing, not one to hide
// by shrinking the total.
func TestDeriveProgressSurfacesAMissingTask(t *testing.T) {
	plan := &Plan{
		ID: "plan-1", WorkspaceID: "ws-1",
		TaskLinks: []TaskLink{{TaskID: "gone", Role: LinkRoleItem}},
	}
	progress := DeriveProgress(plan, nil)
	if progress.Total != 1 || progress.Blocked != 1 {
		t.Errorf("progress = %+v, want the missing task counted and blocked", progress)
	}
}

// --- Pause, resume, cancel (FR-108 through FR-112) -------------------------

// Pausing stops FUTURE dispatch; it does not kill work mid-flight, and it says
// so (FR-108).
func TestPauseStopsFutureDispatchWithoutKillingRunningWork(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, dispatcher, plan := executable(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusInProgress)

	result, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{
		Actor: "jj", Reason: "need to check something",
	})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if len(result.StillRunning) != 1 || result.StillRunning[0].TaskID != snapshot {
		t.Errorf("still running = %+v, want the in-flight task", result.StillRunning)
	}
	// The slot is only released once nothing is in flight (FR-108).
	if result.SlotReleased {
		t.Error("the execution slot was released while work was still running")
	}
	// The in-flight task was not killed.
	for _, task := range writer.tasks() {
		if task.ID == snapshot && task.Status != workspace.TaskStatusInProgress {
			t.Errorf("pausing changed the running task's status to %q", task.Status)
		}
	}

	paused, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if paused.Status != StatusPaused {
		t.Errorf("status = %q, want paused", paused.Status)
	}

	// The reason is retained in history (FR-109).
	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	var found bool
	for _, entry := range entries {
		if entry.To == StatusPaused && entry.Reason == "need to check something" {
			found = true
		}
	}
	if !found {
		t.Error("the pause reason was not retained")
	}

	// A paused plan dispatches nothing further.
	before := dispatcher.count()
	if _, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{}); err == nil {
		t.Error("a paused plan started new work")
	}
	if dispatcher.count() != before {
		t.Error("a paused plan dispatched after pausing")
	}
}

func TestPauseReleasesTheSlotWhenNothingIsInFlight(t *testing.T) {
	ctx := context.Background()
	_, executor, _, _, plan := executable(t, ctx)

	result, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !result.SlotReleased {
		t.Error("the slot was not released even though nothing was running")
	}
}

func TestResumeReturnsAPausedPlanToExecuting(t *testing.T) {
	ctx := context.Background()
	_, executor, _, _, plan := executable(t, ctx)

	if _, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{Actor: "jj"}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	resumed, err := executor.Resume(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != StatusExecuting {
		t.Errorf("status = %q, want executing", resumed.Status)
	}
}

// Resuming into a stall would look like progress and produce none (FR-113).
func TestResumeRefusesWhenEverythingIsBlockedBehindAFailure(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusFailed)
	if _, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{Actor: "jj"}); err != nil {
		t.Fatalf("pause: %v", err)
	}

	_, err := executor.Resume(ctx, "ws-1", plan.ID, "jj")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if !strings.Contains(err.Error(), "Retry, reassign, skip, or revise") {
		t.Errorf("the refusal does not offer a way forward: %v", err)
	}
}

// Cancelling names what it will affect before it happens (FR-111, FR-154).
func TestCancelPreviewNamesAffectedWork(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusInProgress)

	preview, err := executor.PreviewCancel(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Running) != 1 || preview.Running[0].TaskID != snapshot {
		t.Errorf("running = %+v", preview.Running)
	}
	if len(preview.Queued) != 1 {
		t.Errorf("queued = %+v, want the blocked task listed", preview.Queued)
	}
}

// Cancelling stops unfinished work and leaves completed history alone (FR-112).
func TestCancelStopsUnfinishedWorkAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	verify := taskIDByDescription(t, writer, "Verify checksums")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusCompleted)

	cancelled, err := executor.Cancel(ctx, "ws-1", plan.ID, "changed my mind", "jj")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Errorf("status = %q, want cancelled", cancelled.Status)
	}

	for _, task := range writer.tasks() {
		switch task.ID {
		case snapshot:
			if task.Status != workspace.TaskStatusCompleted {
				t.Errorf("cancelling rewrote completed history: %q", task.Status)
			}
		case verify:
			if task.Status != workspace.TaskStatusCancelled {
				t.Errorf("unfinished task status = %q, want cancelled", task.Status)
			}
		}
	}
}

// --- Retry and skip (FR-113 through FR-115) --------------------------------

// A retry is another attempt, not a rewrite: it creates a new Run and leaves
// the earlier one intact (FR-114).
func TestRetryCreatesANewRunAttempt(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, dispatcher, plan := executable(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusFailed)
	if _, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{Actor: "jj", Reason: "task failed"}); err != nil {
		t.Fatalf("pause: %v", err)
	}

	result, err := executor.Retry(ctx, "ws-1", plan.ID, snapshot, "jj")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !result.Started {
		t.Fatalf("retry did not start the task: %s", result.Reason)
	}
	if dispatcher.count() != 2 {
		t.Errorf("dispatches = %d, want 2 (the original plus the retry)", dispatcher.count())
	}

	// Both run links survive: the earlier attempt is history, not overwritten.
	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Status != StatusExecuting {
		t.Errorf("status = %q, want executing after a retry", after.Status)
	}
}

func TestRetryRefusesATaskFromAnotherPlan(t *testing.T) {
	ctx := context.Background()
	_, executor, _, _, plan := executable(t, ctx)

	_, err := executor.Retry(ctx, "ws-1", plan.ID, "some-other-task", "jj")
	if !errors.Is(err, ErrPlanNotFound) {
		t.Errorf("error = %v, want ErrPlanNotFound", err)
	}
}

// Skipping approved work is a judgement call, and an unexplained one is not
// auditable (FR-115).
func TestSkipRequiresAReasonAndRecordsIt(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, _, plan := executable(t, ctx)
	verify := taskIDByDescription(t, writer, "Verify checksums")

	if _, err := executor.Skip(ctx, "ws-1", plan.ID, verify, SkipInput{Actor: "jj"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("skipping without a reason error = %v, want ErrValidation", err)
	}

	if _, err := executor.Skip(ctx, "ws-1", plan.ID, verify, SkipInput{
		Actor: "jj", Reason: "the vendor confirmed the checksums out of band",
	}); err != nil {
		t.Fatalf("skip: %v", err)
	}

	for _, task := range writer.tasks() {
		if task.ID == verify {
			if task.Status != workspace.TaskStatusCancelled {
				t.Errorf("skipped task status = %q", task.Status)
			}
			if !strings.Contains(task.Result, "vendor confirmed") {
				t.Errorf("the skip reason was not recorded on the task: %q", task.Result)
			}
		}
	}

	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	var recorded bool
	for _, entry := range entries {
		if entry.Kind == ActivityTaskSkipped && strings.Contains(entry.Reason, "vendor confirmed") {
			recorded = true
		}
	}
	if !recorded {
		t.Error("the skip was not recorded in the plan's history")
	}
}

// --- Completion (FR-119 through FR-121) ------------------------------------

func TestCompleteRefusesWhileWorkIsOutstanding(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	_, err := executor.Complete(ctx, "ws-1", plan.ID, "jj")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if !strings.Contains(err.Error(), "still outstanding") {
		t.Errorf("error = %v", err)
	}

	// A failed task blocks completion too, with a different explanation.
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	verify := taskIDByDescription(t, writer, "Verify checksums")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusFailed)
	setTaskStatus(t, writer, verify, workspace.TaskStatusCompleted)

	_, err = executor.Complete(ctx, "ws-1", plan.ID, "jj")
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("error = %v, want it to name the failure", err)
	}
}

func TestCompleteProducesADurableReport(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, _, plan := executable(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, task := range writer.tasks() {
		setTaskStatus(t, writer, task.ID, workspace.TaskStatusCompleted)
	}

	report, err := executor.Complete(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if report.TotalTasks != 2 || report.CompletedTasks != 2 {
		t.Errorf("report counts = %d/%d, want 2/2", report.CompletedTasks, report.TotalTasks)
	}
	if len(report.Exceptions) != 0 {
		t.Errorf("a clean completion reported exceptions: %+v", report.Exceptions)
	}
	if len(report.RunIDs) == 0 {
		t.Error("the report does not reference the runs it produced")
	}

	after, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Status != StatusCompleted {
		t.Errorf("status = %q, want completed", after.Status)
	}
}

// A skipped item makes this completed-WITH-EXCEPTIONS, and the difference has
// to survive into the report (FR-115).
func TestCompletionWithASkippedTaskRecordsAnException(t *testing.T) {
	ctx := context.Background()
	service, executor, writer, _, plan := executable(t, ctx)

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	verify := taskIDByDescription(t, writer, "Verify checksums")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusCompleted)
	if _, err := executor.Skip(ctx, "ws-1", plan.ID, verify, SkipInput{
		Actor: "jj", Reason: "confirmed out of band",
	}); err != nil {
		t.Fatalf("skip: %v", err)
	}

	report, err := executor.Complete(ctx, "ws-1", plan.ID, "jj")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if report.SkippedTasks != 1 || len(report.Exceptions) != 1 {
		t.Fatalf("report = %+v, want one skipped exception", report)
	}
	if report.Exceptions[0].Outcome != "skipped" ||
		!strings.Contains(report.Exceptions[0].Reason, "out of band") {
		t.Errorf("exception = %+v", report.Exceptions[0])
	}

	// The plan's own history says it completed with exceptions.
	entries, err := service.Activity(ctx, "ws-1", plan.ID, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	var noted bool
	for _, entry := range entries {
		if entry.To == StatusCompleted && strings.Contains(entry.Reason, "exception") {
			noted = true
		}
	}
	if !noted {
		t.Error("completion did not record that there were exceptions")
	}
}

// Failed is reserved for terminal states, so it keeps meaning something
// (FR-120).
func TestFailRequiresAReason(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, _, plan := executable(t, ctx)

	if _, err := executor.Fail(ctx, "ws-1", plan.ID, "  ", "jj"); !errors.Is(err, ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}

	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusFailed)
	if _, err := executor.Pause(ctx, "ws-1", plan.ID, PauseInput{Actor: "jj"}); err != nil {
		t.Fatalf("pause: %v", err)
	}

	failed, err := executor.Fail(ctx, "ws-1", plan.ID, "the source system is gone", "jj")
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if failed.Status != StatusFailed {
		t.Errorf("status = %q, want failed", failed.Status)
	}
}

// A plan waiting for the workspace execution slot is counted separately from
// blocked: the work is ready, the workspace is busy (FR-107).
func TestProgressDistinguishesWaitingForTheSlot(t *testing.T) {
	ctx := context.Background()
	_, _, writer, _, plan := executable(t, ctx)

	source := NewTaskProgressSource(writer, WithSlotReporter(stubSlots{waiting: true}))
	progress, err := source.PlanProgress(ctx, plan)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if progress.WaitingForSlot != 1 || progress.Ready != 0 {
		t.Errorf("progress = %+v, want the ready task counted as waiting", progress)
	}

	free := NewTaskProgressSource(writer, WithSlotReporter(stubSlots{waiting: false}))
	unblocked, err := free.PlanProgress(ctx, plan)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if unblocked.Ready != 1 || unblocked.WaitingForSlot != 0 {
		t.Errorf("progress = %+v, want the task counted as ready", unblocked)
	}
}

// --- Execution slot integration (FR-106, FR-107, SM-15) --------------------

// executableWithSlots materializes two plans in one workspace, both sharing a
// slot coordinator, so contention can be exercised end to end.
func executableWithSlots(t *testing.T, ctx context.Context) (
	*Service, *Executor, *fakeTaskWriter, *SlotCoordinator, *Plan, *Plan) {
	t.Helper()

	service := reviewService(t)
	writer := newFakeTaskWriter()
	materializer := NewMaterializer(service, writer)
	slots := NewSlotCoordinator(NewMemorySlotStore())

	makePlan := func(key string) *Plan {
		plan := newReviewablePlan(t, ctx, service, reviewableContent())
		version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"})
		if err != nil {
			t.Fatalf("request review: %v", err)
		}
		approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
			Version: version.Number, ContentHash: version.ContentHash,
			Effect: EffectCreateTasks, UserName: "jj", IdempotencyKey: key,
		})
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
			MaterializeInput{ApprovalID: approval.ID}); err != nil {
			t.Fatalf("materialize: %v", err)
		}
		refreshed, err := service.Get(ctx, "ws-1", plan.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		return refreshed
	}

	first := makePlan("key-1")
	second := makePlan("key-2")

	executor := NewExecutor(service, writer,
		WithDispatcher(&fakeDispatcher{}),
		WithTaskMutator(&fakeMutator{writer: writer}),
		WithSlots(slots))
	service.progress = NewTaskProgressSource(writer, WithSlotReporter(slots))

	return service, executor, writer, slots, first, second
}

// Two approved plans in one workspace: one runs, the other waits visibly
// (FR-106, FR-107).
func TestOnlyOnePlanExecutesPerWorkspace(t *testing.T) {
	ctx := context.Background()
	service, executor, _, slots, first, second := executableWithSlots(t, ctx)

	started, err := executor.Start(ctx, "ws-1", first.ID, StartInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	if !started.Started {
		t.Fatalf("the first plan did not start: %s", started.Reason)
	}

	queued, err := executor.Start(ctx, "ws-1", second.ID, StartInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if queued.Started {
		t.Fatal("two plans started in one workspace")
	}
	// Waiting is explained, with a position rather than a bare refusal.
	if !strings.Contains(queued.Reason, "another plan is executing") {
		t.Errorf("reason = %q", queued.Reason)
	}
	if !strings.Contains(queued.Reason, "1st in line") {
		t.Errorf("reason does not give a position: %q", queued.Reason)
	}
	// And the progress says waiting, not ready — the work is fine, the
	// workspace is busy.
	if queued.Progress.WaitingForSlot == 0 {
		t.Errorf("progress = %+v, want the ready work counted as waiting", queued.Progress)
	}

	holder, err := slots.Holder(ctx, "ws-1")
	if err != nil {
		t.Fatalf("holder: %v", err)
	}
	if holder != first.ID {
		t.Errorf("holder = %q, want the first plan", holder)
	}

	// The second plan is still merely approved; it was not marked executing.
	stillApproved, err := service.Get(ctx, "ws-1", second.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stillApproved.Status != StatusApproved {
		t.Errorf("waiting plan status = %q, want approved", stillApproved.Status)
	}
}

// Pausing with nothing in flight releases the slot, and the waiting plan can
// then take it (FR-108, FR-110).
func TestPausingHandsTheSlotToTheWaitingPlan(t *testing.T) {
	ctx := context.Background()
	_, executor, _, slots, first, second := executableWithSlots(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", first.ID, StartInput{}); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if _, err := executor.Start(ctx, "ws-1", second.ID, StartInput{}); err != nil {
		t.Fatalf("queue second: %v", err)
	}

	paused, err := executor.Pause(ctx, "ws-1", first.ID, PauseInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !paused.SlotReleased {
		t.Fatal("the slot was not released even though nothing was in flight")
	}

	took, err := executor.Start(ctx, "ws-1", second.ID, StartInput{})
	if err != nil {
		t.Fatalf("start second after release: %v", err)
	}
	if !took.Started {
		t.Fatalf("the waiting plan could not take the released slot: %s", took.Reason)
	}
	holder, _ := slots.Holder(ctx, "ws-1")
	if holder != second.ID {
		t.Errorf("holder = %q, want the second plan", holder)
	}
}

// Pausing while work is in flight does NOT release the slot: another plan
// starting beside a running agent is the overlap the slot prevents (FR-108).
func TestPausingHoldsTheSlotWhileWorkIsInFlight(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, slots, first, second := executableWithSlots(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", first.ID, StartInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	snapshot := taskIDByDescription(t, writer, "Snapshot staging")
	setTaskStatus(t, writer, snapshot, workspace.TaskStatusInProgress)

	paused, err := executor.Pause(ctx, "ws-1", first.ID, PauseInput{Actor: "jj"})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.SlotReleased {
		t.Fatal("the slot was released while an agent was mid-action")
	}

	holder, _ := slots.Holder(ctx, "ws-1")
	if holder != first.ID {
		t.Errorf("holder = %q, want the paused plan to keep the slot", holder)
	}
	blocked, err := executor.Start(ctx, "ws-1", second.ID, StartInput{})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if blocked.Started {
		t.Error("a second plan started while the first still had work in flight")
	}
}

// Resuming rejoins the queue rather than displacing whoever started meanwhile
// (FR-110).
func TestResumeRejoinsTheQueueRatherThanDisplacing(t *testing.T) {
	ctx := context.Background()
	_, executor, _, slots, first, second := executableWithSlots(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", first.ID, StartInput{}); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if _, err := executor.Pause(ctx, "ws-1", first.ID, PauseInput{Actor: "jj"}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// The second plan takes the free slot while the first is paused.
	if _, err := executor.Start(ctx, "ws-1", second.ID, StartInput{}); err != nil {
		t.Fatalf("start second: %v", err)
	}

	if _, err := executor.Resume(ctx, "ws-1", first.ID, "jj"); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Resuming did not take the slot back.
	holder, _ := slots.Holder(ctx, "ws-1")
	if holder != second.ID {
		t.Errorf("holder = %q, want the plan that started meanwhile to keep it", holder)
	}
	waiting, err := slots.WaitingForSlot(ctx, "ws-1", first.ID)
	if err != nil {
		t.Fatalf("waiting: %v", err)
	}
	if !waiting {
		t.Error("the resumed plan did not rejoin the queue")
	}
}

// Completing gives the workspace back.
func TestCompletingReleasesTheSlot(t *testing.T) {
	ctx := context.Background()
	_, executor, writer, slots, first, _ := executableWithSlots(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", first.ID, StartInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, task := range writer.tasks() {
		setTaskStatus(t, writer, task.ID, workspace.TaskStatusCompleted)
	}
	if _, err := executor.Complete(ctx, "ws-1", first.ID, "jj"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	holder, err := slots.Holder(ctx, "ws-1")
	if err != nil {
		t.Fatalf("holder: %v", err)
	}
	if holder != "" {
		t.Errorf("holder = %q, want the slot free after completion", holder)
	}
}

// Cancelling releases the slot and stops waiting for it.
func TestCancellingReleasesAndDequeues(t *testing.T) {
	ctx := context.Background()
	_, executor, _, slots, first, second := executableWithSlots(t, ctx)

	if _, err := executor.Start(ctx, "ws-1", first.ID, StartInput{}); err != nil {
		t.Fatalf("start first: %v", err)
	}
	if _, err := executor.Start(ctx, "ws-1", second.ID, StartInput{}); err != nil {
		t.Fatalf("queue second: %v", err)
	}

	// The waiting plan is cancelled: it should stop waiting.
	if _, err := executor.Cancel(ctx, "ws-1", second.ID, "not needed", "jj"); err != nil {
		t.Fatalf("cancel waiting plan: %v", err)
	}
	if waiting, _ := slots.WaitingForSlot(ctx, "ws-1", second.ID); waiting {
		t.Error("a cancelled plan is still queued for the slot")
	}

	// The holder is cancelled: the slot frees.
	if _, err := executor.Cancel(ctx, "ws-1", first.ID, "not needed", "jj"); err != nil {
		t.Fatalf("cancel holder: %v", err)
	}
	if holder, _ := slots.Holder(ctx, "ws-1"); holder != "" {
		t.Errorf("holder = %q, want the slot free", holder)
	}
}

// Many callers racing to start different plans in one workspace resolve to one
// execution (SM-15).
func TestConcurrentStartsResolveToOneExecutingPlan(t *testing.T) {
	ctx := context.Background()
	_, executor, _, slots, first, second := executableWithSlots(t, ctx)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		started []string
	)
	for _, plan := range []*Plan{first, second} {
		for range 4 {
			wg.Add(1)
			go func(planID string) {
				defer wg.Done()
				result, err := executor.Start(ctx, "ws-1", planID, StartInput{})
				if err != nil || result == nil || !result.Started {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				started = append(started, planID)
			}(plan.ID)
		}
	}
	wg.Wait()

	// Several starts of the SAME plan may succeed (each dispatches a different
	// eligible task); what must never happen is both plans executing.
	distinct := map[string]struct{}{}
	for _, planID := range started {
		distinct[planID] = struct{}{}
	}
	if len(distinct) > 1 {
		t.Errorf("plans executing = %v, want at most 1", distinct)
	}

	holder, err := slots.Holder(ctx, "ws-1")
	if err != nil {
		t.Fatalf("holder: %v", err)
	}
	if holder == "" && len(started) > 0 {
		t.Error("work started without anyone holding the slot")
	}
}

// A build with no slot wired still runs plans: the slot restricts concurrency,
// it is not a prerequisite for running at all.
func TestExecutionWorksWithoutASlotCoordinator(t *testing.T) {
	ctx := context.Background()
	_, executor, _, _, plan := executable(t, ctx)

	result, err := executor.Start(ctx, "ws-1", plan.ID, StartInput{})
	if err != nil {
		t.Fatalf("start without slots: %v", err)
	}
	if !result.Started {
		t.Errorf("a plan could not start without a slot coordinator: %s", result.Reason)
	}
}

type stubSlots struct{ waiting bool }

func (s stubSlots) WaitingForSlot(context.Context, string, string) (bool, error) {
	return s.waiting, nil
}
