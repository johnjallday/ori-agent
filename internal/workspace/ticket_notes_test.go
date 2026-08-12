package workspace

import (
	"context"
	"strings"
	"testing"
)

// fakeNoteLookup is an in-memory Note store standing in for internal/session.
// It records mutations so tests can assert that linking never touches a Note.
type fakeNoteLookup struct {
	notes  map[string]TicketNoteRef
	writes int
}

func newFakeNoteLookup(refs ...TicketNoteRef) *fakeNoteLookup {
	notes := make(map[string]TicketNoteRef, len(refs))
	for _, ref := range refs {
		notes[ref.ID] = ref
	}
	return &fakeNoteLookup{notes: notes}
}

func (f *fakeNoteLookup) LookupNote(_ context.Context, noteID string) (TicketNoteRef, error) {
	ref, ok := f.notes[noteID]
	if !ok {
		return TicketNoteRef{}, ErrNoteNotFound
	}
	return ref, nil
}

func newNoteLinkedService(t *testing.T) (*TicketService, *Workspace, *fakeNoteLookup) {
	t.Helper()
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	notes := newFakeNoteLookup(
		TicketNoteRef{ID: "note-1", Title: "Research spike", WorkspaceID: ws.ID},
		TicketNoteRef{ID: "note-2", Title: "Meeting notes", WorkspaceID: ws.ID},
		TicketNoteRef{ID: "note-foreign", Title: "Someone else's note", WorkspaceID: "ws-other"},
	)
	svc.SetNoteLookup(notes)
	return svc, ws, notes
}

// FR-17/FR-77: linking is explicit, structured, and idempotent.
func TestTicketService_LinkNote(t *testing.T) {
	svc, ws, _ := newNoteLinkedService(t)
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "needs research",
	})
	ctx := context.Background()

	linked, err := svc.LinkNote(ctx, ws.ID, ticket.ID, "note-1", ticket.Version)
	if err != nil {
		t.Fatalf("LinkNote: %v", err)
	}
	if len(linked.LinkedNoteIDs) != 1 || linked.LinkedNoteIDs[0] != "note-1" {
		t.Fatalf("LinkedNoteIDs = %v", linked.LinkedNoteIDs)
	}

	t.Run("is idempotent", func(t *testing.T) {
		again, err := svc.LinkNote(ctx, ws.ID, ticket.ID, "note-1", 0)
		if err != nil {
			t.Fatalf("re-linking should be safe, got %v", err)
		}
		if len(again.LinkedNoteIDs) != 1 {
			t.Fatalf("re-linking duplicated the reference: %v", again.LinkedNoteIDs)
		}
		if again.Version != linked.Version {
			t.Fatalf("a no-op link bumped the version %d → %d", linked.Version, again.Version)
		}
	})

	t.Run("supports many notes", func(t *testing.T) {
		many, err := svc.LinkNote(ctx, ws.ID, ticket.ID, "note-2", 0)
		if err != nil {
			t.Fatalf("LinkNote: %v", err)
		}
		if len(many.LinkedNoteIDs) != 2 {
			t.Fatalf("LinkedNoteIDs = %v, want both", many.LinkedNoteIDs)
		}
	})

	t.Run("refuses unknown and foreign notes identically", func(t *testing.T) {
		for _, noteID := range []string{"note-missing", "note-foreign"} {
			_, err := svc.LinkNote(ctx, ws.ID, ticket.ID, noteID, 0)
			if !IsNoteNotFound(err) {
				t.Fatalf("linking %q: error = %v, want ErrNoteNotFound", noteID, err)
			}
		}
	})

	t.Run("requires a note id", func(t *testing.T) {
		if _, err := svc.LinkNote(ctx, ws.ID, ticket.ID, "  ", 0); err == nil {
			t.Fatalf("expected an empty note id to be refused")
		}
	})
}

