package workspace

import (
	"testing"
	"time"
)

func runAt() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) }

// readyTicket returns a Ticket in Ready, the state execution starts from.
func readyTicket() *Task {
	return &Task{
		ID:          "tkt-1",
		WorkspaceID: "ws-1",
		Description: "Ship the thing",
		TicketState: TicketStateReady,
		Status:      TaskStatusPending,
		// Newly-Ready work is quiescent until an explicit intent (FR-24).
		AwaitingExecutionIntent: true,
	}
}

// FR-26/FR-27: starting a Run moves Ready to In Progress, records the Run, and
// clears the waiting marker — the explicit run IS the execution intent.
func TestTask_StartTicketRun(t *testing.T) {
	task := readyTicket()

	if err := task.StartTicketRun("run-1", TicketActorUser, runAt()); err != nil {
		t.Fatalf("StartTicketRun: %v", err)
	}

	if task.CanonicalState() != TicketStateInProgress {
		t.Fatalf("state = %q, want in_progress", task.CanonicalState())
	}
	if task.CurrentRunID != "run-1" {
		t.Fatalf("CurrentRunID = %q, want run-1", task.CurrentRunID)
	}
	if task.AwaitingExecutionIntent {
		t.Fatalf("starting a run must clear the waiting-for-intent marker")
	}
	if len(task.StateHistory) != 1 || task.StateHistory[0].RunID != "run-1" {
		t.Fatalf("start must be audited with its run id: %+v", task.StateHistory)
	}
	if task.StartedAt == nil {
		t.Fatalf("StartedAt should be stamped when work begins")
	}
}

// FR-33: a retry is a NEW attempt on the SAME Ticket, not a new Ticket and not
// a state change.
func TestTask_StartTicketRun_RetryAdoptsNewRunWithoutChangingState(t *testing.T) {
	task := readyTicket()
	if err := task.StartTicketRun("run-1", TicketActorUser, runAt()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	task.Error = "previous attempt exploded"
	historyBefore := len(task.StateHistory)

	if err := task.StartTicketRun("run-2", TicketActorUser, runAt()); err != nil {
		t.Fatalf("retry start: %v", err)
	}

	if task.CanonicalState() != TicketStateInProgress {
		t.Fatalf("retry changed state to %q", task.CanonicalState())
	}
	if task.CurrentRunID != "run-2" {
		t.Fatalf("CurrentRunID = %q, want the new attempt", task.CurrentRunID)
	}
	// The previous attempt's error must not keep flagging the new attempt.
	if task.Error != "" {
		t.Fatalf("retry should clear the stale error, got %q", task.Error)
	}
	if len(task.StateHistory) != historyBefore {
		t.Fatalf("a retry within In Progress is not a state change; history grew to %d", len(task.StateHistory))
	}
}

// FR-28/FR-29: a successful Run lands in Review. Never Done.
func TestTask_ApplyRunOutcome_SuccessGoesToReviewNotDone(t *testing.T) {
	task := readyTicket()
	if err := task.StartTicketRun("run-1", TicketActorUser, runAt()); err != nil {
		t.Fatalf("start: %v", err)
	}

	moved := task.ApplyRunOutcome(TicketRunResult{
		Outcome: TicketRunSucceeded, RunID: "run-1", At: runAt(),
	})

	if !moved {
		t.Fatalf("a successful run on In Progress work must move the ticket")
	}
	if task.CanonicalState() != TicketStateReview {
		t.Fatalf("state = %q, want review — a run may never mark work Done", task.CanonicalState())
	}
	last := task.StateHistory[len(task.StateHistory)-1]
	if last.To != TicketStateReview || last.Actor != TicketActorRun || last.RunID != "run-1" {
		t.Fatalf("review transition not audited to the run: %+v", last)
	}
}

// FR-32: a failed attempt leaves the work open and flags it. The Ticket does
// not enter a failure state, because the work is still wanted.
func TestTask_ApplyRunOutcome_FailureLeavesTicketOpen(t *testing.T) {
	for _, outcome := range []TicketRunOutcome{
		TicketRunFailed, TicketRunTimedOut, TicketRunRejected, TicketRunCancelled,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			task := readyTicket()
			if err := task.StartTicketRun("run-1", TicketActorUser, runAt()); err != nil {
				t.Fatalf("start: %v", err)
			}
			historyBefore := len(task.StateHistory)

			moved := task.ApplyRunOutcome(TicketRunResult{Outcome: outcome, RunID: "run-1", At: runAt()})

			if moved {
				t.Fatalf("a %s run must not move the ticket", outcome)
			}
			if task.CanonicalState() != TicketStateInProgress {
				t.Fatalf("state = %q, want in_progress", task.CanonicalState())
			}
			if len(task.StateHistory) != historyBefore {
				t.Fatalf("a failed run is not a state change")
			}

			// The needs-attention signal is derived from the attempt, not stored.
			task.Error = "boom"
			if !task.NeedsAttention() {
				t.Fatalf("a failed latest attempt must raise needs-attention")
			}
		})
	}
}

