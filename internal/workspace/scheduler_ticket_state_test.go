package workspace

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// FR-30 (tasks/prd-760): a schedule belongs to a ticket, and a firing moves
// the ticket's canonical state only through legal transitions with actor
// "scheduler", recorded in StateHistory:
//
//	Ready       -> In Progress
//	Review      -> In Progress   (retry)
//	Done        -> Ready -> In Progress
//	In Progress -> refused (a firing never interrupts running work)
//	Backlog, Cancelled -> do not fire; the failure is shown on the ticket
//
// Both ways a schedule can point at a ticket are covered: the schedule stored
// on the task itself (executeTaskSchedule) and a ScheduledTask entity that
// targets it (rerunTargetTask).

type scheduleFiringCase struct {
	name        string
	ticketState TicketState
	status      TaskStatus // the legacy projection stored beside it
	wantFires   bool
	wantState   TicketState
	// wantMoves are the transitions the firing must append, in order.
	wantMoves [][2]TicketState
}

func scheduleFiringCases() []scheduleFiringCase {
	return []scheduleFiringCase{
		{
			name: "ready pending", ticketState: TicketStateReady, status: TaskStatusPending,
			wantFires: true, wantState: TicketStateInProgress,
			wantMoves: [][2]TicketState{{TicketStateReady, TicketStateInProgress}},
		},
		{
			name: "ready assigned", ticketState: TicketStateReady, status: TaskStatusAssigned,
			wantFires: true, wantState: TicketStateInProgress,
			wantMoves: [][2]TicketState{{TicketStateReady, TicketStateInProgress}},
		},
		{
			name: "review", ticketState: TicketStateReview, status: TaskStatusCompleted,
			wantFires: true, wantState: TicketStateInProgress,
			wantMoves: [][2]TicketState{{TicketStateReview, TicketStateInProgress}},
		},
		{
			name: "done", ticketState: TicketStateDone, status: TaskStatusCompleted,
			wantFires: true, wantState: TicketStateInProgress,
			wantMoves: [][2]TicketState{
				{TicketStateDone, TicketStateReady},
				{TicketStateReady, TicketStateInProgress},
			},
		},
		{
			name: "in progress", ticketState: TicketStateInProgress, status: TaskStatusInProgress,
			wantFires: false, wantState: TicketStateInProgress,
		},
		{
			// Recurring work stays In Progress between occurrences: a successful
			// run never sends a recurring ticket to Review (ApplyRunOutcome,
			// FR-41). Its next firing is a new attempt on the same ticket, so
			// "refuse while In Progress" can only mean "while a run is active".
			name: "in progress between runs", ticketState: TicketStateInProgress, status: TaskStatusCompleted,
			wantFires: true, wantState: TicketStateInProgress,
		},
		{
			name: "backlog", ticketState: TicketStateBacklog, status: TaskStatusBacklog,
			wantFires: false, wantState: TicketStateBacklog,
		},
		{
			name: "cancelled", ticketState: TicketStateCancelled, status: TaskStatusCancelled,
			wantFires: false, wantState: TicketStateCancelled,
		},
	}
}

// scheduleFiringOutcome is what one firing left on the stored ticket.
type scheduleFiringOutcome struct {
	state        TicketState
	status       TaskStatus
	moves        []TicketStateChange
	executions   int
	failureCount int
	// ticketFailure is true when the ticket itself records a failed firing.
	ticketFailure bool
}

func (o scheduleFiringOutcome) String() string {
	pairs := make([]string, 0, len(o.moves))
	for _, m := range o.moves {
		pairs = append(pairs, fmt.Sprintf("%s>%s(%s)", m.From, m.To, m.Actor))
	}
	return fmt.Sprintf("state=%s status=%s moves=%v executions=%d failures=%d ticketFailure=%v",
		o.state, o.status, pairs, o.executions, o.failureCount, o.ticketFailure)
}

type scheduleFirer func(ts *TaskScheduler, ws *Workspace, st *ScheduledTask, now time.Time)

// fireScheduleOnTicket stores a ticket in tc's state, fires its schedule once
// and reports what the stored ticket looks like afterwards. With withEntity
// the schedule is a ScheduledTask held in memory instead of on the task: the
// store migrates a persisted entity onto its target task when it loads the
// workspace (see TestScheduledTaskEntity_IsMigratedOntoItsTicketOnLoad), so the
// entity path is only reachable with an entity built after the load.
func fireScheduleOnTicket(t *testing.T, tc scheduleFiringCase, fire scheduleFirer, withEntity bool) scheduleFiringOutcome {
	t.Helper()

	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := memberWorkspace("Writer")
	ws.ID = "ws-fr30"
	ws.Status = StatusActive
	seed := TicketStateChange{To: tc.ticketState, Actor: TicketActorUser, Timestamp: time.Now().Add(-time.Hour).UTC()}
	task := Task{
		ID:              "t1",
		To:              "Writer",
		TicketState:     tc.ticketState,
		Status:          tc.status,
		StateHistory:    []TicketStateChange{seed},
		Schedule:        &ScheduleConfig{Type: ScheduleDaily},
		ScheduleEnabled: true,
	}
	var st *ScheduledTask
	if withEntity {
		task.Schedule, task.ScheduleEnabled = nil, false
		st = &ScheduledTask{
			ID: "st1", WorkspaceID: ws.ID, TargetTaskID: "t1", Enabled: true,
			Schedule: ScheduleConfig{Type: ScheduleDaily},
		}
	}
	ws.Tasks = []Task{task}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	ts := NewTaskScheduler(store, SchedulerConfig{})
	fire(ts, loaded, st, time.Now())

	after, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace after: %v", err)
	}
	got, err := after.GetTask("t1")
	if err != nil {
		t.Fatalf("get task after: %v", err)
	}

	out := scheduleFiringOutcome{
		state:        got.CanonicalState(),
		status:       got.Status,
		executions:   got.ExecutionCount,
		failureCount: got.FailureCount,
	}
	if len(got.StateHistory) > 1 {
		out.moves = append(out.moves, got.StateHistory[1:]...)
	}
	for _, e := range got.ExecutionHistory {
		if e.Status == "failed" {
			out.ticketFailure = true
		}
	}
	if st != nil {
		// The entity keeps its own counters; the ticket keeps none on this path.
		out.executions = st.ExecutionCount
		out.failureCount = st.FailureCount
	}
	return out
}

