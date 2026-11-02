package workspace

import (
	"errors"
	"testing"
	"time"
)

func bridgeWorkspace(t *testing.T) (*TicketService, Store, *Workspace) {
	t.Helper()
	svc, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Bridges")
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.AgentInstances = []AgentInstance{{Name: "Writer", NodeID: "Writer-node-1"}}
		return nil
	}); err != nil {
		t.Fatalf("add agent: %v", err)
	}
	return svc, store, ws
}

func TestTicketSources_AcceptTheBridges(t *testing.T) {
	for _, source := range []string{TicketSourceFollowUp, TicketSourceNoteItem} {
		if got, err := NormalizeTicketSource(source); err != nil || got != source {
			t.Errorf("NormalizeTicketSource(%q) = %q, %v", source, got, err)
		}
	}
}

func TestPrepareStart_MovesBacklogToReadyLegallyAndAssigns(t *testing.T) {
	svc, _, ws := bridgeWorkspace(t)
	created := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Do it"})

	got, err := svc.PrepareStart(ws.ID, created.ID, TicketStartInput{Assignee: "Writer"})
	if err != nil {
		t.Fatalf("PrepareStart: %v", err)
	}
	if got.State != TicketStateReady || got.Assignee != "Writer" {
		t.Errorf("state %q assignee %q, want Ready for Writer", got.State, got.Assignee)
	}
	last := got.StateHistory[len(got.StateHistory)-1]
	if last.From != TicketStateBacklog || last.To != TicketStateReady || last.Actor != TicketActorUser {
		t.Errorf("history = %+v, want the legal move recorded", last)
	}
}

func TestPrepareStart_AsksWhoWhenNobodyIsSet(t *testing.T) {
	svc, store, ws := bridgeWorkspace(t)
	created := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Do it"})

	_, err := svc.PrepareStart(ws.ID, created.ID, TicketStartInput{})
	validation, ok := IsTicketValidationError(err)
	if !ok || validation.Field != "assignee" {
		t.Fatalf("err = %v, want a question about who", err)
	}
	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	task, err := loaded.GetTask(created.ID)
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if task.CanonicalState() != TicketStateBacklog {
		t.Errorf("a refused start moved the ticket to %q", task.CanonicalState())
	}
}

func TestPrepareStart_UsesTheAgentAlreadySetAndRefusesStrangers(t *testing.T) {
	svc, _, ws := bridgeWorkspace(t)
	created := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "Do it", Assignee: "Writer"})

	got, err := svc.PrepareStart(ws.ID, created.ID, TicketStartInput{})
	if err != nil || got.Assignee != "Writer" {
		t.Fatalf("got %+v, %v, want the existing agent kept", got, err)
	}
	if _, err := svc.PrepareStart(ws.ID, created.ID, TicketStartInput{Assignee: "Stranger"}); err == nil {
		t.Error("an agent outside the workspace was accepted")
	}
}

func TestPrepareStart_RefusesATicketPastReady(t *testing.T) {
	svc, _, ws := bridgeWorkspace(t)
	created := mustCreateTicket(t, svc, TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "Do it", Assignee: "Writer"})
	if _, err := svc.Transition(ws.ID, created.ID, TicketTransitionInput{To: TicketStateCancelled, Actor: TicketActorUser}); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := svc.PrepareStart(ws.ID, created.ID, TicketStartInput{}); err == nil {
		t.Error("a Cancelled ticket was made startable")
	}
	if _, err := svc.PrepareStart(ws.ID, "missing", TicketStartInput{Assignee: "Writer"}); !errors.Is(err, ErrTicketNotFound) {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestMarkNoteLinesClosed_NotesTheTicketAndKeepsItsState(t *testing.T) {
	svc, _, ws := bridgeWorkspace(t)
	fromNote := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "From a note",
		Source: TicketSourceNoteItem, SourceID: "item-1",
	})
	other := mustCreateTicket(t, svc, TicketCreateInput{
		WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Not from that line",
		Source: TicketSourceNoteItem, SourceID: "item-2",
	})

	gone := map[string]bool{"item-1": true}
	marked, err := svc.MarkNoteLinesClosed(ws.ID, gone, time.Now())
	if err != nil || marked != 1 {
		t.Fatalf("marked %d, %v, want 1", marked, err)
	}
	got, err := svc.Get(ws.ID, fromNote.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != TicketStateBacklog {
		t.Errorf("state = %q, want unchanged: Done takes a person", got.State)
	}
	if got.SourceNote != NoteLineClosed {
		t.Errorf("SourceNote = %q", got.SourceNote)
	}
	untouched, err := svc.Get(ws.ID, other.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if untouched.SourceNote != "" {
		t.Errorf("another line's ticket was noted: %q", untouched.SourceNote)
	}
	again, err := svc.MarkNoteLinesClosed(ws.ID, gone, time.Now())
	if err != nil || again != 0 {
		t.Errorf("second pass marked %d, %v, want 0", again, err)
	}
}