// FR-18: unlinking removes the reference and nothing else.
func TestTicketService_UnlinkNote(t *testing.T) {
	svc, ws, notes := newNoteLinkedService(t)
	ctx := context.Background()
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "linked work",
		LinkedNoteIDs: []string{"note-1", "note-2"},
	})

	unlinked, err := svc.UnlinkNote(ctx, ws.ID, ticket.ID, "note-1", 0)
	if err != nil {
		t.Fatalf("UnlinkNote: %v", err)
	}
	if len(unlinked.LinkedNoteIDs) != 1 || unlinked.LinkedNoteIDs[0] != "note-2" {
		t.Fatalf("LinkedNoteIDs = %v, want only note-2", unlinked.LinkedNoteIDs)
	}
	// The Note itself is untouched — no writes reached the note store.
	if notes.writes != 0 {
		t.Fatalf("unlinking wrote to the note store %d times, want 0", notes.writes)
	}

	t.Run("unlinking something not linked is a no-op", func(t *testing.T) {
		before := unlinked.Version
		again, err := svc.UnlinkNote(ctx, ws.ID, ticket.ID, "note-1", 0)
		if err != nil {
			t.Fatalf("repeat unlink should be safe, got %v", err)
		}
		if again.Version != before {
			t.Fatalf("a no-op unlink bumped the version")
		}
	})
}

