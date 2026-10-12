package assistantops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTasks is the host's task owner as a test sees it: the tasks that exist,
// and a Start that counts how often it was really asked.
type fakeTasks struct {
	mu       sync.Mutex
	tasks    map[string]TaskState
	readErr  error
	startErr error
	starts   int
	// onStart runs inside Start, before it answers, with the lock released.
	onStart func(taskID string)
	// missNext makes that many reads answer "missing" although the task is
	// there, as a workspace caught between two writes might.
	missNext int
}

func (h *fakeTasks) Task(_ context.Context, _ string, taskID string) (TaskState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readErr != nil {
		return TaskState{}, h.readErr
	}
	if h.missNext > 0 {
		h.missNext--
		return TaskState{}, fmt.Errorf("%w: %s", ErrTaskMissing, taskID)
	}
	state, ok := h.tasks[taskID]
	if !ok {
		return TaskState{}, fmt.Errorf("%w: %s", ErrTaskMissing, taskID)
	}
	return state, nil
}

func (h *fakeTasks) Start(_ context.Context, _ string, taskID string) error {
	h.mu.Lock()
	h.starts++
	err, hook := h.startErr, h.onStart
	h.mu.Unlock()
	if hook != nil {
		hook(taskID)
	}
	return err
}

func (h *fakeTasks) put(state TaskState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks[state.TaskID] = state
}

func (h *fakeTasks) change(taskID string, change func(*TaskState)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.tasks[taskID]
	change(&state)
	h.tasks[taskID] = state
}

func (h *fakeTasks) remove(taskID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tasks, taskID)
}

func (h *fakeTasks) started() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.starts
}

func (h *fakeTasks) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.tasks)
}

// taskFixture is a fixture whose service can commit and start tasks.
type taskFixture struct {
	*fixture
	tasks *fakeTasks
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	f := &taskFixture{fixture: newFixture(t), tasks: &fakeTasks{tasks: map[string]TaskState{}}}
	f.service.SetTaskHost(f.tasks)
	return f
}

// restart replaces the service with a new one over the same records, as a
// process restart would: nothing it was carrying out is remembered.
func (f *taskFixture) restart() {
	f.service = NewService(f.store, f.guard)
	f.service.now = func() time.Time { return f.clock }
	f.service.SetTaskHost(f.tasks)
}

