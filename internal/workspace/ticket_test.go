package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestTicketState_Valid(t *testing.T) {
	for _, state := range AllTicketStates {
		if !state.Valid() {
			t.Fatalf("state %q should be valid", state)
		}
	}
	for _, bad := range []TicketState{"", "pending", "assigned", "completed", "failed", "timeout", "BACKLOG"} {
		if bad.Valid() {
			t.Fatalf("state %q should not be valid", bad)
		}
	}
}

func TestParseTicketState(t *testing.T) {
	got, err := ParseTicketState("  In_Progress ")
	if err != nil {
		t.Fatalf("ParseTicketState() error = %v", err)
	}
	if got != TicketStateInProgress {
		t.Fatalf("ParseTicketState() = %q, want in_progress", got)
	}
	if _, err := ParseTicketState("archived"); err == nil {
		t.Fatalf("expected error for unknown state")
	}
}

// The fixed V1 workflow is the whole point of canonical state, so the legal
// table gets an exhaustive assertion rather than spot checks.
func TestLegalTicketTransitions(t *testing.T) {
	want := map[TicketState][]TicketState{
		TicketStateBacklog:    {TicketStateReady, TicketStateCancelled},
		TicketStateReady:      {TicketStateBacklog, TicketStateInProgress, TicketStateCancelled},
		TicketStateInProgress: {TicketStateReady, TicketStateReview, TicketStateCancelled},
		TicketStateReview:     {TicketStateInProgress, TicketStateDone, TicketStateCancelled},
		TicketStateDone:       {TicketStateReady},
		TicketStateCancelled:  {TicketStateReady},
	}
	for from, expected := range want {
		got := LegalTicketTransitions(from)
		if len(got) != len(expected) {
			t.Fatalf("LegalTicketTransitions(%s) = %v, want %v", from, got, expected)
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("LegalTicketTransitions(%s) = %v, want %v", from, got, expected)
			}
		}
	}
}

// FR-36: work must pass through Ready; FR-29: a Run can never reach Done.
func TestCanTransitionTicket_RefusesLifecycleShortcuts(t *testing.T) {
	refused := []struct {
		from TicketState
		to   TicketState
		why  string
	}{
		{TicketStateBacklog, TicketStateInProgress, "backlog must pass through ready (FR-36)"},
		{TicketStateBacklog, TicketStateReview, "backlog must pass through ready (FR-36)"},
		{TicketStateBacklog, TicketStateDone, "backlog must pass through ready (FR-36)"},
		{TicketStateInProgress, TicketStateDone, "done requires human review (FR-29, FR-30)"},
		{TicketStateReady, TicketStateReview, "review requires work to have started"},
		{TicketStateReady, TicketStateDone, "done requires human review (FR-30)"},
		{TicketStateDone, TicketStateInProgress, "reopen lands in ready (FR-37)"},
		{TicketStateCancelled, TicketStateInProgress, "reopen lands in ready (FR-39)"},
		{TicketStateBacklog, TicketStateBacklog, "same-state is not a transition"},
	}
	for _, tc := range refused {
		if CanTransitionTicket(tc.from, tc.to) {
			t.Fatalf("%s → %s should be refused: %s", tc.from, tc.to, tc.why)
		}
	}
}

func TestTask_CanonicalState_FallsBackToLegacyStatus(t *testing.T) {
	// Records persisted before TicketState existed must still resolve to a
	// sane canonical state, so canonical behavior works on an unmigrated
	// workspace.
	cases := map[TaskStatus]TicketState{
		TaskStatusBacklog:          TicketStateBacklog,
		TaskStatusPending:          TicketStateReady,
		TaskStatusAssigned:         TicketStateReady,
		TaskStatusInProgress:       TicketStateInProgress,
		TaskStatusWaitingForChoice: TicketStateInProgress,
		TaskStatusFailed:           TicketStateInProgress,
		TaskStatusTimeout:          TicketStateInProgress,
		TaskStatusCompleted:        TicketStateDone,
		TaskStatusCancelled:        TicketStateCancelled,
		"":                         TicketStateReady,
	}
	for status, want := range cases {
		task := &Task{ID: "t", Status: status}
		if got := task.CanonicalState(); got != want {
			t.Fatalf("legacy status %q resolved to %q, want %q", status, got, want)
		}
	}
}

// FR-7: the stored canonical state wins over anything derivable from the
// legacy status, tags, kanban column, assignee, or a stored result.
func TestTask_CanonicalState_StoredStateWinsOverEverything(t *testing.T) {
	task := &Task{
		ID:          "t",
		TicketState: TicketStateReview,
		Status:      TaskStatusFailed,
		To:          "builder",
		Result:      "done already",
		Tags:        []string{"done"},
		Context:     map[string]any{"kanban_column_id": "col-done"},
	}
	if got := task.CanonicalState(); got != TicketStateReview {
		t.Fatalf("CanonicalState() = %q, want review — stored state must be authoritative", got)
	}
}