func assertScheduleFiring(t *testing.T, tc scheduleFiringCase, got scheduleFiringOutcome) {
	t.Helper()
	t.Logf("OBSERVED %s", got)

	if got.state != tc.wantState {
		t.Errorf("canonical state = %q, want %q", got.state, tc.wantState)
	}
	if len(got.moves) != len(tc.wantMoves) {
		t.Errorf("recorded %d transitions, want %d (%v)", len(got.moves), len(tc.wantMoves), tc.wantMoves)
	} else {
		for i, want := range tc.wantMoves {
			m := got.moves[i]
			if m.From != want[0] || m.To != want[1] || m.Actor != TicketActorScheduler {
				t.Errorf("transition %d = %s>%s by %q, want %s>%s by %q",
					i, m.From, m.To, m.Actor, want[0], want[1], TicketActorScheduler)
			}
		}
	}
	if tc.wantFires {
		if got.executions != 1 {
			t.Errorf("execution count = %d, want 1", got.executions)
		}
		return
	}
	if got.executions != 0 {
		t.Errorf("execution count = %d, want 0 for a ticket that must not fire", got.executions)
	}
	if !got.ticketFailure {
		t.Errorf("a refused firing must be shown on the ticket (a failed execution entry)")
	}
}

// TestScheduledFiring_MovesTicketStateOnlyThroughLegalTransitions is the
// failing-first test for FR-30 on the schedule stored on the task.
func TestScheduledFiring_MovesTicketStateOnlyThroughLegalTransitions(t *testing.T) {
	skipUntilScheduledFiringsAreLegal(t)
	for _, tc := range scheduleFiringCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := fireScheduleOnTicket(t, tc, func(ts *TaskScheduler, ws *Workspace, _ *ScheduledTask, now time.Time) {
				task, err := ws.GetTask("t1")
				if err != nil {
					t.Fatalf("get task: %v", err)
				}
				ts.executeTaskSchedule(ws, task, now)
			}, false)
			assertScheduleFiring(t, tc, got)
		})
	}
}

// TestScheduledFiring_ScheduledTaskEntityMovesTicketStateLegally is the same
// contract for a ScheduledTask entity that targets the ticket.
func TestScheduledFiring_ScheduledTaskEntityMovesTicketStateLegally(t *testing.T) {
	skipUntilScheduledFiringsAreLegal(t)
	for _, tc := range scheduleFiringCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := fireScheduleOnTicket(t, tc, func(ts *TaskScheduler, ws *Workspace, st *ScheduledTask, now time.Time) {
				ts.rerunTargetTask(ws, st, now)
			}, true)
			assertScheduleFiring(t, tc, got)
		})
	}
}

// TestScheduledTaskEntity_IsMigratedOntoItsTicketOnLoad pins how a ScheduledTask
// entity that targets a ticket reaches the scheduler today (a finding for
// FR-30/FR-31, not a contract change): the store copies its schedule onto the
// target task and drops the entity, so the scheduler fires the ticket's own
// schedule (executeTaskSchedule) and rerunTargetTask is only reachable with an
// entity that was built after the load.
func TestScheduledTaskEntity_IsMigratedOntoItsTicketOnLoad(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ws := memberWorkspace("Writer")
	ws.ID = "ws-entity-migration"
	ws.Status = StatusActive
	ws.Tasks = []Task{{ID: "t1", To: "Writer", TicketState: TicketStateReady, Status: TaskStatusPending}}
	ws.ScheduledTasks = []ScheduledTask{{
		ID: "st1", WorkspaceID: ws.ID, TargetTaskID: "t1", Name: "Nightly", Enabled: true,
		Schedule: ScheduleConfig{Type: ScheduleDaily},
	}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if len(loaded.ScheduledTasks) != 0 {
		t.Fatalf("scheduled task entities after load = %d, want 0 (migrated onto the ticket)", len(loaded.ScheduledTasks))
	}
	task, err := loaded.GetTask("t1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Schedule == nil || !task.ScheduleEnabled || task.ScheduleName != "Nightly" {
		t.Fatalf("ticket schedule after load = %+v enabled=%v name=%q, want the entity's schedule on the ticket",
			task.Schedule, task.ScheduleEnabled, task.ScheduleName)
	}
}

// skipUntilScheduledFiringsAreLegal keeps the FR-30 contract written but off
// the gate until task 4.3 closes it; ORI_RUN_FR30_CONTRACT=1 runs it red now.
// Task 4.3 deletes this helper and its two call sites.
func skipUntilScheduledFiringsAreLegal(t *testing.T) {
	t.Helper()
	if os.Getenv("ORI_RUN_FR30_CONTRACT") == "" {
		t.Skip("FR-30 gap recorded in tasks/findings (1.2); closed by task 4.3")
	}
}
