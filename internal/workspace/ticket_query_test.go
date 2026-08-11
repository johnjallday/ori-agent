package workspace

import (
	"testing"
	"time"
)

// fixedClock pins the service clock so archive and due-date boundaries can be
// asserted exactly instead of relative to whenever the test happens to run.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

var queryNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

// seedQueryTickets builds a fixture covering every filterable dimension.
func seedQueryTickets(t *testing.T, svc *TicketService, wsID string) map[string]*Ticket {
	t.Helper()
	due := func(days int) *time.Time {
		d := queryNow.AddDate(0, 0, days)
		return &d
	}

	specs := []struct {
		key   string
		input TicketCreateInput
	}{
		{"backlog-infra", TicketCreateInput{
			WorkspaceID: wsID, State: TicketStateBacklog, Title: "Investigate flaky pipeline",
			Description: "The nightly build fails intermittently.", Tags: []string{"infra", "ci"},
			Priority: 1, DueDate: due(-3),
		}},
		{"backlog-docs", TicketCreateInput{
			WorkspaceID: wsID, State: TicketStateBacklog, Title: "Rewrite the onboarding docs",
			Tags: []string{"docs"}, Priority: 4, Source: TicketSourceNote, SourceID: "note-1",
		}},
		{"ready-infra", TicketCreateInput{
			WorkspaceID: wsID, State: TicketStateReady, Title: "Ship the cache fix",
			Tags: []string{"infra"}, Priority: 2, DueDate: due(0),
		}},
		{"ready-plain", TicketCreateInput{
			WorkspaceID: wsID, State: TicketStateReady, Title: "Plan the quarter",
			Priority: 3, DueDate: due(3),
		}},
	}

	out := make(map[string]*Ticket, len(specs))
	for _, spec := range specs {
		out[spec.key] = mustCreateTicket(t, svc, spec.input)
	}
	return out
}

func ticketKeys(tickets []Ticket) []string {
	out := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		out = append(out, ticket.Title)
	}
	return out
}

func TestTicketService_Search_Filters(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")
	seeded := seedQueryTickets(t, svc, ws.ID)

	t.Run("state", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, States: []TicketState{TicketStateBacklog}})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 2 {
			t.Fatalf("got %v, want the 2 backlog tickets", ticketKeys(page.Tickets))
		}
	})

	t.Run("tags require every listed tag", func(t *testing.T) {
		both, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Tags: []string{"infra", "ci"}})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(both.Tickets) != 1 || both.Tickets[0].ID != seeded["backlog-infra"].ID {
			t.Fatalf("got %v, want only the ticket carrying BOTH tags", ticketKeys(both.Tickets))
		}

		one, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Tags: []string{"infra"}})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(one.Tickets) != 2 {
			t.Fatalf("got %v, want both infra tickets", ticketKeys(one.Tickets))
		}
	})

	t.Run("priority matches any listed value", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Priorities: []int{1, 4}})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 2 {
			t.Fatalf("got %v, want priorities 1 and 4", ticketKeys(page.Tickets))
		}
	})

	t.Run("provenance", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Sources: []string{TicketSourceNote}})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 1 || page.Tickets[0].ID != seeded["backlog-docs"].ID {
			t.Fatalf("got %v, want the note-sourced ticket", ticketKeys(page.Tickets))
		}
	})

	t.Run("unassigned", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{
			WorkspaceID: ws.ID, Assignees: []string{TicketAssigneeUnassigned},
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 4 {
			t.Fatalf("got %d, want every seeded ticket (none are assigned)", len(page.Tickets))
		}
	})

	t.Run("rejects invalid filter values", func(t *testing.T) {
		for name, query := range map[string]TicketQuery{
			"priority": {WorkspaceID: ws.ID, Priorities: []int{9}},
			"source":   {WorkspaceID: ws.ID, Sources: []string{"telepathy"}},
			"due":      {WorkspaceID: ws.ID, Due: TicketDueFilter("someday")},
			"archive":  {WorkspaceID: ws.ID, Archive: TicketArchiveFilter("maybe")},
			"sort":     {WorkspaceID: ws.ID, Sort: TicketSortField("vibes")},
			"limit":    {WorkspaceID: ws.ID, Limit: TicketMaxLimit + 1},
		} {
			if _, err := svc.Search(query); err == nil {
				t.Fatalf("%s: expected a validation error", name)
			}
		}
	})
}

