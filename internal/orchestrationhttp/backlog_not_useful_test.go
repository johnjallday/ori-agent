package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FR20: POST /api/orchestration/backlog/{id}/not-useful removes a goal-run
// suggestion and records it with the reason; an ordinary ticket is refused.
func TestBacklogHandler_NotUseful(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Brand")
	svc := workspace.NewBacklogService(store)
	bh := NewBacklogHandler(svc)

	suggestion, err := svc.Create(workspace.BacklogCreateInput{
		WorkspaceID: ws.ID, Description: "Audit every old post",
		SourceType: workspace.TicketSourceGoalSuggestion, SourceID: "run-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := svc.Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "My own idea"})
	if err != nil {
		t.Fatal(err)
	}

	post := func(id, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog/"+id+"/not-useful?workspace_id="+ws.ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		return rec
	}

	if rec := post(ordinary.ID, `{"reason":"no"}`); rec.Code != http.StatusConflict {
		t.Fatalf("ordinary ticket status = %d, want 409: %s", rec.Code, rec.Body.String())
	} else {
		var body struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Code != "not_a_suggestion" {
			t.Fatalf("ordinary ticket body = %s", rec.Body.String())
		}
	}

	if rec := post(suggestion.ID, `{"reason":"We stopped posting there"}`); rec.Code != http.StatusOK {
		t.Fatalf("not useful status = %d: %s", rec.Code, rec.Body.String())
	}
	fresh, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.GetTask(suggestion.ID); err == nil {
		t.Fatal("the suggestion is still in the Backlog")
	}
	if len(fresh.DismissedSuggestions) != 1 || fresh.DismissedSuggestions[0].Reason != "We stopped posting there" ||
		fresh.DismissedSuggestions[0].RunID != "run-7" {
		t.Fatalf("ledger = %+v", fresh.DismissedSuggestions)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog/"+suggestion.ID+"/not-useful?workspace_id="+ws.ID, nil)
	rec := httptest.NewRecorder()
	bh.BacklogItemPathHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}
