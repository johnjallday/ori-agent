package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The canonical Plan surface is a route contract, not a convention: Task
// detail, Run detail, and chat all deep-link to these paths, so they have to
// resolve the same way from every entry point (PRD FR-145, FR-148, FR-149).
func TestWorkspacePlanPageRoutesResolveCanonically(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Plan Route Workspace")

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"plans list", "/workspaces/" + workspace.Slug + "/plans", "workspacePlansPageRoot"},
		{"plan detail", "/workspaces/" + workspace.Slug + "/plans/plan-1", "workspacePlanPageRoot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want 200", tc.path, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("%s did not render %s", tc.path, tc.want)
			}
			// The page carries the identifiers the client needs to call the
			// workspace-scoped API; without them it would have to guess.
			if !strings.Contains(body, `data-workspace-id="`+workspace.ID+`"`) {
				t.Errorf("%s did not carry its workspace id", tc.path)
			}
			if !strings.Contains(body, `data-workspace-slug="`+workspace.Slug+`"`) {
				t.Errorf("%s did not carry its workspace slug", tc.path)
			}
		})
	}
}

// The retired "Plan the suggested ticket" bookmark still arrives somewhere
// useful: the workspace, where the suggestion opens in Give work. Only that
// exact request redirects; the collection address keeps serving its page, and a
// redirect creates nothing.
func TestLegacyAssessmentBookmarkArrivesAtTheWorkspaceNotThePlansPage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Legacy Assessment Bookmark")

	bookmark := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"/plans?next=assessment", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, bookmark)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bookmark status = %d, want 303", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/workspaces/"+workspace.Slug+"?next=assessment"; got != want {
		t.Fatalf("bookmark redirect = %q, want %q", got, want)
	}

	// The new address is the workspace page itself, not another redirect.
	arrival := httptest.NewRecorder()
	handler.ServeHTTP(arrival, httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"?next=assessment", nil))
	if arrival.Code != http.StatusOK {
		t.Fatalf("arrival status = %d, want 200", arrival.Code)
	}

	for _, path := range []string{
		"/workspaces/" + workspace.Slug + "/plans",
		"/workspaces/" + workspace.Slug + "/plans?panel=history",
		"/workspaces/" + workspace.Slug + "/plans?next=other",
	} {
		other := httptest.NewRecorder()
		handler.ServeHTTP(other, httptest.NewRequest(http.MethodGet, path, nil))
		if other.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200 (only next=assessment redirects)", path, other.Code)
		}
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspace.ID+"/plans?scope=all", nil))
	var body struct {
		Plans []any `json:"plans"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plans: %v", err)
	}
	if len(body.Plans) != 0 {
		t.Fatalf("arriving from the bookmark created %d plan(s)", len(body.Plans))
	}
}

// The Plans collection lists the plans a workspace has; it offers no way to start
// one. The creation form, the "turn planning on" banner and their copy are gone
// from the served page, and an empty collection says creation is retired and
// links to Tickets. The route itself, and every plan behind it, is unchanged.
func TestWorkspacePlansPageOffersNoWayToCreateAPlan(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Plans Page Retired Workspace")

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"/plans", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, gone := range []string{
		"plan-create-form", "plan-create-request", "plan-create-panel", "plan-disabled-banner",
		"Start a plan", "Create plan", "What would you like planned", "Structured planning is off",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("the Plans page still carries %q", gone)
		}
	}
	for _, want := range []string{
		`id="plan-retired-note"`, `id="plan-active-list"`, `id="plan-history-list"`,
		"/workspaces/" + workspace.Slug + "?tickets=all",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Plans page lost %q", want)
		}
	}
}

// Workspace Settings keeps every planning control so a save writes the policy
// back as loaded, but they sit in a folded "Existing plans" section, and the
// copy no longer invites anyone to start a plan.
func TestWorkspaceSettingsFoldsPlanningIntoExistingPlans(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Settings Existing Plans Workspace")

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	open := strings.Index(body, `id="workspace-detail-settings-existing-plans"`)
	if open < 0 {
		t.Fatal("the Existing plans section is missing from Workspace Settings")
	}
	closing := strings.Index(body[open:], "</details>")
	if closing < 0 {
		t.Fatal("the Existing plans section is not closed")
	}
	section := body[open : open+closing]
	tagStart := strings.LastIndex(body[:open], "<details")
	tagEnd := open + strings.Index(body[open:], ">")
	if tagStart < 0 || strings.Contains(body[tagStart:tagEnd], " open") {
		t.Error("the Existing plans section must be a <details> folded by default")
	}
	for _, id := range []string{
		"workspace-detail-settings-plan-enabled", "workspace-detail-settings-planning-mode",
		"workspace-detail-settings-clarification-mode", "workspace-detail-settings-tasks-dir",
		"workspace-detail-settings-execution-mode", "workspace-detail-settings-write-prd",
		"workspace-detail-settings-write-task-list", "workspace-detail-settings-require-branch",
		"workspace-detail-settings-require-scan", "workspace-detail-settings-sync-tasks",
		"workspace-detail-settings-guidance", "workspace-detail-settings-enforced",
	} {
		if !strings.Contains(section, `id="`+id+`"`) {
			t.Errorf("control %s is not inside the Existing plans section", id)
		}
		if strings.Count(body, `id="`+id+`"`) != 1 {
			t.Errorf("control %s must exist exactly once", id)
		}
	}
	for _, gone := range []string{
		"to create a more formal plan", "Turn this on when you want the workspace manager to create",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("Workspace Settings still invites starting a plan: %q", gone)
		}
	}
	// The controls that are not about plans stay where they were, outside it.
	for _, id := range []string{
		"workspace-detail-settings-mode", "workspace-detail-settings-confirmation",
		"workspace-detail-settings-ask-handoff", "workspace-detail-settings-save-notes",
	} {
		if strings.Contains(section, `id="`+id+`"`) {
			t.Errorf("control %s must not be folded into Existing plans", id)
		}
	}
}

// A Plan detail page must carry its own Plan ID, so the client asks the
// workspace-scoped API for that exact Plan rather than inferring one.
func TestWorkspacePlanDetailPageCarriesPlanID(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Plan Detail Route Workspace")

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"/plans/plan-42", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-plan-id="plan-42"`) {
		t.Error("plan detail page did not carry its plan id")
	}
}

