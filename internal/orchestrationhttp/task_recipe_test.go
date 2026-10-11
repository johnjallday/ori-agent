package orchestrationhttp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// How a first task's record reads to the surfaces that show it. The mapping
// itself lives in (and is tested in) the personalassistant package, which this
// package cannot import; these are the same three readings of the record.
func readsFinished(task *workspace.Task) bool {
	state := task.CanonicalState()
	return (state == workspace.TicketStateReview || state == workspace.TicketStateDone) && strings.TrimSpace(task.Result) != ""
}

func readsFailed(task *workspace.Task) bool { return task.NeedsAttention() }

func readsRunning(task *workspace.Task) bool {
	return task.CanonicalState() == workspace.TicketStateInProgress && task.Status == workspace.TaskStatusInProgress && !task.NeedsAttention()
}

// firstTaskContext marks a task as a shown folder's first task.
func firstTaskContext() map[string]any {
	return map[string]any{"template_id": "folder-digest", "template_starter_task": true}
}

// recipeTaskFixture is a workspace holding one seeded first assessment: a task
// under a host recipe, added the only way one can be.
func recipeTaskFixture(t *testing.T, handler *scriptedTaskHandler, description string) (*TaskHandler, workspace.Store) {
	t.Helper()
	ws := &workspace.Workspace{
		ID: "ws-1", Name: "widget",
		AgentInstances: []workspace.AgentInstance{{ID: "dev-id", Name: "Development Assistant"}},
	}
	if _, err := ws.AddRecipeTask(workspace.Task{
		ID: "assess", WorkspaceID: "ws-1", To: "Development Assistant", Description: description,
		Status:  workspace.TaskStatusPending,
		Context: firstTaskContext(),
	}, workspace.FirstTaskRecipeSoftwareAssessment, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	return NewTaskHandler(store, nil, handler, workspace.NewEventBus(16, 64)), store
}

func runRecipeTask(t *testing.T, th *TaskHandler, store workspace.Store) (string, *workspace.Task, error) {
	t.Helper()
	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask("assess")
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := th.executeTaskWithDependencies(ws, task)
	saved, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	after, err := saved.GetTask("assess")
	if err != nil {
		t.Fatal(err)
	}
	return result, after, runErr
}

// A recipe task is one call. Its answer is kept as written: an ordinary task
// whose answer reads like a question is run again and then blocked, and one
// whose description names files is split into steps. Neither happens here.
func TestRecipeTaskRunsExactlyOnceWhateverItsWordingSuggests(t *testing.T) {
	// Phrases the ordinary loop treats as "the model needs the user": each would
	// cost two more model calls and end as a block.
	answer := "## Suggested first task\nRecommended next steps: add a health check. Would you like to proceed? Just say so.\n\nNext task: Add a health check."
	// A description the step heuristic would split: it names files and folders
	// and asks for a review and a summary.
	description := "Review the files and folders of this project and summarize what the files say"

	handler := &scriptedTaskHandler{result: answer}
	th, store := recipeTaskFixture(t, handler, description)
	result, after, err := runRecipeTask(t, th, store)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("the recipe was called %d times, want exactly once", handler.calls)
	}
	if result != answer || after.Result != answer {
		t.Fatalf("the answer was not kept as written: %q / %q", result, after.Result)
	}
	if len(after.ExecutionSteps) != 0 {
		t.Fatalf("a recipe task was split into %d steps", len(after.ExecutionSteps))
	}
	if after.Status == workspace.TaskStatusWaitingForChoice || after.Context["human_loop"] != nil {
		t.Fatalf("a recipe task ended waiting on the user: %s %v", after.Status, after.Context["human_loop"])
	}
	if !readsFinished(after) {
		t.Fatalf("the task does not read as finished: state %s, status %s", after.CanonicalState(), after.Status)
	}
	if workspace.FirstTaskRecipe(after) != workspace.FirstTaskRecipeSoftwareAssessment {
		t.Fatal("the run stripped the recipe marker")
	}

	// The same answer and description on an ordinary task do take the other path.
	ordinary := &scriptedTaskHandler{result: answer}
	plain, plainStore := recipeTaskFixture(t, ordinary, "Tell me about this project")
	ws, _ := plainStore.Get("ws-1")
	task, _ := ws.GetTask("assess")
	delete(task.Context, workspace.TaskContextFirstTaskRecipe)
	delete(task.Context, workspace.TaskContextFirstTaskRecipeVersion)
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatal(err)
	}
	_, _ = plain.executeTaskWithDependencies(ws, task)
	if ordinary.calls <= 1 {
		t.Fatalf("an ordinary task with that answer ran %d time(s); the test's premise no longer holds", ordinary.calls)
	}
}

