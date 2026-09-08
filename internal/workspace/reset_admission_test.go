package workspace

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// These tests own their file stores and use no providers or external processes.
// The final Update pause is outside the provider, so tracking only provider calls
// or sampling runningTasks after dispatch cannot satisfy the admission contract.
type resetPausedTaskHandler struct {
	started  chan context.Context
	release  chan struct{}
	finished atomic.Bool
}

func (h *resetPausedTaskHandler) ExecuteTask(ctx context.Context, _ string, _ Task) (string, error) {
	h.started <- ctx
	select {
	case <-h.release:
		h.finished.Store(true)
		return "finished normally", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type resetPausedFinalStore struct {
	Store
	providerFinished *atomic.Bool
	once             sync.Once
	started          chan struct{}
	release          chan struct{}
}

func (s *resetPausedFinalStore) Update(id string, fn func(*Workspace) error) error {
	if s.providerFinished.Load() {
		s.once.Do(func() { close(s.started); <-s.release })
	}
	return s.Store.Update(id, fn)
}

func resetWait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("owned worker did not reach its checkpoint")
		var zero T
		return zero
	}
}

func TestResetAdmissionTracksExecutionThroughFinalPersistence(t *testing.T) {
	for _, kind := range []string{"task", "step", "manual_orchestrator"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			base, err := NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := base.Close(); err != nil {
					t.Error(err)
				}
			})
			ws := newWorkspaceWithTasks(t, []Task{{ID: "task", To: "agent-a", Status: TaskStatusAssigned}})
			if err := ws.AddAgent("orchestrator"); err != nil {
				t.Fatal(err)
			}
			ws.Workflows = map[string]Workflow{"workflow": {
				ID: "workflow", Status: WorkflowStatusInProgress,
				Steps: []WorkflowStep{{ID: "step", Name: "step", Type: StepTypeTask, AssignedTo: "agent-a", Status: StepStatusReady}},
			}}
			if err := base.Save(ws); err != nil {
				t.Fatal(err)
			}
			h := &resetPausedTaskHandler{started: make(chan context.Context, 1), release: make(chan struct{})}
			store := &resetPausedFinalStore{Store: base, providerFinished: &h.finished, started: make(chan struct{}), release: make(chan struct{})}
			g := &resetstate.WorkGate{}
			var join func()
			switch kind {
			case "task":
				e := NewTaskExecutor(store, h, ExecutorConfig{AdmissionGate: g})
				e.checkAndExecuteTasks()
				join = e.wg.Wait
			case "step":
				e := NewStepExecutor(store, h, StepExecutorConfig{AdmissionGate: g})
				e.checkAndExecuteSteps()
				join = e.wg.Wait
			case "manual_orchestrator":
				o := NewOrchestrator(store, nil, nil, nil)
				o.SetAdmissionGate(g)
				o.SetTaskHandler(h)
				done := make(chan error, 1)
				go func() { done <- o.ExecuteTask(t.Context(), ws.ID, ws.Tasks[0]) }()
				join = func() {
					if err := resetWait(t, done); err != nil {
						t.Error(err)
					}
				}
			}
			finishProvider := sync.OnceFunc(func() { close(h.release) })
			finishSave := sync.OnceFunc(func() { close(store.release) })
			t.Cleanup(func() { finishProvider(); finishSave(); join() })
			ctx := resetWait(t, h.started)
			if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("active execution: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("reset cancelled user work")
			}
			// Refusal must leave ordinary work open, not partly fence the app.
			release, err := g.Enter()
			if err != nil {
				t.Fatal(err)
			}
			release()
			finishProvider()
			resetWait(t, store.started)
			if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("final persistence not tracked: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("reset cancelled final persistence")
			}
			finishSave()
			// Run the join only once (manual completion is a single channel read).
			waitOnce := sync.OnceFunc(join)
			join = waitOnce
			join()
			if err := g.TryFence(t.Context()); err != nil {
				t.Fatalf("finished execution: %v", err)
			}
			fresh, err := NewFileStore(root)
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
			if kind == "step" {
				step := persisted.Workflows["workflow"].Steps[0]
				if step.Status != StepStatusCompleted || step.Result != "finished normally" || persisted.Workflows["workflow"].Status != WorkflowStatusCompleted {
					t.Fatalf("lost step finalization: %+v", step)
				}
			} else {
				task, err := persisted.GetTask("task")
				if err != nil {
					t.Fatal(err)
				}
				if task.Status != TaskStatusCompleted || task.Result != "finished normally" {
					t.Fatalf("lost task finalization: %+v", task)
				}
			}
		})
	}
}

func TestResetAdmissionRefusesDispatchBeforeTouchingOwners(t *testing.T) {
	g := &resetstate.WorkGate{}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Nil owners intentionally panic if a fenced entry reaches discovery,
	// startup reconciliation, claims or writes. No native runtime is built.
	task := NewTaskExecutor(nil, nil, ExecutorConfig{AdmissionGate: g})
	task.Start()
	task.checkAndExecuteTasks()
	task.executeTask(nil, Task{}, TaskProviderProfile{})
	task.Stop()
	step := NewStepExecutor(nil, nil, StepExecutorConfig{AdmissionGate: g})
	step.Start()
	step.checkAndExecuteSteps()
	step.processWorkflow(nil, "")
	step.executeStep(nil, nil, nil)
	step.Stop()
	scheduler := NewTaskScheduler(nil, SchedulerConfig{AdmissionGate: g})
	scheduler.Start()
	scheduler.checkScheduledTasks()
	scheduler.Stop()
	o := NewOrchestrator(nil, nil, nil, nil)
	o.SetAdmissionGate(g)
	if err := o.ExecuteTask(t.Context(), "", Task{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if err := o.ExecuteMission(t.Context(), "", ""); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	o.ExecuteTasksSequentially(t.Context(), "", []Task{{}})
}

func TestResetAdmissionReleasesFailedTaskAndStepClaims(t *testing.T) {
	g := &resetstate.WorkGate{}
	store := NewInMemoryStore()
	task := NewTaskExecutor(store, nil, ExecutorConfig{AdmissionGate: g})
	// An already in-progress task cannot be claimed again. The synchronous
	// failure used to leak its context and running slot; it must not leak a permit.
	task.executeTask(&Workspace{}, Task{ID: "duplicate", Status: TaskStatusInProgress}, TaskProviderProfile{})
	if len(task.runningTasks) != 0 {
		t.Fatal("failed claim retained running task")
	}
	step := NewStepExecutor(store, nil, StepExecutorConfig{AdmissionGate: g})
	step.executeStep(&Workspace{ID: "missing"}, &Workflow{ID: "missing"}, &WorkflowStep{ID: "missing"})
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatalf("failed claim leaked admission: %v", err)
	}
}