// FR-134: a late or duplicate Run callback must never overwrite a state the
// user chose in the meantime.
func TestTask_ApplyRunOutcome_LateAndDuplicateCallbacksAreInert(t *testing.T) {
	t.Run("duplicate callback is idempotent", func(t *testing.T) {
		task := readyTicket()
		_ = task.StartTicketRun("run-1", TicketActorUser, runAt())
		if !task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded, At: runAt()}) {
			t.Fatalf("first callback should move the ticket")
		}
		historyAfterFirst := len(task.StateHistory)

		if task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded, At: runAt()}) {
			t.Fatalf("a duplicate callback must not move the ticket again")
		}
		if len(task.StateHistory) != historyAfterFirst {
			t.Fatalf("duplicate callback appended history")
		}
	})

	// A run that finishes after the user has already acted must lose.
	for _, state := range []TicketState{
		TicketStateDone, TicketStateCancelled, TicketStateReady, TicketStateBacklog, TicketStateReview,
	} {
		t.Run("late callback against "+string(state), func(t *testing.T) {
			task := readyTicket()
			task.TicketState = state
			task.Status = legacyStatusForTicketState(state, "")
			before := task.CanonicalState()

			if task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded, At: runAt()}) {
				t.Fatalf("late callback moved a %s ticket", state)
			}
			if task.CanonicalState() != before {
				t.Fatalf("late callback changed state %q → %q", before, task.CanonicalState())
			}
		})
	}
}

// FR-41: a recurring occurrence finishing means only that this occurrence
// finished. It must not close the Ticket, and it must not queue it for review
// on every tick.
func TestTask_ApplyRunOutcome_RecurringOccurrenceDoesNotCloseTheTicket(t *testing.T) {
	recurring := func(scheduleType ScheduleType) *Task {
		task := readyTicket()
		task.ScheduleEnabled = true
		task.Schedule = &ScheduleConfig{Type: scheduleType}
		_ = task.StartTicketRun("run-1", TicketActorUser, runAt())
		return task
	}

	for _, scheduleType := range []ScheduleType{
		ScheduleInterval, ScheduleDaily, ScheduleWeekly, ScheduleMonthly, ScheduleCron,
	} {
		t.Run(string(scheduleType), func(t *testing.T) {
			task := recurring(scheduleType)
			if !task.IsRecurring() {
				t.Fatalf("%s should be recurring", scheduleType)
			}

			// Several successful occurrences in a row.
			for i := range 3 {
				if task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded, At: runAt()}) {
					t.Fatalf("occurrence %d moved a recurring ticket", i+1)
				}
			}
			if task.CanonicalState() != TicketStateInProgress {
				t.Fatalf("state = %q, want in_progress — recurring work stays open", task.CanonicalState())
			}
			if !task.ScheduleEnabled {
				t.Fatalf("a successful occurrence must not disable the schedule")
			}
		})
	}

	// A one-shot schedule is NOT recurring: its result is finished work and
	// belongs in review.
	t.Run("one-shot still goes to review", func(t *testing.T) {
		task := recurring(ScheduleOnce)
		if task.IsRecurring() {
			t.Fatalf("a once schedule must not count as recurring")
		}
		if !task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded, At: runAt()}) {
			t.Fatalf("a one-shot success should move the ticket to review")
		}
		if task.CanonicalState() != TicketStateReview {
			t.Fatalf("state = %q, want review", task.CanonicalState())
		}
	})

	t.Run("an unscheduled ticket is not recurring", func(t *testing.T) {
		task := readyTicket()
		if task.IsRecurring() {
			t.Fatalf("a ticket with no schedule must not count as recurring")
		}
	})
}

