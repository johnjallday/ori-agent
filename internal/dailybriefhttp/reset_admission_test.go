package dailybriefhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type resetHTTPBriefGenerator struct {
	started chan context.Context
	release chan struct{}
}

func (g *resetHTTPBriefGenerator) Generate(ctx context.Context, _ dailybrief.GenerationRequest, _ dailybrief.Config) (dailybrief.GenerationResult, error) {
	g.started <- ctx
	select {
	case <-g.release:
		return dailybrief.GenerationResult{Status: dailybrief.GenerationSucceeded, ContentJSON: `{}`}, nil
	case <-ctx.Done():
		return dailybrief.GenerationResult{}, ctx.Err()
	}
}

func waitHTTPBrief[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("daily brief HTTP child missed its checkpoint")
		var zero T
		return zero
	}
}

func TestResetAdmissionDailyBriefHTTPRegistersChildBefore202(t *testing.T) {
	db, err := database.Open(t.Context(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	profiles := userprofile.NewSQLiteStore(db)
	workspaces := session.NewSQLiteStore(db)
	hq := personalhq.NewService(profiles, workspaces)
	designateHQ(t, hq, workspaces, "ws-hq-reset")
	generator := &resetHTTPBriefGenerator{started: make(chan context.Context, 1), release: make(chan struct{})}
	gate := &resetstate.WorkGate{}
	svc := dailybrief.NewService(dailybrief.NewSQLiteStore(db), generator)
	svc.SetAdmissionGate(gate)
	h := NewHandler(svc, hq, userprofile.LocalUserProvider{})
	h.SetAdmissionGate(gate)
	finish := sync.OnceFunc(func() { close(generator.release) })
	t.Cleanup(finish)

	rec := httptest.NewRecorder()
	h.RequestRefresh(rec, httptest.NewRequest(http.MethodPost, "/api/personal-hq/brief/refresh", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launch = %d %s", rec.Code, rec.Body.String())
	}
	ctx := waitHTTPBrief(t, generator.started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("child not registered after 202: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("reset cancelled background generation")
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced ordinary work: %v", err)
	}
	release()
	finish()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetCurrent(t.Context(), "ws-hq-reset")
	if err != nil || current.Status != dailybrief.GenerationSucceeded {
		t.Fatalf("final revision = %+v, %v", current, err)
	}
}

func TestResetAdmissionDailyBriefHTTPRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, nil, nil)
	h.SetAdmissionGate(gate)
	for _, request := range []struct {
		handle http.HandlerFunc
		method string
	}{
		{h.RequestFirstOpen, http.MethodPost}, {h.RequestRefresh, http.MethodPost}, {h.UpdateConfig, http.MethodPut},
	} {
		rec := httptest.NewRecorder()
		request.handle(rec, httptest.NewRequest(request.method, "/", nil))
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("refusal = %d %s", rec.Code, rec.Body.String())
		}
	}
}
