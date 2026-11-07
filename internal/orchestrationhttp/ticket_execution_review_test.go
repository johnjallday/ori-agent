package orchestrationhttp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestTicketExecutionReviewDuplicateDoesNotRenewApproval(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)
	h := NewTicketHandler(f.service)
	approvals, starts := 0, 0
	h.SetStartReview(func(*http.Request, string, string, string, string) (func() error, error) {
		return func() error { approvals++; return nil }, nil
	})
	h.SetStarter(func(string, string) error { starts++; return nil })
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /api/workspaces/{studioID}/tickets/{ticketID}/start", h.TicketStartHandler)
	for range 2 {
		response, _ := f.start(t, ticket.ID, "Writer")
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	if approvals != 1 || starts != 1 {
		t.Fatalf("approval/start counts: %d/%d", approvals, starts)
	}
}

func TestTicketExecutionReviewRefusalPrecedesLifecycleMutation(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)
	h := NewTicketHandler(f.service)
	h.SetStartReview(func(*http.Request, string, string, string, string) (func() error, error) {
		return nil, errors.New("stale disclosure")
	})
	h.SetStarter(func(string, string) error { t.Fatal("started after refusal"); return nil })
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /api/workspaces/{studioID}/tickets/{ticketID}/start", h.TicketStartHandler)
	response, _ := f.start(t, ticket.ID, "Writer")
	if response.Code != http.StatusConflict {
		t.Fatal(response.Code)
	}
	saved, err := f.service.Get(f.ws.ID, ticket.ID)
	if err != nil || saved.State != workspace.TicketStateBacklog || saved.Assignee != "" {
		t.Fatalf("mutated before review: %+v %v", saved, err)
	}
}