// FR-30/FR-36: Done is reachable only from Review, by an explicit user action.
func TestTicketService_DoneRequiresReviewAndAnExplicitAction(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "needs acceptance",
	})

	// Not from Ready, and not from In Progress.
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateDone, Actor: TicketActorUser}); err == nil {
		t.Fatalf("Ready → Done must be refused")
	}
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateInProgress, Actor: TicketActorUser}); err != nil {
		t.Fatalf("Ready → In Progress: %v", err)
	}
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateDone, Actor: TicketActorUser}); err == nil {
		t.Fatalf("In Progress → Done must be refused; acceptance happens from Review")
	}

	// Through Review, by an explicit action, it works.
	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReview, Actor: TicketActorUser}); err != nil {
		t.Fatalf("In Progress → Review: %v", err)
	}
	done, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateDone, Actor: TicketActorUser})
	if err != nil {
		t.Fatalf("Review → Done: %v", err)
	}
	if done.State != TicketStateDone {
		t.Fatalf("state = %q, want done", done.State)
	}
}

// FR-31: requesting changes from Review returns the work to In Progress and
// keeps every earlier attempt.
func TestTicketService_RequestChangesReturnsToInProgress(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "retry me",
	})

	for _, next := range []TicketState{TicketStateInProgress, TicketStateReview} {
		if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: next, Actor: TicketActorUser}); err != nil {
			t.Fatalf("transition %s: %v", next, err)
		}
	}
	back, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
		To: TicketStateInProgress, Actor: TicketActorUser, Reason: "needs another pass",
	})
	if err != nil {
		t.Fatalf("Review → In Progress: %v", err)
	}
	if back.State != TicketStateInProgress {
		t.Fatalf("state = %q, want in_progress", back.State)
	}
	// Creation + 3 transitions.
	if len(back.StateHistory) != 4 {
		t.Fatalf("history has %d entries, want 4 — earlier attempts must be preserved", len(back.StateHistory))
	}
	last := back.StateHistory[len(back.StateHistory)-1]
	if last.Reason != "needs another pass" {
		t.Fatalf("the review decision's reason was dropped: %+v", last)
	}
}

// FR-37/FR-38/FR-39: cancel and reopen are explicit, and reopening lands in
// Ready rather than resuming execution.
func TestTicketService_CancelAndReopen(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	t.Run("cancel then reopen to Ready", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateReady, Title: "cancel me",
		})
		cancelled, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
			To: TicketStateCancelled, Actor: TicketActorUser, Reason: "no longer needed",
		})
		if err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if cancelled.State != TicketStateCancelled || cancelled.CompletedAt == nil {
			t.Fatalf("cancel did not close the ticket: %+v", cancelled)
		}

		// Reopening goes to Ready — never straight back into execution.
		if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateInProgress, Actor: TicketActorUser}); err == nil {
			t.Fatalf("Cancelled → In Progress must be refused")
		}
		reopened, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if reopened.State != TicketStateReady {
			t.Fatalf("state = %q, want ready", reopened.State)
		}
		// Reopened work is quiescent again: no schedule silently restored.
		if !reopened.AwaitingExecutionIntent {
			t.Fatalf("reopened work must wait for a fresh execution intent (FR-39)")
		}
		if len(reopened.StateHistory) < 3 {
			t.Fatalf("cancellation history must survive reopening: %+v", reopened.StateHistory)
		}
	})

	t.Run("done reopens to Ready with history intact", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateReady, Title: "reopen me",
		})
		for _, next := range []TicketState{TicketStateInProgress, TicketStateReview, TicketStateDone} {
			if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: next, Actor: TicketActorUser}); err != nil {
				t.Fatalf("transition %s: %v", next, err)
			}
		}
		reopened, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: TicketStateReady, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if reopened.CompletedAt != nil {
			t.Fatalf("reopening must clear the completion stamp")
		}
		if len(reopened.StateHistory) != 5 {
			t.Fatalf("history has %d entries, want 5 — completion history must survive", len(reopened.StateHistory))
		}
	})
}