// FR-75/FR-78: the reverse direction is derived from the same structured
// relationship, so the two views cannot disagree.
func TestTicketService_TicketsLinkedToNote(t *testing.T) {
	svc, ws, _ := newNoteLinkedService(t)
	ctx := context.Background()

	a := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "first", LinkedNoteIDs: []string{"note-1"},
	})
	mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "unrelated",
	})
	b := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateReady, Title: "second", LinkedNoteIDs: []string{"note-1", "note-2"},
	})

	linked, err := svc.TicketsLinkedToNote(ws.ID, "note-1")
	if err != nil {
		t.Fatalf("TicketsLinkedToNote: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("got %d tickets, want the 2 linked to note-1", len(linked))
	}
	found := map[string]bool{}
	for _, summary := range linked {
		found[summary.ID] = true
		// Summaries carry enough to display and navigate, and no more.
		if summary.Title == "" || summary.StateLabel == "" || summary.OwningWorkspaceID != ws.ID {
			t.Fatalf("summary is missing display identity: %+v", summary)
		}
	}
	if !found[a.ID] || !found[b.ID] {
		t.Fatalf("reverse lookup missed a linked ticket")
	}

	// Forward and reverse agree.
	refs, err := svc.LinkedNotes(ctx, ws.ID, b.ID)
	if err != nil {
		t.Fatalf("LinkedNotes: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("forward lookup returned %d notes, want 2", len(refs))
	}
}

// FR-73 through FR-76: creating from a Note is a reviewed decision that
// preserves the Note and copies its content only once.
func TestTicketService_CreateTicketFromNote(t *testing.T) {
	svc, ws, notes := newNoteLinkedService(t)
	ctx := context.Background()

	ticket, err := svc.CreateTicketFromNote(ctx, ws.ID, "note-1", TicketCreateInput{
		State:       TicketStateBacklog,
		Title:       "Reviewed title the user edited",
		Description: "Reviewed body",
	})
	if err != nil {
		t.Fatalf("CreateTicketFromNote: %v", err)
	}

	if ticket.Source != TicketSourceNote || ticket.SourceID != "note-1" {
		t.Fatalf("provenance = %q/%q, want note/note-1", ticket.Source, ticket.SourceID)
	}
	if len(ticket.LinkedNoteIDs) != 1 || ticket.LinkedNoteIDs[0] != "note-1" {
		t.Fatalf("the originating note must be linked: %v", ticket.LinkedNoteIDs)
	}
	// The user's reviewed values win over the note's.
	if ticket.Title != "Reviewed title the user edited" {
		t.Fatalf("Title = %q, want the reviewed value", ticket.Title)
	}
	// The Note itself was never written.
	if notes.writes != 0 {
		t.Fatalf("creating from a note wrote to the note store %d times, want 0", notes.writes)
	}

	t.Run("falls back to the note title when none is supplied", func(t *testing.T) {
		fromNote, err := svc.CreateTicketFromNote(ctx, ws.ID, "note-2", TicketCreateInput{
			State: TicketStateReady,
		})
		if err != nil {
			t.Fatalf("CreateTicketFromNote: %v", err)
		}
		if fromNote.Title != "Meeting notes" {
			t.Fatalf("Title = %q, want the note's name as prefill", fromNote.Title)
		}
	})

	t.Run("still requires an explicit capture state", func(t *testing.T) {
		if _, err := svc.CreateTicketFromNote(ctx, ws.ID, "note-1", TicketCreateInput{}); err == nil {
			t.Fatalf("creating from a note must still make the Backlog/Ready choice explicit")
		}
	})

	t.Run("refuses a foreign note", func(t *testing.T) {
		_, err := svc.CreateTicketFromNote(ctx, ws.ID, "note-foreign", TicketCreateInput{
			State: TicketStateBacklog, Title: "stolen",
		})
		if !IsNoteNotFound(err) {
			t.Fatalf("error = %v, want ErrNoteNotFound", err)
		}
	})
}

// FR-76: prefill is a ONE-TIME copy. Editing either side never rewrites the
// other — there is no synchronization anywhere in this path.
func TestTicketService_NoteAndTicketBodiesStayIndependent(t *testing.T) {
	svc, ws, notes := newNoteLinkedService(t)
	ctx := context.Background()

	ticket, err := svc.CreateTicketFromNote(ctx, ws.ID, "note-1", TicketCreateInput{
		State: TicketStateBacklog, Title: "Research spike", Description: "copied once",
	})
	if err != nil {
		t.Fatalf("CreateTicketFromNote: %v", err)
	}

	// Edit the Ticket.
	newTitle := "Ticket went its own way"
	newBody := "ticket body only"
	if _, err := svc.Update(ws.ID, ticket.ID, TicketUpdateInput{
		Title: &newTitle, Description: &newBody,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The Note is unchanged, and no write ever reached its store.
	ref, err := notes.LookupNote(ctx, "note-1")
	if err != nil {
		t.Fatalf("LookupNote: %v", err)
	}
	if ref.Title != "Research spike" {
		t.Fatalf("editing the ticket changed the note title to %q", ref.Title)
	}
	if notes.writes != 0 {
		t.Fatalf("editing a ticket wrote to the note store")
	}

	// Re-reading the Ticket does not pull the Note's content back in.
	current, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if current.Title != newTitle || current.Description != newBody {
		t.Fatalf("a reload re-synchronized from the note: %q / %q", current.Title, current.Description)
	}
}

// FR-18/FR-72: deleting a Ticket unlinks but never deletes a Note, and a Note
// that disappears leaves the Ticket intact.
func TestTicketService_DeletionIsIndependentInBothDirections(t *testing.T) {
	svc, ws, notes := newNoteLinkedService(t)
	ctx := context.Background()

	t.Run("deleting a ticket keeps the note", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "doomed",
			LinkedNoteIDs: []string{"note-1"},
		})
		if err := svc.Delete(ws.ID, ticket.ID, 0); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := notes.LookupNote(ctx, "note-1"); err != nil {
			t.Fatalf("deleting a ticket deleted its linked note: %v", err)
		}
		if notes.writes != 0 {
			t.Fatalf("deleting a ticket wrote to the note store")
		}
	})

	t.Run("a deleted note leaves the ticket readable", func(t *testing.T) {
		ticket := mustCreateTicket(t, svc, TicketCreateInput{
			WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "survivor",
			LinkedNoteIDs: []string{"note-2"},
		})
		delete(notes.notes, "note-2")

		current, err := svc.Get(ws.ID, ticket.ID)
		if err != nil {
			t.Fatalf("a stale note link must not break the ticket: %v", err)
		}
		// The reference is RETAINED rather than silently rewritten, so the
		// user decides what to do about it.
		if len(current.LinkedNoteIDs) != 1 {
			t.Fatalf("the stale link was silently removed: %v", current.LinkedNoteIDs)
		}
		// Display resolution simply skips it.
		refs, err := svc.LinkedNotes(ctx, ws.ID, ticket.ID)
		if err != nil {
			t.Fatalf("LinkedNotes: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("a deleted note should not resolve for display, got %+v", refs)
		}
	})
}

// FR-17/FR-71: with no note store wired, links are refused rather than
// accepted unvalidated.
func TestTicketService_LinkNote_RefusedWithoutANoteStore(t *testing.T) {
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Alpha")
	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "no lookup",
	})

	if _, err := svc.LinkNote(context.Background(), ws.ID, ticket.ID, "note-1", 0); err == nil {
		t.Fatalf("linking without a note store must be refused, not silently accepted")
	}
}

