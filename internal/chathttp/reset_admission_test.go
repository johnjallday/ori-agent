package chathttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionExitOwnsDelayedShutdownBeforeAcknowledgement(t *testing.T) {
	gate := &resetstate.WorkGate{}
	handler := NewCommandHandler(nil)
	handler.SetAdmissionGate(gate)
	started := make(chan struct{})
	finish := make(chan struct{})
	handler.SetShutdownFunc(func() { close(started); <-finish })
	response := httptest.NewRecorder()
	handler.HandleExit(response, httptest.NewRequest(http.MethodPost, "/exit", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("exit status = %d: %s", response.Code, response.Body.String())
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("delayed shutdown was unowned: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("active shutdown was unowned: %v", err)
	}
	closeOnce := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(closeOnce)
	closeOnce()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionExitRefusesBeforeAcknowledgement(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	handler := NewCommandHandler(nil)
	handler.SetAdmissionGate(gate)
	var shutdowns atomic.Int32
	handler.SetShutdownFunc(func() { shutdowns.Add(1) })
	response := httptest.NewRecorder()
	handler.HandleExit(response, httptest.NewRequest(http.MethodPost, "/exit", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced exit status = %d: %s", response.Code, response.Body.String())
	}
	time.Sleep(150 * time.Millisecond)
	if shutdowns.Load() != 0 {
		t.Fatal("fenced exit launched shutdown")
	}
}
