package workspacerun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func learningBridgeFixture(t *testing.T, runs Store) (*TaskRunBridge, workspace.Task) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Practice"})
	task := workspace.Task{ID: "ticket", WorkspaceID: ws.ID, To: "Ori", Description: "A bounded draft", Status: workspace.TaskStatusAssigned}
	if err := ws.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: "A result"}))
	service := NewService(runs, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	return NewTaskRunBridge(runs, service, store), task
}

func TestLearningRunReservationPrecedesCanonicalCreation(t *testing.T) {
	runs := NewMemoryStore()
	bridge, task := learningBridgeFixture(t, runs)
	var reserved string
	bridge.SetRunReservation(func(ctx context.Context, got workspace.Task, id string) error {
		if got.ID != task.ID || id == "" {
			t.Fatal("reservation lost canonical Ticket identity")
		}
		if existing, err := runs.ListRuns(ctx, task.WorkspaceID); err != nil || len(existing) != 0 {
			t.Fatal("Run created before durable owner reservation")
		}
		reserved = id
		return nil
	})
	result, err := bridge.ExecuteTaskRun(t.Context(), "Ori", task)
	if err != nil || result.RunID == "" || result.RunID != reserved {
		t.Fatalf("reserved identity changed: %s %+v %v", reserved, result, err)
	}
	run, err := runs.GetRun(t.Context(), task.WorkspaceID, reserved)
	if err != nil || run.Scope.TargetTaskID != task.ID || run.Status != RunStatusSucceeded {
		t.Fatalf("exact canonical attempt: %+v %v", run, err)
	}
}

type failingRunCreate struct{ Store }

func (s failingRunCreate) CreateRun(context.Context, *Run) error {
	return errors.New("injected creation failure")
}

func TestLearningRunReservationUnknownOutcomeDoesNotAdoptLaterAttempt(t *testing.T) {
	runs := NewMemoryStore()
	bridge, task := learningBridgeFixture(t, failingRunCreate{runs})
	var reserved string
	claim := func(_ context.Context, _ workspace.Task, id string) error {
		if reserved != "" {
			return errors.New("the original attempt must be reconciled")
		}
		reserved = id
		return nil
	}
	bridge.SetRunReservation(claim)
	if _, err := bridge.ExecuteTaskRun(t.Context(), "Ori", task); err == nil || reserved == "" {
		t.Fatal("expected a reserved attempt followed by failed creation")
	}
	original := reserved
	// Simulate the transport/store becoming healthy. The owner's durable claim
	// must still refuse a fresh attempt; the bridge cannot bypass that refusal.
	bridge.service.store = runs
	bridge.store = runs
	if _, err := bridge.ExecuteTaskRun(t.Context(), "Ori", task); err == nil {
		t.Fatal("an unknown attempt was retried as a new execution")
	}
	if reserved != original {
		t.Fatal("a later attempt replaced the original reservation")
	}
	if got, err := runs.ListRuns(t.Context(), task.WorkspaceID); err != nil || len(got) != 0 {
		t.Fatal("refused reservation created a Run")
	}
}

func TestLearningHostRunIdentityCannotComeFromJSON(t *testing.T) {
	var req CreateRunRequest
	if err := json.Unmarshal([]byte(`{"HostRunID":"forged","host_run_id":"also-forged","profile_id":"general"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.HostRunID != "" {
		t.Fatal("a browser supplied a host-reserved Run identity")
	}
}
