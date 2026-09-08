package workspaceplan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type resetPlanFinalStore struct {
	Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
}

func (s *resetPlanFinalStore) SetPlanStatus(ctx context.Context, workspaceID, planID string, to Status, activity Activity) error {
	if to == StatusCompleted {
		s.once.Do(func() { close(s.started); <-s.release })
		if s.fail {
			return errors.New("owned final-plan failure")
		}
	}
	return s.Store.SetPlanStatus(ctx, workspaceID, planID, to, activity)
}

func waitResetPlan[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("automatic plan missed owned checkpoint")
		var zero T
		return zero
	}
}

func TestResetAdmissionAutomaticPlanTracksDispatchAndFinalPersistence(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "save_failure"}[fail], func(t *testing.T) {
			ctx := context.Background()
			service, _, auto, writer, dispatcher, plan := autoExecutable(t, ctx, autoContent())
			finalStore := &resetPlanFinalStore{Store: service.store, started: make(chan struct{}), release: make(chan struct{}), fail: fail}
			service.store = finalStore
			gate := &resetstate.WorkGate{}
			auto.SetAdmissionGate(gate)
			dispatchStarted := make(chan struct{})
			dispatchRelease := make(chan struct{})
			var firstDispatch sync.Once
			dispatcher.onDispatch = func(taskID string) {
				firstDispatch.Do(func() { close(dispatchStarted); <-dispatchRelease })
				_ = (&fakeMutator{writer: writer}).MutateTask("ws-1", taskID, func(task *workspace.Task) error {
					task.Status = workspace.TaskStatusCompleted
					return nil
				})
			}
			finishDispatch := sync.OnceFunc(func() { close(dispatchRelease) })
			finishPlan := sync.OnceFunc(func() { close(finalStore.release) })
			t.Cleanup(func() { finishDispatch(); finishPlan(); auto.Stop() })

			result, err := auto.Launch(ctx, "ws-1", plan.ID, "fixture")
			if err != nil || !result.Launched {
				t.Fatalf("launch = %+v, %v", result, err)
			}
			waitResetPlan(t, dispatchStarted)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("dispatch not tracked: %v", err)
			}
			if auto.base.Err() != nil {
				t.Fatal("reset cancelled automatic plan")
			}
			release, err := gate.Enter()
			if err != nil {
				t.Fatalf("busy refusal fenced ordinary work: %v", err)
			}
			release()
			finishDispatch()
			waitResetPlan(t, finalStore.started)
			if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
				t.Fatalf("final plan write not tracked: %v", err)
			}
			finishPlan()
			auto.Wait()
			if err := gate.TryFence(t.Context()); err != nil {
				t.Fatal(err)
			}
			persisted, err := service.Get(ctx, "ws-1", plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fail && persisted.Status == StatusCompleted {
				t.Fatal("failed completion was claimed persisted")
			}
			if !fail && persisted.Status != StatusCompleted {
				t.Fatalf("status = %s", persisted.Status)
			}
		})
	}
}

func TestResetAdmissionAutomaticPlanRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	auto := NewAutoRunner(nil)
	auto.SetAdmissionGate(gate)
	if _, err := auto.Launch(t.Context(), "workspace", "plan", "actor"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	auto.Stop()
}
