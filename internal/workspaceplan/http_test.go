package workspaceplan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestAPI wires the handler behind a real ServeMux with the canonical route
// patterns, so the tests exercise path extraction the way the server does
// rather than a hand-rolled request.
func newTestAPI(t *testing.T) (*httptest.Server, *Service) {
	t.Helper()

	service := NewService(NewMemoryStore())
	handler := NewHandler(service)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/plans", handler.CreatePlan)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/plans", handler.ListPlans)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/plans/{planID}", handler.GetPlan)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/plans/{planID}", handler.DeletePlan)
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/plans/{planID}/activity", handler.GetPlanActivity)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/plans/{planID}/archive", handler.ArchivePlan)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/plans/{planID}/reopen", handler.ReopenPlan)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, service
}

func doJSON(t *testing.T, server *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode %s %s response: %v", method, path, err)
	}
	return resp.StatusCode, decoded
}

func TestAPICreateAndGetPlan(t *testing.T) {
	server, _ := newTestAPI(t)

	status, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans",
		`{"request":"Plan the migration","actor":"jj"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%v)", status, created)
	}
	planID, _ := created["id"].(string)
	if planID == "" {
		t.Fatalf("create response has no plan id: %v", created)
	}
	if created["studio_id"] != "ws-1" {
		t.Errorf("studio_id = %v, want ws-1 (FR-163)", created["studio_id"])
	}
	if created["status"] != string(StatusDraft) {
		t.Errorf("status = %v, want draft", created["status"])
	}
	if created["status_label"] != "Draft" {
		t.Errorf("status_label = %v, want Draft (status must not be color-only, FR-162)", created["status_label"])
	}
	if created["original_request"] != "Plan the migration" {
		t.Errorf("original_request = %v, want the exact request (FR-21)", created["original_request"])
	}

	status, fetched := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plans/"+planID, "")
	if status != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (%v)", status, fetched)
	}
	if fetched["id"] != planID {
		t.Errorf("get returned %v, want %s", fetched["id"], planID)
	}
}

// A Plan ID belonging to another workspace must read as not found, so the API
// never confirms that an ID exists elsewhere (FR-163, FR-167).
func TestAPIRejectsCrossWorkspacePlanAccess(t *testing.T) {
	server, _ := newTestAPI(t)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/ws-2/plans/" + planID},
		{http.MethodPost, "/api/workspaces/ws-2/plans/" + planID + "/archive"},
		{http.MethodPost, "/api/workspaces/ws-2/plans/" + planID + "/reopen"},
		{http.MethodDelete, "/api/workspaces/ws-2/plans/" + planID},
		{http.MethodGet, "/api/workspaces/ws-2/plans/" + planID + "/activity"},
	} {
		status, body := doJSON(t, server, tc.method, tc.path, "")
		if status != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", tc.method, tc.path, status)
		}
		if body["code"] != string(CodeNotFound) {
			t.Errorf("%s %s code = %v, want %s", tc.method, tc.path, body["code"], CodeNotFound)
		}
	}

	status, list := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-2/plans?scope=all", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200", status)
	}
	plans, _ := list["plans"].([]any)
	if len(plans) != 0 {
		t.Errorf("other workspace listed %d plans, want 0", len(plans))
	}
}

// A body claiming a different studio_id must not be able to file a Plan under
// a workspace the route did not name (FR-168).
func TestAPIBodyCannotOverrideTheRouteWorkspace(t *testing.T) {
	server, _ := newTestAPI(t)

	status, body := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans",
		`{"request":"Plan A","studio_id":"ws-2"}`)
	if status != http.StatusNotFound {
		t.Fatalf("mismatched studio_id status = %d, want 404 (%v)", status, body)
	}
}

func TestAPIListSeparatesActiveFromHistory(t *testing.T) {
	server, _ := newTestAPI(t)

	_, first := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	_, second := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan B"}`)
	archivedID, _ := second["id"].(string)

	if status, body := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+archivedID+"/archive", `{"reason":"cancelled"}`); status != http.StatusOK {
		t.Fatalf("archive status = %d (%v)", status, body)
	}

	_, active := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plans", "")
	activePlans, _ := active["plans"].([]any)
	if len(activePlans) != 1 {
		t.Fatalf("active plans = %d, want 1", len(activePlans))
	}
	if got := activePlans[0].(map[string]any)["id"]; got != first["id"] {
		t.Errorf("active plan = %v, want %v", got, first["id"])
	}

	_, history := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plans?scope=history", "")
	historyPlans, _ := history["plans"].([]any)
	if len(historyPlans) != 1 {
		t.Fatalf("history plans = %d, want 1", len(historyPlans))
	}
	entry := historyPlans[0].(map[string]any)
	if entry["archived"] != true || entry["archive_reason"] != "cancelled" {
		t.Errorf("history entry = %v, want archived with its reason", entry)
	}
}