func (f *taskFixture) prepared() *Snapshot {
	f.t.Helper()
	snapshot, err := f.prepare("tab-a", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func (f *taskFixture) claim(snapshot *Snapshot, taskID string, change func(*TaskCreate)) (CreateClaim, error) {
	req := TaskCreate{OperationID: snapshot.ID, DraftRevision: snapshot.Draft.Revision, WorkspaceID: "ws-album",
		AgentName: snapshot.Draft.AgentName, AgentNodeID: snapshot.Draft.AgentNodeID, TaskID: taskID}
	if change != nil {
		change(&req)
	}
	return f.service.ClaimTaskCreate(f.t.Context(), req)
}

// reviewed prepares a draft and carries its save through to a confirmed task,
// "task-1".
func (f *taskFixture) reviewed() *Snapshot {
	f.t.Helper()
	const taskID = "task-1"
	snapshot := f.prepared()
	claim, err := f.claim(snapshot, taskID, nil)
	if err != nil || claim.Existing {
		f.t.Fatalf("claim: %+v %v", claim, err)
	}
	f.tasks.put(TaskState{TaskID: taskID, OperationID: snapshot.ID, AgentName: claim.AgentName, AgentNodeID: claim.AgentNodeID, Status: "pending", Startable: true})
	if err := f.service.ConfirmTaskCreate(f.t.Context(), snapshot.ID, taskID); err != nil {
		f.t.Fatal(err)
	}
	return f.read(snapshot.ID)
}

func (f *taskFixture) read(id string) *Snapshot {
	f.t.Helper()
	snapshot, err := f.service.Snapshot(f.t.Context(), id, "tab-a")
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

func receiptKinds(snapshot *Snapshot) []ReceiptKind {
	kinds := make([]ReceiptKind, 0, len(snapshot.Receipts))
	for _, receipt := range snapshot.Receipts {
		kinds = append(kinds, receipt.Kind)
	}
	return kinds
}

func countReceipts(snapshot *Snapshot, kind ReceiptKind) int {
	count := 0
	for _, receipt := range snapshot.Receipts {
		if receipt.Kind == kind {
			count++
		}
	}
	return count
}

func lastReceipt(snapshot *Snapshot, kind ReceiptKind) Receipt {
	var found Receipt
	for _, receipt := range snapshot.Receipts {
		if receipt.Kind == kind {
			found = receipt
		}
	}
	return found
}

func TestClaimTaskCreate_ReservesOneTaskAndConfirmsItFromTheTaskItself(t *testing.T) {
	f := newTaskFixture(t)
	prepared := f.prepared()

	// The human changed the assignee in the form to another roster agent.
	claim, err := f.claim(prepared, "task-1", func(req *TaskCreate) { req.AgentName, req.AgentNodeID = "engineer", "" })
	if err != nil || claim.Existing || claim.TaskID != "task-1" || claim.AgentName != "Engineer" || claim.AgentNodeID != "Engineer-node-1" {
		t.Fatalf("claim must return the roster's own instance: %+v %v", claim, err)
	}
	// Reserved is not created: a reader sees no task while the write is in flight.
	during := f.read(prepared.ID)
	if during.Status != StatusPrepared || during.Task != nil || countReceipts(during, ReceiptTaskCreated) != 0 {
		t.Fatalf("a reservation must not read as a task: %+v", during)
	}

	f.tasks.put(TaskState{TaskID: "task-1", OperationID: prepared.ID, AgentName: "Engineer", AgentNodeID: "Engineer-node-1", Status: "pending", Startable: true})
	if err := f.service.ConfirmTaskCreate(t.Context(), prepared.ID, "task-1"); err != nil {
		t.Fatal(err)
	}
	after := f.read(prepared.ID)
	if after.Status != StatusReviewed || after.Task == nil || after.Task.TaskID != "task-1" || after.Task.RunID != "" || after.Task.ClaimedAt != nil {
		t.Fatalf("confirmed create: %+v", after)
	}
	if countReceipts(after, ReceiptTaskCreated) != 1 || lastReceipt(after, ReceiptTaskCreated).Ref != "task-1" {
		t.Fatalf("receipts: %v", receiptKinds(after))
	}
	if after.Context.AgentName != "Engineer" || after.Context.AgentNodeID != "Engineer-node-1" {
		t.Fatalf("the reviewed assignee is the saved one: %+v", after.Context)
	}
	if after.Pending != 0 {
		t.Fatalf("a saved task leaves no draft step pending: %+v", after.Commands)
	}
	// Saving authorizes creation only.
	if f.tasks.started() != 0 || countReceipts(after, ReceiptStartAccepted) != 0 {
		t.Fatal("a save must not start the task")
	}

	// The same save again, a second tab, or a retry after a lost response.
	repeat, err := f.claim(prepared, "task-2", nil)
	if err != nil || !repeat.Existing || repeat.TaskID != "task-1" || repeat.AgentName != "Engineer" {
		t.Fatalf("a repeat must return the first task: %+v %v", repeat, err)
	}
	if again := f.read(prepared.ID); again.Revision != after.Revision || countReceipts(again, ReceiptTaskCreated) != 1 {
		t.Fatalf("a repeat must write nothing: %+v", again)
	}
}

func TestClaimTaskCreate_ConcurrentSavesReserveExactlyOneTask(t *testing.T) {
	f := newTaskFixture(t)
	prepared := f.prepared()
	const savers = 12
	var won, inFlight atomic.Int32
	var winner atomic.Value
	var wg sync.WaitGroup
	for i := range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			taskID := fmt.Sprintf("task-%d", i)
			claim, err := f.claim(prepared, taskID, nil)
			switch {
			case err == nil && !claim.Existing:
				won.Add(1)
				winner.Store(taskID)
			case errors.Is(err, ErrCreateInFlight) || errors.Is(err, ErrConflict):
				inFlight.Add(1)
			default:
				t.Errorf("unexpected outcome: %+v %v", claim, err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || inFlight.Load() != savers-1 {
		t.Fatalf("exactly one save may reserve the task: won=%d refused=%d", won.Load(), inFlight.Load())
	}
	stored, err := f.store.Get(t.Context(), prepared.ID)
	if err != nil || stored.Task == nil || stored.Task.TaskID != winner.Load().(string) || stored.Status != StatusPrepared {
		t.Fatalf("stored reservation: %+v %v", stored, err)
	}
}

func TestClaimTaskCreate_RefusesWhatWasNotReviewed(t *testing.T) {
	cases := map[string]struct {
		arrange func(*taskFixture, *Snapshot)
		change  func(*TaskCreate)
		want    error
	}{
		"stale draft revision": {change: func(req *TaskCreate) { req.DraftRevision++ }, want: ErrConflict},
		"another workspace":    {change: func(req *TaskCreate) { req.WorkspaceID = "ws-other" }, want: ErrInvalidCommand},
		"no task identity":     {change: func(req *TaskCreate) { req.TaskID = " " }, want: ErrInvalidCommand},
		"unassigned":           {change: func(req *TaskCreate) { req.AgentName, req.AgentNodeID = "", "" }, want: ErrAssignee},
		"the word unassigned":  {change: func(req *TaskCreate) { req.AgentName, req.AgentNodeID = "Unassigned", "" }, want: ErrAssignee},
		// A namesake that is not in this workspace's roster, or the right name
		// with another instance's node, is never accepted.
		"agent outside the roster": {change: func(req *TaskCreate) { req.AgentName, req.AgentNodeID = "Drummer", "" }, want: ErrAssignee},
		"another instance's node":  {change: func(req *TaskCreate) { req.AgentNodeID = "Mixer-node-9" }, want: ErrAssignee},
		"agent removed since the draft": {arrange: func(f *taskFixture, _ *Snapshot) {
			f.guard.set(func(g *fakeGuard) { delete(g.roster["ws-album"], "Mixer") })
		}, want: ErrAssignee},
		"roster could not be read": {arrange: func(f *taskFixture, _ *Snapshot) {
			f.guard.set(func(g *fakeGuard) { g.agentErr = errors.New("disk") })
		}, want: ErrUnavailable},
		"expired": {arrange: func(f *taskFixture, _ *Snapshot) { f.clock = f.clock.Add(DefaultTTL + time.Minute) }, want: ErrExpired},
		"conversation deleted": {arrange: func(f *taskFixture, _ *Snapshot) {
			f.guard.set(func(g *fakeGuard) { g.conversation = fmt.Errorf("%w: gone", ErrInvalidated) })
		}, want: ErrInvalidated},
		"dismissed": {arrange: func(f *taskFixture, prepared *Snapshot) {
			if _, err := f.service.Cancel(f.t.Context(), prepared.ID, "tab-a", prepared.Revision); err != nil {
				f.t.Fatal(err)
			}
		}, want: ErrTransition},
		"another user": {arrange: func(f *taskFixture, _ *Snapshot) {
			f.guard.set(func(g *fakeGuard) { g.user = "someone-else" })
		}, want: ErrNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newTaskFixture(t)
			prepared := f.prepared()
			if tc.arrange != nil {
				tc.arrange(f, prepared)
			}
			claim, err := f.claim(prepared, "task-1", tc.change)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %+v %v, want %v", claim, err, tc.want)
			}
			stored, getErr := f.store.Get(t.Context(), prepared.ID)
			if getErr != nil || stored.Task != nil {
				t.Fatalf("a refused save must reserve nothing: %+v %v", stored, getErr)
			}
			// A wrong assignee asks for a corrected review; it ends nothing.
			if errors.Is(tc.want, ErrAssignee) && stored.Status != StatusPrepared {
				t.Fatalf("an assignee refusal must leave the draft open: %s", stored.Status)
			}
		})
	}

	t.Run("a navigation operation prepares no task", func(t *testing.T) {
		f := newTaskFixture(t)
		navigation := f.create("tab-a", true)
		_, err := f.service.ClaimTaskCreate(t.Context(), TaskCreate{OperationID: navigation.ID, DraftRevision: 1,
			WorkspaceID: "ws-album", AgentName: "Mixer", TaskID: "task-1"})
		if !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a host without tasks commits nothing", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		f.service.SetTaskHost(nil)
		if _, err := f.claim(prepared, "task-1", nil); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestReleaseTaskCreate_LetsTheSameReviewedFormBeSavedAgain(t *testing.T) {
	f := newTaskFixture(t)
	prepared := f.prepared()
	if _, err := f.claim(prepared, "task-1", nil); err != nil {
		t.Fatal(err)
	}
	// The workspace save failed: nothing was written.
	if err := f.service.ReleaseTaskCreate(t.Context(), prepared.ID, "task-1"); err != nil {
		t.Fatal(err)
	}
	released := f.read(prepared.ID)
	if released.Status != StatusPrepared || released.Task != nil || released.Draft == nil {
		t.Fatalf("released: %+v", released)
	}
	// Releasing twice, or releasing another request's reservation, changes nothing.
	if err := f.service.ReleaseTaskCreate(t.Context(), prepared.ID, "task-1"); err != nil {
		t.Fatal(err)
	}
	claim, err := f.claim(prepared, "task-2", nil)
	if err != nil || claim.Existing || claim.TaskID != "task-2" {
		t.Fatalf("second save: %+v %v", claim, err)
	}
	if err := f.service.ReleaseTaskCreate(t.Context(), prepared.ID, "task-1"); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.store.Get(t.Context(), prepared.ID)
	if stored.Task == nil || stored.Task.TaskID != "task-2" {
		t.Fatalf("a stale release must not drop the live reservation: %+v", stored.Task)
	}
}

func TestCreateRecovery_SettlesALostResponseFromTheTask(t *testing.T) {
	t.Run("task written, confirmation lost", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.tasks.put(TaskState{TaskID: "task-1", OperationID: prepared.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-1", Status: "pending", Startable: true})
		f.restart() // the process died before it could confirm

		recovered := f.read(prepared.ID)
		if recovered.Status != StatusReviewed || recovered.Task == nil || recovered.Task.TaskID != "task-1" || countReceipts(recovered, ReceiptTaskCreated) != 1 {
			t.Fatalf("the written task must be bound on the next read: %+v", recovered)
		}
		// The browser retries the save it never heard back from.
		retry, err := f.claim(prepared, "task-2", nil)
		if err != nil || !retry.Existing || retry.TaskID != "task-1" || f.tasks.count() != 1 {
			t.Fatalf("the retry must get the first task back: %+v %v", retry, err)
		}
	})

	t.Run("retry arrives before any read", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.tasks.put(TaskState{TaskID: "task-1", OperationID: prepared.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-1", Status: "pending", Startable: true})
		f.restart()
		retry, err := f.claim(prepared, "task-2", nil)
		if err != nil || !retry.Existing || retry.TaskID != "task-1" {
			t.Fatalf("got %+v %v", retry, err)
		}
	})

	t.Run("nothing written", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.restart() // died between the reservation and the workspace save

		recovered := f.read(prepared.ID)
		if recovered.Status != StatusPrepared || recovered.Task != nil || countReceipts(recovered, ReceiptTaskCreated) != 0 {
			t.Fatalf("a reservation with no task must be given back: %+v", recovered)
		}
		claim, err := f.claim(prepared, "task-2", nil)
		if err != nil || claim.Existing || claim.TaskID != "task-2" {
			t.Fatalf("the form must be savable again: %+v %v", claim, err)
		}
	})

	t.Run("a task with that identity but another stamp is not this operation's", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.tasks.put(TaskState{TaskID: "task-1", OperationID: "some-other-operation", Status: "pending", Startable: true})
		if err := f.service.ConfirmTaskCreate(t.Context(), prepared.ID, "task-1"); !errors.Is(err, ErrConflict) {
			t.Fatalf("confirm must check the stamp: %v", err)
		}
		if recovered := f.read(prepared.ID); recovered.Status != StatusPrepared || recovered.Task != nil {
			t.Fatalf("got %+v", recovered)
		}
	})

	t.Run("a write still in flight is left alone", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		// Same process, save not finished: a poll must not release it, and
		// "could not read the task" must not either.
		for range 3 {
			f.read(prepared.ID)
		}
		stored, _ := f.store.Get(t.Context(), prepared.ID)
		if stored.Task == nil || stored.Task.TaskID != "task-1" {
			t.Fatalf("an in-flight reservation was dropped: %+v", stored.Task)
		}
		if _, err := f.claim(prepared, "task-2", nil); !errors.Is(err, ErrCreateInFlight) {
			t.Fatalf("a second save during the first must wait: %v", err)
		}
		f.restart()
		f.tasks.mu.Lock()
		f.tasks.readErr = errors.New("disk")
		f.tasks.mu.Unlock()
		f.read(prepared.ID)
		stored, _ = f.store.Get(t.Context(), prepared.ID)
		if stored.Task == nil {
			t.Fatal("an unreadable task is not a missing task")
		}
	})
}

func TestStart_ClaimsOnceAndReportsExecutionOnlyFromTheTask(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()

	accepted, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != StatusStartRequested || accepted.Task.ClaimedAt == nil || accepted.Task.RunID != "" {
		t.Fatalf("accepted is not started: %+v %+v", accepted, accepted.Task)
	}
	if countReceipts(accepted, ReceiptStartAccepted) != 1 || countReceipts(accepted, ReceiptRunEstablished) != 0 || f.tasks.started() != 1 {
		t.Fatalf("receipts %v starts %d", receiptKinds(accepted), f.tasks.started())
	}

	// Duplicate delivery, a reload, a transport retry and another tab.
	for _, tab := range []string{"tab-a", "tab-a", "tab-b", ""} {
		again, err := f.service.Start(t.Context(), reviewed.ID, tab)
		if err != nil || again.Status != StatusStartRequested {
			t.Fatalf("repeat from %q: %+v %v", tab, again, err)
		}
	}
	if f.tasks.started() != 1 {
		t.Fatalf("Start was delivered %d times", f.tasks.started())
	}

	// Still waiting inside the grace period: nothing is invented.
	f.clock = f.clock.Add(DefaultStartGrace / 2)
	if waiting := f.read(reviewed.ID); waiting.Status != StatusStartRequested {
		t.Fatalf("waiting: %s", waiting.Status)
	}

	// The task itself reports a run.
	f.tasks.change("task-1", func(state *TaskState) {
		state.Ran, state.Startable, state.RunID, state.Status = true, false, "run-1", "in_progress"
	})
	started := f.read(reviewed.ID)
	if started.Status != StatusStarted || started.Task.RunID != "run-1" {
		t.Fatalf("started: %+v %+v", started, started.Task)
	}
	if countReceipts(started, ReceiptRunEstablished) != 1 || lastReceipt(started, ReceiptRunEstablished).Ref != "run-1" {
		t.Fatalf("receipts: %v", receiptKinds(started))
	}

	// Completed, then rerun by the user from the task's own controls: the
	// operation keeps its run and never starts the task again.
	f.tasks.change("task-1", func(state *TaskState) { state.RunID, state.Status = "run-2", "completed" })
	later, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
	if err != nil || later.Status != StatusStarted || later.Task.RunID != "run-1" || f.tasks.started() != 1 {
		t.Fatalf("a completed task must not be replayed: %+v %+v %v starts=%d", later, later.Task, err, f.tasks.started())
	}
	if countReceipts(later, ReceiptRunEstablished) != 1 {
		t.Fatalf("receipts: %v", receiptKinds(later))
	}
}

func TestStart_ConcurrentConfirmationsDeliverOneStart(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	// The real start owner makes the task run; do the same so late arrivals
	// see a running task, as they would.
	f.tasks.onStart = func(taskID string) {
		f.tasks.change(taskID, func(state *TaskState) {
			state.Ran, state.Startable, state.RunID, state.Status = true, false, "run-1", "in_progress"
		})
	}
	const clicks = 16
	var wg sync.WaitGroup
	for i := range clicks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot, err := f.service.Start(t.Context(), reviewed.ID, fmt.Sprintf("tab-%d", i))
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Errorf("click %d: %v", i, err)
			}
			if err == nil && snapshot.Status != StatusStartRequested && snapshot.Status != StatusStarted {
				t.Errorf("click %d: %s", i, snapshot.Status)
			}
		}()
	}
	wg.Wait()
	if f.tasks.started() != 1 {
		t.Fatalf("Start was delivered %d times", f.tasks.started())
	}
	final := f.read(reviewed.ID)
	if final.Status != StatusStarted || final.Task.RunID != "run-1" || countReceipts(final, ReceiptRunEstablished) != 1 || countReceipts(final, ReceiptStartAccepted) > 1 {
		t.Fatalf("final: %+v %v", final.Task, receiptKinds(final))
	}
}

func TestStart_RefusalsKeepTheTaskAndSayWhy(t *testing.T) {
	t.Run("the start owner refuses", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.tasks.startErr = &StartRefusal{Reason: "Ori is resetting. Try again in a moment."}
		refused, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if !errors.Is(err, ErrStartRefused) || refused == nil {
			t.Fatalf("got %+v %v", refused, err)
		}
		if refused.Status != StatusReviewed || refused.Task.TaskID != "task-1" || refused.Task.ClaimedAt != nil {
			t.Fatalf("a refused start returns to the reviewed task: %+v %+v", refused, refused.Task)
		}
		receipt := lastReceipt(refused, ReceiptStartRefused)
		if receipt.Reason != "Ori is resetting. Try again in a moment." || countReceipts(refused, ReceiptStartAccepted) != 0 || countReceipts(refused, ReceiptRunEstablished) != 0 {
			t.Fatalf("receipts: %+v", refused.Receipts)
		}
		// Trying again is the same checked claim, and may now succeed.
		f.tasks.startErr = nil
		retried, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if err != nil || retried.Status != StatusStartRequested || f.tasks.started() != 2 {
			t.Fatalf("retry: %+v %v starts=%d", retried, err, f.tasks.started())
		}
	})

	t.Run("an internal error is not shown", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.tasks.startErr = errors.New("workspace ws-album not found: open /Users/x/secret.json: permission denied")
		refused, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if !errors.Is(err, ErrStartRefused) || refused.Status != StatusReviewed {
			t.Fatalf("got %+v %v", refused, err)
		}
		if reason := lastReceipt(refused, ReceiptStartRefused).Reason; reason != "Ori could not start the task right now." {
			t.Fatalf("reason leaked: %q", reason)
		}
	})

	t.Run("the assignee left the workspace", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.guard.set(func(g *fakeGuard) { delete(g.roster["ws-album"], "Mixer") })
		refused, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if !errors.Is(err, ErrAssignee) || refused == nil || refused.Status != StatusReviewed || f.tasks.started() != 0 {
			t.Fatalf("got %+v %v starts=%d", refused, err, f.tasks.started())
		}
		if reason := lastReceipt(refused, ReceiptStartRefused).Reason; reason != "Mixer is not in this workspace any more. Reassign the task, then start it." {
			t.Fatalf("reason: %q", reason)
		}
		// Removing an agent leaves its tasks unassigned: that is said as it is,
		// and the entry agent is not used instead.
		f.tasks.change("task-1", func(state *TaskState) { state.AgentName, state.AgentNodeID = "unassigned", "" })
		unassigned, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if !errors.Is(err, ErrAssignee) || f.tasks.started() != 0 ||
			lastReceipt(unassigned, ReceiptStartRefused).Reason != "The task has no agent now. Assign it to an agent in this workspace, then start it." {
			t.Fatalf("unassigned: %+v %v", unassigned.Receipts, err)
		}
		// The user reassigns the task itself; the task's assignee is what is checked.
		f.tasks.change("task-1", func(state *TaskState) { state.AgentName, state.AgentNodeID = "Engineer", "Engineer-node-1" })
		if started, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); err != nil || started.Status != StatusStartRequested || f.tasks.started() != 1 {
			t.Fatalf("after reassignment: %+v %v", started, err)
		}
	})

	t.Run("the roster could not be read", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.guard.set(func(g *fakeGuard) { g.agentErr = errors.New("disk") })
		if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
		if after := f.read(reviewed.ID); after.Status != StatusReviewed || countReceipts(after, ReceiptStartRefused) != 0 || f.tasks.started() != 0 {
			t.Fatalf("could-not-check must record nothing: %+v", after)
		}
	})

	t.Run("the task is not waiting to start", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.tasks.change("task-1", func(state *TaskState) { state.Startable, state.Status = false, "backlog" })
		refused, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if !errors.Is(err, ErrStartRefused) || refused.Status != StatusReviewed || f.tasks.started() != 0 {
			t.Fatalf("got %+v %v", refused, err)
		}
	})

	t.Run("a draft that was never saved", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.service.Start(t.Context(), prepared.ID, "tab-a"); !errors.Is(err, ErrTransition) || f.tasks.started() != 0 {
			t.Fatalf("got %v", err)
		}
		// A reservation whose write has not been confirmed is not a task either.
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Start(t.Context(), prepared.ID, "tab-a"); !errors.Is(err, ErrTransition) || f.tasks.started() != 0 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("ended operations start nothing", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.clock = f.clock.Add(DefaultTTL + time.Minute)
		if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); !errors.Is(err, ErrExpired) || f.tasks.started() != 0 {
			t.Fatalf("expired: %v", err)
		}
		g := newTaskFixture(t)
		other := g.reviewed()
		g.guard.set(func(guard *fakeGuard) { guard.owner.StateVersion++ })
		if _, err := g.service.Start(t.Context(), other.ID, "tab-a"); !errors.Is(err, ErrInvalidated) || g.tasks.started() != 0 {
			t.Fatalf("relationship changed: %v", err)
		}
	})
}

