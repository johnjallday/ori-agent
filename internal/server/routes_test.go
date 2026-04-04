package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLegacyStudiosRoutesRemoved(t *testing.T) {
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

	// Legacy /api/studios runtime routes should return 404
	req := httptest.NewRequest(http.MethodGet, "/api/studios/test/tasks", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy /api/studios runtime route to return 404, got %d", rec.Code)
	}

	// Legacy /studios page routes should also return 404 (no longer redirect)
	req = httptest.NewRequest(http.MethodGet, "/studios/test", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusMovedPermanently {
		t.Fatalf("expected /studios page route to no longer redirect, got 301")
	}
}
