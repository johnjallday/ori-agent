package server

import (
	"bytes"
	"encoding/json"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type discardDesktopOpener struct{}

func (discardDesktopOpener) OpenFolder(string) error          { return nil }
func (discardDesktopOpener) OpenFile(string) error            { return nil }
func (discardDesktopOpener) RevealInFileManager(string) error { return nil }

func newRoutesTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return newRoutesTestServer(t).Handler()
}

func newRoutesTestServer(t *testing.T) *Server {
	t.Helper()

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := newServerFixtureRoot(t)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	// DefaultWorkspaceRoot() resolves to $HOME/Ori Workspaces regardless of
	// CWD, so any test built on this handler that creates a workspace (or
	// counts existing ones, e.g. first-run classification) would otherwise
	// read/write the real developer machine's workspace tree. t.Setenv
	// restores the original HOME after the test.
	t.Setenv("HOME", tmpDir)
	t.Setenv("ORI_DATA_DIR", tmpDir)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	builder.WithDesktopOpener(discardDesktopOpener{})

	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	closeInjectionTestServer(t, srv)

	return srv
}

type routeTestWorkspace struct {
	ID   string
	Slug string
}

func createRouteTestWorkspace(t *testing.T, handler http.Handler, name string) routeTestWorkspace {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		t.Fatalf("marshal workspace create: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Folder struct {
			ID         string `json:"id"`
			FolderSlug string `json:"folder_slug"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workspace create: %v", err)
	}
	if response.Folder.ID == "" || response.Folder.FolderSlug == "" {
		t.Fatalf("workspace create omitted route identity: %s", rec.Body.String())
	}
	return routeTestWorkspace{ID: response.Folder.ID, Slug: response.Folder.FolderSlug}
}

func TestLegacyStudiosRoutesRemoved(t *testing.T) {
	handler := newRoutesTestHandler(t)

	// Legacy /api/studios runtime routes should return 404
	req := httptest.NewRequest(http.MethodGet, "/api/studios/test/tasks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy /api/studios runtime route to return 404, got %d", rec.Code)
	}

	// Legacy /studios page routes should also return 404 (no longer redirect)
	req = httptest.NewRequest(http.MethodGet, "/studios/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusMovedPermanently {
		t.Fatalf("expected /studios page route to no longer redirect, got 301")
	}
}

func TestWorkspaceRunRoutesRegistered(t *testing.T) {
	handler := newRoutesTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-1/runs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected workspace runs list to return 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"runs"`) {
		t.Fatalf("expected runs response body, got %s", rec.Body.String())
	}

	body := bytes.NewReader([]byte(`{"profile_id":"missing","executor":{"kind":"ori_agent","ref":"agent"},"prompt":"do work"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-1/runs", body)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid workspace run create to return 400, got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestPersonalHQRoutesRegistered(t *testing.T) {
	handler := newRoutesTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected personal hq status to return 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status"`) {
		t.Fatalf("expected status response body, got %s", rec.Body.String())
	}

	// Method contract: the status pattern is GET-only at the mux level, so a
	// mismatched method doesn't match any registered pattern for this exact
	// path and is a 404 (net/http.ServeMux only synthesizes 405 when another
	// method is registered on the same path; see handler_test.go in
	// personalhqhttp for the handler's own RequireMethod 405 behavior).
	req = httptest.NewRequest(http.MethodPost, "/api/personal-hq/status", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected POST to status to return 404, got %d", rec.Code)
	}

	// Designating an unknown workspace must return an actionable error, not
	// a bare 500.
	body := bytes.NewReader([]byte(`{"workspace_id":"does-not-exist"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/personal-hq/designate", body)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected designate of unknown workspace to return 404, got %d body %s", rec.Code, rec.Body.String())
	}

	// The Watchtower route is registered even with no HQ yet; its own gate
	// returns 403 rather than leaving the endpoint unmounted.
	req = httptest.NewRequest(http.MethodGet, "/api/personal-hq/watchtower?workspace_id=missing", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected Watchtower HQ gate to return 403, got %d body %s", rec.Code, rec.Body.String())
	}
}

// TestBrandNewProfileLandsOnHome covers the home-first onboarding direction:
// a brand-new profile must land on Home and be free to explore. Mission 01 is
// the pull invitation and routes into the normal workspace Map rather than a
// forced setup page.
func TestBrandNewProfileLandsOnHome(t *testing.T) {
	handler := newRoutesTestHandler(t)
	page := func(path, peer string) string {
		t.Helper()
		req := httptest.NewRequest("GET", "http://localhost"+path, nil)
		req.RemoteAddr = peer
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		return rec.Body.String()
	}
	local := "127.0.0.1:43210"
	if !strings.Contains(page("/", local), "home-command-bridge") {
		t.Fatal("unmarked installation lost Home")
	}
	if runtime.GOOS != "darwin" {
		return
	}
	if err := onboarding.ResetSearchLanding(os.Getenv("ORI_DATA_DIR")); err != nil {
		t.Fatal(err)
	}
	fresh := page("/", local)
	if !strings.Contains(fresh, "Start with one folder.") || strings.Contains(fresh, "onboardingModal") || strings.Contains(fresh, "home-command-bridge") {
		t.Fatal("new Mac install did not render isolated search page")
	}
	for _, path := range []string{"/?hire=1", "/?quest=build-hq"} {
		if !strings.Contains(page(path, local), "home-command-bridge") {
			t.Fatal("overrode explicit entry", path)
		}
	}
	if strings.Contains(page("/", "192.0.2.5:1010"), "id=\"desk\"") {
		t.Fatal("nonlocal search desk")
	}
	if err := onboarding.DecideSearchLanding(os.Getenv("ORI_DATA_DIR"), onboarding.SearchFirst, onboarding.SearchLater); err != nil {
		t.Fatal(err)
	}
	home := page("/", local)
	if !strings.Contains(home, "Start or resume workspace setup") || strings.Contains(home, "onboardingModal") {
		t.Fatal("deferred setup forced on Home")
	}
}

func TestWorkspaceProjectOpenRouteUsesRuntimeHandler(t *testing.T) {
	handler := newRoutesTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/missing/project/open", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected project-open runtime route to use canonical folder storage, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/workspaces/missing/project/open", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected project-open GET to return 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceListIncludesFolderSlugForPageNavigation(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "List Navigation Workspace")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace list status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Folders []struct {
			ID         string `json:"id"`
			FolderSlug string `json:"folder_slug"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}
	for _, item := range response.Folders {
		if item.ID == workspace.ID {
			if item.FolderSlug != workspace.Slug {
				t.Fatalf("listed folder_slug = %q, want %q", item.FolderSlug, workspace.Slug)
			}
			return
		}
	}
	t.Fatalf("created workspace missing from list: %s", rec.Body.String())
}

func TestWorkspaceDetailRouteUsesSlugAndBootstrapsUUID(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Marketing Site")
	if workspace.Slug != "marketing-site" || workspace.ID == workspace.Slug {
		t.Fatalf("test requires distinct ID and slug, got %#v", workspace)
	}

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("slug detail status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-workspace-id="` + workspace.ID + `"`,
		`data-workspace-slug="marketing-site"`,
		`const workspaceId = "` + workspace.ID + `";`,
		`const workspaceSlug = "marketing-site";`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("slug detail page omitted %q", want)
		}
	}
	if strings.Contains(body, "window.location.pathname.split('/')") {
		t.Error("detail page still derives its internal workspace ID from the slug path")
	}

	for _, path := range []string{
		"/workspaces/" + workspace.ID,
		"/workspaces/unknown-workspace",
		"/workspaces/Marketing-Site",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}

// The workspace canvas page is retired and /workspaces/{slug}/canvas is a 404,
// by decision. Every other unknown descendant of a real workspace redirects to
// its Overview, so the 404 has to be asserted next to that redirect: deleting
// the page without the explicit branch would turn an old bookmark into a
// silent landing on the Overview.
func TestWorkspaceCanvasRouteIsRetiredWhileOtherUnknownPagesRedirect(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Retired Canvas Workspace")

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	for _, path := range []string{
		"/workspaces/" + workspace.Slug + "/canvas",
		"/workspaces/" + workspace.Slug + "/canvas?mode=map",
		"/workspaces/unknown-workspace/canvas",
	} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}

	rec := get("/workspaces/" + workspace.Slug + "/no-such-page?mode=map")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("an unknown descendant should still redirect, status = %d", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/workspaces/"+workspace.Slug+"?mode=map"; got != want {
		t.Errorf("redirect Location = %q, want %q", got, want)
	}
}

// The canvas layout save and the scheduler-node API are retired. The route
// golden no longer lists the layout path, so this is what keeps them gone. The
// station-layout write that the Personal HQ map still uses is a different,
// live endpoint: it only accepts PUT, so a GET is refused rather than missing.
func TestRetiredCanvasEndpointsStayGoneWhileStationLayoutRemains(t *testing.T) {
	handler := newRoutesTestHandler(t)

	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
		return rec.Code
	}

	for _, path := range []string{
		"/api/orchestration/workspace/layout",
		"/api/orchestration/workspaces/ws-1/scheduler-nodes",
		"/api/orchestration/workspaces/ws-1/scheduler-nodes/node-1",
		"/api/orchestration/workspaces/ws-1/scheduler-nodes/node-1/trigger",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if code := do(method, path); code != http.StatusNotFound {
				t.Errorf("%s %s status = %d, want 404 (the endpoint is retired)", method, path, code)
			}
		}
	}

	if code := do(http.MethodGet, "/api/orchestration/workspace/station-layout"); code != http.StatusMethodNotAllowed {
		t.Errorf("GET station-layout status = %d, want 405 (the route must stay registered)", code)
	}
}

