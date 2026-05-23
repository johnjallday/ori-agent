package actioncenterhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newTestHandler(t *testing.T) (*Handler, *workspace.InMemoryStore, workspace.OpportunityStore) {
	t.Helper()
	wsStore := workspace.NewInMemoryStore()
	opps := workspace.NewOpportunityStore(wsStore)
	return NewHandler(wsStore, opps), wsStore, opps
}

func makeWorkspaceWithOpportunities(t *testing.T, store *workspace.InMemoryStore, opps workspace.OpportunityStore, name string, finds []workspace.Opportunity) string {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save ws: %v", err)
	}
	for _, f := range finds {
		f.WorkspaceID = ws.ID
		if _, _, err := opps.Upsert(f); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	return ws.ID
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listResponse {
	t.Helper()
	var body listResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return body
}

func TestList_AggregatesAcrossWorkspacesAndFiltersByActive(t *testing.T) {
	h, store, opps := newTestHandler(t)
	makeWorkspaceWithOpportunities(t, store, opps, "Brand", []workspace.Opportunity{
		{Title: "Brand-A", Priority: "high"},
	})
	makeWorkspaceWithOpportunities(t, store, opps, "Home", []workspace.Opportunity{
		{Title: "Home-A", Priority: "medium"},
		{Title: "Home-B", Priority: "low", Status: workspace.OpportunityResolved}, // filtered out by default
	})

	req := httptest.NewRequest("GET", "/api/action-center/opportunities", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := decodeList(t, rec)
	if body.Total != 2 {
		t.Errorf("expected 2 active items; got %d", body.Total)
	}
	titles := map[string]bool{}
	for _, it := range body.Items {
		titles[it.Title] = true
		if it.WorkspaceName == "" {
			t.Errorf("workspace name missing for %q", it.Title)
		}
	}
	if titles["Home-B"] {
		t.Error("resolved item should be filtered by default")
	}
}

func TestList_SortByPriority(t *testing.T) {
	h, store, opps := newTestHandler(t)
	makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{
		{Title: "low", Priority: "low"},
		{Title: "critical", Priority: "critical"},
		{Title: "medium", Priority: "medium"},
		{Title: "high", Priority: "high"},
	})

	req := httptest.NewRequest("GET", "/api/action-center/opportunities?sort=priority", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	body := decodeList(t, rec)
	want := []string{"critical", "high", "medium", "low"}
	if len(body.Items) != len(want) {
		t.Fatalf("got %d items", len(body.Items))
	}
	for i, item := range body.Items {
		if item.Title != want[i] {
			t.Errorf("position %d: got %q; want %q", i, item.Title, want[i])
		}
	}
}

func TestList_AllIncludesResolved(t *testing.T) {
	h, store, opps := newTestHandler(t)
	makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{
		{Title: "active"},
		{Title: "done", Status: workspace.OpportunityResolved},
	})

	req := httptest.NewRequest("GET", "/api/action-center/opportunities?status=all", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	body := decodeList(t, rec)
	if body.Total != 2 {
		t.Errorf("status=all should include resolved; got %d", body.Total)
	}
}

func TestList_WorkspaceFilter(t *testing.T) {
	h, store, opps := newTestHandler(t)
	a := makeWorkspaceWithOpportunities(t, store, opps, "A", []workspace.Opportunity{{Title: "in A"}})
	_ = makeWorkspaceWithOpportunities(t, store, opps, "B", []workspace.Opportunity{{Title: "in B"}})

	req := httptest.NewRequest("GET", "/api/action-center/opportunities?workspace="+a, nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	body := decodeList(t, rec)
	if body.Total != 1 || body.Items[0].Title != "in A" {
		t.Errorf("workspace filter wrong: %+v", body.Items)
	}
}

func mux() *http.ServeMux {
	// Build a real mux so PathValue() works.
	return http.NewServeMux()
}

func TestGet_SetsSeenAt(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "needs seen"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("GET /api/action-center/opportunities/{workspaceID}/{opportunityID}", h.Get)
	req := httptest.NewRequest("GET", "/api/action-center/opportunities/"+wsID+"/"+oppID, nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, err := opps.Get(wsID, oppID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SeenAt == nil {
		t.Error("SeenAt should be set after GET single")
	}
}

func TestDismiss_AppliesReason(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "noisy"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/dismiss", h.Dismiss)
	body := strings.NewReader(`{"reason":"duplicate"}`)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/dismiss", body)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, _ := opps.Get(wsID, oppID)
	if got.Status != workspace.OpportunityDismissed {
		t.Errorf("status = %q; want dismissed", got.Status)
	}
	if got.DismissalReason != workspace.DismissalDuplicate {
		t.Errorf("reason = %q; want duplicate", got.DismissalReason)
	}
	if got.DismissedAt == nil {
		t.Error("DismissedAt should be set")
	}
}

func TestDismiss_RejectsInvalidReason(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "x"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/dismiss", h.Dismiss)
	body := strings.NewReader(`{"reason":"bogus"}`)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/dismiss", body)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 on invalid reason", rec.Code)
	}
}

func TestSnooze_Preset(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "later"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/snooze", h.Snooze)
	body := strings.NewReader(`{"preset":"next_week"}`)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/snooze", body)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	got, _ := opps.Get(wsID, oppID)
	if got.Status != workspace.OpportunitySnoozed {
		t.Errorf("status = %q; want snoozed", got.Status)
	}
	if got.SnoozedUntil == nil || !got.SnoozedUntil.After(time.Now()) {
		t.Errorf("SnoozedUntil should be in the future; got %v", got.SnoozedUntil)
	}
}

func TestSnooze_CustomTimestamp(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "later"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/snooze", h.Snooze)
	body := strings.NewReader(`{"until":"` + future + `"}`)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/snooze", body)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSnooze_RequiresFutureTimestamp(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "x"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/snooze", h.Snooze)
	body := strings.NewReader(`{"until":"` + past + `"}`)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/snooze", body)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for past snooze", rec.Code)
	}
}

func TestResolve_SetsResolvedAt(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "fix"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/resolve", h.Resolve)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/resolve", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, _ := opps.Get(wsID, oppID)
	if got.Status != workspace.OpportunityResolved {
		t.Errorf("status = %q; want resolved", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Error("ResolvedAt should be set")
	}
}

func TestMutation_NotFoundReturns404(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "x"}})

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/resolve", h.Resolve)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/not-a-real-id/resolve", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}
