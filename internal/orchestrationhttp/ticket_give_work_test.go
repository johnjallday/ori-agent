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

// The create-and-start contract behind Give work (PRD 760 FR-13, FR-14):
//
//	no agent           -> Backlog (the client's choice; an agent on Backlog is refused)
//	an agent           -> Ready, quiet: nothing runs until someone starts it
//	an agent and start -> Ready, then one run
//	a refused start    -> still Ready, says why, and a retry makes no second ticket

type giveWorkFixture struct {
	mux     *http.ServeMux
	store   workspace.Store
	ws      *workspace.Workspace
	starts  *atomic.Int32
	starter func() error
}

func newGiveWorkFixture(t *testing.T) *giveWorkFixture {
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

	f := &giveWorkFixture{store: store, ws: ws, starts: &atomic.Int32{}}
	th := NewTicketHandler(workspace.NewTicketService(store))
	th.SetStarter(func(_, _ string) error {
		f.starts.Add(1)
		if f.starter != nil {
			return f.starter()
		}
		return nil
	})
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /api/workspaces/{studioID}/tickets", th.TicketCollectionHandler)
	return f
}

type giveWorkResponse struct {
	workspace.Ticket
	Start *ticketStartOutcome `json:"start"`
}

func (f *giveWorkFixture) post(t *testing.T, body string) (*httptest.ResponseRecorder, giveWorkResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+f.ws.ID+"/tickets", strings.NewReader(body))
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

