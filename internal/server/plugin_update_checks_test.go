package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
)

func pluginUpdateTestServer(t *testing.T) *Server {
	t.Helper()
	pluginsDir := t.TempDir()
	handler := pluginhttp.NewHandler(nil, nil, filepath.Join(pluginsDir, "skills"), pluginsDir)
	return &Server{Handlers: &HandlerFacade{Plugin: handler}}
}

func TestServerOwnsPluginUpdateCheckerLifecycle(t *testing.T) {
	server := pluginUpdateTestServer(t)
	server.Start()
	server.Start() // the checker remains idempotent when Server.Start is repeated

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.Handlers.Plugin.UpdateChecker().Snapshot().LastSuccessfulCheckAt != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot := server.Handlers.Plugin.UpdateChecker().Snapshot(); snapshot.LastSuccessfulCheckAt == nil || snapshot.Checking {
		t.Fatalf("immediate startup check did not complete: %+v", snapshot)
	}

	server.Shutdown()
	server.Shutdown()
}

func TestPluginUpdateStatusRouteIsReadOnly(t *testing.T) {
	server := pluginUpdateTestServer(t)
	mux := http.NewServeMux()
	registerPluginRoutes(mux, server)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/updates", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins/updates = %d: %s", rr.Code, rr.Body.String())
	}
	var snapshot plugin.UpdateSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode cached update route: %v", err)
	}
	if snapshot.Checking || len(snapshot.Updates) != 0 {
		t.Fatalf("idle route snapshot = %+v", snapshot)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/updates", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/plugins/updates = %d, want 405", rr.Code)
	}
}