// A recipe run that ends without a result is a failure with one plain sentence
// and a code, not a question to the user. That holds for a plain error, for a
// reason the user can repair, and for an empty answer.
func TestRecipeTaskFailureIsRecordedAsAFailureNeverAsAQuestion(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		result   string
		code     string
		sentence string
	}{
		"a provider error": {
			err: errors.New("exit status 1: stderr mentions /Users/someone/.codex/config.toml"), code: "run_failed",
			sentence: "The assessment did not finish. Nothing in the project was run or changed.",
		},
		"a reason the user can repair": {
			err: &workspace.TaskBlockedError{
				ReasonCode: "project_folder_unavailable", Reason: "The project folder is missing.",
				Repair: &workspace.TaskRepairAction{Code: "show_folder", Label: "Show the folder", URL: "/?panel=today&folder=show"},
			},
			code: "project_folder_unavailable", sentence: "The project folder is missing.",
		},
		"an empty answer": {
			result: "   ", code: "empty_result",
			sentence: "The model returned nothing to report. Nothing in the project was run or changed.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			handler := &scriptedTaskHandler{result: tc.result, err: tc.err}
			th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
			_, after, err := runRecipeTask(t, th, store)
			if err == nil {
				t.Fatal("a run with no result was reported as finished")
			}
			if handler.calls != 1 {
				t.Fatalf("a failed recipe run was called %d times; it must not be retried", handler.calls)
			}
			if after.Status != workspace.TaskStatusFailed || after.Error != tc.sentence || after.Result != "" {
				t.Fatalf("after = status %s, error %q, result %q", after.Status, after.Error, after.Result)
			}
			if after.Context[workspace.TaskContextFirstTaskFailure] != tc.code {
				t.Fatalf("failure code = %v, want %s", after.Context[workspace.TaskContextFirstTaskFailure], tc.code)
			}
			if after.Context["human_loop"] != nil {
				t.Fatalf("a recipe failure opened a question: %v", after.Context["human_loop"])
			}
			// What is stored for the user never carries the raw error.
			if strings.Contains(after.Error, "/Users/") || strings.Contains(after.Error, "exit status") {
				t.Fatalf("the raw error was stored: %q", after.Error)
			}
			if !readsFailed(after) || readsFinished(after) {
				t.Fatalf("the task does not read as failed: state %s, status %s", after.CanonicalState(), after.Status)
			}
		})
	}
}

// Trying again is the user's click: the task reads as running while it runs
// again, the old failure is cleared, and a success ends it as finished. A
// second start while one is running is refused.
func TestRecipeTaskCanBeTriedAgainByTheUser(t *testing.T) {
	handler := &scriptedTaskHandler{err: errors.New("the model did not answer")}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	if _, after, err := runRecipeTask(t, th, store); err == nil || after.Status != workspace.TaskStatusFailed {
		t.Fatalf("first run: %v, %s", err, after.Status)
	}

	// The second attempt observes its own task while it runs.
	var during *workspace.Task
	second := &observingTaskHandler{store: store, seen: &during, result: "## What it is\nA service.\n\nNext task: Add a health check."}
	th.taskHandler = second
	result, after, err := runRecipeTask(t, th, store)
	if err != nil || result == "" {
		t.Fatalf("second run: %q, %v", result, err)
	}
	if during == nil {
		t.Fatal("the second run never happened")
	}
	if during.Status != workspace.TaskStatusInProgress || during.Error != "" || during.Context[workspace.TaskContextFirstTaskFailure] != nil {
		t.Fatalf("while trying again the task still read as failed: status %s, error %q, code %v",
			during.Status, during.Error, during.Context[workspace.TaskContextFirstTaskFailure])
	}
	if !readsRunning(during) {
		t.Fatalf("while trying again the task does not read as running: state %s, status %s", during.CanonicalState(), during.Status)
	}
	if !readsFinished(after) || after.Error != "" || after.Context[workspace.TaskContextFirstTaskFailure] != nil {
		t.Fatalf("after the retry: state %s, error %q", after.CanonicalState(), after.Error)
	}
	if handler.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = %d then %d, want one each", handler.calls, second.calls)
	}
}

// A run the server lost (it restarted while the model was answering) is put
// back to pending at start-up with its ticket still In Progress. Nothing is
// running it, so it reads as not finished, and it can be tried again.
func TestRecipeTaskOrphanedByARestartReadsAsNotFinished(t *testing.T) {
	handler := &scriptedTaskHandler{result: "## What it is\nA service."}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	ws, _ := store.Get("ws-1")
	task, _ := ws.GetTask("assess")
	if err := task.StartTicketRun("", workspace.TicketActorUser, task.CreatedAt); err != nil {
		t.Fatalf("start: %v", err)
	}
	// What start-up reconciliation does to a task that was running.
	task.ForceStatus(workspace.TaskStatusPending)
	task.StartedAt = nil
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	// The record as start-up leaves it: In Progress on its ticket, pending in
	// its status, with nothing running it.
	if task.CanonicalState() != workspace.TicketStateInProgress || task.Status != workspace.TaskStatusPending {
		t.Fatalf("the orphaned record is not what start-up leaves: %s / %s", task.CanonicalState(), task.Status)
	}

	if _, after, err := runRecipeTask(t, th, store); err != nil || !readsFinished(after) {
		t.Fatalf("trying an orphaned run again: %v, state %s, status %s", err, after.CanonicalState(), after.Status)
	}
	if handler.calls != 1 {
		t.Fatalf("calls = %d", handler.calls)
	}
}

// observingTaskHandler records its own task as stored while it is running.
type observingTaskHandler struct {
	store  workspace.Store
	seen   **workspace.Task
	result string
	calls  int
}

func (o *observingTaskHandler) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	o.calls++
	if ws, err := o.store.Get(task.WorkspaceID); err == nil {
		if stored, err := ws.GetTask(task.ID); err == nil {
			copied := *stored
			*o.seen = &copied
		}
	}
	return o.result, nil
}
