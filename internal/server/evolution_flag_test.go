package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerBuilder_EvolutionFeatureFlagDisabled(t *testing.T) {
	t.Setenv("ORI_EVOLUTION_ENABLED", "false")
	isolateServerBuildEnv(t)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}

	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	closeInjectionTestServer(t, srv)
	if srv.Handlers.Evolution != nil {
		t.Fatal("expected evolution handler to be disabled")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/evolution/assistant", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when evolution route disabled, got %d", rr.Code)
	}
}