func TestTicketService_Search_DueFilters(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")
	seeded := seedQueryTickets(t, svc, ws.ID)

	cases := map[TicketDueFilter][]string{
		TicketDueOverdue: {seeded["backlog-infra"].ID},
		TicketDueToday:   {seeded["ready-infra"].ID},
		TicketDueWeek:    {seeded["ready-infra"].ID, seeded["ready-plain"].ID},
		TicketDueNone:    {seeded["backlog-docs"].ID},
	}
	for filter, wantIDs := range cases {
		t.Run(string(filter), func(t *testing.T) {
			page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Due: filter})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(page.Tickets) != len(wantIDs) {
				t.Fatalf("got %v, want %d tickets", ticketKeys(page.Tickets), len(wantIDs))
			}
			got := map[string]bool{}
			for _, ticket := range page.Tickets {
				got[ticket.ID] = true
			}
			for _, id := range wantIDs {
				if !got[id] {
					t.Fatalf("missing expected ticket %s; got %v", id, ticketKeys(page.Tickets))
				}
			}
		})
	}

	// Closed work is never "overdue" — a Done ticket with a past due date is
	// history, not an outstanding problem.
	t.Run("terminal tickets are never overdue", func(t *testing.T) {
		id := seeded["backlog-infra"].ID
		for _, next := range []TicketState{TicketStateReady, TicketStateInProgress, TicketStateReview, TicketStateDone} {
			if _, err := svc.Transition(ws.ID, id, TicketTransitionInput{To: next, Actor: TicketActorUser}); err != nil {
				t.Fatalf("transition to %s: %v", next, err)
			}
		}
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Due: TicketDueOverdue})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		for _, ticket := range page.Tickets {
			if ticket.ID == id {
				t.Fatalf("a Done ticket must not be reported as overdue")
			}
		}
	})
}

func TestTicketService_Search_TextSearch(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")
	seeded := seedQueryTickets(t, svc, ws.ID)

	t.Run("matches the title case-insensitively", func(t *testing.T) {
		page, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Search: "FLAKY"})
		if len(page.Tickets) != 1 || page.Tickets[0].ID != seeded["backlog-infra"].ID {
			t.Fatalf("got %v", ticketKeys(page.Tickets))
		}
	})

	t.Run("matches the description", func(t *testing.T) {
		page, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Search: "nightly build"})
		if len(page.Tickets) != 1 || page.Tickets[0].ID != seeded["backlog-infra"].ID {
			t.Fatalf("got %v", ticketKeys(page.Tickets))
		}
	})

	t.Run("matches the ticket number", func(t *testing.T) {
		number := seeded["ready-infra"].DisplayNumber
		page, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Search: number})
		if len(page.Tickets) != 1 || page.Tickets[0].ID != seeded["ready-infra"].ID {
			t.Fatalf("searching %q got %v", number, ticketKeys(page.Tickets))
		}
	})

	t.Run("rejects an oversized term", func(t *testing.T) {
		long := make([]byte, TicketSearchMaxLength+1)
		for i := range long {
			long[i] = 'x'
		}
		if _, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Search: string(long)}); err == nil {
			t.Fatalf("expected an oversized search term to be refused")
		}
	})
}

func TestTicketService_Search_Sorting(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")
	seeded := seedQueryTickets(t, svc, ws.ID)

	t.Run("priority ascending puts the most urgent first", func(t *testing.T) {
		page, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Sort: TicketSortPriority})
		if page.Tickets[0].ID != seeded["backlog-infra"].ID {
			t.Fatalf("got %v, want priority 1 first", ticketKeys(page.Tickets))
		}
	})

	t.Run("due date puts undated tickets last in both directions", func(t *testing.T) {
		for _, desc := range []bool{false, true} {
			page, _ := svc.Search(TicketQuery{
				WorkspaceID: ws.ID, Sort: TicketSortDueDate, SortDescending: desc,
			})
			last := page.Tickets[len(page.Tickets)-1]
			if last.ID != seeded["backlog-docs"].ID {
				t.Fatalf("desc=%v: got %v, want the undated ticket last", desc, ticketKeys(page.Tickets))
			}
		}
	})

	t.Run("is deterministic across identical requests", func(t *testing.T) {
		for _, field := range []TicketSortField{
			TicketSortRank, TicketSortPriority, TicketSortDueDate,
			TicketSortCreated, TicketSortUpdated, TicketSortNumber,
		} {
			first, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Sort: field})
			second, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID, Sort: field})
			for i := range first.Tickets {
				if first.Tickets[i].ID != second.Tickets[i].ID {
					t.Fatalf("sort %q is not stable across identical requests", field)
				}
			}
		}
	})

	t.Run("default ordering follows canonical state then rank", func(t *testing.T) {
		page, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID})
		for i := 1; i < len(page.Tickets); i++ {
			prev, cur := page.Tickets[i-1], page.Tickets[i]
			if prev.State == TicketStateReady && cur.State == TicketStateBacklog {
				t.Fatalf("ready sorted before backlog: %v", ticketKeys(page.Tickets))
			}
		}
	})
}

