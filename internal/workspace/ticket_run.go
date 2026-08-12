package workspace

import (
	"strings"
	"time"
)

// Ticket ←→ Workspace Run coupling
// (tasks/prd-workspace-ticket-management.md FR-26 through FR-33, FR-95,
// FR-100, FR-134).
//
// The whole point of this file is that a Run's status and a Ticket's state are
// different things (FR-8). A Run says what happened on one attempt; the Ticket
// says where the work stands. The mapping between them is deliberately narrow:
//
//   - A successful Run moves an In Progress Ticket to Review. Never to Done —
//     acceptance is a human act (FR-28, FR-29, FR-30).
//   - A failed, timed-out, rejected, or cancelled Run leaves the Ticket open In
//     Progress and raises a needs-attention signal derived from the latest
//     attempt. The Ticket does not enter a failure state, because the work is
//     still wanted (FR-32).
//   - Nothing here ever closes a Ticket, and nothing here ever reopens one.

// TicketRunOutcome is how one execution attempt finished.
type TicketRunOutcome string

const (
	TicketRunSucceeded TicketRunOutcome = "succeeded"
	TicketRunFailed    TicketRunOutcome = "failed"
	TicketRunTimedOut  TicketRunOutcome = "timed_out"
	TicketRunRejected  TicketRunOutcome = "rejected"
	TicketRunCancelled TicketRunOutcome = "cancelled"
)

// Succeeded reports whether the attempt produced an acceptable result.
func (o TicketRunOutcome) Succeeded() bool { return o == TicketRunSucceeded }

// TicketRunResult carries what a finished Run wants to record on its Ticket.
// Everything except Outcome is attempt-level data that is stored but never
// interpreted as lifecycle state.
type TicketRunResult struct {
	Outcome TicketRunOutcome
	RunID   string
	Actor   string
	Reason  string
	At      time.Time
}

// ApplyRunOutcome moves a Ticket according to a finished Run and reports
// whether the Ticket's canonical state actually changed.
//
// It is safe to call twice with the same Run and safe to call late: the only
// transition it will ever make is In Progress → Review, so a duplicate
// callback finds the Ticket already in Review and does nothing, and a callback
// that arrives after the user has accepted, cancelled, or reopened the work
// finds a state it is not allowed to move and leaves it alone (FR-134).
//
// That guard is the reason this returns a bool instead of an error: a late
// callback is not a failure, it is a race that resolved in the user's favour.
func (t *Task) ApplyRunOutcome(result TicketRunResult) bool {
	if t == nil {
		return false
	}
	at := result.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	actor := strings.TrimSpace(result.Actor)
	if actor == "" {
		actor = TicketActorRun
	}

	// A Run outcome only ever speaks about work that is currently running. If
	// the Ticket has moved on — accepted, cancelled, reopened, or pushed back
	// to Ready — the attempt's opinion is stale by definition.
	if t.CanonicalState() != TicketStateInProgress {
		return false
	}

	if !result.Outcome.Succeeded() {
		// The Ticket stays exactly where it is. The failed attempt is already
		// recorded on the record and in the Run's own immutable history; the
		// needs-attention signal is derived from it at read time rather than
		// stored as a lifecycle state that would then need clearing.
		return false
	}

	// Recurring work is never finished by one occurrence (FR-41). Sending a
	// recurring Ticket to Review after every run would put it in a queue the
	// user has to clear on a schedule, and accepting it there would close work
	// that is about to run again. It stays In Progress; each occurrence
	// exposes its own outcome through its own Run.
	//
	// Per-occurrence review is a selected future direction, deliberately not
	// built here.
	if t.IsRecurring() {
		return false
	}

	change := TicketStateChange{
		Actor:     actor,
		ActorID:   "",
		Reason:    strings.TrimSpace(result.Reason),
		RunID:     strings.TrimSpace(result.RunID),
		Timestamp: at.UTC(),
	}
	// TransitionTicket validates legality and appends history; a refusal here
	// would mean the state machine and this mapping disagree, so it is left to
	// surface rather than being forced.
	return t.TransitionTicket(TicketStateReview, change) == nil
}

// IsRecurring reports whether the Ticket runs on a repeating schedule, as
// opposed to a one-shot execution or a single scheduled time.
//
// This is the distinction FR-41 turns on: a one-shot run finishing means the
// work is done and needs review, while a recurring occurrence finishing means
// only that this occurrence finished.
func (t *Task) IsRecurring() bool {
	if t == nil || t.Schedule == nil || !t.ScheduleEnabled {
		return false
	}
	switch t.Schedule.Type {
	case ScheduleInterval, ScheduleDaily, ScheduleWeekly, ScheduleMonthly, ScheduleCron:
		return true
	}
	// ScheduleOnce and anything unrecognized are treated as one-shot: the
	// safe default is to surface the result for review rather than to leave
	// finished work silently open.
	return false
}

// RequireTicketRunnable is the guard every execution entry point calls before
// starting work. It refuses a Ticket that is not in a state where starting a
// Run makes sense, naming the action so the caller can render an actionable
// message (FR-20, FR-21, FR-26).
//
// Backlog is refused because it is not committed work. Done and Cancelled are
// refused because closed work must be explicitly reopened first — silently
// re-running a Ticket the user closed would erase a decision they made.
func RequireTicketRunnable(task *Task, action string) error {
	if err := RequireTaskNotBacklog(task, action); err != nil {
		return err
	}
	if task == nil {
		return nil
	}
	state := task.CanonicalState()
	if state.Terminal() {
		action = strings.TrimSpace(action)
		if action == "" {
			action = "this action"
		}
		return &TicketValidationError{
			Field: "state",
			Message: action + ": ticket is " + state.Label() +
				"; reopen it to Ready before running it again",
		}
	}
	return nil
}

// StartTicketRun moves a Ticket into In Progress for an execution attempt and
// records the Run that is about to begin (FR-26, FR-27).
//
// Ready → In Progress is the ordinary path. A Ticket already In Progress stays
// there and simply adopts the new Run, which is what makes a retry a new
// attempt on the same Ticket rather than a new Ticket (FR-33).
func (t *Task) StartTicketRun(runID, actor string, at time.Time) error {
	if t == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if strings.TrimSpace(actor) == "" {
		actor = TicketActorUser
	}
	if runID = strings.TrimSpace(runID); runID != "" {
		t.CurrentRunID = runID
	}
	// Starting work clears the waiting marker: an explicit run IS the
	// execution intent the marker was waiting for (FR-24, FR-25).
	t.AwaitingExecutionIntent = false

	if t.CanonicalState() == TicketStateInProgress {
		// A retry on already-running work. The previous attempt's error is
		// cleared so the needs-attention signal reflects THIS attempt, while
		// the attempt itself remains in execution history (FR-33).
		t.Error = ""
		return nil
	}

	return t.TransitionTicket(TicketStateInProgress, TicketStateChange{
		Actor:     actor,
		RunID:     runID,
		Timestamp: at.UTC(),
	})
}
