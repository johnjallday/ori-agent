package workspaceplan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestAPI wires the handler behind a real ServeMux with the canonical route
// patterns, so the tests exercise path extraction the way the server does
// rather than a hand-rolled request.
func newTestAPI(t *testing.T) (*httptest.Server, *Service) {
	return newTestAPIWithModel(t, nil)
}

// newTestAPIWithModel mounts the Plan API on the same route patterns the server
// registers, so the tests exercise path extraction and method dispatch the way
// production does.
func newTestAPIWithModel(t *testing.T, model PlanModel) (*httptest.Server, *Service) {
	t.Helper()

	opts := []ServiceOption{}
	if model != nil {
		opts = append(opts, WithGenerator(NewGenerator(model)))
	}
	service := NewService(NewMemoryStore(), opts...)
	handler := NewHandler(service)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans", handler.PlanCollection)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}", handler.PlanItem)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/activity", handler.GetPlanActivity)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/archive", handler.ArchivePlan)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/reopen", handler.ReopenPlan)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/draft", handler.PlanDraft)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/clarifications/{clarificationID}", handler.AnswerClarification)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/snapshots", handler.DraftSnapshots)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/snapshots/{snapshotID}/recover", handler.RecoverDraftSnapshot)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/versions", handler.PlanVersions)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/versions/{version}", handler.PlanVersion)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/compare", handler.PlanCompare)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/decision", handler.PlanDecision)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/approvals", handler.PlanApprovals)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/revise-approved", handler.PlanReviseApproved)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/revision", handler.PlanRevision)

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

// --- Drafting API (FR-29, FR-30) ------------------------------------------

func TestAPIDraftSaveUsesOptimisticConcurrency(t *testing.T) {
	server, _ := newTestAPI(t)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)
	draftPath := "/api/workspaces/ws-1/plans/" + planID + "/draft"

	status, saved := doJSON(t, server, http.MethodPatch, draftPath,
		`{"objective":"First objective","revision":0,"content":{"execution":{"mode":"step_through"}}}`)
	if status != http.StatusOK {
		t.Fatalf("first save status = %d (%v)", status, saved)
	}
	if saved["objective"] != "First objective" {
		t.Errorf("objective = %v", saved["objective"])
	}
	if saved["draft_revision"] != float64(1) {
		t.Errorf("draft_revision = %v, want 1", saved["draft_revision"])
	}

	// A stale editor is refused, and told enough to recover rather than only
	// that it failed (FR-30, FR-151).
	status, conflict := doJSON(t, server, http.MethodPatch, draftPath,
		`{"objective":"Second objective","revision":0}`)
	if status != http.StatusConflict {
		t.Fatalf("stale save status = %d, want 409 (%v)", status, conflict)
	}
	if conflict["code"] != string(CodeStaleDraft) {
		t.Errorf("code = %v, want %s", conflict["code"], CodeStaleDraft)
	}
	details, _ := conflict["details"].(map[string]any)
	if details == nil || details["current_revision"] != float64(1) {
		t.Errorf("conflict does not carry the winning revision: %v", conflict["details"])
	}
	if _, ok := details["current"]; !ok {
		t.Error("conflict does not carry the current plan for recovery")
	}
}

// Saving a draft creates nothing: no version, no approval, no task (FR-29).
func TestAPIDraftSaveHasNoSideEffects(t *testing.T) {
	server, service := newTestAPI(t)
	ctx := context.Background()

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	if status, _ := doJSON(t, server, http.MethodPatch, "/api/workspaces/ws-1/plans/"+planID+"/draft",
		`{"objective":"Objective","revision":0,"content":{"execution":{"mode":"step_through"}}}`); status != http.StatusOK {
		t.Fatalf("save status = %d", status)
	}

	versions, err := service.Store().ListVersions(ctx, "ws-1", planID)
	if err != nil || len(versions) != 0 {
		t.Errorf("versions = %d (%v), want 0", len(versions), err)
	}
	approvals, err := service.Store().ListApprovals(ctx, "ws-1", planID)
	if err != nil || len(approvals) != 0 {
		t.Errorf("approvals = %d (%v), want 0", len(approvals), err)
	}
	plan, err := service.Get(ctx, "ws-1", planID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(plan.TaskLinks) != 0 || plan.Status != StatusDraft {
		t.Errorf("saving a draft produced work: status=%q links=%d", plan.Status, len(plan.TaskLinks))
	}
}

// Generation being unavailable is reported distinctly, so the UI can disable
// only the generate controls (FR-58).
func TestAPIReportsModelUnavailabilityDistinctly(t *testing.T) {
	server, _ := newTestAPI(t) // no generator

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	status, body := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans/"+planID+"/draft", `{}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%v)", status, body)
	}
	if body["code"] != string(CodeModelUnavailable) {
		t.Errorf("code = %v, want %s", body["code"], CodeModelUnavailable)
	}
}

