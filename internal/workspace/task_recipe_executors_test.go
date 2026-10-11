package workspace

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// recipeContext marks a task literal as running under the assessment recipe,
// the way AddRecipeTask leaves a stored one.
func recipeContext() map[string]any {
	return map[string]any{TaskContextFirstTaskRecipe: FirstTaskRecipeSoftwareAssessment, TaskContextFirstTaskRecipeVersion: 1}
}

// The workspace orchestrator has its own way of running a task, reached from
// the workspace task route and from the Home assistant. When a run fails it can
// hand the work to a coordinator, which runs as an ordinary agent with that
// agent's tools, and record what the coordinator wrote as the task's result.
// That must never happen to a task under a host recipe: its report would be an
// agent's free-hand answer, written with tools the recipe forbids.
func TestOrchestratorNeverAdaptsARecipeTask(t *testing.T) {
	failure := &RecipeRunError{Code: "project_folder_unavailable", Sentence: "The project folder is missing."}
	run := func(t *testing.T, recipe bool) (*fakeAdapter, *Task, error) {
		t.Helper()
		store := newExecutorTestStore(t)
		ws := NewWorkspace(CreateWorkspaceParams{Name: "recipe-orchestrator", Agents: []string{"orchestrator", "Manager", "Writer"}})
		// The delegation loop's coordinator is the workspace's entry agent.
		for i := range ws.AgentInstances {
			ws.AgentInstances[i].EntryPoint = ws.AgentInstances[i].Name == "Manager"
		}
		task := Task{ID: "assess", WorkspaceID: ws.ID, To: "Writer", Description: "Assess this project", Status: TaskStatusPending}
		var seedErr error
		if recipe {
			// The only way a task comes to carry the marker.
			_, seedErr = ws.AddRecipeTask(task, FirstTaskRecipeSoftwareAssessment, 1)
			task.Context = recipeContext()
		} else {
			seedErr = ws.AddTask(task)
		}
		if seedErr != nil {
			t.Fatalf("seed: %v", seedErr)
		}
		if err := store.Save(ws); err != nil {
			t.Fatalf("save: %v", err)
		}
		adapter := &fakeAdapter{steps: []CoordinatorAdaptResult{{Resolved: true, DirectResult: "WRITTEN BY A COORDINATOR WITH TOOLS"}}}
		o := NewOrchestrator(store, nil, nil, nil)
		o.SetTaskHandler(&erroringTaskHandler{err: failure})
		o.SetDelegationLoop(NewDelegationLoop(store, &fakeExecutor{}, adapter, DelegationCaps{}))

		err := o.ExecuteTask(context.Background(), ws.ID, task)
		saved, getErr := store.Get(ws.ID)
		if getErr != nil {
			t.Fatalf("get: %v", getErr)
		}
		after, getErr := saved.GetTask("assess")
		if getErr != nil {
			t.Fatalf("task: %v", getErr)
		}
		return adapter, after, err
	}

	adapter, after, err := run(t, true)
	if adapter.calls != 0 {
		t.Fatalf("a recipe task was handed to the coordinator %d time(s)", adapter.calls)
	}
	if err == nil || after.Status != TaskStatusFailed || after.Result != "" {
		t.Fatalf("a failed recipe run => %v, status %s, result %q", err, after.Status, after.Result)
	}
	// What is stored is the recipe's own sentence.
	if after.Error != failure.Sentence {
		t.Fatalf("stored error = %q", after.Error)
	}

	// The premise: the same failure on an ordinary task is adapted.
	adapter, after, err = run(t, false)
	if adapter.calls == 0 {
		t.Fatal("an ordinary task was not adapted; this test's premise no longer holds")
	}
	if err != nil || after.Result != "WRITTEN BY A COORDINATOR WITH TOOLS" {
		t.Fatalf("an adapted ordinary task => %v, result %q", err, after.Result)
	}
}

// The background executor runs every assigned task it finds. A task under a
// host recipe runs when the user starts it and at no other time, so becoming
// assigned (to another agent, or again after its ticket was reopened) is not a
// start: nothing is read and no model is called.
func TestBackgroundExecutorNeverStartsARecipeTask(t *testing.T) {
	store := newExecutorTestStore(t)
	ws := newWorkspaceWithTasks(t, []Task{
		{ID: "assess", To: "agent-a", Status: TaskStatusAssigned, Context: recipeContext()},
		{ID: "ordinary", To: "agent-a", Status: TaskStatusAssigned},
	})
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := &fakeTaskHandler{}
	te := NewTaskExecutor(store, handler, ExecutorConfig{PollInterval: time.Hour, MaxConcurrent: 2})
	t.Cleanup(te.Stop)

	te.checkAndExecuteTasks()
	if status := waitForTaskStatus(t, store, ws.ID, "ordinary", time.Now().Add(2*time.Second)); status != TaskStatusCompleted {
		t.Fatalf("the ordinary task = %q, want completed", status)
	}
	// A second pass, as the poller would make.
	te.checkAndExecuteTasks()
	time.Sleep(50 * time.Millisecond)

	handler.mu.Lock()
	seen := handler.seenIDs["assess"]
	handler.mu.Unlock()
	if seen != 0 || atomic.LoadInt64(&handler.calls) != 1 {
		t.Fatalf("the recipe task was run %d time(s) by the background executor (calls=%d)", seen, atomic.LoadInt64(&handler.calls))
	}
	saved, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task, _ := saved.GetTask("assess"); task == nil || task.Status != TaskStatusAssigned || task.Result != "" {
		t.Fatalf("the recipe task was touched: %+v", task)
	}
}
