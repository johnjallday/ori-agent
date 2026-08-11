package workspace

import (
	"testing"
)

// Group 3 of tasks/prd-workspace-ticket-management.md: the Board is a
// projection of canonical Ticket state, and legacy Kanban data is inert.

// FR-43 through FR-46: the Board's columns come from canonical state, and
// Cancelled is not one of them.
func TestBoardColumnsProjectCanonicalState(t *testing.T) {
	var columns []TicketState
	for _, state := range AllTicketStates {
		if state == TicketStateCancelled {
			continue
		}
		columns = append(columns, state)
	}

	want := []TicketState{
		TicketStateBacklog, TicketStateReady, TicketStateInProgress,
		TicketStateReview, TicketStateDone,
	}
	if len(columns) != len(want) {
		t.Fatalf("board columns = %v, want %v", columns, want)
	}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("board columns = %v, want %v", columns, want)
		}
	}
}

// FR-7/FR-47/FR-118: `kanban_column_id` is a migration hint at most. It must
// never determine a Ticket's state, and canonical operations must never write
// it — otherwise the Board quietly becomes a second lifecycle authority again.
func TestTicketService_NeverReadsOrWritesKanbanState(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "legacy board data",
	})

	// Seed the legacy presentation state a pre-Ticket board would have written,
	// deliberately contradicting the canonical state.
	err := store.Update(ws.ID, func(w *Workspace) error {
		return w.MutateTask(ticket.ID, func(task *Task) error {
			task.Context = map[string]any{
				"kanban_column_id": "col-done",
				"kanban_labels":    []string{"legacy-label"},
				"kanban_due_date":  "2026-01-01",
				"unrelated_key":    "keep me",
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("seed legacy context: %v", err)
	}

	// A contradicting column must not change what the Ticket's state IS.
	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.State != TicketStateBacklog {
		t.Fatalf("State = %q, want backlog — kanban_column_id must not be lifecycle authority", current.State)
	}

	// Canonical mutations run over the record.
	newTitle := "renamed"
	if _, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{Title: &newTitle}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	moved, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
		To: TicketStateReady, Actor: TicketActorUser,
	})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if moved.State != TicketStateReady {
		t.Fatalf("State = %q, want ready", moved.State)
	}

	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	task, err := stored.GetTask(ticket.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	// The legacy values are RETAINED untouched — Group 7's migration needs
	// them as evidence, and rollback needs them intact. Canonical code neither
	// reads them for lifecycle nor rewrites them.
	if got := task.Context["kanban_column_id"]; got != "col-done" {
		t.Fatalf("kanban_column_id = %v, want the original value preserved for migration/rollback", got)
	}
	if got := task.Context["kanban_due_date"]; got != "2026-01-01" {
		t.Fatalf("kanban_due_date = %v, want preserved", got)
	}
	if got := task.Context["unrelated_key"]; got != "keep me" {
		t.Fatalf("canonical mutation clobbered unrelated context: %v", got)
	}
	// And canonical tags/due date are their own fields, not the legacy ones.
	if len(task.Tags) != 0 {
		t.Fatalf("legacy kanban_labels must not become canonical tags before migration, got %v", task.Tags)
	}
	if task.DueDate != nil {
		t.Fatalf("legacy kanban_due_date must not become the canonical due date before migration")
	}
}

// FR-46 through FR-48: a Board drag is a state transition, so it obeys exactly
// the same legality rules as every other move. Dropping a Backlog card into
// In Progress must be refused, not silently allowed because a column accepted
// the drop.
func TestTicketService_BoardMoveObeysTransitionLegality(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "dragged too far",
	})

	for _, illegal := range []TicketState{TicketStateInProgress, TicketStateReview, TicketStateDone} {
		_, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: illegal, Actor: TicketActorUser})
		if _, ok := IsIllegalTicketTransition(err); !ok {
			t.Fatalf("dropping backlog into %s: error = %v, want IllegalTicketTransitionError", illegal, err)
		}
		current, _ := svc.Get(ws.ID, ticket.ID)
		if current.State != TicketStateBacklog {
			t.Fatalf("refused board move changed state to %q", current.State)
		}
	}

	// The one legal destination works and lands with a fresh rank.
	moved, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
		To: TicketStateReady, Actor: TicketActorUser,
	})
	if err != nil {
		t.Fatalf("legal board move: %v", err)
	}
	if moved.StateRank <= 0 {
		t.Fatalf("StateRank = %d, want a deterministic destination rank", moved.StateRank)
	}
}

// FR-59/FR-60/FR-91: ranks are per owner and per state, so a Board reorder in
// a rolled-up view can never renumber another workspace's column.
func TestTicketService_RankSpacesAreScopedToOwnerAndState(t *testing.T) {
	svc, store := newTicketTestService(t)
	parent := newTicketTestWorkspace(t, store, "Parent")

	child := NewWorkspace(CreateWorkspaceParams{Name: "Child"})
	child.ParentID = parent.ID
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}

	parentReady := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: parent.ID, State: TicketStateReady, Title: "parent ready",
	})
	childReady := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: child.ID, State: TicketStateReady, Title: "child ready",
	})

	// Both start at rank 1 in their own owner's Ready space.
	if parentReady.StateRank != 1 || childReady.StateRank != 1 {
		t.Fatalf("ranks = %d/%d, want 1/1 — rank spaces are per owner", parentReady.StateRank, childReady.StateRank)
	}

	// A parent-scoped reorder cannot include the child's ticket.
	if _, err := svc.Reorder(parent.ID, TicketStateReady, []string{parentReady.ID, childReady.ID}); err == nil {
		t.Fatalf("expected a cross-owner reorder to be refused")
	}
	// And the refusal leaves both ranks alone.
	current, err := svc.Get(child.ID, childReady.ID)
	if err != nil {
		t.Fatalf("Get child ticket: %v", err)
	}
	if current.StateRank != 1 {
		t.Fatalf("refused cross-owner reorder changed the child's rank to %d", current.StateRank)
	}
}
