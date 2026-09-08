package orchestrationhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The fake executor deliberately has NO gate: these tests must detect a lost
// caller-to-child handoff rather than succeed on a nested provider permit.
type resetTaskProvider struct {
	started chan context.Context
	release chan struct{}
}

func (p *resetTaskProvider) ExecuteTask(ctx context.Context, _ string, _ workspace.Task) (string, error) {
	p.started <- ctx
	select {
	case <-p.release:
		return "The fixture calculation is 42.", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type resetTaskFinalStore struct {
	workspace.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
}

func (s *resetTaskFinalStore) Save(ws *workspace.Workspace) error {
	task, err := ws.GetTask("task")
	if err == nil && task.Status == workspace.TaskStatusCompleted {
		s.once.Do(func() { close(s.started); <-s.release })
		if s.fail {
			return errors.New("owned final-save failure")
		}
	}
	return s.Store.Save(ws)
}

type resetSchedulerStore struct {
	*resetTaskFinalStore
	mu sync.Mutex
	ws *workspace.Workspace
}

func (s *resetSchedulerStore) Get(id string) (*workspace.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ws == nil || s.ws.ID != id {
		return nil, errors.New("workspace not found")
	}
	return s.ws, nil
}

func (s *resetSchedulerStore) Save(ws *workspace.Workspace) error {
	s.mu.Lock()
	s.ws = ws
	s.mu.Unlock()
	return s.resetTaskFinalStore.Save(ws)
}

func resetTaskWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("owned task missed its checkpoint")
		var zero T
		return zero
	}
}