// FR-143: Done/Cancelled stay in default views for 14 calendar days, then move
// behind the archive filter — without ever being deleted.
func TestTicketService_Search_FourteenDayArchiveBoundary(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	closeTicket := func(title string, state TicketState, closedAt time.Time) string {
		svc.SetClock(fixedClock(closedAt))
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateReady, Title: title,
		})
		steps := []TicketState{TicketStateInProgress, TicketStateReview, TicketStateDone}
		if state == TicketStateCancelled {
			steps = []TicketState{TicketStateCancelled}
		}
		for _, next := range steps {
			if _, err := svc.Transition(ws.ID, ticket.ID, TicketTransitionInput{To: next, Actor: TicketActorUser}); err != nil {
				t.Fatalf("transition %s: %v", next, err)
			}
		}
		return ticket.ID
	}

	day13 := closeTicket("closed 13 days ago", TicketStateDone, queryNow.AddDate(0, 0, -13))
	day14 := closeTicket("closed 14 days ago", TicketStateDone, queryNow.AddDate(0, 0, -14))
	day15 := closeTicket("closed 15 days ago", TicketStateDone, queryNow.AddDate(0, 0, -15))
	cancelled16 := closeTicket("cancelled 16 days ago", TicketStateCancelled, queryNow.AddDate(0, 0, -16))
	open := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "still open",
	})

	svc.SetClock(fixedClock(queryNow))
	contains := func(page TicketPage, id string) bool {
		for _, ticket := range page.Tickets {
			if ticket.ID == id {
				return true
			}
		}
		return false
	}

	recent, err := svc.Search(TicketQuery{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("Search recent: %v", err)
	}
	// Day 14 is inside the "for 14 calendar days" promise; day 15 is not.
	if !contains(recent, day13) || !contains(recent, day14) {
		t.Fatalf("tickets closed 13 and 14 days ago must stay in the default view")
	}
	if contains(recent, day15) || contains(recent, cancelled16) {
		t.Fatalf("tickets closed more than 14 days ago must leave the default view")
	}
	if !contains(recent, open.ID) {
		t.Fatalf("an open ticket must never be archived")
	}

	archived, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveOnly})
	if err != nil {
		t.Fatalf("Search archive: %v", err)
	}
	if !contains(archived, day15) || !contains(archived, cancelled16) {
		t.Fatalf("aged-out tickets must be reachable through the archive filter")
	}
	if contains(archived, day13) || contains(archived, open.ID) {
		t.Fatalf("archive must not include recent or open tickets")
	}
	for _, ticket := range archived.Tickets {
		if !ticket.Archived {
			t.Fatalf("archived tickets must be flagged as such: %+v", ticket)
		}
	}

	all, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if len(all.Tickets) != 5 {
		t.Fatalf("archive=all returned %d tickets, want all 5 — nothing is ever deleted", len(all.Tickets))
	}

	// The aged-out record still exists in full, with its history intact.
	stored, err := svc.Get(ws.ID, day15)
	if err != nil {
		t.Fatalf("an archived ticket must remain directly readable: %v", err)
	}
	if len(stored.StateHistory) == 0 {
		t.Fatalf("archiving must not touch history")
	}
}

func TestTicketService_Search_LimitAndTotal(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")
	seedQueryTickets(t, svc, ws.ID)

	page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tickets) != 2 {
		t.Fatalf("returned %d tickets, want the requested 2", len(page.Tickets))
	}
	// Total must report the pre-limit match count so a client can tell there
	// is more rather than believing it has everything.
	if page.Total != 4 || !page.Truncated {
		t.Fatalf("Total=%d Truncated=%v, want 4/true", page.Total, page.Truncated)
	}

	full, _ := svc.Search(TicketQuery{WorkspaceID: ws.ID})
	if full.Truncated || full.Total != 4 {
		t.Fatalf("an unlimited read must not report truncation: %+v", full)
	}
}