func (f *giveWorkFixture) stored(t *testing.T, id string) *workspace.Task {
	t.Helper()
	ws, err := f.store.Get(f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	task, err := ws.GetTask(id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task
}

func (f *giveWorkFixture) ticketCount(t *testing.T) int {
	t.Helper()
	ws, err := f.store.Get(f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	return len(ws.Tasks)
}

// PRD 760 FR-19: what came back is readable from ticket detail, and only there.
func TestTicketDetail_CarriesTheLatestResultButTheListDoesNot(t *testing.T) {
	f := newGiveWorkFixture(t)
	_, created := f.post(t, `{"state":"ready","title":"Write it"}`)
	if err := f.store.Update(f.ws.ID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(created.ID, func(task *workspace.Task) error {
			task.Result = "The finished draft"
			return nil
		})
	}); err != nil {
		t.Fatalf("store result: %v", err)
	}
	service := workspace.NewTicketService(f.store)
	detail, err := service.Get(f.ws.ID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.LatestResult != "The finished draft" {
		t.Errorf("detail LatestResult = %q", detail.LatestResult)
	}
	page, err := service.Search(workspace.TicketQuery{WorkspaceID: f.ws.ID})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Tickets) != 1 || page.Tickets[0].LatestResult != "" {
		t.Errorf("list must not carry results: %+v", page.Tickets)
	}
}

func TestGiveWork_NoAgentIsBacklogAndStartsNothing(t *testing.T) {
	f := newGiveWorkFixture(t)
	rec, got := f.post(t, `{"state":"backlog","title":"A thing to do"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got.State != workspace.TicketStateBacklog || got.Assignee != "" {
		t.Errorf("state %q assignee %q, want Backlog with nobody", got.State, got.Assignee)
	}
	if got.Start != nil || f.starts.Load() != 0 {
		t.Errorf("start outcome %+v, starts %d, want no start", got.Start, f.starts.Load())
	}
}

func TestGiveWork_AnAgentIsReadyAndStaysQuiet(t *testing.T) {
	f := newGiveWorkFixture(t)
	rec, got := f.post(t, `{"state":"ready","title":"Write it","assignee":"Writer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got.State != workspace.TicketStateReady || got.Assignee != "Writer" {
		t.Errorf("state %q assignee %q, want Ready for Writer", got.State, got.Assignee)
	}
	if f.starts.Load() != 0 {
		t.Errorf("starts = %d, naming someone is not asking them to start", f.starts.Load())
	}
	// The executor auto-runs legacy assigned. This is what keeps it from running.
	if task := f.stored(t, got.ID); task.Status != workspace.TaskStatusPending {
		t.Errorf("legacy status = %q, want pending so the executor leaves it alone", task.Status)
	}
}

func TestGiveWork_StartBeginsOneRun(t *testing.T) {
	f := newGiveWorkFixture(t)
	rec, got := f.post(t, `{"state":"ready","title":"Write it","assignee":"Writer","start":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got.Start == nil || !got.Start.Started || got.Start.Error != "" {
		t.Errorf("start outcome = %+v, want started", got.Start)
	}
	if f.starts.Load() != 1 {
		t.Errorf("starts = %d, want 1", f.starts.Load())
	}
}

func TestGiveWork_RefusedStartLeavesReadyAndSaysWhy(t *testing.T) {
	f := newGiveWorkFixture(t)
	f.starter = func() error { return errors.New("workspace is paused") }
	rec, got := f.post(t, `{"state":"ready","title":"Write it","assignee":"Writer","start":true,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a refused start is not a failed create: status = %d: %s", rec.Code, rec.Body.String())
	}
	if got.Start == nil || got.Start.Started || got.Start.Error != "workspace is paused" {
		t.Errorf("start outcome = %+v, want the refusal's reason", got.Start)
	}
	if task := f.stored(t, got.ID); task.CanonicalState() != workspace.TicketStateReady {
		t.Errorf("state = %q, want Ready", task.CanonicalState())
	}

	// Start this: the same request, once the cause is gone.
	f.starter = nil
	rec, again := f.post(t, `{"state":"ready","title":"Write it","assignee":"Writer","start":true,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a replay is not a create: status = %d", rec.Code)
	}
	if again.ID != got.ID || f.ticketCount(t) != 1 {
		t.Errorf("retry made a second ticket: %q vs %q, %d stored", again.ID, got.ID, f.ticketCount(t))
	}
	if again.Start == nil || !again.Start.Started {
		t.Errorf("retry start outcome = %+v, want started", again.Start)
	}
	if f.starts.Load() != 2 {
		t.Errorf("starts = %d, want the refused one and the retry", f.starts.Load())
	}
}

func TestGiveWork_RepeatedRequestMakesOneTicketAndOneRun(t *testing.T) {
	f := newGiveWorkFixture(t)
	body := `{"state":"ready","title":"Write it","assignee":"Writer","start":true,"idempotency_key":"k2"}`
	_, first := f.post(t, body)
	// The run begins asynchronously, so the ticket can still read Ready here.
	rec, second := f.post(t, body)
	if rec.Code != http.StatusOK || second.ID != first.ID {
		t.Fatalf("status %d, ids %q and %q, want a replay of one ticket", rec.Code, first.ID, second.ID)
	}
	if f.ticketCount(t) != 1 || f.starts.Load() != 1 {
		t.Errorf("%d tickets and %d starts, want 1 and 1", f.ticketCount(t), f.starts.Load())
	}
	if second.Start == nil || !second.Start.Started {
		t.Errorf("replay start outcome = %+v, want started", second.Start)
	}
}

func TestGiveWork_ReplayFindsATicketThatHasMovedOn(t *testing.T) {
	f := newGiveWorkFixture(t)
	body := `{"state":"ready","title":"Write it","assignee":"Writer","start":true,"idempotency_key":"k3"}`
	_, first := f.post(t, body)
	if err := f.store.Update(f.ws.ID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(first.ID, func(task *workspace.Task) error {
			return task.StartTicketRun("run-1", workspace.TicketActorUser, task.CreatedAt)
		})
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	rec, second := f.post(t, body)
	if rec.Code != http.StatusOK || second.ID != first.ID {
		t.Fatalf("status %d (%s), want the started ticket", rec.Code, rec.Body.String())
	}
	if second.State != workspace.TicketStateInProgress || f.starts.Load() != 1 {
		t.Errorf("state %q, starts %d, want In Progress and no second run", second.State, f.starts.Load())
	}
}

func TestGiveWork_RefusesWhatTheRulesForbid(t *testing.T) {
	cases := []struct {
		name, body, field string
	}{
		{"an assignee on Backlog", `{"state":"backlog","title":"X","assignee":"Writer"}`, "state"},
		{"a start with nobody", `{"state":"ready","title":"X","start":true}`, "assignee"},
		{"a start from Backlog", `{"state":"backlog","title":"X","assignee":"Writer","start":true}`, "state"},
		{"an agent outside the workspace", `{"state":"ready","title":"X","assignee":"Stranger"}`, "assignee"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGiveWorkFixture(t)
			rec, _ := f.post(t, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"field":"`+tc.field+`"`) {
				t.Errorf("error does not name field %q: %s", tc.field, rec.Body.String())
			}
			if f.ticketCount(t) != 0 || f.starts.Load() != 0 {
				t.Errorf("%d tickets, %d starts, want nothing created or started", f.ticketCount(t), f.starts.Load())
			}
		})
	}
}

func TestGiveWork_SameKeyWithDifferentWorkIsRefused(t *testing.T) {
	f := newGiveWorkFixture(t)
	f.post(t, `{"state":"backlog","title":"First","idempotency_key":"k4"}`)
	rec, _ := f.post(t, `{"state":"backlog","title":"Second","idempotency_key":"k4"}`)
	if rec.Code < 400 {
		t.Errorf("status = %d, a reused key must not silently return other work", rec.Code)
	}
	if f.ticketCount(t) != 1 {
		t.Errorf("%d tickets, want 1", f.ticketCount(t))
	}
}

func TestGiveWork_WithoutAStarterSaysSoAndKeepsTheTicket(t *testing.T) {
	f := newGiveWorkFixture(t)
	th := NewTicketHandler(workspace.NewTicketService(f.store))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{studioID}/tickets", th.TicketCollectionHandler)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+f.ws.ID+"/tickets",
		strings.NewReader(`{"state":"ready","title":"X","assignee":"Writer","start":true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "not available") {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
	if f.ticketCount(t) != 1 {
		t.Errorf("%d tickets, want the ticket kept", f.ticketCount(t))
	}
}
