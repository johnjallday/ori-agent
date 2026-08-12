package workspace

import (
	"testing"
)

// Group 6 of tasks/prd-workspace-ticket-management.md: every capture surface
// produces ONE kind of record — a canonical Ticket.
//
// The legacy BacklogService is a compatibility adapter over TicketService, so
// these assertions cover every caller that goes through it: Action Center
// conversion, the Home assistant, BACKLOG.md import, and the legacy HTTP
// routes. That is the point of consolidating at the service rather than at
// each call site (FR-80, FR-85, FR-96).

func TestBacklogService_CreateProducesACanonicalTicket(t *testing.T) {
	store := newBacklogTestStore(t)
	legacy := NewBacklogService(store)
	tickets := NewTicketService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	task, err := legacy.Create(BacklogCreateInput{
		WorkspaceID: ws.ID,
		Description: "captured through the legacy path",
		Details:     "body",
		Tags:        []string{"infra"},
		Priority:    2,
		SourceType:  BacklogSourceActionCenter,
		SourceID:    "opp-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Canonical fields the legacy path never set on its own.
	if task.TicketState != TicketStateBacklog {
		t.Fatalf("TicketState = %q, want backlog", task.TicketState)
	}
	if task.TicketNumber == 0 {
		t.Fatalf("a legacy capture must still receive an immutable ticket number")
	}
	if task.TicketVersion == 0 {
		t.Fatalf("a legacy capture must still receive a concurrency token")
	}
	if len(task.StateHistory) != 1 || task.StateHistory[0].To != TicketStateBacklog {
		t.Fatalf("a legacy capture must still be audited: %+v", task.StateHistory)
	}
	if task.StateRank == 0 {
		t.Fatalf("a legacy capture must still receive a per-state rank")
	}
	// Legacy fields stay consistent so old consumers keep working.
	if task.Status != TaskStatusBacklog {
		t.Fatalf("legacy Status = %q, want backlog", task.Status)
	}
	if task.BacklogRank != task.StateRank {
		t.Fatalf("BacklogRank (%d) must track StateRank (%d) during the compatibility window",
			task.BacklogRank, task.StateRank)
	}
	// Provenance survives.
	if task.SourceType != BacklogSourceActionCenter || task.SourceID != "opp-1" {
		t.Fatalf("provenance lost: %q/%q", task.SourceType, task.SourceID)
	}

	// And the canonical API sees exactly the same record — one object, not two.
	ticket, err := tickets.Get(ws.ID, task.ID)
	if err != nil {
		t.Fatalf("canonical Get: %v", err)
	}
	if ticket.ID != task.ID || ticket.Number != task.TicketNumber {
		t.Fatalf("the legacy path created a record the canonical API does not recognize")
	}
	if ticket.Title != "captured through the legacy path" {
		t.Fatalf("Title = %q", ticket.Title)
	}

	page, err := tickets.Search(TicketQuery{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("workspace holds %d tickets, want 1 — a legacy capture must not create a duplicate", page.Total)
	}
}

func TestBacklogService_CreateReadyUnassignedProducesACanonicalTicket(t *testing.T) {
	store := newBacklogTestStore(t)
	legacy := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	task, err := legacy.CreateReadyUnassigned(BacklogCreateInput{
		WorkspaceID: ws.ID, Description: "committed through the legacy path",
	})
	if err != nil {
		t.Fatalf("CreateReadyUnassigned: %v", err)
	}

	if task.CanonicalState() != TicketStateReady {
		t.Fatalf("state = %q, want ready", task.CanonicalState())
	}
	if task.TicketNumber == 0 {
		t.Fatalf("legacy Ready capture must still receive a ticket number")
	}
	// Ready work created this way is still quiescent (FR-24).
	if !task.AwaitingExecutionIntent {
		t.Fatalf("legacy Ready capture must stay quiescent")
	}
	if task.To != "" {
		t.Fatalf("legacy Ready capture must not assign an agent, got %q", task.To)
	}
}

// FR-19: capture surfaces may default to Backlog, but the resulting record
// must still say plainly which state it landed in.
func TestBacklogService_CaptureStatesAreDistinguishable(t *testing.T) {
	store := newBacklogTestStore(t)
	legacy := NewBacklogService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	captured, err := legacy.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "captured"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	committed, err := legacy.CreateReadyUnassigned(BacklogCreateInput{WorkspaceID: ws.ID, Description: "committed"})
	if err != nil {
		t.Fatalf("CreateReadyUnassigned: %v", err)
	}

	if captured.CanonicalState() == committed.CanonicalState() {
		t.Fatalf("the two capture choices must remain distinguishable after the fact")
	}
	// Numbers keep incrementing across both paths — one sequence per workspace.
	if committed.TicketNumber <= captured.TicketNumber {
		t.Fatalf("numbers = %d, %d; want one increasing per-workspace sequence",
			captured.TicketNumber, committed.TicketNumber)
	}
}

// FR-7/FR-96: a legacy mutation must move CANONICAL state, not only the legacy
// projection.
//
// This is a regression test for a real divergence: BacklogService.Promote used
// to set Status=pending while leaving TicketState=backlog, so legacy consumers
// saw the item promoted and canonical consumers still saw it in Backlog — two
// answers to "what state is this in", which is exactly the dual authority this
// feature removes. Found by cross-checking the two APIs against one record on
// a live server.
func TestBacklogService_PromoteMovesCanonicalState(t *testing.T) {
	store := newBacklogTestStore(t)
	legacy := NewBacklogService(store)
	tickets := NewTicketService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	captured, err := legacy.Create(BacklogCreateInput{
		WorkspaceID: ws.ID, Description: "promote me through the legacy door",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	promoted, err := legacy.Promote(ws.ID, captured.ID)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Both views must agree.
	if promoted.TicketState != TicketStateReady {
		t.Fatalf("canonical TicketState = %q, want ready", promoted.TicketState)
	}
	if promoted.CanonicalState() != TicketStateReady {
		t.Fatalf("CanonicalState = %q, want ready", promoted.CanonicalState())
	}
	if promoted.Status != TaskStatusPending {
		t.Fatalf("legacy Status = %q, want pending", promoted.Status)
	}

	ticket, err := tickets.Get(ws.ID, captured.ID)
	if err != nil {
		t.Fatalf("canonical Get: %v", err)
	}
	if ticket.State != TicketStateReady {
		t.Fatalf("the canonical API still reports %q after a legacy promote", ticket.State)
	}
	// The transition is audited like any other, not silently applied.
	if len(ticket.StateHistory) != 2 {
		t.Fatalf("a legacy promote must be recorded in history, got %+v", ticket.StateHistory)
	}
	if !ticket.AwaitingExecutionIntent {
		t.Fatalf("promoted work must remain quiescent (FR-24)")
	}
	// And the item leaves the legacy Backlog projection too.
	items, err := legacy.List(ws.ID, false)
	if err != nil {
		t.Fatalf("legacy List: %v", err)
	}
	for _, item := range items {
		if item.Task.ID == captured.ID {
			t.Fatalf("a promoted item still appears in the legacy Backlog list")
		}
	}
}

// FR-60/FR-91: a legacy reorder must move the canonical rank too, or the two
// lists order differently.
func TestBacklogService_ReorderMovesCanonicalRank(t *testing.T) {
	store := newBacklogTestStore(t)
	legacy := NewBacklogService(store)
	tickets := NewTicketService(store)
	ws := newBacklogTestWorkspace(t, store, "Alpha")

	ids := make([]string, 0, 3)
	for _, title := range []string{"first", "second", "third"} {
		task, err := legacy.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: title})
		if err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
		ids = append(ids, task.ID)
	}

	reversed := []string{ids[2], ids[1], ids[0]}
	if _, err := legacy.Reorder(ws.ID, reversed); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	page, err := tickets.Search(TicketQuery{
		WorkspaceID: ws.ID, States: []TicketState{TicketStateBacklog},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tickets) != 3 {
		t.Fatalf("got %d tickets, want 3", len(page.Tickets))
	}
	for i, wantID := range reversed {
		if page.Tickets[i].ID != wantID {
			t.Fatalf("canonical order does not match the legacy reorder at %d: %q vs %q",
				i, page.Tickets[i].ID, wantID)
		}
	}
}

// FR-101: workspace counts must distinguish canonical states and count each
// Ticket exactly once.
func TestTicketService_StateCountsAreExactlyOncePerTicket(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "b1"})
	mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "b2"})
	ready := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "r1"})
	if _, err := svc.Transition(ws.ID, ready.ID, TicketTransitionInput{To: TicketStateInProgress, Actor: TicketActorUser}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	counts := map[TicketState]int{}
	total := 0
	for _, state := range AllTicketStates {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, States: []TicketState{state}})
		if err != nil {
			t.Fatalf("Search %s: %v", state, err)
		}
		counts[state] = page.Total
		total += page.Total
	}

	if counts[TicketStateBacklog] != 2 || counts[TicketStateInProgress] != 1 {
		t.Fatalf("counts = %+v, want 2 backlog and 1 in progress", counts)
	}
	if counts[TicketStateReady] != 0 {
		t.Fatalf("a ticket that moved on must not still be counted in Ready: %+v", counts)
	}

	all, err := svc.Search(TicketQuery{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The per-state counts must sum to the total: no Ticket counted twice, and
	// none missing from every bucket.
	if total != all.Total {
		t.Fatalf("per-state counts sum to %d but the workspace holds %d tickets", total, all.Total)
	}
}