func TestWorkspaceDescendantPagesKeepUUIDStateSeparateFromSlugLinks(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Descendant Route Workspace")

	for _, tc := range []struct {
		name   string
		path   string
		marker string
	}{
		{"diagnostics", "/diagnostics", "workspace-diagnostics-view"},
		{"task", "/task/task%201", "workspaceTaskPageRoot"},
		{"run", "/runs/run%201", "workspaceRunPageRoot"},
		{"agent", "/agents/Local%20Manager", "workspace-agent-detail-view"},
		{"plans", "/plans", "workspacePlansPageRoot"},
		{"plan", "/plans/plan%201", "workspacePlanPageRoot"},
		{"notes", "/notes/note%201", "noteMainContent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{
				tc.marker,
				`data-workspace-id="` + workspace.ID + `"`,
				`data-workspace-slug="` + workspace.Slug + `"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("response omitted %q", want)
				}
			}
			if strings.Contains(body, `href="/workspaces/`+workspace.ID) {
				t.Errorf("response used UUID in a browser href")
			}
		})
	}
}

func TestWorkspaceMalformedDescendantsRedirectWithinCurrentSlug(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Malformed Descendant Workspace")

	for from, want := range map[string]string{
		"/workspaces/" + workspace.Slug + "/task/?panel=tasks":      "/workspaces/" + workspace.Slug + "?panel=tasks",
		"/workspaces/" + workspace.Slug + "/notes/a/b?panel=notes":  "/workspaces/" + workspace.Slug + "/notes?panel=notes",
		"/workspaces/" + workspace.Slug + "/unknown?panel=settings": "/workspaces/" + workspace.Slug + "?panel=settings",
	} {
		req := httptest.NewRequest(http.MethodGet, from, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s status = %d, want 303", from, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != want {
			t.Errorf("GET %s redirected to %q, want %q", from, got, want)
		}
	}
}

