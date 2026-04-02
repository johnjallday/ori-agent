package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLegacyStudiosRuntimeRoutesRemainUnavailable(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/studios/test/tasks", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected legacy /api/studios runtime route to resolve to 404, got %d", rec.Code)
	}
}
