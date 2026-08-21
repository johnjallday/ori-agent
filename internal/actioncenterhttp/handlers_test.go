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
	backlogService := workspace.NewBacklogService(wsStore)
	return NewHandler(wsStore, opps, backlogService), wsStore, opps
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
		if it.WorkspaceName == "" || it.WorkspaceSlug == "" {
			t.Errorf("workspace name/slug missing for %q", it.Title)
		}
		if it.WorkspaceSlug == it.WorkspaceID {
			t.Errorf("test requires distinct workspace id/slug for %q", it.Title)
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

func TestList_SnoozeSuppressionAndExpiry(t *testing.T) {
	h, store, opps := newTestHandler(t)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{
		{Title: "fresh"},
		{Title: "still-snoozed", Status: workspace.OpportunitySnoozed, SnoozedUntil: &future},
		{Title: "expired-snooze", Status: workspace.OpportunitySnoozed, SnoozedUntil: &past},
	})

	// Default (active) view.
	req := httptest.NewRequest("GET", "/api/action-center/opportunities", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	body := decodeList(t, rec)

	got := map[string]workspace.OpportunityStatus{}
	for _, it := range body.Items {
		got[it.Title] = it.Status
	}
	if _, shown := got["still-snoozed"]; shown {
		t.Error("a still-snoozed item must be hidden from the default view")
	}
	if got["fresh"] != workspace.OpportunityNew {
		t.Errorf("fresh status = %q; want new", got["fresh"])
	}
	if st, ok := got["expired-snooze"]; !ok {
		t.Error("an expired snooze must re-surface in the default view")
	} else if st != workspace.OpportunityNew {
		t.Errorf("expired snooze should re-surface as new; got %q", st)
	}

	// Explicit status=snoozed still shows the currently-snoozed item.
	req = httptest.NewRequest("GET", "/api/action-center/opportunities?status=snoozed", nil)
	rec = httptest.NewRecorder()
	h.List(rec, req)
	snoozed := decodeList(t, rec)
	titles := map[string]bool{}
	for _, it := range snoozed.Items {
		titles[it.Title] = true
	}
	if !titles["still-snoozed"] {
		t.Error("status=snoozed should list the currently-snoozed item")
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

func TestAddToBacklog_CreatesLinkedItemAndMarksPlanned(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{
		Title:             "Brand voice drift",
		Summary:           "Copy has drifted from the style guide",
		Evidence:          "See paragraph 3",
		Priority:          "high",
		RecommendedAction: "Rewrite the intro",
		SourceRunID:       "run-1",
	}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/add-to-backlog", h.AddToBacklog)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/add-to-backlog", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp addToBacklogResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Item == nil {
		t.Fatal("expected a linked backlog item in the response")
	}
	if resp.Item.Description != "Brand voice drift" {
		t.Errorf("item description = %q, want the opportunity title", resp.Item.Description)
	}
	if resp.Item.Status != workspace.TaskStatusBacklog {
		t.Errorf("item status = %q, want backlog", resp.Item.Status)
	}
	if resp.Item.To != "" {
		t.Errorf("item must not be assigned, got To=%q", resp.Item.To)
	}
	if resp.Item.SourceType != workspace.BacklogSourceActionCenter || resp.Item.SourceID != oppID {
		t.Errorf("provenance = (%q, %q), want (%q, %q)", resp.Item.SourceType, resp.Item.SourceID, workspace.BacklogSourceActionCenter, oppID)
	}
	if resp.Item.Priority != 1 {
		t.Errorf("priority = %d, want 1 (high)", resp.Item.Priority)
	}
	if !strings.Contains(resp.Item.Details, "Copy has drifted") ||
		!strings.Contains(resp.Item.Details, "See paragraph 3") ||
		!strings.Contains(resp.Item.Details, "Rewrite the intro") ||
		!strings.Contains(resp.Item.Details, "run-1") {
		t.Errorf("details missing copied evidence/summary/recommended-action/source-run: %q", resp.Item.Details)
	}

	got, err := opps.Get(wsID, oppID)
	if err != nil {
		t.Fatalf("get opportunity: %v", err)
	}
	if got.Status != workspace.OpportunityPlanned {
		t.Errorf("opportunity status = %q, want planned (not resolved — the finding itself isn't fixed)", got.Status)
	}
	if got.LinkedTaskID != resp.Item.ID || got.LinkedWorkspaceID != wsID {
		t.Errorf("link = (%q, %q), want (%q, %q)", got.LinkedTaskID, got.LinkedWorkspaceID, resp.Item.ID, wsID)
	}

	// The opportunity must leave the default (active) triage queue.
	listReq := httptest.NewRequest("GET", "/api/action-center/opportunities", nil)
	listRec := httptest.NewRecorder()
	h.List(listRec, listReq)
	activeList := decodeList(t, listRec)
	for _, item := range activeList.Items {
		if item.ID == oppID {
			t.Error("a planned opportunity must not appear in the default active list")
		}
	}
}

func TestAddToBacklog_RepeatedCallIsIdempotent(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "Missing alt text"}})
	list, _ := opps.List(wsID)
	oppID := list[0].ID

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/add-to-backlog", h.AddToBacklog)

	req1 := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/add-to-backlog", nil)
	rec1 := httptest.NewRecorder()
	m.ServeHTTP(rec1, req1)
	var first addToBacklogResponse
	if err := json.NewDecoder(rec1.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	req2 := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/"+oppID+"/add-to-backlog", nil)
	rec2 := httptest.NewRecorder()
	m.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("repeat call status %d, body %s", rec2.Code, rec2.Body.String())
	}
	var second addToBacklogResponse
	if err := json.NewDecoder(rec2.Body).Decode(&second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}

	if second.Item.ID != first.Item.ID {
		t.Errorf("repeat call created a new item (%q) instead of returning the existing one (%q)", second.Item.ID, first.Item.ID)
	}

	items, err := h.backlogService.List(wsID, false)
	if err != nil {
		t.Fatalf("list backlog: %v", err)
	}
	count := 0
	for _, it := range items {
		if it.Task.SourceID == oppID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one backlog item for the opportunity, found %d", count)
	}
}

func TestAddToBacklog_NotFoundReturns404(t *testing.T) {
	h, store, opps := newTestHandler(t)
	wsID := makeWorkspaceWithOpportunities(t, store, opps, "X", []workspace.Opportunity{{Title: "x"}})

	m := mux()
	m.HandleFunc("POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/add-to-backlog", h.AddToBacklog)
	req := httptest.NewRequest("POST", "/api/action-center/opportunities/"+wsID+"/not-a-real-id/add-to-backlog", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
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
