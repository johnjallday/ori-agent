package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// This fixture has one known HTTP writer and no runtime services or children.
// A WorkGate alone is deliberately not a production Lifecycle.
type resetHTTPGateLifecycle struct {
	*resetstate.WorkGate
	drains atomic.Int32
}

func (l *resetHTTPGateLifecycle) Drain(context.Context) error { l.drains.Add(1); return nil }

func TestResetAdmissionHTTPBusyThenStagedRecoveryStaysReachable(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	lease, err := resetstate.Acquire(p.DataDir)
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, lease.Close()) })
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(p.DataDir, "sessions.db"), WALMode: true})
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, db.Close()) })
	folders, err := workspace.NewFileStore(p.Workspaces)
	requireResetNoError(t, err)
	t.Cleanup(func() { requireResetNoError(t, folders.Close()) })
	cfg := config.NewManagerWithSecretStore(filepath.Join(p.DataDir, "settings.json"), f.Secrets())
	requireResetNoError(t, cfg.Load())
	setup := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	before, err := os.ReadFile(setup.PersistencePath())
	requireResetNoError(t, err)
	owners := settingsreset.Owners{DataDir: p.DataDir, Config: cfg, Setup: setup, Database: db, Workspaces: folders,
		Vaults:         vault.NewStore(db, vault.StoreOptions{VaultFilesBaseDir: p.DataDir, ManagedVaultRoot: p.Vaults}),
		CheckLifecycle: func(context.Context) []settingsreset.Blocker { return nil },
	}
	planner := settingsreset.NewPlanner(func() settingsreset.Owners { return owners })
	life := &resetHTTPGateLifecycle{WorkGate: lease.WorkGate()}
	h := settingshttp.NewResetHandler(setup, nil, p.DataDir)
	h.SetPreviewPlanner(planner)
	h.SetCoordinator(settingsreset.NewCoordinator(lease, planner, life))
	s := &Server{resetWork: life.WorkGate, Handlers: &HandlerFacade{Reset: h}}
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	var ordinaryCalls atomic.Int32
	ordinary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordinaryCalls.Add(1)
		if r.URL.Path == "/paused-save" {
			started <- r.Context()
			<-finish
			if err := cfg.Save(); err != nil {
				http.Error(w, "fixture save failed", 500)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	host := f.StartHTTP(t, s.resetAdmissionMiddleware(ordinary))
	finishOnce := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(finishOnce) // Release before the owned listener's cleanup joins.
	request := func(method, path string, body []byte, expected int, dest any) {
		t.Helper()
		response, err := host.Do(t.Context(), method, path, bytes.NewReader(body))
		requireResetNoError(t, err)
		defer func() { requireResetNoError(t, response.Body.Close()) }()
		if response.StatusCode != expected {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("%s %s: %d, want %d: %s", method, path, response.StatusCode, expected, data)
		}
		if dest != nil {
			requireResetNoError(t, json.NewDecoder(response.Body).Decode(dest))
		}
	}
	var preview settingsreset.Preview
	request(http.MethodGet, "/api/reset/preview?intent=selected_data&category=setup_steps", nil, http.StatusOK, &preview)
	if len(preview.Blockers) != 0 {
		t.Fatalf("fixture preview blocked: %+v", preview.Blockers)
	}
	body, err := json.Marshal(settingsreset.ExecuteRequest{PreviewID: preview.ID, RequestID: "http-owned-request", Confirmation: "RESET"})
	requireResetNoError(t, err)
	done := make(chan error, 1)
	go func() {
		response, err := host.Do(t.Context(), http.MethodPost, "/paused-save", nil)
		if err == nil {
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("paused save did not complete")
			}
			_ = response.Body.Close()
		}
		done <- err
	}()
	var workCtx context.Context
	select {
	case workCtx = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP writer did not start")
	}
	var refusal settingshttp.ResetResponse
	request(http.MethodPost, "/api/reset", body, http.StatusConflict, &refusal)
	if refusal.Code != "active_work" || workCtx.Err() != nil || life.Snapshot().Fenced || life.drains.Load() != 0 {
		t.Fatalf("busy reset cancelled/drained/fenced: %+v", refusal)
	}
	request(http.MethodPost, "/ordinary-save", nil, http.StatusNoContent, nil)
	finishOnce()
	requireResetNoError(t, <-done)
	var accepted settingshttp.ResetResponse
	request(http.MethodPost, "/api/reset", body, http.StatusAccepted, &accepted)
	if accepted.Success || accepted.Operation == nil || accepted.Operation.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("not pending: %+v", accepted)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		request(method, "/ordinary-save", nil, http.StatusServiceUnavailable, nil)
	}
	var recovered, replay settingshttp.ResetResponse
	request(http.MethodGet, "/api/reset/operations/"+preview.OperationID, nil, http.StatusOK, &recovered)
	request(http.MethodPost, "/api/reset", body, http.StatusAccepted, &replay)
	if recovered.Operation == nil || replay.Operation == nil || recovered.Operation.ID != preview.OperationID || replay.Operation.Revision != accepted.Operation.Revision || life.drains.Load() != 1 || ordinaryCalls.Load() != 2 {
		t.Fatal("recovery/replay bypassed or repeated admission")
	}
	after, err := os.ReadFile(setup.PersistencePath())
	requireResetNoError(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("admission applied selected setup fields")
	}
	f.AssertPreserved(t)
}

func TestResetAdmissionControlMuxCannotBypassIntoOrdinaryRoutes(t *testing.T) {
	g := &resetstate.WorkGate{}
	requireResetNoError(t, g.TryFence(t.Context()))
	s := &Server{resetWork: g, Handlers: &HandlerFacade{Reset: settingshttp.NewResetHandler(nil, nil, t.TempDir())}}
	handler := s.resetAdmissionMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("control path reached an ordinary handler") }))
	for _, path := range []string{"/api/reset/anything-new", "/api/reset-other", "/api/reset/operations/id/../../settings", "/api/reset/../settings", "/api/reset%2f..%2fsettings", "/api/RESET", "/settings"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusMovedPermanently && w.Code != http.StatusBadRequest {
			t.Fatalf("unsafe alias %s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "reset_pending") {
		t.Fatal("fence response not explicit/no-store")
	}
}

func TestResetAdmissionHTTPPanicReleasesPermit(t *testing.T) {
	g := &resetstate.WorkGate{}
	s := &Server{resetWork: g}
	handler := s.resetAdmissionMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("owned handler failure") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("test handler did not panic")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/save", nil))
	}()
	requireResetNoError(t, g.TryFence(t.Context()))
}

func TestResetAdmissionBuilderInstrumentationDoesNotAdvertiseReadiness(t *testing.T) {
	// NewServerBuilder allocates dependencies but does not open application
	// stores. Do not call Build here: even regression tests must avoid native
	// credential discovery and broad production constructors.
	b, err := NewServerBuilder()
	requireResetNoError(t, err)
	if b.resetWork == nil || b.server.resetWork != b.resetWork {
		t.Fatal("builder and HTTP have different admission gates")
	}
	if b.resetPreviewOwners().CheckLifecycle != nil {
		t.Fatal("partial instrumentation enabled destructive reset")
	}
}
