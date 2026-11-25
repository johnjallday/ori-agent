package workspace

import "testing"

func TestInboxReviewTicket_OnlyConfirmedOwnedActiveDestination(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "House"})
	ws.FolderSlug = "house"
	ws.Status = StatusActive
	ws.OwnerUserID = "local"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	svc := NewTicketService(store)
	if _, err := svc.CreateInboxReviewTicket("other", ws.ID, "a", "Compare quotes"); err == nil {
		t.Fatal("foreign workspace")
	}
	if _, err := svc.Create(TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateBacklog, Title: "Spoof", Source: TicketSourceInboxReview}); err == nil {
		t.Fatal("client forged notebook provenance")
	}
	a, err := svc.CreateInboxReviewTicket("local", ws.ID, "a", "Compare quotes")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != TicketStateBacklog || a.Assignee != "" || a.CurrentRunID != "" || a.Source != TicketSourceInboxReview || len(a.LinkedNoteIDs) != 0 {
		t.Fatal("wrong ticket state or source")
	}
	if err := store.Update(ws.ID, func(ws *Workspace) error { ws.Tasks[0].Description = "Renamed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transition(ws.ID, a.ID, TicketTransitionInput{To: TicketStateCancelled, Actor: TicketActorUser}); err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateInboxReviewTicket("local", ws.ID, "a", "Compare quotes")
	if err != nil || again.ID != a.ID || again.Title != "Renamed" || again.State != TicketStateCancelled {
		t.Fatalf("rename lost identity: %v", err)
	}
}
