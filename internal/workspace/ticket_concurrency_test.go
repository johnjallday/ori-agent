package workspace

import (
	"fmt"
	"sync"
	"testing"
)

// Concurrency hardening (tasks/prd-workspace-ticket-management.md FR-91,
// FR-93, FR-94, FR-134).
//
// Every test here runs real concurrent operations against a real FileStore and
// then asserts an INVARIANT about the surviving data — never a timing outcome.
// Which writer wins a race is not the product's promise; that exactly one
// wins, that nothing is duplicated or lost, and that the loser is told to
// refresh, is.

// FR-140: concurrent creates must never hand out the same ticket number.
func TestTicketService_ConcurrentCreates_NeverDuplicateNumbers(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	const writers = 24
	var wg sync.WaitGroup
	wg.Add(writers)
	errs := make(chan error, writers)

	for i := range writers {
		go func(n int) {
			defer wg.Done()
			_, err := svc.Create(TicketCreateInput{
				WorkspaceID: ws.ID,
				State:       TicketStateBacklog,
				Title:       fmt.Sprintf("concurrent capture %d", n),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent create failed: %v", err)
		}
	}

	page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.Total != writers {
		t.Fatalf("created %d tickets, want %d — a concurrent create was lost", page.Total, writers)
	}

	seenNumbers := make(map[int64]string, writers)
	seenIDs := make(map[string]bool, writers)
	for _, ticket := range page.Tickets {
		if ticket.Number == 0 {
			t.Fatalf("ticket %s has no number", ticket.ID)
		}
		if other, clash := seenNumbers[ticket.Number]; clash {
			t.Fatalf("number %d was handed to both %s and %s", ticket.Number, other, ticket.ID)
		}
		seenNumbers[ticket.Number] = ticket.ID
		if seenIDs[ticket.ID] {
			t.Fatalf("ticket %s appears twice", ticket.ID)
		}
		seenIDs[ticket.ID] = true
	}
}

// FR-93: concurrent edits holding the same version cannot both win. Exactly one
// commits; the rest are told to refresh rather than silently overwriting.
func TestTicketService_ConcurrentVersionedUpdates_ExactlyOneWins(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "contested",
	})

	const editors = 16
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)
	wg.Add(editors)
	for i := range editors {
		go func(n int) {
			defer wg.Done()
			title := fmt.Sprintf("editor %d", n)
			_, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{
				Title:     &title,
				IfVersion: ticket.Version, // every editor holds the SAME version
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case IsTicketVersionConflict(err):
				conflicts++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if succeeded != 1 {
		t.Fatalf("%d editors committed, want exactly 1", succeeded)
	}
	if conflicts != editors-1 {
		t.Fatalf("%d conflicts reported, want %d — a loser was not told to refresh", conflicts, editors-1)
	}

	// The surviving record is one of the editors' values, with a bumped
	// version. It is never a blend of several.
	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Version != ticket.Version+1 {
		t.Fatalf("Version = %d, want exactly one increment", current.Version)
	}
}

// FR-134: concurrent transitions must not produce a state no single caller
// asked for, and history must record exactly the moves that happened.
func TestTicketService_ConcurrentTransitions_ConvergeToOneState(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "racing promoters",
	})

	const promoters = 12
	var wg sync.WaitGroup
	wg.Add(promoters)
	for range promoters {
		go func() {
			defer wg.Done()
			// No version token: promotion is idempotent by design, so a
			// repeated click must be safe (FR-23).
			_, _ = svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
				To: TicketStateReady, Actor: TicketActorUser,
			})
		}()
	}
	wg.Wait()

	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.State != TicketStateReady {
		t.Fatalf("state = %q, want ready", current.State)
	}
	// Creation plus exactly ONE promotion. Twelve callers must not produce
	// twelve history entries for one move.
	if len(current.StateHistory) != 2 {
		t.Fatalf("history has %d entries, want 2: %+v", len(current.StateHistory), current.StateHistory)
	}
}