// A malformed descendant path resolves back to the canonical list rather than
// rendering a Plan page for an ID that was never valid.
func TestWorkspacePlanMalformedPathRedirectsToTheList(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Malformed Plan Route Workspace")

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"/plans/plan-1/extra/segments?panel=history", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	wantLocation := "/workspaces/" + workspace.Slug + "/plans?panel=history"
	if location := rec.Header().Get("Location"); location != wantLocation {
		t.Errorf("redirect target = %q, want %q", location, wantLocation)
	}
}

// A Plan ID belonging to another workspace must not resolve through this
// workspace's API. The page always renders (it is a shell); the data behind it
// is what enforces ownership (FR-163, FR-167).
func TestWorkspacePlanAPIRejectsCrossWorkspaceIDs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)

	owner := createWorkspaceForTest(t, handler, "Plan Owner")
	other := createWorkspaceForTest(t, handler, "Other Workspace")
	created := createPlanForTest(t, handler, owner, "Plan the migration")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+other+"/plans/"+created, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace read status = %d, want 404", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["code"] != "plan_not_found" {
		t.Errorf("error code = %v, want plan_not_found", body["code"])
	}
}

// The app-wide /api/workspaces/ catch-all matches every method, so a wrong verb
// on a Plan route would silently return an unrelated 200 unless the Plan routes
// own their own method checks.
func TestWorkspacePlanAPIReturnsMethodNotAllowedForWrongVerbs(t *testing.T) {
	handler := newRoutesTestHandler(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/api/workspaces/ws-1/plans"},
		{http.MethodPost, "/api/workspaces/ws-1/plans/plan-1"},
		{http.MethodGet, "/api/workspaces/ws-1/plans/plan-1/archive"},
		{http.MethodGet, "/api/workspaces/ws-1/plans/plan-1/reopen"},
		{http.MethodDelete, "/api/workspaces/ws-1/plans/plan-1/activity"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405 (body: %s)",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// A Plan is owned by exactly one workspace, and the owning-workspace foreign
// key enforces that in storage. Filing one against an unknown workspace is a
// 404 about the workspace, not an opaque 500 (FR-2).
func TestWorkspacePlanAPIRejectsAnUnknownWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)

	body := strings.NewReader(`{"request":"Plan something"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/no-such-workspace/plans", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if decoded["code"] != "workspace_not_found" {
		t.Errorf("error code = %v, want workspace_not_found", decoded["code"])
	}
}

func createWorkspaceForTest(t *testing.T, handler http.Handler, name string) string {
	t.Helper()

	body := strings.NewReader(`{"name":"` + name + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created workspace: %v", err)
	}
	for _, id := range []string{created.ID, created.Workspace.ID, created.Folder.ID} {
		if id != "" {
			return id
		}
	}
	t.Fatalf("created workspace has no id: %s", rec.Body.String())
	return ""
}

func createPlanForTest(t *testing.T, handler http.Handler, workspaceID, request string) string {
	t.Helper()

	body := strings.NewReader(`{"request":"` + request + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/plans", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created plan: %v", err)
	}
	planID, _ := created["id"].(string)
	if planID == "" {
		t.Fatalf("created plan has no id: %s", rec.Body.String())
	}
	return planID
}
