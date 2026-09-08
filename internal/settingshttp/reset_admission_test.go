package settingshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type admissionLifecycle struct {
	mu     sync.Mutex
	fenced bool
	drains int
}

func (l *admissionLifecycle) TryFence(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fenced {
		return settingsreset.ErrActiveWork
	}
	l.fenced = true
	return nil
}
func (l *admissionLifecycle) Drain(context.Context) error {
	l.mu.Lock()
	l.drains++
	l.mu.Unlock()
	return nil
}

func admissionHTTPFixture(t *testing.T) (*resetfixture.Fixture, *resetfixture.HTTPServer, *admissionLifecycle) {
	t.Helper()
	if !resetstate.Supported() {
		t.Skip("native reset lease unavailable")
	}
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	lease, err := resetstate.Acquire(p.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	folders, err := workspace.NewFileStore(p.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := folders.Close(); err != nil {
			t.Error(err)
		}
	})
	cfg := config.NewManagerWithSecretStore(filepath.Join(p.DataDir, "settings.json"), f.Secrets())
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	setup := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	owners := settingsreset.Owners{DataDir: p.DataDir, Config: cfg, Setup: setup, Database: db, Workspaces: folders,
		Vaults:         vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: p.DataDir, ManagedVaultRoot: p.Vaults}),
		CheckLifecycle: func(context.Context) []settingsreset.Blocker { return nil }}
	planner := settingsreset.NewPlanner(func() settingsreset.Owners { return owners })
	life := &admissionLifecycle{} // This fixture has no production dispatchers/children.
	h := NewResetHandler(setup, nil, p.DataDir)
	h.SetPreviewPlanner(planner)
	h.SetCoordinator(settingsreset.NewCoordinator(lease, planner, life))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/reset", h.HandleReset)
	mux.HandleFunc("/api/reset/preview", h.GetResetPreview)
	mux.HandleFunc("/api/reset/operations/", h.GetOperation)
	return f, f.StartHTTP(t, mux), life
}

func readAdmissionResponse(t *testing.T, response *http.Response, dest any) {
	t.Helper()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Error("cacheable reset state")
	}
	if err := json.NewDecoder(response.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}

func TestResetOutcomeReplayedRequestCannotRunAnotherWipe(t *testing.T) {
	f, server, life := admissionHTTPFixture(t)
	response, err := server.Do(t.Context(), http.MethodGet, "/api/reset/preview?intent=selected_data&category=setup_steps", nil)
	if err != nil {
		t.Fatal(err)
	}
	var preview settingsreset.Preview
	readAdmissionResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.OperationID == "" || len(preview.Blockers) != 0 {
		t.Fatal("fixture preview not admissible")
	}
	before, err := os.ReadFile(filepath.Join(f.Paths().DataDir, "app_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(settingsreset.ExecuteRequest{PreviewID: preview.ID, RequestID: "owned-replay", Confirmation: "RESET"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response, err = server.Do(t.Context(), http.MethodPost, "/api/reset", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var result ResetResponse
		readAdmissionResponse(t, response, &result)
		if response.StatusCode != http.StatusAccepted || result.Success || !result.RequiresRestart || len(result.ResetItems) != 0 || result.Operation == nil || result.Operation.ID != preview.OperationID || result.Operation.State != settingsreset.StateAwaitingRestart {
			t.Fatal("admission/replay was not the same pending operation:", result)
		}
	}
	// Recovery works with the ID known BEFORE the first POST response, not a
	// health request or another submission that could retry failed effects.
	response, err = server.Do(t.Context(), http.MethodGet, "/api/reset/operations/"+preview.OperationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var recovered ResetResponse
	readAdmissionResponse(t, response, &recovered)
	if response.StatusCode != http.StatusOK || recovered.Operation == nil || recovered.Operation.ID != preview.OperationID || recovered.Operation.Revision != 2 {
		t.Fatal("lost-response recovery failed")
	}
	life.mu.Lock()
	drains := life.drains
	life.mu.Unlock()
	if drains != 1 {
		t.Fatal("replay started another reset lifecycle")
	}
	after, err := os.ReadFile(filepath.Join(f.Paths().DataDir, "app_state.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("admission deleted live setup data:", err)
	}
	f.AssertPreserved(t)
}

func TestResetExecuteStrictProtectionAndLegacyRefusal(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	agents := &fakeAgentStore{}
	h := NewResetHandler(nil, agents, f.Paths().DataDir)
	for _, test := range []struct {
		body   string
		status int
		code   string
	}{
		{`{"settings":true,"confirmation":"RESET"}`, 409, "preview_required"},
		{`{"settings":true,"agents":true,"sessions":true,"onboarding":true,"confirmation":"RESET"}`, 409, "preview_required"},
		{`{"preview_id":"known","request_id":"request","confirmation":"RESET"}`, 503, "lifecycle_unavailable"},
		{`{"settings":true,"preview_id":"p","request_id":"r","confirmation":"RESET"}`, 400, "invalid_request"},
		{`{"settings":false,"confirmation":"RESET"}`, 400, "invalid_request"},
		{`{"settings":null,"confirmation":"RESET"}`, 400, "invalid_request"},
		{`{"settings":true,"confirmation":"RESET","confirmation":"RESET"}`, 400, "invalid_request"},
		{`{"settings":true,"Confirmation":"RESET"}`, 400, "invalid_request"},
		{`{"settings":true,"confirmation":null}`, 400, "invalid_request"},
		{`{"settings":true,"confirmation":"RESET"} {}`, 400, "invalid_request"},
		{`{"settings":true,"confirmation":"RESET","path":"/outside"}`, 400, "invalid_request"},
		{`{"preview_id":"` + strings.Repeat("x", 1024) + `","request_id":"r","confirmation":"RESET"}`, 400, "invalid_request"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		h.HandleReset(w, r)
		var result ResetResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != test.status || result.Code != test.code || result.Success {
			t.Fatalf("status/code = %d/%s, want %d/%s", w.Code, result.Code, test.status, test.code)
		}
	}
	for _, headers := range [][2]string{{"", "application/json"}, {"XMLHttpRequest", "text/plain"}} {
		r := httptest.NewRequest(http.MethodPost, "/api/reset", strings.NewReader(`{"settings":true,"confirmation":"RESET"}`))
		r.Header.Set("X-Requested-With", headers[0])
		r.Header.Set("Content-Type", headers[1])
		w := httptest.NewRecorder()
		h.HandleReset(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal("request protection bypassed")
		}
	}
	if agents.clearCalls != 0 {
		t.Fatal("rejected HTTP request cleared a live cache")
	}
	for _, name := range []string{"settings.json", "agents.json", "app_state.json", "sessions.db"} {
		if _, err := os.Stat(filepath.Join(f.Paths().DataDir, name)); err != nil {
			t.Fatal("rejected request deleted live data:", err)
		}
	}
	f.AssertPreserved(t)
}