func TestTask_TransitionTicket_RecordsHistoryAndProjectsLegacyStatus(t *testing.T) {
	task := &Task{ID: "t", TicketState: TicketStateBacklog, Status: TaskStatusBacklog}
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	if err := task.TransitionTicket(TicketStateReady, TicketStateChange{
		Actor: TicketActorUser, Reason: "committing", Timestamp: at,
	}); err != nil {
		t.Fatalf("TransitionTicket() error = %v", err)
	}

	if task.TicketState != TicketStateReady {
		t.Fatalf("TicketState = %q, want ready", task.TicketState)
	}
	if task.Status != TaskStatusPending {
		t.Fatalf("legacy Status = %q, want pending projection", task.Status)
	}
	if len(task.StateHistory) != 1 {
		t.Fatalf("StateHistory has %d entries, want 1", len(task.StateHistory))
	}
	entry := task.StateHistory[0]
	if entry.From != TicketStateBacklog || entry.To != TicketStateReady {
		t.Fatalf("history entry = %s → %s, want backlog → ready", entry.From, entry.To)
	}
	if entry.Actor != TicketActorUser || entry.Reason != "committing" || !entry.Timestamp.Equal(at) {
		t.Fatalf("history entry lost actor/reason/timestamp: %+v", entry)
	}
}

// A refused transition must leave the record completely untouched — no
// partial state change, no orphan history entry.
func TestTask_TransitionTicket_RefusedLeavesRecordUnchanged(t *testing.T) {
	task := &Task{ID: "t", TicketState: TicketStateBacklog, Status: TaskStatusBacklog}

	err := task.TransitionTicket(TicketStateDone, TicketStateChange{Actor: TicketActorUser})
	if err == nil {
		t.Fatalf("expected backlog → done to be refused")
	}
	transitionErr, ok := IsIllegalTicketTransition(err)
	if !ok {
		t.Fatalf("error = %T, want *IllegalTicketTransitionError", err)
	}
	if transitionErr.From != TicketStateBacklog || transitionErr.To != TicketStateDone {
		t.Fatalf("error carries %s → %s, want backlog → done", transitionErr.From, transitionErr.To)
	}
	// The message must name the legal destinations so the UI can act on it.
	if !strings.Contains(err.Error(), "Ready") {
		t.Fatalf("error message should name legal destinations, got %q", err.Error())
	}
	if task.TicketState != TicketStateBacklog || task.Status != TaskStatusBacklog {
		t.Fatalf("refused transition mutated state: %q/%q", task.TicketState, task.Status)
	}
	if len(task.StateHistory) != 0 {
		t.Fatalf("refused transition appended history: %+v", task.StateHistory)
	}
}

// FR-37: reopening clears the completion stamp but never the history.
func TestTask_TransitionTicket_ReopenPreservesHistory(t *testing.T) {
	task := &Task{ID: "t", TicketState: TicketStateReview, Status: TaskStatusCompleted}

	if err := task.TransitionTicket(TicketStateDone, TicketStateChange{Actor: TicketActorUser}); err != nil {
		t.Fatalf("review → done: %v", err)
	}
	if task.CompletedAt == nil {
		t.Fatalf("done should stamp CompletedAt")
	}
	if err := task.TransitionTicket(TicketStateReady, TicketStateChange{Actor: TicketActorUser}); err != nil {
		t.Fatalf("done → ready: %v", err)
	}
	if task.CompletedAt != nil {
		t.Fatalf("reopen should clear CompletedAt")
	}
	if len(task.StateHistory) != 2 {
		t.Fatalf("StateHistory has %d entries, want 2 — reopening must not erase history", len(task.StateHistory))
	}
}

