package personalhqhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type followUpTicketFixture struct {
	handler  *Handler
	store    *workspace.InMemoryStore
	tickets  *workspace.TicketService
	services *followup.Service
}

func newFollowUpTicketFixture(t *testing.T) followUpTicketFixture {
	t.Helper()
	handler, store := newFollowUpHTTPHandler(t)
	services, ok := handler.followups.(*followup.Service)
	if !ok {
		t.Fatalf("follow-ups are %T", handler.followups)
	}
	tickets := workspace.NewTicketService(store)
	handler.SetFollowUpTickets(services, func() *workspace.TicketService { return tickets })
	return followUpTicketFixture{handler: handler, store: store, tickets: tickets, services: services}
}

func (f followUpTicketFixture) workspace(t *testing.T, name string) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.ID = name + "-id"
	ws.FolderSlug = name
	ws.OwnerUserID = userprofile.LocalUserID
	if err := f.store.Save(ws); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	return ws
}

func (f followUpTicketFixture) followUpIn(t *testing.T, workspaceID, title string) *followup.FollowUp {
	t.Helper()
	item, err := f.services.Capture(t.Context(), followup.CaptureInput{
		UserID: userprofile.LocalUserID, WorkspaceID: workspaceID,
		Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
		Title: title, Provenance: followup.ProvenanceExplicit,
		Source: followup.SourceRef{Type: "test", ID: title},
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	return item
}

type makeTicketReply struct {
	Status   int
	FollowUp struct {
		RelatedTicket *relatedTicket `json:"related_ticket"`
	} `json:"followup"`
	Ticket  *relatedTicket `json:"ticket"`
	Created bool           `json:"created"`
	Code    string         `json:"code"`
	Details struct {
		Field string `json:"field"`
	} `json:"details"`
}

func (f followUpTicketFixture) makeTicket(t *testing.T, id, workspaceID string) makeTicketReply {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q,"workspace_id":%q}`, id, workspaceID)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/followups/ticket", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.MakeTicketFromFollowUp(rec, req)
	reply := makeTicketReply{Status: rec.Code}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil && rec.Code < 500 {
		t.Logf("decode %d: %v: %s", rec.Code, err, rec.Body.String())
	}
	return reply
}

// PRD 760 FR-25: the ticket lands in Backlog in the owning workspace, and doing
// it again opens the same ticket.
func TestMakeTicket_CreatesOneBacklogTicketInTheOwningWorkspace(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	ws := f.workspace(t, "album")
	item := f.followUpIn(t, ws.ID, "Send the mix")

	first := f.makeTicket(t, item.ID, "")
	if first.Status != http.StatusCreated || !first.Created || first.Ticket == nil {
		t.Fatalf("first = %+v", first)
	}
	if first.Ticket.WorkspaceID != ws.ID || first.Ticket.State != string(workspace.TicketStateBacklog) {
		t.Errorf("ticket = %+v, want Backlog in %s", first.Ticket, ws.ID)
	}
	if first.Ticket.Href != "/workspaces/album?ticket="+first.Ticket.ID {
		t.Errorf("href = %q", first.Ticket.Href)
	}
	if first.FollowUp.RelatedTicket == nil || first.FollowUp.RelatedTicket.ID != first.Ticket.ID {
		t.Errorf("follow-up does not show its ticket: %+v", first.FollowUp.RelatedTicket)
	}

	again := f.makeTicket(t, item.ID, "")
	if again.Status != http.StatusOK || again.Created || again.Ticket == nil || again.Ticket.ID != first.Ticket.ID {
		t.Fatalf("repeat = %+v, want the same ticket", again)
	}
	loaded, err := f.store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.Tasks) != 1 {
		t.Fatalf("%d tickets, want 1", len(loaded.Tasks))
	}
	task := loaded.Tasks[0]
	if task.SourceType != workspace.TicketSourceFollowUp || task.SourceID != item.ID {
		t.Errorf("source = %q %q", task.SourceType, task.SourceID)
	}
}

// PRD 760 FR-25: a repeat opens the existing ticket even when it names another
// workspace, so a follow-up never ends up with two tickets.
func TestMakeTicket_ARepeatNamingAnotherWorkspaceStillOpensTheFirstTicket(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	album := f.workspace(t, "album")
	other := f.workspace(t, "other")
	item := f.followUpIn(t, album.ID, "Send the mix")

	first := f.makeTicket(t, item.ID, "")
	again := f.makeTicket(t, item.ID, other.ID)
	if again.Ticket == nil || first.Ticket == nil || again.Ticket.ID != first.Ticket.ID || again.Created {
		t.Fatalf("repeat = %+v, first = %+v", again.Ticket, first.Ticket)
	}
	loaded, err := f.store.Get(other.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.Tasks) != 0 {
		t.Errorf("%d tickets in the other workspace, want none", len(loaded.Tasks))
	}
}

func TestMakeTicket_AWorkspaceThatDoesNotExistIsRefusedAndLinksNothing(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	album := f.workspace(t, "album")
	item := f.followUpIn(t, album.ID, "Send the mix")

	reply := f.makeTicket(t, item.ID, "somewhere-else")
	if reply.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", reply.Status)
	}
	stored, err := f.services.Get(t.Context(), userprofile.LocalUserID, item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.RelatedTask != nil {
		t.Errorf("linked to %+v after a refusal", stored.RelatedTask)
	}
}

func TestMakeTicket_AWorkspaceOwnedBySomeoneElseIsRefused(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	album := f.workspace(t, "album")
	foreign := f.workspace(t, "foreign")
	foreign.OwnerUserID = "someone-else"
	if err := f.store.Save(foreign); err != nil {
		t.Fatalf("save: %v", err)
	}
	item := f.followUpIn(t, album.ID, "Send the mix")

	reply := f.makeTicket(t, item.ID, foreign.ID)
	if reply.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", reply.Status)
	}
	loaded, err := f.store.Get(foreign.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.Tasks) != 0 {
		t.Errorf("%d tickets made in a foreign workspace", len(loaded.Tasks))
	}
}

func TestMakeTicket_SomeoneElsesFollowUpIsNotFound(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	album := f.workspace(t, "album")
	item, err := f.services.Capture(t.Context(), followup.CaptureInput{
		UserID: "someone-else", WorkspaceID: album.ID,
		Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
		Title: "Theirs", Provenance: followup.ProvenanceExplicit,
		Source: followup.SourceRef{Type: "test", ID: "theirs"},
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if reply := f.makeTicket(t, item.ID, album.ID); reply.Status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", reply.Status)
	}
}

// PRD 760 FR-26: the link is a link. Closing either side leaves the other open.
func TestMakeTicket_ClosingEitherSideLeavesTheOtherOpen(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	ws := f.workspace(t, "album")

	closedFollowUp := f.followUpIn(t, ws.ID, "Close the follow-up")
	ticketOfClosed := f.makeTicket(t, closedFollowUp.ID, "")
	if _, err := f.services.Complete(t.Context(), userprofile.LocalUserID, closedFollowUp.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	stillOpen, err := f.tickets.Get(ws.ID, ticketOfClosed.Ticket.ID)
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	if stillOpen.State != workspace.TicketStateBacklog {
		t.Errorf("ticket state = %q after its follow-up closed", stillOpen.State)
	}

	closedTicket := f.followUpIn(t, ws.ID, "Close the ticket")
	ticketToClose := f.makeTicket(t, closedTicket.ID, "")
	if _, err := f.tickets.Transition(ws.ID, ticketToClose.Ticket.ID, workspace.TicketTransitionInput{
		To: workspace.TicketStateCancelled, Actor: workspace.TicketActorUser,
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	item, err := f.services.Get(t.Context(), userprofile.LocalUserID, closedTicket.ID)
	if err != nil {
		t.Fatalf("get follow-up: %v", err)
	}
	if !item.IsOpen() {
		t.Errorf("follow-up status = %q after its ticket closed", item.Status)
	}
}

// PRD 760 FR-26: a list shows the ticket's state as it stands now.
func TestFollowUpViews_ShowTheTicketsCurrentState(t *testing.T) {
	f := newFollowUpTicketFixture(t)
	ws := f.workspace(t, "album")
	item := f.followUpIn(t, ws.ID, "Send the mix")
	made := f.makeTicket(t, item.ID, "")
	if _, err := f.tickets.Transition(ws.ID, made.Ticket.ID, workspace.TicketTransitionInput{
		To: workspace.TicketStateReady, Actor: workspace.TicketActorUser,
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/followups/item?id="+item.ID, nil)
	rec := httptest.NewRecorder()
	f.handler.GetFollowUp(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		FollowUp struct {
			Href          string         `json:"href"`
			RelatedTicket *relatedTicket `json:"related_ticket"`
		} `json:"followup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.FollowUp.RelatedTicket == nil || payload.FollowUp.RelatedTicket.State != string(workspace.TicketStateReady) {
		t.Errorf("related ticket = %+v, want Ready", payload.FollowUp.RelatedTicket)
	}
	if payload.FollowUp.Href != "/workspaces/album?follow_up="+item.ID {
		t.Errorf("href = %q", payload.FollowUp.Href)
	}
}