// A generation that cannot validate returns the issues, so the editor can show
// what is wrong (FR-45).
func TestAPIGenerationFailureReturnsItsIssues(t *testing.T) {
	invalid := `{"objective":"","groups":[]}`
	server, _ := newTestAPIWithModel(t, &scriptedModel{responses: []string{invalid, invalid, invalid}})

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	status, body := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans/"+planID+"/draft", `{}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%v)", status, body)
	}
	if body["code"] != string(CodeValidationFailed) {
		t.Errorf("code = %v, want %s", body["code"], CodeValidationFailed)
	}
	details, _ := body["details"].(map[string]any)
	issues, _ := details["issues"].([]any)
	if len(issues) == 0 {
		t.Errorf("failure carries no issues: %v", body["details"])
	}
}

func TestAPIGeneratesADraft(t *testing.T) {
	server, _ := newTestAPIWithModel(t, &scriptedModel{responses: []string{validDraftResponse}})

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	status, drafted := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans/"+planID+"/draft", `{}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d (%v)", status, drafted)
	}
	if drafted["objective"] != "Migrate reporting safely" {
		t.Errorf("objective = %v", drafted["objective"])
	}
	if drafted["status"] != string(StatusDraft) {
		t.Errorf("status = %v, want draft", drafted["status"])
	}
}

func TestAPIAnswersAndSkipsClarifications(t *testing.T) {
	server, _ := newTestAPIWithModel(t, &scriptedModel{responses: []string{clarificationResponse}})

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	status, waiting := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/draft", `{"allow_clarification":true}`)
	if status != http.StatusOK {
		t.Fatalf("draft status = %d (%v)", status, waiting)
	}
	if waiting["status"] != string(StatusNeedsInput) {
		t.Fatalf("status = %v, want needs_input", waiting["status"])
	}

	draft, _ := waiting["draft"].(map[string]any)
	questions, _ := draft["clarifications"].([]any)
	if len(questions) != 2 {
		t.Fatalf("questions = %d, want 2", len(questions))
	}

	var requiredID, optionalID string
	for _, raw := range questions {
		question, _ := raw.(map[string]any)
		id, _ := question["id"].(string)
		if required, _ := question["required"].(bool); required {
			requiredID = id
		} else {
			optionalID = id
		}
	}

	// A required question cannot be skipped (FR-28).
	status, refused := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/clarifications/"+requiredID, `{"skip":true}`)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("skipping a required question status = %d, want 422 (%v)", status, refused)
	}

	if status, body := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/clarifications/"+optionalID,
		`{"skip":true,"skip_reason":"no deadline"}`); status != http.StatusOK {
		t.Fatalf("skip status = %d (%v)", status, body)
	}

	status, released := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/clarifications/"+requiredID,
		`{"answer":"Staging only"}`)
	if status != http.StatusOK {
		t.Fatalf("answer status = %d (%v)", status, released)
	}
	if released["status"] != string(StatusDraft) {
		t.Errorf("status = %v, want draft once required questions are answered", released["status"])
	}
}

