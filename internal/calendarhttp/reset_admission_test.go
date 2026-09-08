package calendarhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type blockingResetPrepExecutor struct {
	inner   TaskExecutor
	started chan context.Context
	finish  chan struct{}
}

func (e *blockingResetPrepExecutor) ExecuteTask(ctx context.Context, workspaceID string, task agentworkspace.Task) error {
	e.started <- ctx
	select {
	case <-e.finish:
		return e.inner.ExecuteTask(ctx, workspaceID, task)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestResetAdmissionMeetingPrepTransfersBeforeResponse(t *testing.T) {
	ws, _ := newPrepTestWorkspace("ws-owned", "local")
	handler, folders, notes, _ := newPrepTestHandler(t, ws, "local")
	gate := &resetstate.WorkGate{}
	handler.SetAdmissionGate(gate)
	finish := make(chan struct{})
	executor := &blockingResetPrepExecutor{
		inner:   &fakeTaskExecutor{folders: folders, simFn: successSim(notes)},
		started: make(chan context.Context, 1), finish: finish,
	}
	handler.SetTaskExecutor(executor)
	body, err := json.Marshal(validPrepRequest(ws.ID))
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/calendar-ops/events/prepare", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Prepare(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare status = %d: %s", response.Code, response.Body.String())
	}
	var workCtx context.Context
	select {
	case workCtx = <-executor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("meeting prep did not start")
	}
	cancel()
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("meeting prep not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("response cancellation stopped meeting prep: %v", workCtx.Err())
	default:
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

func TestResetAdmissionMeetingPrepRefusesBeforeOwnerAccess(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(nil, nil, nil, nil, nil)
	handler.SetAdmissionGate(gate)
	response := httptest.NewRecorder()
	handler.Prepare(response, httptest.NewRequest(http.MethodPost, "/api/calendar-ops/events/prepare", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced prepare status = %d: %s", response.Code, response.Body.String())
	}
}
