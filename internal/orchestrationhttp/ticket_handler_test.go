package orchestrationhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// newTicketTestMux mirrors the real route registration (see
// registerTicketRoutes in internal/server/routes.go) so these tests exercise
// path-value extraction and pattern precedence — notably that the literal
// /reorder segment wins over the {ticketID} wildcard — rather than calling
// handler methods directly with hand-set path values.
func newTicketTestMux(t *testing.T) (*http.ServeMux, workspace.Store) {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	th := NewTicketHandler(workspace.NewTicketService(store))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{studioID}/tickets", th.TicketCollectionHandler)
	mux.HandleFunc("POST /api/workspaces/{studioID}/tickets", th.TicketCollectionHandler)
	mux.HandleFunc("POST /api/workspaces/{studioID}/tickets/reorder", th.TicketReorderHandler)
	mux.HandleFunc("GET /api/workspaces/{studioID}/tickets/{ticketID}", th.TicketItemHandler)
	mux.HandleFunc("PATCH /api/workspaces/{studioID}/tickets/{ticketID}", th.TicketItemHandler)
	mux.HandleFunc("DELETE /api/workspaces/{studioID}/tickets/{ticketID}", th.TicketItemHandler)
	mux.HandleFunc("POST /api/workspaces/{studioID}/tickets/{ticketID}/transition", th.TicketTransitionHandler)
	return mux, store
}

func newTicketHandlerWorkspace(t *testing.T, store workspace.Store, name string) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

func doTicketRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeTicket(t *testing.T, rec *httptest.ResponseRecorder) workspace.Ticket {
	t.Helper()
	var ticket workspace.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v; body=%s", err, rec.Body.String())
	}
	return ticket
}