func resetTaskIdle(t *testing.T, gate *resetstate.WorkGate) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 {
		if time.Now().After(deadline) {
			t.Fatal("owned task permits did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestResetAdmissionManualDispatchThroughFinalSave(t *testing.T) {
	for _, kind := range []string{"manual", "manual_parent", "first_open", "assist", "assist_parent", "review", "review_parent", "scheduler", "manual_save_failure"} {
		t.Run(kind, func(t *testing.T) {
			f := resetfixture.New(t)
			root := f.Paths().Workspaces
			base, err := workspace.NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := base.Close(); err != nil {
					t.Error(err)
				}
			})
			ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "reset-task", Agents: []string{"fixture-agent"}})
			task := workspace.Task{ID: "task", WorkspaceID: ws.ID, To: "fixture-agent", Description: "Calculate six times seven", Status: workspace.TaskStatusPending}
			if strings.HasPrefix(kind, "assist") {
				task.Status = workspace.TaskStatusWaitingForChoice
			}
			if strings.HasPrefix(kind, "review") {
				task.Status = workspace.TaskStatusFailed
				task.ExecutionHistory = []workspace.TaskExecution{{Validation: &workspace.TaskValidationResult{ValidationStatus: workspace.TaskValidationNeedsReview}}}
			}
			if err := ws.AddTask(task); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(kind, "_parent") {
				child := workspace.Task{ID: "child", WorkspaceID: ws.ID, ParentTaskID: "task", To: "fixture-agent", Description: "Calculate six times seven", Status: workspace.TaskStatusPending}
				if err := ws.AddTask(child); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "scheduler" {
				ws.ScheduledTasks = []workspace.ScheduledTask{{ID: "scheduled", CanvasNodeID: "node", TargetTaskID: "task"}}
			}
			if err := base.Save(ws); err != nil {
				t.Fatal(err)
			}
			provider := &resetTaskProvider{started: make(chan context.Context, 1), release: make(chan struct{})}
			finalStore := &resetTaskFinalStore{Store: base, started: make(chan struct{}), release: make(chan struct{}), fail: kind == "manual_save_failure"}
			gate := &resetstate.WorkGate{}
			if strings.HasPrefix(kind, "review") {
				loaded, loadErr := base.Get(ws.ID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				loadedTask, loadErr := loaded.GetTask("task")
				if loadErr != nil || len(loadedTask.ExecutionHistory) != 1 {
					t.Fatalf("review fixture not persisted: %+v, %v", loadedTask, loadErr)
				}
			}
			var taskStore workspace.Store = finalStore
			if kind == "scheduler" {
				taskStore = &resetSchedulerStore{resetTaskFinalStore: finalStore, ws: ws}
			}
			h := orchestrationhttp.NewTaskHandler(taskStore, agentcomm.NewCommunicator(taskStore), provider, nil)
			h.SetAdmissionGate(gate)
			finishProvider := sync.OnceFunc(func() { close(provider.release) })
			finishSave := sync.OnceFunc(func() { close(finalStore.release) })
			t.Cleanup(func() { finishProvider(); finishSave(); resetTaskIdle(t, gate) })

			path, body, status := "/execute", `{"task_id":"task"}`, http.StatusAccepted
			var handler http.HandlerFunc = h.ExecuteTaskHandler
			switch {
			case kind == "first_open":
				handler = func(w http.ResponseWriter, _ *http.Request) {
					if err := h.StartTaskAsync(ws.ID, "task"); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					w.WriteHeader(http.StatusAccepted)
				}
			case strings.HasPrefix(kind, "assist"):
				handler, path, body, status = h.TasksPathHandler, "/api/orchestration/tasks/task/assist", `{"action":"retry"}`, http.StatusOK
			case strings.HasPrefix(kind, "review"):
				handler, path, body = h.TasksPathHandler, "/api/orchestration/tasks/task/review", `{"action":"rerun","history_index":0}`
			case kind == "scheduler":
				handler, path, status = h.SchedulerNodeTriggerHandler, "/scheduler-nodes/node/trigger?workspace_id="+ws.ID, http.StatusOK
			}
			srv := f.StartHTTP(t, handler)
			resp, err := srv.Do(t.Context(), http.MethodPost, path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			if err != nil || resp.StatusCode != status {
				t.Fatalf("launch = %d %s, %v", resp.StatusCode, data, err)
			}
			ctx := resetTaskWait(t, provider.started)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("handoff not tracked after response: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("reset cancelled active task")
			}
			release, err := gate.Enter()
			if err != nil {
				t.Fatalf("busy refusal fenced ordinary work: %v", err)
			}
			release()
			finishProvider()
			resetTaskWait(t, finalStore.started)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("final save not tracked: %v", err)
			}
			finishSave()
			resetTaskIdle(t, gate)
			if err := gate.TryFence(t.Context()); err != nil {
				t.Fatal(err)
			}
			fresh, err := workspace.NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := fresh.Close(); err != nil {
					t.Error(err)
				}
			}()
			persisted, err := fresh.Get(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := persisted.GetTask("task")
			if err != nil {
				t.Fatal(err)
			}
			if finalStore.fail {
				if result.Status == workspace.TaskStatusCompleted {
					t.Fatal("failed save was claimed persisted")
				}
			} else if result.Status != workspace.TaskStatusCompleted || !strings.Contains(result.Result, "42") {
				t.Fatalf("lost final task/parent result: %+v", result)
			}
			f.AssertPreserved(t)
		})
	}
}

func TestResetAdmissionTaskEntryRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Nil stores would panic on discovery. No provider/native discovery exists.
	h := orchestrationhttp.NewTaskHandler(nil, nil, &resetTaskProvider{}, nil)
	h.SetAdmissionGate(gate)
	if err := h.StartTaskAsync("workspace", "task"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	for _, request := range []struct {
		handle http.HandlerFunc
		path   string
	}{
		{h.ExecuteTaskHandler, "/execute"},
		{h.TasksPathHandler, "/api/orchestration/tasks/task/assist"},
		{h.TasksPathHandler, "/api/orchestration/tasks/task/review"},
		{h.SchedulerNodeTriggerHandler, "/scheduler-nodes/node/trigger?workspace_id=workspace"},
	} {
		w := httptest.NewRecorder()
		request.handle(w, httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(`{}`)))
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("fence = %d %s", w.Code, w.Body.String())
		}
	}

	legacy := orchestrationhttp.NewHandlerLegacy(nil, nil)
	legacy.SetAdmissionGate(gate)
	legacy.SetEventBus(workspace.NewEventBus(1, 1))
	legacy.SetTaskHandler(&resetTaskProvider{})
	if err := legacy.StartTaskAsync("workspace", "task"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("legacy gate wiring: %v", err)
	}
}
