package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newRoutesTestHandler(t *testing.T) http.Handler {
	t.Helper()

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := t.TempDir()
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

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}

	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	return srv.Handler()
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
}

// TestBrandNewProfileRedirectsHomeToGuidedMap covers task 4.1/4.2: a
// profile that has never seen the guided Personal HQ first-launch
// experience must land on the workspace launcher (Map mode) instead of
// Home, and stop redirecting once onboarding is no longer "unseen".
func TestBrandNewProfileRedirectsHomeToGuidedMap(t *testing.T) {
	handler := newRoutesTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for a brand-new profile, got %d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/workspaces?hq_onboarding=1" {
		t.Fatalf("expected redirect to the guided Map, got %q", loc)
	}

	// Once onboarding state moves off "unseen" (e.g. the user skipped),
	// normal Home launch resumes.
	skipReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/onboarding-state", strings.NewReader(`{"state":"skipped"}`))
	skipReq.Header.Set("Content-Type", "application/json")
	skipRec := httptest.NewRecorder()
	handler.ServeHTTP(skipRec, skipReq)
	if skipRec.Code != http.StatusOK {
		t.Fatalf("expected onboarding-state update to succeed, got %d body=%s", skipRec.Code, skipRec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected normal Home launch after skipping, got %d", rec2.Code)
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

func TestWorkspaceNotesRoutesServeNotePage(t *testing.T) {
	handler := newRoutesTestHandler(t)

	tests := []struct {
		name     string
		path     string
		contains []string
	}{
		{
			name: "workspace notes app",
			path: "/workspaces/ws-1/notes",
			contains: []string{
				`id="noteMainContent"`,
				`data-workspace-id="ws-1"`,
				`data-note-id=""`,
				`data-page-mode="workspace"`,
			},
		},
		{
			name: "workspace note deep link",
			path: "/workspaces/ws-1/notes/note-1",
			contains: []string{
				`id="noteMainContent"`,
				`data-workspace-id="ws-1"`,
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

	req := httptest.NewRequest(http.MethodGet, "/workspaces/ws-1/runs/run-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /workspaces/{workspaceID}/runs/{runID} page route to return 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`id="workspaceRunPageRoot"`,
		`const workspaceId = "ws-1";`,
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