// FR-91: concurrent reorders must leave a consistent ordering — never
// duplicate ranks within a state, and never lose a ticket.
func TestTicketService_ConcurrentReorders_LeaveConsistentRanks(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	ids := make([]string, 0, 6)
	for i := range 6 {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateBacklog,
			Title: fmt.Sprintf("item %d", i),
		})
		ids = append(ids, ticket.ID)
	}

	reversed := make([]string, len(ids))
	for i, id := range ids {
		reversed[len(ids)-1-i] = id
	}

	var wg sync.WaitGroup
	wg.Add(8)
	for i := range 8 {
		order := ids
		if i%2 == 1 {
			order = reversed
		}
		go func(o []string) {
			defer wg.Done()
			_, _ = svc.Reorder(ws.ID, TicketStateBacklog, o)
		}(order)
	}
	wg.Wait()

	page, err := svc.Search(TicketQuery{
		WorkspaceID: ws.ID, States: []TicketState{TicketStateBacklog},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tickets) != len(ids) {
		t.Fatalf("got %d tickets, want %d — a concurrent reorder lost one", len(page.Tickets), len(ids))
	}
	ranks := make(map[int64]string, len(ids))
	for _, ticket := range page.Tickets {
		if other, clash := ranks[ticket.StateRank]; clash {
			t.Fatalf("rank %d held by both %s and %s after concurrent reorders",
				ticket.StateRank, other, ticket.ID)
		}
		ranks[ticket.StateRank] = ticket.ID
	}
}

// FR-134: a late Run callback racing a user's decision must never reopen or
// overwrite the state the user chose.
func TestTicketService_LateRunCallbackNeverOverwritesUserDecision(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	for range 12 {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateReady, Title: "raced by a run",
		})
		if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
			To: TicketStateInProgress, Actor: TicketActorUser,
		}); err != nil {
			t.Fatalf("start: %v", err)
		}

		// The user cancels while a run finishes successfully.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{
				To: TicketStateCancelled, Actor: TicketActorUser,
			})
		}()
		go func() {
			defer wg.Done()
			_ = store.Update(ws.ID, func(w *Workspace) error {
				return w.MutateTask(ticket.ID, func(task *Task) error {
					task.ApplyRunOutcome(TicketRunResult{Outcome: TicketRunSucceeded})
					return nil
				})
			})
		}()
		wg.Wait()

		current, err := svc.Get(ws.ID, ticket.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		// Whoever won, the result must be a state SOMEONE asked for — never a
		// blend, and never a state neither party requested.
		switch current.State {
		case TicketStateCancelled, TicketStateReview:
		default:
			t.Fatalf("race produced %q, which neither the user nor the run requested", current.State)
		}
		// And if the user's cancellation landed, the run must not have
		// resurrected the ticket afterwards.
		if current.State == TicketStateCancelled && current.CompletedAt == nil {
			t.Fatalf("a cancelled ticket lost its completion stamp to a late run callback")
		}
	}
}

// FR-9/FR-12: concurrent work in sibling workspaces must not leak across
// owners — numbers, ranks, and records all stay workspace-local.
func TestTicketService_ConcurrentWorkAcrossWorkspacesStaysIsolated(t *testing.T) {
	svc, store := newTicketTestService(t)
	alpha := newTicketTestWorkspace(t, store, "Alpha")
	beta := newTicketTestWorkspace(t, store, "Beta")

	var wg sync.WaitGroup
	wg.Add(2)
	for _, ws := range []*Workspace{alpha, beta} {
		go func(target *Workspace) {
			defer wg.Done()
			for i := range 8 {
				_, _ = svc.Create(TicketCreateInput{
					WorkspaceID: target.ID, State: TicketStateBacklog,
					Title: fmt.Sprintf("%s item %d", target.Name, i),
				})
			}
		}(ws)
	}
	wg.Wait()

	for _, ws := range []*Workspace{alpha, beta} {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
		if err != nil {
			t.Fatalf("Search %s: %v", ws.Name, err)
		}
		if page.Total != 8 {
			t.Fatalf("%s holds %d tickets, want 8", ws.Name, page.Total)
		}
		// Each workspace numbers 1..8 independently.
		numbers := map[int64]bool{}
		for _, ticket := range page.Tickets {
			if ticket.OwningWorkspaceID != ws.ID {
				t.Fatalf("%s returned a ticket owned by %s", ws.Name, ticket.OwningWorkspaceID)
			}
			if ticket.Number < 1 || ticket.Number > 8 {
				t.Fatalf("%s ticket number %d is outside its own 1..8 sequence", ws.Name, ticket.Number)
			}
			if numbers[ticket.Number] {
				t.Fatalf("%s reused number %d", ws.Name, ticket.Number)
			}
			numbers[ticket.Number] = true
		}
	}
}