func TestStart_ManualStartWinsWithoutASecondRun(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	// The user pressed the task's own Start before the drawer's.
	f.tasks.change("task-1", func(state *TaskState) {
		state.Ran, state.Startable, state.RunID, state.Status = true, false, "run-manual", "in_progress"
	})
	observed, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
	if err != nil || observed.Status != StatusStarted || observed.Task.RunID != "run-manual" || observed.Task.ClaimedAt != nil {
		t.Fatalf("got %+v %+v %v", observed, observed.Task, err)
	}
	if f.tasks.started() != 0 || countReceipts(observed, ReceiptStartAccepted) != 0 || countReceipts(observed, ReceiptRunEstablished) != 1 {
		t.Fatalf("the operation must not claim a start it did not make: starts=%d %v", f.tasks.started(), receiptKinds(observed))
	}
}

func TestStartRecovery_AnUnprovenStartIsNeverRepeatedOnItsOwn(t *testing.T) {
	// claimOnly leaves the operation exactly as a process that died after the
	// claim and before the start owner answered would.
	claimOnly := func(f *taskFixture, id string) {
		f.t.Helper()
		op, err := f.store.Get(f.t.Context(), id)
		if err != nil {
			f.t.Fatal(err)
		}
		now := f.clock
		op.Status, op.Task.ClaimedAt, op.UpdatedAt = StatusStartRequested, &now, now
		if _, err := f.store.Update(f.t.Context(), op, op.Revision); err != nil {
			f.t.Fatal(err)
		}
	}

	t.Run("no outcome recorded and no run", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		claimOnly(f, reviewed.ID)
		f.restart()

		unresolved := f.read(reviewed.ID)
		if unresolved.Status != StatusUnresolved || unresolved.Task.TaskID != "task-1" || countReceipts(unresolved, ReceiptStartUnresolved) != 1 {
			t.Fatalf("got %+v %v", unresolved, receiptKinds(unresolved))
		}
		// Reads, however many, start nothing.
		for range 4 {
			f.read(reviewed.ID)
		}
		if f.tasks.started() != 0 {
			t.Fatal("recovery must not start the task")
		}
		// Only the user's explicit retry goes back through the claim.
		retried, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if err != nil || retried.Status != StatusStartRequested || f.tasks.started() != 1 || countReceipts(retried, ReceiptStartAccepted) != 1 {
			t.Fatalf("retry: %+v %v", retried, err)
		}
	})

	t.Run("no outcome recorded but the task ran", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		claimOnly(f, reviewed.ID)
		f.tasks.change("task-1", func(state *TaskState) { state.Ran, state.Startable, state.RunID = true, false, "run-1" })
		f.restart()
		started, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
		if err != nil || started.Status != StatusStarted || started.Task.RunID != "run-1" || f.tasks.started() != 0 {
			t.Fatalf("got %+v %v starts=%d", started, err, f.tasks.started())
		}
	})

	t.Run("unresolved, then the run appears", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		claimOnly(f, reviewed.ID)
		f.restart()
		if got := f.read(reviewed.ID); got.Status != StatusUnresolved {
			t.Fatalf("got %s", got.Status)
		}
		f.tasks.change("task-1", func(state *TaskState) { state.Ran, state.Startable, state.RunID = true, false, "run-1" })
		if got := f.read(reviewed.ID); got.Status != StatusStarted || got.Task.RunID != "run-1" || f.tasks.started() != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("accepted, but nothing ran within the grace period", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); err != nil {
			t.Fatal(err)
		}
		f.clock = f.clock.Add(DefaultStartGrace + time.Second)
		unresolved := f.read(reviewed.ID)
		if unresolved.Status != StatusUnresolved || lastReceipt(unresolved, ReceiptStartUnresolved).Reason != "The start was accepted, but the task has not begun." {
			t.Fatalf("got %+v", unresolved.Receipts)
		}
		if f.tasks.started() != 1 {
			t.Fatal("recovery must not start the task again")
		}
	})

	t.Run("a start still being delivered is left alone", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		var during Status
		f.tasks.onStart = func(string) {
			// A poll arrives while the start owner has not answered yet.
			during = f.read(reviewed.ID).Status
		}
		if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); err != nil {
			t.Fatal(err)
		}
		if during != StatusStartRequested {
			t.Fatalf("a concurrent read called it %s", during)
		}
	})
}