func TestAPIListsAndRecoversDraftSnapshots(t *testing.T) {
	server, _ := newTestAPI(t)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)
	draftPath := "/api/workspaces/ws-1/plans/" + planID + "/draft"

	if status, _ := doJSON(t, server, http.MethodPatch, draftPath,
		`{"objective":"First","revision":0,"autosave":true,"content":{"execution":{"mode":"step_through"}}}`); status != http.StatusOK {
		t.Fatalf("first save failed")
	}
	if status, _ := doJSON(t, server, http.MethodPatch, draftPath,
		`{"objective":"Second","revision":1,"autosave":true,"content":{"execution":{"mode":"step_through"}}}`); status != http.StatusOK {
		t.Fatalf("second save failed")
	}

	status, listed := doJSON(t, server, http.MethodGet,
		"/api/workspaces/ws-1/plans/"+planID+"/snapshots", "")
	if status != http.StatusOK {
		t.Fatalf("snapshots status = %d (%v)", status, listed)
	}
	snapshots, _ := listed["snapshots"].([]any)
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snapshots))
	}
	// The retention policy travels with the data so the UI does not invent one.
	if listed["retained"] != float64(MaxDraftSnapshots) || listed["retain_days"] != float64(30) {
		t.Errorf("retention policy = retained:%v days:%v", listed["retained"], listed["retain_days"])
	}

	// A snapshot captures the draft as it was BEFORE a save, so the newest one
	// holds the state the last edit replaced — recovering it is an undo. The
	// current draft is kept separately, which is what "the latest draft plus
	// ten recovery snapshots" means (FR-30).
	newest, _ := snapshots[0].(map[string]any)
	snapshotID, _ := newest["id"].(string)
	if newest["objective"] != "First" {
		t.Fatalf("newest snapshot objective = %v, want the state the last save replaced", newest["objective"])
	}

	status, recovered := doJSON(t, server, http.MethodPost,
		"/api/workspaces/ws-1/plans/"+planID+"/snapshots/"+snapshotID+"/recover", `{"actor":"jj"}`)
	if status != http.StatusOK {
		t.Fatalf("recover status = %d (%v)", status, recovered)
	}
	if recovered["objective"] != "First" {
		t.Errorf("recovered objective = %v, want the snapshot's content", recovered["objective"])
	}

	// Recovering is itself undoable: the draft it replaced became a snapshot.
	_, after := doJSON(t, server, http.MethodGet, "/api/workspaces/ws-1/plans/"+planID+"/snapshots", "")
	afterSnapshots, _ := after["snapshots"].([]any)
	if len(afterSnapshots) != 3 {
		t.Fatalf("snapshots after recovery = %d, want 3", len(afterSnapshots))
	}
	restorable, _ := afterSnapshots[0].(map[string]any)
	if restorable["objective"] != "Second" {
		t.Errorf("newest snapshot after recovery = %v, want the replaced draft", restorable["objective"])
	}
}

func TestAPIDraftingRoutesRejectWrongMethods(t *testing.T) {
	server, _ := newTestAPI(t)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)
	base := "/api/workspaces/ws-1/plans/" + planID

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, base + "/draft"},
		{http.MethodDelete, base + "/draft"},
		{http.MethodGet, base + "/clarifications/clr-1"},
		{http.MethodPost, base + "/snapshots"},
		{http.MethodGet, base + "/snapshots/snap-1/recover"},
	} {
		status, body := doJSON(t, server, tc.method, tc.path, "")
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405 (%v)", tc.method, tc.path, status, body)
		}
	}
}

// --- Review and approval API (FR-59 through FR-79) -------------------------

// seedReviewablePlan creates a plan through the API whose draft is complete
// enough to become a version.
func seedReviewablePlan(t *testing.T, server *httptest.Server, mode ExecutionMode) string {
	t.Helper()

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)

	body := fmt.Sprintf(`{
	  "objective":"Migrate reporting safely",
	  "revision":0,
	  "content":{
	    "execution":{"mode":%q},
	    "groups":[{"id":"grp-1","title":"Prepare","items":[
	      {"id":"itm-1","description":"Snapshot staging"},
	      {"id":"itm-2","description":"Verify checksums","depends_on":["itm-1"]}]}]}
	}`, mode)
	if status, resp := doJSON(t, server, http.MethodPatch,
		"/api/workspaces/ws-1/plans/"+planID+"/draft", body); status != http.StatusOK {
		t.Fatalf("seed draft status = %d (%v)", status, resp)
	}
	return planID
}