func TestNormalizeTicketTitle(t *testing.T) {
	got, err := NormalizeTicketTitle("  fix the thing  ")
	if err != nil || got != "fix the thing" {
		t.Fatalf("NormalizeTicketTitle() = %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", "line one\nline two", strings.Repeat("x", TicketTitleMaxLength+1)} {
		if _, err := NormalizeTicketTitle(bad); err == nil {
			t.Fatalf("expected error for title %q", bad)
		} else if _, ok := IsTicketValidationError(err); !ok {
			t.Fatalf("title error should be a field validation error, got %T", err)
		}
	}
}

func TestNormalizeTicketPriority(t *testing.T) {
	if got, err := NormalizeTicketPriority(0); err != nil || got != TicketPriorityDefault {
		t.Fatalf("unset priority = %d, %v; want default %d", got, err, TicketPriorityDefault)
	}
	if got, err := NormalizeTicketPriority(1); err != nil || got != 1 {
		t.Fatalf("priority 1 = %d, %v", got, err)
	}
	for _, bad := range []int{-1, 6, 99} {
		if _, err := NormalizeTicketPriority(bad); err == nil {
			t.Fatalf("expected error for priority %d", bad)
		}
	}
}

func TestNormalizeTicketSource(t *testing.T) {
	if got, err := NormalizeTicketSource(""); err != nil || got != TicketSourceManual {
		t.Fatalf("empty source = %q, %v; want manual", got, err)
	}
	if got, err := NormalizeTicketSource(" Note "); err != nil || got != TicketSourceNote {
		t.Fatalf("note source = %q, %v", got, err)
	}
	if _, err := NormalizeTicketSource("telepathy"); err == nil {
		t.Fatalf("expected error for unsupported source")
	}
}

func TestNormalizeLinkedNoteIDs(t *testing.T) {
	got := NormalizeLinkedNoteIDs([]string{" n1 ", "n2", "n1", "", "  "})
	if len(got) != 2 || got[0] != "n1" || got[1] != "n2" {
		t.Fatalf("NormalizeLinkedNoteIDs() = %v, want [n1 n2] preserving first-seen order", got)
	}
	if NormalizeLinkedNoteIDs(nil) != nil {
		t.Fatalf("nil input should stay nil")
	}
}

func TestFormatTicketNumber(t *testing.T) {
	if got := FormatTicketNumber(12); got != "#12" {
		t.Fatalf("FormatTicketNumber(12) = %q", got)
	}
	if got := FormatTicketNumber(0); got != "" {
		t.Fatalf("unnumbered ticket should render empty, got %q", got)
	}
	if got := FormatQualifiedTicketNumber("Alpha", 12); got != "Alpha #12" {
		t.Fatalf("FormatQualifiedTicketNumber() = %q, want %q", got, "Alpha #12")
	}
	if got := FormatQualifiedTicketNumber("", 12); got != "#12" {
		t.Fatalf("unnamed workspace should fall back to bare number, got %q", got)
	}
}

// FR-32: a failed attempt leaves the Ticket open and flags it, rather than
// moving it into a failure state.
func TestTask_NeedsAttention(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"in progress with error", Task{TicketState: TicketStateInProgress, Error: "boom"}, true},
		{"in progress after failed run", Task{TicketState: TicketStateInProgress, Status: TaskStatusFailed}, true},
		{"in progress after timeout", Task{TicketState: TicketStateInProgress, Status: TaskStatusTimeout}, true},
		{"in progress healthy", Task{TicketState: TicketStateInProgress}, false},
		{"done with stale error", Task{TicketState: TicketStateDone, Error: "boom"}, false},
		{"backlog", Task{TicketState: TicketStateBacklog}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.task.NeedsAttention(); got != tc.want {
				t.Fatalf("NeedsAttention() = %v, want %v", got, tc.want)
			}
		})
	}
}

// FR-92/FR-141: the canonical envelope carries both identifiers plus owner
// identity, and never leaks a legacy envelope shape.
func TestNewTicket_CanonicalRepresentation(t *testing.T) {
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{
		ID:            "task-1",
		WorkspaceID:   "ws-child",
		TicketNumber:  7,
		TicketState:   TicketStateReady,
		StateRank:     3,
		Description:   "Ship the thing",
		Details:       "## Context\nmarkdown body",
		Tags:          []string{"infra"},
		Priority:      2,
		DueDate:       &due,
		SourceType:    TicketSourceNote,
		SourceID:      "note-9",
		LinkedNoteIDs: []string{"note-9"},
		TicketVersion: 4,
		CreatedAt:     created,
	}

	ticket := NewTicket(task, "ws-child", "Child Studio")

	if ticket.ID != "task-1" || ticket.Number != 7 {
		t.Fatalf("identifiers = %q/%d, want task-1/7", ticket.ID, ticket.Number)
	}
	if ticket.DisplayNumber != "#7" || ticket.QualifiedNumber != "Child Studio #7" {
		t.Fatalf("numbers = %q/%q", ticket.DisplayNumber, ticket.QualifiedNumber)
	}
	if ticket.OwningWorkspaceID != "ws-child" || ticket.OwningWorkspaceName != "Child Studio" {
		t.Fatalf("owner identity missing: %+v", ticket)
	}
	// Description → title, Details → markdown body.
	if ticket.Title != "Ship the thing" || ticket.Description != "## Context\nmarkdown body" {
		t.Fatalf("title/description mapping wrong: %q / %q", ticket.Title, ticket.Description)
	}
	if ticket.State != TicketStateReady || ticket.StateLabel != "Ready" {
		t.Fatalf("state = %q/%q", ticket.State, ticket.StateLabel)
	}
	if ticket.UpdatedAt != created {
		t.Fatalf("UpdatedAt should fall back to CreatedAt for untouched records, got %v", ticket.UpdatedAt)
	}
	if len(ticket.LegalTransitions) == 0 {
		t.Fatalf("canonical representation must expose legal transitions")
	}
	// Slices must be copies, not aliases into the persisted record.
	ticket.Tags[0] = "mutated"
	if task.Tags[0] != "infra" {
		t.Fatalf("NewTicket aliased the record's tag slice")
	}
}