func TestTaskDeleted_EndsContinuationAndStartsNothing(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	f.tasks.remove("task-1")
	gone := f.read(reviewed.ID)
	if gone.Status != StatusInvalidated || lastReceipt(gone, ReceiptContinuationEnd).Reason != "the task no longer exists" {
		t.Fatalf("got %+v", gone)
	}
	if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); !errors.Is(err, ErrInvalidated) || f.tasks.started() != 0 {
		t.Fatalf("got %v", err)
	}
	// It is never replaced: the operation cannot reserve another task.
	if _, err := f.service.ClaimTaskCreate(t.Context(), TaskCreate{OperationID: reviewed.ID, DraftRevision: 1, WorkspaceID: "ws-album", AgentName: "Mixer", TaskID: "task-2"}); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("got %v", err)
	}
}

func TestEndedOperation_NamesATaskOnlyIfItWasConfirmed(t *testing.T) {
	t.Run("dismissed while its save was being written", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		during := f.read(prepared.ID)
		cancelled, err := f.service.Cancel(t.Context(), prepared.ID, "tab-a", during.Revision)
		if err != nil || cancelled.Status != StatusCancelled || cancelled.Task != nil {
			t.Fatalf("got %+v %v", cancelled, err)
		}
		// The save lands anyway: the task is the user's, and the ended
		// operation is neither revived nor made to point at it.
		f.tasks.put(TaskState{TaskID: "task-1", OperationID: prepared.ID, Status: "pending", Startable: true})
		if err := f.service.ConfirmTaskCreate(t.Context(), prepared.ID, "task-1"); err != nil {
			t.Fatal(err)
		}
		if after := f.read(prepared.ID); after.Status != StatusCancelled || after.Task != nil {
			t.Fatalf("got %+v", after)
		}
	})

	t.Run("expired with a reservation whose task was never written", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.restart()
		f.clock = f.clock.Add(DefaultTTL + time.Minute)
		expired := f.read(prepared.ID)
		if expired.Status != StatusExpired || expired.Task != nil {
			t.Fatalf("an expired reservation must not read as a task: %+v", expired)
		}
	})

	t.Run("expired by the sweep", func(t *testing.T) {
		f := newTaskFixture(t)
		reserved := f.prepared()
		if _, err := f.claim(reserved, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		g := newTaskFixture(t)
		confirmed := g.reviewed()
		for _, fixture := range []*taskFixture{f, g} {
			if _, err := fixture.store.Sweep(t.Context(), fixture.clock.Add(DefaultTTL+time.Minute), CleanupBatch); err != nil {
				t.Fatal(err)
			}
		}
		swept, err := f.store.Get(t.Context(), reserved.ID)
		if err != nil || swept.Status != StatusExpired || swept.Task != nil {
			t.Fatalf("swept reservation: %+v %v", swept, err)
		}
		// A confirmed task is still named after the operation expires.
		kept, err := g.store.Get(t.Context(), confirmed.ID)
		if err != nil || kept.Status != StatusExpired || kept.Task == nil || kept.Task.TaskID != "task-1" {
			t.Fatalf("swept confirmed operation: %+v %v", kept, err)
		}
	})
}