func TestAPIArchiveAndReopenRoundTrip(t *testing.T) {
	server, _ := newTestAPI(t)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	if status, _ := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/archive", ""); status != http.StatusOK {
		t.Fatalf("archive status = %d", status)
	}
	status, reopened := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/reopen", "")
	if status != http.StatusOK {
		t.Fatalf("reopen status = %d (%v)", status, reopened)
	}
	if reopened["archived"] != false {
		t.Errorf("archived = %v after reopen, want false", reopened["archived"])
	}
}

// Deletion of a Plan that produced work is a conflict with a stable code the
// client can turn into an Archive offer (FR-17, FR-166).
func TestAPIDeleteRefusesPlansWithEffects(t *testing.T) {
	server, service := newTestAPI(t)
	ctx := context.Background()

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	if err := service.Store().LinkTasks(ctx, "ws-1", planID, []TaskLink{{
		PlanID: planID, WorkspaceID: "ws-1", TaskID: "task-1", Version: 1,
		GroupID: "grp-1", ItemID: "itm-1", Role: LinkRoleItem, CreatedAt: service.Now(),
	}}); err != nil {
		t.Fatalf("link task: %v", err)
	}

	status, body := doJSON(t, server, http.MethodDelete, "/api/workspaces/ws-1/plans/"+planID, "")
	if status != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409 (%v)", status, body)
	}
	if body["code"] != string(CodeNotDeletable) {
		t.Errorf("code = %v, want %s", body["code"], CodeNotDeletable)
	}

	// A Plan that never produced anything deletes cleanly.
	_, clean := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan B"}`)
	cleanID, _ := clean["id"].(string)
	if status, body := doJSON(t, server, http.MethodDelete,
		"/api/workspaces/ws-1/plans/"+cleanID, ""); status != http.StatusOK {
		t.Fatalf("delete clean plan status = %d (%v)", status, body)
	}
}

func TestAPIRejectsInvalidRequests(t *testing.T) {
	server, _ := newTestAPI(t)

	if status, _ := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans", `{"request":"   "}`); status != http.StatusUnprocessableEntity {
		t.Errorf("empty request status = %d, want 422", status)
	}
	if status, body := doJSON(t, server, http.MethodGet,
		"/api/workspaces/ws-1/plans?status=not_a_status", ""); status != http.StatusUnprocessableEntity {
		t.Errorf("bad status filter = %d, want 422 (%v)", status, body)
	}
	if status, _ := doJSON(t, server, http.MethodGet,
		"/api/workspaces/ws-1/plans?limit=-1", ""); status != http.StatusBadRequest {
		t.Errorf("negative limit status = %d, want 400", status)
	}
	if status, _ := doJSON(t, server, http.MethodGet,
		"/api/workspaces/ws-1/plans/missing-plan", ""); status != http.StatusNotFound {
		t.Errorf("missing plan status = %d, want 404", status)
	}
}

func TestAPIActivityReturnsLifecycleHistory(t *testing.T) {
	server, service := newTestAPI(t)
	ctx := context.Background()

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	if _, err := service.Transition(ctx, "ws-1", planID, TransitionInput{
		To: StatusNeedsInput, Source: SourceModel, Reason: "missing environment",
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	status, body := doJSON(t, server, http.MethodGet,
		"/api/workspaces/ws-1/plans/"+planID+"/activity", "")
	if status != http.StatusOK {
		t.Fatalf("activity status = %d (%v)", status, body)
	}
	entries, _ := body["activity"].([]any)
	if len(entries) != 2 {
		t.Fatalf("activity entries = %d, want 2", len(entries))
	}
	last := entries[1].(map[string]any)
	if last["to"] != string(StatusNeedsInput) || last["reason"] != "missing environment" {
		t.Errorf("activity entry = %v", last)
	}
}

func TestAPIRejectsWrongMethods(t *testing.T) {
	server, _ := newTestAPI(t)

	// The mux itself refuses a method the route does not declare, which keeps
	// a mistaken verb from reaching the handler at all.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		server.URL+"/api/workspaces/ws-1/plans", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("put plans: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PUT /plans status = %d, want 405", resp.StatusCode)
	}
}
