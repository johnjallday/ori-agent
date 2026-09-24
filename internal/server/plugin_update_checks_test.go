package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
)

func pluginUpdateTestServer(t *testing.T) *Server {
	t.Helper()
	pluginsDir := t.TempDir()
	handler := pluginhttp.NewHandler(nil, nil, pluginsDir)
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

// The cached snapshot route tells the page which answers came from a reviewed
// release, and stays read-only while doing so: the installed record is not
// touched by the check or by reading the route.
func TestPluginUpdateStatusRouteMarksReviewedReleaseAnswers(t *testing.T) {
	updates, _, entry := reviewedUpdatesFixture("0.9.0")
	pluginsDir := t.TempDir()
	handler := pluginhttp.NewHandler(nil, nil, pluginsDir)
	record, err := json.Marshal([]plugin.InstalledPlugin{
		reviewedInstall(entry, "0.8.0", entry.SourceRepository+".git"),
	})
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(pluginsDir, "installed.json")
	if err := os.WriteFile(storePath, record, 0o600); err != nil {
		t.Fatal(err)
	}
	handler.UpdateChecker().SetAvailabilityOverride(updates.availability)
	handler.UpdateChecker().Start(time.Hour)
	defer handler.UpdateChecker().Stop()
	deadline := time.Now().Add(2 * time.Second)
	for handler.UpdateChecker().Snapshot().LastSuccessfulCheckAt == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	mux := http.NewServeMux()
	registerPluginRoutes(mux, &Server{Handlers: &HandlerFacade{Plugin: handler}})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/updates", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins/updates = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Updates []map[string]any `json:"updates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || len(body.Updates) != 1 {
		t.Fatalf("snapshot = %s (err=%v)", rr.Body.String(), err)
	}
	row := body.Updates[0]
	if row["available_version"] != "0.9.0" || row["available"] != true || row["reviewed_release"] != true {
		t.Fatalf("reviewed row = %#v", row)
	}
	if after, err := os.ReadFile(storePath); err != nil || string(after) != string(record) {
		t.Fatalf("the update check rewrote the installed record (err=%v)", err)
	}

	// A recorded-source result omits the key entirely, so every existing payload
	// keeps the source wording.
	plain, err := json.Marshal(plugin.UpdateAvailability{Name: "local", InstalledVersion: "1", AvailableVersion: "2", Available: true})
	if err != nil || strings.Contains(string(plain), "reviewed_release") {
		t.Fatalf("a recorded-source result carried the reviewed flag: %s (err=%v)", plain, err)
	}
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