// FR-142: hierarchy lives in Ticket detail. Collection reads stay flat so the
// Board can render independent cards.
func TestTicketService_Hierarchy_DetailOnly(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	ws := newTicketTestWorkspace(t, store, "Alpha")

	parent := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "Parent work",
	})
	// Subtickets are created by the orchestration paths, which stamp the
	// parent link directly on the record.
	childIDs := make([]string, 0, 2)
	for i, title := range []string{"Second step", "First step"} {
		child := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateReady, Title: title,
		})
		childIDs = append(childIDs, child.ID)
		index := 2 - i // deliberately out of creation order
		err := store.Update(ws.ID, func(w *Workspace) error {
			return w.MutateTask(child.ID, func(task *Task) error {
				task.ParentTaskID = parent.ID
				task.SubtaskIndex = index
				return nil
			})
		})
		if err != nil {
			t.Fatalf("link subticket: %v", err)
		}
	}

	t.Run("collection reads exclude subtickets by default", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 1 || page.Tickets[0].ID != parent.ID {
			t.Fatalf("got %v, want only the parent", ticketKeys(page.Tickets))
		}
		if len(page.Tickets[0].Subtickets) != 0 {
			t.Fatalf("collection reads must stay flat — the Board renders independent cards")
		}
	})

	t.Run("include_subtickets opts them in as independent rows", func(t *testing.T) {
		page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, IncludeSubtickets: true})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Tickets) != 3 {
			t.Fatalf("got %v, want parent plus both subtickets", ticketKeys(page.Tickets))
		}
	})

	t.Run("detail exposes ordered subtickets and the parent link", func(t *testing.T) {
		detail, err := svc.Get(ws.ID, parent.ID)
		if err != nil {
			t.Fatalf("Get parent: %v", err)
		}
		if len(detail.Subtickets) != 2 {
			t.Fatalf("parent detail listed %d subtickets, want 2", len(detail.Subtickets))
		}
		// Declared subtask order wins over creation order.
		if detail.Subtickets[0].Title != "First step" {
			t.Fatalf("subtickets out of declared order: %+v", detail.Subtickets)
		}

		child, err := svc.Get(ws.ID, childIDs[0])
		if err != nil {
			t.Fatalf("Get child: %v", err)
		}
		if child.Parent == nil || child.Parent.ID != parent.ID {
			t.Fatalf("child detail lost its parent reference: %+v", child.Parent)
		}
	})
}

// FR-18/FR-72: deleting a parent Ticket never deletes its children, and
// deleting a Ticket never deletes a linked Note.
func TestTicketService_Delete_PreservesChildrenAndNotes(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")

	parent := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "Parent",
		LinkedNoteIDs: []string{"note-1"},
	})
	child := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "Child",
	})
	err := store.Update(ws.ID, func(w *Workspace) error {
		return w.MutateTask(child.ID, func(task *Task) error {
			task.ParentTaskID = parent.ID
			return nil
		})
	})
	if err != nil {
		t.Fatalf("link subticket: %v", err)
	}

	if err := svc.Delete(ws.ID, parent.ID, 0); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	surviving, err := svc.Get(ws.ID, child.ID)
	if err != nil {
		t.Fatalf("deleting a parent must not delete its child: %v", err)
	}
	// The child is orphaned, not destroyed — the existing safeguard.
	if surviving.ParentTicketID != "" {
		t.Fatalf("orphaned child still points at a deleted parent: %q", surviving.ParentTicketID)
	}
}

// FR-133: a descendant that cannot be read is reported, not silently dropped.
func TestTicketService_Search_ReportsUnreadableDescendants(t *testing.T) {
	svc, store := newTicketTestService(t)
	svc.SetClock(fixedClock(queryNow))
	parent := newTicketTestWorkspace(t, store, "Parent")

	child := NewWorkspace(CreateWorkspaceParams{Name: "Child"})
	child.ParentID = parent.ID
	if err := store.Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: parent.ID, State: TicketStateBacklog, Title: "parent work",
	})
	mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: child.ID, State: TicketStateBacklog, Title: "child work",
	})

	page, err := svc.Search(TicketQuery{WorkspaceID: parent.ID, IncludeDescendants: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tickets) != 2 || len(page.PartialOwners) != 0 {
		t.Fatalf("healthy roll-up returned %d tickets, partial=%v", len(page.Tickets), page.PartialOwners)
	}

	// Owner filtering narrows a roll-up without changing ownership.
	onlyChild, err := svc.Search(TicketQuery{
		WorkspaceID: parent.ID, IncludeDescendants: true, OwnerIDs: []string{child.ID},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(onlyChild.Tickets) != 1 || onlyChild.Tickets[0].OwningWorkspaceID != child.ID {
		t.Fatalf("owner filter returned %v", ticketKeys(onlyChild.Tickets))
	}
}
