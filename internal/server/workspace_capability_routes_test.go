package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
	"github.com/johnjallday/ori-agent/internal/workspacecapabilityhttp"
)

// newBuiltTestBuilder builds a real server and returns the builder, so a test
// can inspect wiring the Server facade does not expose. It mirrors
// newRoutesTestHandler's isolation: a temp CWD and a temp HOME, so nothing
// touches the developer's real workspace tree.
func newBuiltTestBuilder(t *testing.T) *ServerBuilder {
	t.Helper()

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWD) })
	t.Setenv("HOME", tmpDir)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	if _, err := builder.Build(); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return builder
}

// TestWorkspaceCapabilityRoutesRegistered proves the capability lifecycle API is
// mounted on the real server, wired through the real builder — not just that the
// handler compiles.
//
// The workspace does not exist in this fixture, so the expected answer is 404
// from the ownership boundary. That is exactly what distinguishes "route
// registered, boundary enforced" from "route missing": an unregistered path
// would also 404, so the test additionally asserts the response is the handler's
// JSON error rather than the mux's bare not-found.
func TestWorkspaceCapabilityRoutesRegistered(t *testing.T) {
	handler := newRoutesTestHandler(t)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"catalog", http.MethodGet, "/api/workspaces/ws-1/capabilities"},
		{"install", http.MethodPost, "/api/workspaces/ws-1/capabilities/file-janitor/install"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 for an unknown workspace (body %s)", rec.Code, rec.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("expected the handler's JSON error (route registered), got %q", rec.Body.String())
			}
			if !strings.Contains(strings.ToLower(rec.Body.String()), "workspace") {
				t.Fatalf("expected a workspace-scoped error, got %s", rec.Body.String())
			}
		})
	}
}

// TestWorkspaceCapabilityRegistryWiredWithFileJanitorRuntime proves the builder
// actually binds File Janitor's compiled runtime (FR-2, FR-3). Without the
// binding the capability would still list, but every status would report
// "unavailable" — a station that never leaves an error state.
func TestWorkspaceCapabilityRegistryWiredWithFileJanitorRuntime(t *testing.T) {
	builder := newBuiltTestBuilder(t)

	if builder.workspaceCapabilityRegistry == nil {
		t.Fatal("capability registry was not wired")
	}
	if builder.workspaceCapabilityService == nil {
		t.Fatal("capability lifecycle service was not wired")
	}
	if builder.workspaceCapabilityHandler == nil {
		t.Fatal("capability handler was not wired")
	}

	def, ok := builder.workspaceCapabilityRegistry.Definition(workspace.CapabilityFileJanitor)
	if !ok {
		t.Fatal("file-janitor is not registered in the built server's registry")
	}
	if def.Version != workspacecapability.FileJanitorDefinitionVersion {
		t.Fatalf("definition version = %d, want %d", def.Version, workspacecapability.FileJanitorDefinitionVersion)
	}
	if _, bound := builder.workspaceCapabilityRegistry.Runtime(workspace.CapabilityFileJanitor); !bound {
		t.Fatal("File Janitor's compiled runtime was not bound to the registry")
	}
}

// TestWorkspaceCapabilityWiringFailureIsIsolated covers FR-145: when
// capabilities fail to wire, the handler is nil — and neither route registration
// nor the rest of the API may break. An unwired handler must answer 503, which
// is visible and repairable, rather than panicking at request time.
func TestWorkspaceCapabilityWiringFailureIsIsolated(t *testing.T) {
	mux := http.NewServeMux()

	// A nil handler is what an unwired capability subsystem leaves behind.
	var unwired *workspacecapabilityhttp.Handler
	unwired.Register(mux)

	// Registration is a no-op rather than a panic, so an unrelated route
	// registered on the same mux still works.
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("an unrelated route broke when capabilities failed to wire: %d", rec.Code)
	}

	// And a handler constructed without a service reports 503 rather than
	// panicking.
	degraded := workspacecapabilityhttp.NewHandler(nil, nil, nil)
	degradedMux := http.NewServeMux()
	degraded.Register(degradedMux)

	rec = httptest.NewRecorder()
	degradedMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-1/capabilities", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 from a degraded capability handler", rec.Code)
	}
}
