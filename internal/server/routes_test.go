package server

import (
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