func createTicketViaAPI(t *testing.T, mux *http.ServeMux, studioID, body string) workspace.Ticket {
	t.Helper()
	rec := doTicketRequest(t, mux, http.MethodPost, "/api/workspaces/"+studioID+"/tickets", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	return decodeTicket(t, rec)
}

// FR-19: the API refuses to guess the capture state.
func TestTicketHandler_Create_RequiresExplicitState(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	base := "/api/workspaces/" + ws.ID + "/tickets"

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing state", `{"title":"no state"}`},
		{"empty state", `{"state":"","title":"no state"}`},
		{"unknown state", `{"state":"archived","title":"x"}`},
		{"lifecycle shortcut", `{"state":"done","title":"x"}`},
		{"missing title", `{"state":"backlog"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doTicketRequest(t, mux, http.MethodPost, base, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// FR-92: responses are canonical Ticket envelopes carrying both identifiers.
func TestTicketHandler_CreateAndGet_ReturnsCanonicalEnvelope(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")

	created := createTicketViaAPI(t, mux, ws.ID,
		`{"state":"backlog","title":"canonical shape","description":"body","tags":["infra"],"priority":2}`)

	if created.ID == "" || created.Number != 1 || created.DisplayNumber != "#1" {
		t.Fatalf("identifiers missing: %+v", created)
	}
	if created.OwningWorkspaceID != ws.ID || created.OwningWorkspaceName != "Alpha" {
		t.Fatalf("owner identity missing: %+v", created)
	}
	if created.State != workspace.TicketStateBacklog || created.StateLabel != "Backlog" {
		t.Fatalf("state = %q/%q", created.State, created.StateLabel)
	}
	if created.Version != 1 {
		t.Fatalf("Version = %d, want 1", created.Version)
	}
	if len(created.LegalTransitions) == 0 {
		t.Fatalf("response must expose legal transitions so the UI renders only legal actions")
	}

	rec := doTicketRequest(t, mux, http.MethodGet,
		"/api/workspaces/"+ws.ID+"/tickets/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeTicket(t, rec); got.ID != created.ID || got.Number != created.Number {
		t.Fatalf("get returned a different record: %+v", got)
	}
}

func TestTicketHandler_List(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")

	createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"captured"}`)
	createTicketViaAPI(t, mux, ws.ID, `{"state":"ready","title":"committed"}`)

	t.Run("returns every state by default", func(t *testing.T) {
		rec := doTicketRequest(t, mux, http.MethodGet, "/api/workspaces/"+ws.ID+"/tickets", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var envelope struct {
			Tickets  []workspace.Ticket `json:"tickets"`
			Count    int                `json:"count"`
			StudioID string             `json:"studio_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if envelope.Count != 2 || len(envelope.Tickets) != 2 {
			t.Fatalf("count = %d, want 2", envelope.Count)
		}
		if envelope.StudioID != ws.ID {
			t.Fatalf("studio_id = %q, want %q", envelope.StudioID, ws.ID)
		}
	})

	t.Run("filters by state", func(t *testing.T) {
		rec := doTicketRequest(t, mux, http.MethodGet,
			"/api/workspaces/"+ws.ID+"/tickets?state=backlog", "")
		var envelope struct {
			Tickets []workspace.Ticket `json:"tickets"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
		if len(envelope.Tickets) != 1 || envelope.Tickets[0].State != workspace.TicketStateBacklog {
			t.Fatalf("state filter returned %+v", envelope.Tickets)
		}
	})

	t.Run("rejects an unknown state filter", func(t *testing.T) {
		rec := doTicketRequest(t, mux, http.MethodGet,
			"/api/workspaces/"+ws.ID+"/tickets?state=archived", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// FR-88: PATCH edits content only. State moves through the transition route.
func TestTicketHandler_Patch_CannotChangeState(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"stays put"}`)

	rec := doTicketRequest(t, mux, http.MethodPatch,
		"/api/workspaces/"+ws.ID+"/tickets/"+ticket.ID,
		fmt.Sprintf(`{"title":"renamed","state":"done","version":%d}`, ticket.Version))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	updated := decodeTicket(t, rec)
	if updated.Title != "renamed" {
		t.Fatalf("Title = %q, want renamed", updated.Title)
	}
	if updated.State != workspace.TicketStateBacklog {
		t.Fatalf("PATCH changed lifecycle state to %q — state must only move through /transition", updated.State)
	}
}

// FR-93: a stale version token gets a 409 carrying the current record.
func TestTicketHandler_Patch_VersionConflictReturns409WithCurrent(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"contested"}`)
	path := "/api/workspaces/" + ws.ID + "/tickets/" + ticket.ID

	first := doTicketRequest(t, mux, http.MethodPatch, path,
		fmt.Sprintf(`{"title":"winner","version":%d}`, ticket.Version))
	if first.Code != http.StatusOK {
		t.Fatalf("first update status = %d; body=%s", first.Code, first.Body.String())
	}

	second := doTicketRequest(t, mux, http.MethodPatch, path,
		fmt.Sprintf(`{"title":"loser","version":%d}`, ticket.Version))
	if second.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d, want 409; body=%s", second.Code, second.Body.String())
	}

	var apiErr struct {
		Code    string `json:"code"`
		Details struct {
			Current workspace.Ticket `json:"current"`
		} `json:"details"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("decode conflict: %v; body=%s", err, second.Body.String())
	}
	if apiErr.Details.Current.Title != "winner" {
		t.Fatalf("409 must carry the current record so the client can recover, got %+v", apiErr.Details.Current)
	}
}

// FR-4/FR-14: an explicit null clears the due date; an omitted key leaves it.
func TestTicketHandler_Patch_DistinguishesOmittedFromCleared(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	ticket := createTicketViaAPI(t, mux, ws.ID,
		`{"state":"backlog","title":"dated","due_date":"2026-09-01T00:00:00Z"}`)
	if ticket.DueDate == nil {
		t.Fatalf("creation dropped the due date")
	}
	path := "/api/workspaces/" + ws.ID + "/tickets/" + ticket.ID

	// Omitted key: due date survives.
	rec := doTicketRequest(t, mux, http.MethodPatch, path, `{"title":"still dated"}`)
	after := decodeTicket(t, rec)
	if after.DueDate == nil {
		t.Fatalf("an omitted due_date key must leave the value untouched")
	}

	// Explicit null: due date is cleared.
	rec = doTicketRequest(t, mux, http.MethodPatch, path, `{"due_date":null}`)
	cleared := decodeTicket(t, rec)
	if cleared.DueDate != nil {
		t.Fatalf("an explicit null due_date must clear the value, got %v", cleared.DueDate)
	}
}

func TestTicketHandler_Transition(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")

	t.Run("promotes backlog to ready", func(t *testing.T) {
		ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"promote me"}`)
		rec := doTicketRequest(t, mux, http.MethodPost,
			"/api/workspaces/"+ws.ID+"/tickets/"+ticket.ID+"/transition",
			fmt.Sprintf(`{"to":"ready","reason":"committing","version":%d}`, ticket.Version))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		promoted := decodeTicket(t, rec)
		if promoted.State != workspace.TicketStateReady {
			t.Fatalf("State = %q, want ready", promoted.State)
		}
		if promoted.ID != ticket.ID || promoted.Number != ticket.Number {
			t.Fatalf("promotion changed identity")
		}
		if len(promoted.StateHistory) != 2 {
			t.Fatalf("StateHistory has %d entries, want 2", len(promoted.StateHistory))
		}
	})

	t.Run("refuses an illegal destination with the legal ones attached", func(t *testing.T) {
		ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"skip the queue"}`)
		rec := doTicketRequest(t, mux, http.MethodPost,
			"/api/workspaces/"+ws.ID+"/tickets/"+ticket.ID+"/transition", `{"to":"done"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}

		var apiErr struct {
			Details struct {
				CurrentState     string   `json:"current_state"`
				LegalTransitions []string `json:"legal_transitions"`
			} `json:"details"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if apiErr.Details.CurrentState != "backlog" || len(apiErr.Details.LegalTransitions) == 0 {
			t.Fatalf("409 must name the current and legal states, got %+v", apiErr.Details)
		}
	})

	t.Run("rejects an unknown destination", func(t *testing.T) {
		ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"bad target"}`)
		rec := doTicketRequest(t, mux, http.MethodPost,
			"/api/workspaces/"+ws.ID+"/tickets/"+ticket.ID+"/transition", `{"to":"archived"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

// FR-9/FR-12: ownership is the route. A foreign ID is a 404, never a
// cross-workspace mutation.
func TestTicketHandler_OwnerScoping(t *testing.T) {
	mux, store := newTicketTestMux(t)
	alpha := newTicketHandlerWorkspace(t, store, "Alpha")
	beta := newTicketHandlerWorkspace(t, store, "Beta")

	betaTicket := createTicketViaAPI(t, mux, beta.ID, `{"state":"backlog","title":"beta work"}`)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"get", http.MethodGet, "/api/workspaces/" + alpha.ID + "/tickets/" + betaTicket.ID, ""},
		{"patch", http.MethodPatch, "/api/workspaces/" + alpha.ID + "/tickets/" + betaTicket.ID, `{"title":"stolen"}`},
		{"delete", http.MethodDelete, "/api/workspaces/" + alpha.ID + "/tickets/" + betaTicket.ID, ""},
		{"transition", http.MethodPost, "/api/workspaces/" + alpha.ID + "/tickets/" + betaTicket.ID + "/transition", `{"to":"ready"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doTicketRequest(t, mux, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	// The real owner's record is untouched by every attempt above.
	rec := doTicketRequest(t, mux, http.MethodGet, "/api/workspaces/"+beta.ID+"/tickets/"+betaTicket.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner lost access to its own ticket: %d", rec.Code)
	}
	if got := decodeTicket(t, rec); got.Title != "beta work" {
		t.Fatalf("foreign request mutated the record: %q", got.Title)
	}
}

// FR-91: reorder is atomic, and its literal path segment must not be
// swallowed by the {ticketID} wildcard.
func TestTicketHandler_Reorder(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	path := "/api/workspaces/" + ws.ID + "/tickets/reorder"

	a := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"a"}`)
	b := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"b"}`)
	c := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"c"}`)

	rec := doTicketRequest(t, mux, http.MethodPost, path,
		fmt.Sprintf(`{"state":"backlog","ordered_ids":[%q,%q,%q]}`, c.ID, a.ID, b.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Tickets []workspace.Ticket `json:"tickets"`
		State   string             `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Tickets) != 3 || envelope.Tickets[0].ID != c.ID {
		t.Fatalf("reorder result = %+v", envelope.Tickets)
	}

	// A bad member fails the whole request without partial reordering.
	bad := doTicketRequest(t, mux, http.MethodPost, path,
		fmt.Sprintf(`{"state":"backlog","ordered_ids":[%q,"unknown"]}`, a.ID))
	if bad.Code < 400 {
		t.Fatalf("status = %d, want a 4xx", bad.Code)
	}

	after := doTicketRequest(t, mux, http.MethodGet,
		"/api/workspaces/"+ws.ID+"/tickets?state=backlog", "")
	var current struct {
		Tickets []workspace.Ticket `json:"tickets"`
	}
	_ = json.Unmarshal(after.Body.Bytes(), &current)
	if len(current.Tickets) != 3 || current.Tickets[0].ID != c.ID {
		t.Fatalf("failed reorder partially applied: %+v", current.Tickets)
	}
}

func TestTicketHandler_Delete(t *testing.T) {
	mux, store := newTicketTestMux(t)
	ws := newTicketHandlerWorkspace(t, store, "Alpha")
	ticket := createTicketViaAPI(t, mux, ws.ID, `{"state":"backlog","title":"doomed"}`)
	path := "/api/workspaces/" + ws.ID + "/tickets/" + ticket.ID

	stale := doTicketRequest(t, mux, http.MethodDelete, path+"?version=99", "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, want 409; body=%s", stale.Code, stale.Body.String())
	}

	ok := doTicketRequest(t, mux, http.MethodDelete, fmt.Sprintf("%s?version=%d", path, ticket.Version), "")
	if ok.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", ok.Code, ok.Body.String())
	}
	if gone := doTicketRequest(t, mux, http.MethodGet, path, ""); gone.Code != http.StatusNotFound {
		t.Fatalf("deleted ticket still readable: %d", gone.Code)
	}
}