// FR-79/FR-104: linked Notes reach a Run as identity only. Injecting bodies
// here would route around the workspace-note tools that already enforce this
// workspace's permissions.
func TestTaskPreparedContext_LinkedNotesAreIdentityOnly(t *testing.T) {
	task := &Task{
		ID:            "tkt-1",
		WorkspaceID:   "ws-1",
		Description:   "work with context",
		TicketState:   TicketStateReady,
		LinkedNoteIDs: []string{"note-1", "note-2"},
	}

	item, ok := linkedNotesContextItem(*task)
	if !ok {
		t.Fatalf("linked notes are missing from prepared run context")
	}

	// Identity is advertised...
	if !strings.Contains(item.Ref, "note-1") || !strings.Contains(item.Ref, "note-2") {
		t.Fatalf("linked note ids missing from context ref: %q", item.Ref)
	}
	// ...but access stays behind the existing note tools.
	if item.Access != "on_demand" {
		t.Fatalf("Access = %q, want on_demand — note bodies must not be injected", item.Access)
	}
	// Nothing resembling note content may appear anywhere in the item.
	for _, value := range []string{item.Name, item.Detail} {
		if strings.Contains(value, "Findings") || strings.Contains(value, "cache is cold") {
			t.Fatalf("note body leaked into prepared context: %q", value)
		}
	}

	// A ticket with no linked notes adds no item at all.
	if _, ok := linkedNotesContextItem(Task{ID: "t"}); ok {
		t.Fatalf("a ticket with no linked notes must not add a context item")
	}
}

// FR-98: link and unlink publish their own canonical events.
func TestTicketService_NoteLinkEvents(t *testing.T) {
	svc, ws, _ := newNoteLinkedService(t)
	bus := NewEventBus(32, 32)
	svc.SetEventBus(bus)
	ctx := context.Background()

	received := make(chan Event, 8)
	bus.SubscribeToEventType(EventTicketNoteLinked, func(e Event) { received <- e })
	bus.SubscribeToEventType(EventTicketNoteUnlinked, func(e Event) { received <- e })

	ticket := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "eventful",
	})
	if _, err := svc.LinkNote(ctx, ws.ID, ticket.ID, "note-1", 0); err != nil {
		t.Fatalf("LinkNote: %v", err)
	}
	if _, err := svc.UnlinkNote(ctx, ws.ID, ticket.ID, "note-1", 0); err != nil {
		t.Fatalf("UnlinkNote: %v", err)
	}

	seen := map[EventType]Event{}
	for len(seen) < 2 {
		event := <-received
		seen[event.Type] = event
	}
	if got := seen[EventTicketNoteLinked].Data["note_id"]; got != "note-1" {
		t.Fatalf("link event payload = %v", seen[EventTicketNoteLinked].Data)
	}
	if got := seen[EventTicketNoteUnlinked].Data["note_id"]; got != "note-1" {
		t.Fatalf("unlink event payload = %v", seen[EventTicketNoteUnlinked].Data)
	}
}