func TestAPIReviewApprovalRoundTrip(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	status, version := doJSON(t, server, http.MethodPost, base+"/versions", `{"actor":"jj"}`)
	if status != http.StatusCreated {
		t.Fatalf("request review status = %d (%v)", status, version)
	}
	number := int(version["version"].(float64))
	hash, _ := version["content_hash"].(string)
	if number != 1 || hash == "" {
		t.Fatalf("version = %d hash = %q", number, hash)
	}

	// The review contract states the exact version, its hash, and every effect.
	status, contract := doJSON(t, server, http.MethodGet, fmt.Sprintf("%s/versions/%d", base, number), "")
	if status != http.StatusOK {
		t.Fatalf("contract status = %d (%v)", status, contract)
	}
	if contract["content_hash"] != hash {
		t.Errorf("contract hash = %v, want %q", contract["content_hash"], hash)
	}
	if contract["action_label"] != "Approve and Create Tasks" {
		t.Errorf("action label = %v", contract["action_label"])
	}
	if contract["starts_execution"] != false {
		t.Errorf("starts_execution = %v, want false for step_through", contract["starts_execution"])
	}
	if contract["approvable"] != true {
		t.Errorf("contract is not approvable: %v", contract["blockers"])
	}

	// Approving binds to that exact hash.
	status, approval := doJSON(t, server, http.MethodPost, base+"/approvals", fmt.Sprintf(
		`{"version":%d,"content_hash":%q,"effect":"create_tasks","user_name":"jj","idempotency_key":"key-1"}`,
		number, hash))
	if status != http.StatusCreated {
		t.Fatalf("approve status = %d (%v)", status, approval)
	}
	if approval["content_hash"] != hash {
		t.Errorf("approval hash = %v", approval["content_hash"])
	}

	// The approval appears in history (FR-79).
	_, history := doJSON(t, server, http.MethodGet, base+"/approvals", "")
	approvals, _ := history["approvals"].([]any)
	if len(approvals) != 1 {
		t.Errorf("approval history = %d, want 1", len(approvals))
	}
}

// An auto plan's approval says it will start work (FR-64).
func TestAPIReviewContractLabelsAnAutoPlanAsStarting(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionAuto)
	base := "/api/workspaces/ws-1/plans/" + planID

	_, version := doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	number := int(version["version"].(float64))

	_, contract := doJSON(t, server, http.MethodGet, fmt.Sprintf("%s/versions/%d", base, number), "")
	if contract["action_label"] != "Approve and Start" {
		t.Errorf("action label = %v, want Approve and Start", contract["action_label"])
	}
	if contract["starts_execution"] != true {
		t.Errorf("starts_execution = %v, want true", contract["starts_execution"])
	}
}

// A stale browser tab cannot approve: the hash it holds no longer matches
// (FR-68, FR-69).
func TestAPIRejectsAStaleApproval(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	_, version := doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	number := int(version["version"].(float64))

	status, body := doJSON(t, server, http.MethodPost, base+"/approvals", fmt.Sprintf(
		`{"version":%d,"content_hash":"stale-hash","effect":"create_tasks","idempotency_key":"key-1"}`,
		number))
	if status != http.StatusConflict {
		t.Fatalf("stale approval status = %d, want 409 (%v)", status, body)
	}
	if body["code"] != string(CodeApprovalMismatch) {
		t.Errorf("code = %v, want %s", body["code"], CodeApprovalMismatch)
	}
}

// A client cannot ask for more than the version declares (FR-63).
func TestAPIRejectsAnApprovalEffectTheVersionDoesNotDeclare(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	_, version := doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	number := int(version["version"].(float64))
	hash, _ := version["content_hash"].(string)

	status, body := doJSON(t, server, http.MethodPost, base+"/approvals", fmt.Sprintf(
		`{"version":%d,"content_hash":%q,"effect":"create_tasks_and_start","idempotency_key":"key-1"}`,
		number, hash))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%v)", status, body)
	}
	if body["code"] != string(CodeApprovalMismatch) {
		t.Errorf("code = %v", body["code"])
	}
}

func TestAPIRequestChangesAndRejectRetainTheVersion(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	status, plan := doJSON(t, server, http.MethodPost, base+"/decision",
		`{"decision":"request_changes","reason":"too wide"}`)
	if status != http.StatusOK {
		t.Fatalf("request changes status = %d (%v)", status, plan)
	}
	if plan["status"] != string(StatusDraft) {
		t.Errorf("status = %v, want draft", plan["status"])
	}

	_, listed := doJSON(t, server, http.MethodGet, base+"/versions", "")
	versions, _ := listed["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want the reviewed one retained", len(versions))
	}
	retained, _ := versions[0].(map[string]any)
	if retained["status"] != string(VersionChangesRequested) {
		t.Errorf("version status = %v", retained["status"])
	}
	if listed["max_versions"] != float64(MaxReviewVersions) {
		t.Errorf("max_versions = %v", listed["max_versions"])
	}

	// An unknown decision is refused rather than guessed at.
	if status, _ := doJSON(t, server, http.MethodPost, base+"/decision",
		`{"decision":"approve"}`); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown decision status = %d, want 422", status)
	}
}