// FR-38/FR-39: cancelling disarms future execution, and reopening does not
// silently re-arm it.
func TestTicketService_CancelDisablesScheduleAndReopenDoesNotRestoreIt(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "scheduled work",
	})

	// Arm a recurring schedule the way the scheduler would.
	next := time.Now().Add(time.Hour)
	err := store.Update(ws.ID, func(w *Workspace) error {
		return w.MutateTask(ticket.ID, func(task *Task) error {
			task.Schedule = &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"}
			task.ScheduleEnabled = true
			task.NextRun = &next
			return nil
		})
	})
	if err != nil {
		t.Fatalf("arm schedule: %v", err)
	}

	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
		To: TicketStateCancelled, Actor: TicketActorUser,
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	stored, _ := store.Get(ws.ID)
	task, _ := stored.GetTask(ticket.ID)
	if task.ScheduleEnabled || task.NextRun != nil {
		t.Fatalf("cancelling must stop future scheduled execution: enabled=%v next=%v",
			task.ScheduleEnabled, task.NextRun)
	}
	// The configuration is kept so the user can see what was disabled.
	if task.Schedule == nil {
		t.Fatalf("cancelling must not discard the schedule configuration")
	}

	if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
		To: TicketStateReady, Actor: TicketActorUser,
	}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	stored, _ = store.Get(ws.ID)
	task, _ = stored.GetTask(ticket.ID)
	if task.ScheduleEnabled || task.NextRun != nil {
		t.Fatalf("reopening must NOT silently re-arm a disabled schedule (FR-39)")
	}
	if !task.AwaitingExecutionIntent {
		t.Fatalf("reopened work must wait for a fresh execution intent")
	}
}

// FR-21/FR-103: the runnable guard reads canonical state, so it holds
// identically no matter which endpoint the call arrived through.
func TestRequireTicketRunnable(t *testing.T) {
	t.Run("refuses backlog", func(t *testing.T) {
		task := &Task{ID: "t", TicketState: TicketStateBacklog, Description: "captured"}
		if err := RequireTicketRunnable(task, "cannot run task"); err == nil {
			t.Fatalf("expected a Backlog ticket to be refused")
		}
	})

	t.Run("refuses backlog through the legacy status field alone", func(t *testing.T) {
		// A record that predates TicketState still resolves to Backlog.
		task := &Task{ID: "t", Status: TaskStatusBacklog, Description: "captured"}
		if err := RequireTaskNotBacklog(task, "cannot run task"); err == nil {
			t.Fatalf("legacy-only Backlog records must still be guarded")
		}
	})

	t.Run("refuses closed work until it is reopened", func(t *testing.T) {
		for _, state := range []TicketState{TicketStateDone, TicketStateCancelled} {
			task := &Task{ID: "t", TicketState: state, Description: "closed"}
			err := RequireTicketRunnable(task, "cannot run task")
			if err == nil {
				t.Fatalf("expected a %s ticket to be refused", state)
			}
			if _, ok := IsTicketValidationError(err); !ok {
				t.Fatalf("error should be actionable, got %T", err)
			}
		}
	})

	t.Run("allows ready and in progress", func(t *testing.T) {
		for _, state := range []TicketState{TicketStateReady, TicketStateInProgress, TicketStateReview} {
			task := &Task{ID: "t", TicketState: state, Description: "open"}
			if err := RequireTicketRunnable(task, "cannot run task"); err != nil {
				t.Fatalf("state %s should be runnable: %v", state, err)
			}
		}
	})
}

// FR-25: assigning an agent records the assignment without starting anything.
func TestTicketService_AssignmentDoesNotStartWork(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "assign only",
	})

	// The execution engine stamps an assignee directly on the record.
	err := store.Update(ws.ID, func(w *Workspace) error {
		return w.MutateTask(ticket.ID, func(task *Task) error {
			task.To = "builder"
			return nil
		})
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.State != TicketStateReady {
		t.Fatalf("state = %q — assigning must not start work (FR-25)", current.State)
	}
	if current.Assignee != "builder" {
		t.Fatalf("assignee = %q, want builder", current.Assignee)
	}
	if current.CurrentRunID != "" {
		t.Fatalf("assigning must not create a run")
	}
}