func TestWorkspaceDetailOldSlugReturns404AfterRename(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Before Rename")

	renameBody := bytes.NewBufferString(`{"name":"After Rename"}`)
	renameReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspace.ID+"/rename", renameBody)
	renameReq.Header.Set("Content-Type", "application/json")
	renameRec := httptest.NewRecorder()
	handler.ServeHTTP(renameRec, renameReq)
	if renameRec.Code != http.StatusOK {
		t.Fatalf("rename status = %d: %s", renameRec.Code, renameRec.Body.String())
	}

	for path, want := range map[string]int{
		"/workspaces/before-rename": http.StatusNotFound,
		"/workspaces/after-rename":  http.StatusOK,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestWorkspaceNotesRoutesServeNotePage(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Notes Route Workspace")

	tests := []struct {
		name     string
		path     string
		contains []string
	}{
		{
			name: "workspace notes app",
			path: "/workspaces/" + workspace.Slug + "/notes",
			contains: []string{
				`id="noteMainContent"`,
				`data-workspace-id="` + workspace.ID + `"`,
				`data-note-id=""`,
				`data-page-mode="workspace"`,
			},
		},
		{
			name: "workspace note deep link",
			path: "/workspaces/" + workspace.Slug + "/notes/note-1",
			contains: []string{
				`id="noteMainContent"`,
				`data-workspace-id="` + workspace.ID + `"`,
				`data-note-id="note-1"`,
				`data-page-mode="workspace"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected %s to return 200, got %d", tt.path, rec.Code)
			}
			body := rec.Body.String()
			for _, want := range tt.contains {
				if !strings.Contains(body, want) {
					t.Fatalf("expected %s response to contain %q", tt.path, want)
				}
			}
		})
	}
}

func TestWorkspaceRunPageRouteServesRunDetailPage(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Run Route Workspace")

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspace.Slug+"/runs/run-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /workspaces/{workspaceID}/runs/{runID} page route to return 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`id="workspaceRunPageRoot"`,
		`const workspaceId = "` + workspace.ID + `";`,
		`const runId = "run-1";`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected workspace run page response to contain %q", want)
		}
	}
}

func TestFocusedNotePageRouteServesSingleNotePage(t *testing.T) {
	handler := newRoutesTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/notes/note-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /notes/{noteId} page route to return 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`id="noteMainContent"`,
		`data-workspace-id=""`,
		`data-note-id="note-1"`,
		`data-page-mode="focused"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected /notes/{noteId} response to contain %q", want)
		}
	}
}

// TestWorkspacesLauncherRedirectsToHome covers the compatibility contract for
// the retired launcher route (PRD FR3, FR4, FR6, FR142). Every supported
// query-driven entry point must survive the redirect with its intent intact,
// and unrelated parameters must not be dropped on the floor.
func TestWorkspacesLauncherRedirectsToHome(t *testing.T) {
	handler := newRoutesTestHandler(t)

	cases := []struct {
		name string
		from string
		want string
	}{
		{"bare launcher", "/workspaces", "/"},
		{"tree view survives", "/workspaces?view=tree", "/?view=tree"},
		// Map is the default and is expressed by the parameter's ABSENCE, so an
		// explicit view=map is normalized away rather than carried across.
		{"map view normalizes to the default", "/workspaces?view=map", "/"},
		// The retired Cards view must never resurrect; it lands on Map (FR6).
		{"retired cards view normalizes to Map", "/workspaces?view=cards", "/"},
		{"unknown view normalizes to Map", "/workspaces?view=nonsense", "/"},
		{"create intent survives", "/workspaces?create=1", "/?create=1"},
		{
			"blueprint preselection survives",
			"/workspaces?create=1&blueprint=email-ops",
			"/?blueprint=email-ops&create=1",
		},
		{"personal HQ focus survives", "/workspaces?focus=personal-hq", "/?focus=personal-hq"},
		{"hq onboarding state survives", "/workspaces?hq=setup", "/?hq=setup"},
		// An unrelated parameter is not this route's business to discard.
		{"unrelated parameters survive", "/workspaces?utm_source=email", "/?utm_source=email"},
		{
			"tree view plus unrelated parameters",
			"/workspaces?view=tree&utm_source=email",
			"/?utm_source=email&view=tree",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.from, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// A TEMPORARY redirect for the first stable release, so rollback
			// stays safe (PRD §9).
			if rec.Code != http.StatusFound {
				t.Fatalf("GET %s: expected 302, got %d", tc.from, rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Errorf("GET %s: redirected to %q, want %q", tc.from, got, tc.want)
			}
		})
	}
}

// TestWorkspaceScopedRoutesSurviveTheMigration pins FR7: only the exact
// /workspaces path redirects. Workspace-scoped routes and their descendants
// keep working, and the API is untouched.
func TestWorkspaceScopedRoutesSurviveTheMigration(t *testing.T) {
	handler := newRoutesTestHandler(t)
	workspace := createRouteTestWorkspace(t, handler, "Scoped Route Workspace")

	for _, path := range []string{
		"/workspaces/" + workspace.Slug,
		"/workspaces/" + workspace.Slug + "/notes",
		"/workspaces/" + workspace.Slug + "/task/task-1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusFound {
			if loc := rec.Header().Get("Location"); loc == "/" {
				t.Errorf("GET %s was redirected to Home; workspace-scoped routes must be untouched", path)
			}
		}
	}

	// The API namespace must not be caught by the page redirect either.
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusFound && rec.Header().Get("Location") == "/" {
		t.Error("GET /api/workspaces was redirected to Home; the API must be untouched")
	}
}

// TestNoInternalLinksTargetTheRetiredLauncher pins FR11/FR12: rendered pages
// must link to Home directly rather than leaning on the compatibility redirect.
func TestNoInternalLinksTargetTheRetiredLauncher(t *testing.T) {
	handler := newRoutesTestHandler(t)

	for _, path := range []string{"/", "/agents", "/action-center"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			continue
		}
		body := rec.Body.String()
		if strings.Contains(body, `href="/workspaces"`) {
			t.Errorf("page %s still links to the retired launcher (href=\"/workspaces\")", path)
		}
		if strings.Contains(body, `href="/workspaces?`) {
			t.Errorf("page %s still links to the retired launcher with a query", path)
		}
	}
}

// TestNoStaticAssetLinksTargetTheRetiredLauncher scans the shipped JavaScript
// for launcher links too.
//
// The rendered-page check above cannot see them: several surfaces build their
// markup in JS at runtime, and two such links (the workspace-scoped page's
// "Workspaces" back button and the Agents MCP note) survived the migration's
// template audit precisely because nothing rendered them server-side.
func TestNoStaticAssetLinksTargetTheRetiredLauncher(t *testing.T) {
	root := filepath.Join("..", "web", "static", "js")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- fixed repo-relative walk
		if readErr != nil {
			return readErr
		}
		for _, bad := range []string{`href="/workspaces"`, `href='/workspaces'`} {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s links to the retired launcher (%s); point it at \"/\"", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking static JS: %v", err)
	}
}
