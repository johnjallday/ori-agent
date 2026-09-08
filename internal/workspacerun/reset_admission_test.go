package workspacerun

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type resetRunExecutor struct {
	started chan context.Context
	release chan struct{}
}

func (e *resetRunExecutor) Execute(ctx context.Context, _ *Run) error {
	e.started <- ctx
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*resetRunExecutor) Cancel(context.Context, *Run) error                  { return nil }
func (*resetRunExecutor) Artifacts(context.Context, *Run) ([]Artifact, error) { return nil, nil }

type resetRunFinalStore struct {
	Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
}

func (s *resetRunFinalStore) UpdateStatus(ctx context.Context, workspaceID, runID string, status RunStatus, message string) error {
	if status == RunStatusSucceeded {
		s.once.Do(func() { close(s.started); <-s.release })
		if s.fail {
			return errors.New("owned final-status failure")
		}
	}
	return s.Store.UpdateStatus(ctx, workspaceID, runID, status, message)
}

func waitResetRun[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("workspace run missed owned checkpoint")
		var zero T
		return zero
	}
}

func newResetRunService(store Store, executor ExecutorRunner, gate *resetstate.WorkGate) *Service {
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindSystemTool, executor)
	svc := NewService(store, NewProfileRegistry(), executors, &stubEnvironmentManager{}, NewValidator(), nil)
	svc.SetAdmissionGate(gate)
	return svc
}

func resetRunRequest(approval string) CreateRunRequest {
	return CreateRunRequest{
		ProfileID: ProfileGeneral, Executor: Executor{Kind: ExecutorKindSystemTool, Ref: "owned"}, Prompt: "fixture run",
		Policy: Policy{Approval: approval}, ValidationRequest: &ValidationRequest{Profile: ValidationProfileNone},
	}
}

func TestResetAdmissionWorkspaceRunTracksExecutionThroughFinalStatus(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "save_failure"}[fail], func(t *testing.T) {
			base := NewMemoryStore()
			store := &resetRunFinalStore{Store: base, started: make(chan struct{}), release: make(chan struct{}), fail: fail}
			executor := &resetRunExecutor{started: make(chan context.Context, 1), release: make(chan struct{})}
			gate := &resetstate.WorkGate{}
			svc := newResetRunService(store, executor, gate)
			run, err := svc.CreateRun(t.Context(), "workspace", resetRunRequest(PolicyApprovalNone))
			if err != nil {
				t.Fatal(err)
			}
			finishExecutor := sync.OnceFunc(func() { close(executor.release) })
			finishStatus := sync.OnceFunc(func() { close(store.release) })
			t.Cleanup(func() { finishExecutor(); finishStatus() })
			done := make(chan error, 1)
			go func() { done <- svc.ExecuteRun(t.Context(), "workspace", run.ID) }()
			ctx := waitResetRun(t, executor.started)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("execution not tracked: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("reset cancelled run")
			}
			release, err := gate.Enter()
			if err != nil {
				t.Fatalf("busy refusal fenced ordinary work: %v", err)
			}
			release()
			finishExecutor()
			waitResetRun(t, store.started)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("final status not tracked: %v", err)
			}
			finishStatus()
			runErr := waitResetRun(t, done)
			if fail && runErr == nil {
				t.Fatal("final status failure hidden")
			}
			if !fail && runErr != nil {
				t.Fatal(runErr)
			}
			if err := gate.TryFence(t.Context()); err != nil {
				t.Fatal(err)
			}
			persisted, err := base.GetRun(t.Context(), "workspace", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fail && persisted.Status == RunStatusSucceeded {
				t.Fatal("failed final status was claimed persisted")
			}
			if !fail && persisted.Status != RunStatusSucceeded {
				t.Fatalf("status = %s", persisted.Status)
			}
		})
	}
}

func TestResetAdmissionWorkspaceRunRetainsApprovalEnvironment(t *testing.T) {
	for _, action := range []string{"approve", "reject", "stop"} {
		t.Run(action, func(t *testing.T) {
			gate := &resetstate.WorkGate{}
			executor := &stubLifecycleExecutor{}
			env := &stubEnvironmentManager{}
			executors := NewExecutorRegistry()
			executors.Register(ExecutorKindSystemTool, executor)
			svc := NewService(NewMemoryStore(), NewProfileRegistry(), executors, env, NewValidator(), nil)
			svc.SetAdmissionGate(gate)
			run, err := svc.CreateRun(t.Context(), "workspace", resetRunRequest(PolicyApprovalFinalOnly))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.ExecuteRun(t.Context(), "workspace", run.ID); err != nil {
				t.Fatal(err)
			}
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("awaiting approval environment untracked: %v", err)
			}
			if env.tornDown {
				t.Fatal("environment closed before decision")
			}
			switch action {
			case "approve":
				err = svc.ApproveRun(t.Context(), "workspace", run.ID)
			case "reject":
				err = svc.RejectRun(t.Context(), "workspace", run.ID, "fixture rejection")
			case "stop":
				err = svc.StopRun(t.Context(), "workspace", run.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !env.tornDown {
				t.Fatal("decision did not join environment teardown")
			}
			if err := gate.TryFence(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResetAdmissionWorkspaceRunHTTPRegistersBeforeCreatedResponse(t *testing.T) {
	base := NewMemoryStore()
	executor := &resetRunExecutor{started: make(chan context.Context, 1), release: make(chan struct{})}
	gate := &resetstate.WorkGate{}
	svc := newResetRunService(base, executor, gate)
	h := NewHandler(base, svc)
	h.SetAdmissionGate(gate)
	finish := sync.OnceFunc(func() { close(executor.release) })
	t.Cleanup(finish)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/workspace/runs", strings.NewReader(`{"profile_id":"general","executor":{"kind":"system_tool","ref":"owned"},"prompt":"fixture run","policy":{"approval":"none"},"validation_request":{"profile":"none"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("workspaceID", "workspace")
	rec := httptest.NewRecorder()
	h.CreateRun(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	ctx := waitResetRun(t, executor.started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("HTTP child not tracked: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("reset cancelled HTTP run")
	}
	finish()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionWorkspaceRunRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil, nil, nil, nil, nil)
	svc.SetAdmissionGate(gate)
	if _, err := svc.CreateRun(t.Context(), "workspace", CreateRunRequest{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	for _, invoke := range []func() error{
		func() error { return svc.ExecuteRun(t.Context(), "workspace", "run") },
		func() error { return svc.StopRun(t.Context(), "workspace", "run") },
		func() error { return svc.ApproveRun(t.Context(), "workspace", "run") },
		func() error { return svc.RejectRun(t.Context(), "workspace", "run", "reason") },
	} {
		if err := invoke(); !errors.Is(err, resetstate.ErrWorkFenced) {
			t.Fatal(err)
		}
	}
	h := NewHandler(nil, nil)
	h.SetAdmissionGate(gate)
	for _, handle := range []http.HandlerFunc{h.CreateRun, h.StopRun, h.ApproveRun, h.RejectRun} {
		rec := httptest.NewRecorder()
		handle(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("refusal = %d %s", rec.Code, rec.Body.String())
		}
	}
}
