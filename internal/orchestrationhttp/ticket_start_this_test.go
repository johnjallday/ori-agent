package orchestrationhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Start this (PRD 760 FR-27): it asks who when no agent is set, records each legal
// transition, and starts one run.

type startThisFixture struct {
	mux     *http.ServeMux
	service *workspace.TicketService
	store   workspace.Store
	ws      *workspace.Workspace
	starts  *atomic.Int32
	refusal error
}

func newStartThisFixture(t *testing.T) *startThisFixture {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Work"})
	ws.AgentInstances = []workspace.AgentInstance{{Name: "Writer", NodeID: "Writer-node-1"}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	service := workspace.NewTicketService(store)
	f := &startThisFixture{service: service, store: store, ws: ws, starts: &atomic.Int32{}}
	handler := NewTicketHandler(service)
	handler.SetStarter(func(_, _ string) error {
		f.starts.Add(1)
		return f.refusal
	})
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /api/workspaces/{studioID}/tickets/{ticketID}/start", handler.TicketStartHandler)
	return f
}

func (f *startThisFixture) backlogTicket(t *testing.T) *workspace.Ticket {
	t.Helper()
	ticket, err := f.service.Create(workspace.TicketCreateInput{
		WorkspaceID: f.ws.ID, State: workspace.TicketStateBacklog, Title: "Mix it", Actor: workspace.TicketActorUser,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return ticket
}

func (f *startThisFixture) start(t *testing.T, ticketID, assignee string) (*httptest.ResponseRecorder, giveWorkResponse) {
	t.Helper()
	body := `{"assignee":"` + assignee + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+f.ws.ID+"/tickets/"+ticketID+"/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var decoded giveWorkResponse
	if rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, decoded
}

func TestStartThis_AsksWhoWhenNoAgentIsSetAndChangesNothing(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)

	rec, _ := f.start(t, ticket.ID, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var refusal struct {
		Details struct {
			Field string `json:"field"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || refusal.Details.Field != "assignee" {
		t.Fatalf("refusal = %s, want field assignee", rec.Body.String())
	}
	if f.starts.Load() != 0 {
		t.Errorf("%d runs started", f.starts.Load())
	}
	stored, err := f.service.Get(f.ws.ID, ticket.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.State != workspace.TicketStateBacklog {
		t.Errorf("state = %q, want it still in Backlog", stored.State)
	}
}

func TestStartThis_MovesBacklogToReadyInHistoryAndStartsOneRun(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)

	rec, reply := f.start(t, ticket.ID, "Writer")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if reply.Start == nil || !reply.Start.Started || reply.Start.Error != "" {
		t.Fatalf("start = %+v", reply.Start)
	}
	if reply.State != workspace.TicketStateReady || reply.Assignee != "Writer" {
		t.Errorf("ticket = %q assigned %q", reply.State, reply.Assignee)
	}
	if f.starts.Load() != 1 {
		t.Errorf("%d runs started, want 1", f.starts.Load())
	}
	moved := false
	for _, change := range reply.StateHistory {
		if change.From == workspace.TicketStateBacklog && change.To == workspace.TicketStateReady {
			moved = true
		}
	}
	if !moved {
		t.Errorf("history = %+v, want Backlog to Ready recorded", reply.StateHistory)
	}
}

func TestStartThis_ARepeatMakesNoSecondRun(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)

	f.start(t, ticket.ID, "Writer")
	rec, reply := f.start(t, ticket.ID, "")
	if rec.Code != http.StatusOK || reply.Start == nil || !reply.Start.Started {
		t.Fatalf("repeat = %d %+v", rec.Code, reply.Start)
	}
	if f.starts.Load() != 1 {
		t.Errorf("%d runs started, want 1", f.starts.Load())
	}
}

func TestStartThis_ARefusalSaysWhyAndCanBeTriedAgain(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)
	f.refusal = errors.New("no model key")

	_, reply := f.start(t, ticket.ID, "Writer")
	if reply.Start == nil || reply.Start.Error != "no model key" || reply.State != workspace.TicketStateReady {
		t.Fatalf("refused = %q %+v", reply.State, reply.Start)
	}

	f.refusal = nil
	_, retried := f.start(t, ticket.ID, "")
	if retried.Start == nil || !retried.Start.Started {
		t.Fatalf("retry = %+v", retried.Start)
	}
	if f.starts.Load() != 2 {
		t.Errorf("%d attempts, want the retry to reach the starter", f.starts.Load())
	}
}

func TestStartThis_RefusesAnAgentFromElsewhereAndAnyLaterState(t *testing.T) {
	f := newStartThisFixture(t)
	ticket := f.backlogTicket(t)

	if rec, _ := f.start(t, ticket.ID, "Stranger"); rec.Code != http.StatusBadRequest {
		t.Errorf("stranger status = %d", rec.Code)
	}
	if _, err := f.service.Transition(f.ws.ID, ticket.ID, workspace.TicketTransitionInput{
		To: workspace.TicketStateCancelled, Actor: workspace.TicketActorUser,
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if rec, _ := f.start(t, ticket.ID, "Writer"); rec.Code != http.StatusBadRequest {
		t.Errorf("cancelled status = %d", rec.Code)
	}
	if f.starts.Load() != 0 {
		t.Errorf("%d runs started", f.starts.Load())
	}
}