func TestSettle_ASingleMissingReadIsNotBelieved(t *testing.T) {
	t.Run("a confirmed task is not declared deleted", func(t *testing.T) {
		f := newTaskFixture(t)
		reviewed := f.reviewed()
		f.tasks.missNext = 1
		if after := f.read(reviewed.ID); after.Status != StatusReviewed || after.Task == nil {
			t.Fatalf("one missing read ended the operation: %+v", after)
		}
		// Two in a row is a deleted task.
		f.tasks.missNext = 2
		if after := f.read(reviewed.ID); after.Status != StatusInvalidated {
			t.Fatalf("got %s", after.Status)
		}
	})

	t.Run("a written task is not given back for a second save", func(t *testing.T) {
		f := newTaskFixture(t)
		prepared := f.prepared()
		if _, err := f.claim(prepared, "task-1", nil); err != nil {
			t.Fatal(err)
		}
		f.tasks.put(TaskState{TaskID: "task-1", OperationID: prepared.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-1", Status: "pending", Startable: true})
		f.restart()
		f.tasks.missNext = 1
		// The retry of the save must still find the first task.
		retry, err := f.claim(prepared, "task-2", nil)
		if err != nil || !retry.Existing || retry.TaskID != "task-1" {
			t.Fatalf("one missing read let a second task be reserved: %+v %v", retry, err)
		}
	})
}

func TestReceipts_KeepWhatWasCreatedAndRunThroughManyRetries(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	f.tasks.startErr = &StartRefusal{Reason: "Ori is being reset, so nothing can start right now."}
	for range MaxReceipts + 6 {
		if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); !errors.Is(err, ErrStartRefused) {
			t.Fatalf("got %v", err)
		}
	}
	refused := f.read(reviewed.ID)
	if len(refused.Receipts) > MaxReceipts || countReceipts(refused, ReceiptTaskCreated) != 1 || refused.Status != StatusReviewed {
		t.Fatalf("receipts after many refusals: %d %v", len(refused.Receipts), receiptKinds(refused))
	}
	if last := refused.Receipts[len(refused.Receipts)-1]; last.Kind != ReceiptStartRefused {
		t.Fatalf("the latest answer must be kept: %+v", last)
	}
	f.tasks.startErr = nil
	f.tasks.onStart = func(taskID string) {
		f.tasks.change(taskID, func(state *TaskState) { state.Ran, state.Startable, state.RunID = true, false, "run-1" })
	}
	started, err := f.service.Start(t.Context(), reviewed.ID, "tab-a")
	if err != nil || started.Status != StatusStarted || started.Task.RunID != "run-1" {
		t.Fatalf("got %+v %v", started, err)
	}
	if len(started.Receipts) > MaxReceipts || countReceipts(started, ReceiptTaskCreated) != 1 ||
		countReceipts(started, ReceiptRunEstablished) != 1 || countReceipts(started, ReceiptStartAccepted) != 1 {
		t.Fatalf("receipts after the run: %v", receiptKinds(started))
	}
}

func TestCancel_LeavesTheTaskAndItsRunAlone(t *testing.T) {
	f := newTaskFixture(t)
	reviewed := f.reviewed()
	if _, err := f.service.Start(t.Context(), reviewed.ID, "tab-a"); err != nil {
		t.Fatal(err)
	}
	f.tasks.change("task-1", func(state *TaskState) { state.Ran, state.Startable, state.RunID = true, false, "run-1" })
	started := f.read(reviewed.ID)
	cancelled, err := f.service.Cancel(t.Context(), reviewed.ID, "tab-a", started.Revision)
	if err != nil || cancelled.Status != StatusCancelled || cancelled.Task == nil || cancelled.Task.RunID != "run-1" {
		t.Fatalf("got %+v %v", cancelled, err)
	}
	if f.tasks.count() != 1 || f.tasks.started() != 1 {
		t.Fatal("dismissing must not touch the task")
	}
}