func TestAPIComparesTwoVersions(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	doJSON(t, server, http.MethodPost, base+"/decision", `{"decision":"request_changes"}`)

	// Change the plan, then snapshot a second version.
	if status, resp := doJSON(t, server, http.MethodPatch, base+"/draft", `{
	  "objective":"Migrate reporting safely",
	  "revision":1,
	  "content":{"execution":{"mode":"step_through"},
	    "groups":[{"id":"grp-1","title":"Prepare","items":[
	      {"id":"itm-1","description":"Snapshot staging","assignee":"builder"}]}]}
	}`); status != http.StatusOK {
		t.Fatalf("second draft status = %d (%v)", status, resp)
	}
	doJSON(t, server, http.MethodPost, base+"/versions", `{}`)

	status, diff := doJSON(t, server, http.MethodGet, base+"/compare?from=1&to=2", "")
	if status != http.StatusOK {
		t.Fatalf("compare status = %d (%v)", status, diff)
	}
	if diff["identical"] == true {
		t.Error("two different versions compared as identical")
	}
	items, _ := diff["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("compare reported no item changes: %v", diff)
	}

	// A malformed comparison is refused rather than defaulted.
	if status, _ := doJSON(t, server, http.MethodGet, base+"/compare?from=abc&to=2", ""); status != http.StatusBadRequest {
		t.Errorf("bad compare status = %d, want 400", status)
	}
}

func TestAPIReviseApprovedRequiresAnIntent(t *testing.T) {
	server, service := newTestAPI(t)
	ctx := context.Background()
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	_, version := doJSON(t, server, http.MethodPost, base+"/versions", `{}`)
	number := int(version["version"].(float64))
	hash, _ := version["content_hash"].(string)
	doJSON(t, server, http.MethodPost, base+"/approvals", fmt.Sprintf(
		`{"version":%d,"content_hash":%q,"effect":"create_tasks","idempotency_key":"key-1"}`, number, hash))

	// Move the plan to approved the way materialization would.
	if err := service.Store().SetVersionDecision(ctx, "ws-1", planID, number,
		VersionApproved, "jj", "", service.Now()); err != nil {
		t.Fatalf("mark approved: %v", err)
	}
	if _, err := service.Transition(ctx, "ws-1", planID, TransitionInput{
		To: StatusApproved, Source: SourceUser, Actor: "jj",
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	if status, body := doJSON(t, server, http.MethodPost, base+"/revise-approved",
		`{"intent":""}`); status != http.StatusUnprocessableEntity {
		t.Errorf("unclassified revision status = %d, want 422 (%v)", status, body)
	}

	status, revised := doJSON(t, server, http.MethodPost, base+"/revise-approved",
		`{"intent":"additive","actor":"jj"}`)
	if status != http.StatusOK {
		t.Fatalf("revise status = %d (%v)", status, revised)
	}
	if revised["status"] != string(StatusDraft) || revised["draft_intent"] != string(RevisionAdditive) {
		t.Errorf("revision = %v / %v", revised["status"], revised["draft_intent"])
	}
}

func TestAPIReviewRoutesRejectWrongMethods(t *testing.T) {
	server, _ := newTestAPI(t)
	planID := seedReviewablePlan(t, server, ExecutionStepThrough)
	base := "/api/workspaces/ws-1/plans/" + planID

	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, base + "/versions"},
		{http.MethodPost, base + "/versions/1"},
		{http.MethodPost, base + "/compare"},
		{http.MethodGet, base + "/decision"},
		{http.MethodDelete, base + "/approvals"},
		{http.MethodGet, base + "/revise-approved"},
	} {
		status, body := doJSON(t, server, tc.method, tc.path, "")
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405 (%v)", tc.method, tc.path, status, body)
		}
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
