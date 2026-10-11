package orchestrationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
func recipeTaskFixture(t *testing.T, handler workspace.TaskHandler, description string) (*TaskHandler, workspace.Store) {
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
// and a code, not a question to the user. The task handler describes its own
// failures (a reason the user can repair, an empty answer); anything that
// reaches this executor undescribed, from around the handler, is recorded only
// as "did not finish", never in its own words.
func TestRecipeTaskFailureIsRecordedAsAFailureNeverAsAQuestion(t *testing.T) {
	const generic = "The assessment did not finish. Nothing in the project was run or changed."
	for name, tc := range map[string]struct {
		err      error
		result   string
		code     string
		sentence string
	}{
		"a reason the user can repair": {
			err:  &workspace.RecipeRunError{Code: "project_folder_unavailable", Sentence: "The project folder is missing."},
			code: "project_folder_unavailable", sentence: "The project folder is missing.",
		},
		"an empty answer, as the handler reports it": {
			err:  &workspace.RecipeRunError{Code: workspace.RecipeEmptyResult, Sentence: "The model returned nothing to report. Nothing in the project was run or changed."},
			code: "empty_result", sentence: "The model returned nothing to report. Nothing in the project was run or changed.",
		},
		"a described failure that was wrapped on the way": {
			err:  fmt.Errorf("run lifecycle: %w", &workspace.RecipeRunError{Code: workspace.RecipeTimedOut, Sentence: "The assessment ran out of time before the model answered. Nothing in the project was run or changed."}),
			code: "timed_out", sentence: "The assessment ran out of time before the model answered. Nothing in the project was run or changed.",
		},
		"an undescribed error from around the handler": {
			err: errors.New("exit status 1: stderr mentions /Users/someone/.codex/config.toml"), code: "run_failed", sentence: generic,
		},
		// A handler other than Ori's own might hand back a block. It is still a
		// failure here, and its words are not trusted either.
		"an undescribed block": {
			err:  &workspace.TaskBlockedError{ReasonCode: "needs_input", Reason: "Which folder did you mean? /Users/someone/secret", Question: "Pick one"},
			code: "run_failed", sentence: generic,
		},
		"an undescribed empty answer": {result: "   ", code: "run_failed", sentence: generic},
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
			if after.Context["human_loop"] != nil || after.Status == workspace.TaskStatusWaitingForChoice {
				t.Fatalf("a recipe failure opened a question: %s %v", after.Status, after.Context["human_loop"])
			}
			// What is stored for the user never carries the raw error.
			if strings.Contains(after.Error, "/Users/") || strings.Contains(after.Error, "exit status") || strings.Contains(after.Error, "Which folder") {
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

// recordingTaskHandler answers every task and remembers which ones it was asked
// to run.
type recordingTaskHandler struct {
	result string
	ran    []string
}

func (r *recordingTaskHandler) ExecuteTask(_ context.Context, _ string, task workspace.Task) (string, error) {
	r.ran = append(r.ran, task.ID)
	return r.result, nil
}

func addTask(t *testing.T, store workspace.Store, task workspace.Task) {
	t.Helper()
	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.AddTask(task); err != nil {
		t.Fatalf("add %s: %v", task.ID, err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
}

// changeAssessment edits the seeded assessment's stored record.
func changeAssessment(t *testing.T, store workspace.Store, change func(*workspace.Task)) {
	t.Helper()
	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask("assess")
	if err != nil {
		t.Fatal(err)
	}
	change(task)
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
}

func storedTask(t *testing.T, store workspace.Store, id string) *workspace.Task {
	t.Helper()
	ws, err := store.Get("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// Another task that lists the assessment as an input does not start it. An
// ordinary input that has not run is run automatically, up to three times; an
// assessment runs when the user starts it and at no other time, so the task
// that depends on it is told so instead.
func TestAnotherTasksInputNeverStartsARecipeTask(t *testing.T) {
	handler := &recordingTaskHandler{result: "## What it is\nA service.\n\nNext task: Add a health check."}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	addTask(t, store, workspace.Task{
		ID: "follow-up", WorkspaceID: "ws-1", To: "Development Assistant", Description: "Summarize the assessment",
		Status: workspace.TaskStatusPending, InputTaskIDs: []string{"assess"},
		// What a client can set to ask for more attempts of an ordinary input.
		Context: map[string]any{"max_attempts": 3},
	})

	ws, _ := store.Get("ws-1")
	followUp, _ := ws.GetTask("follow-up")
	_, err := th.executeTaskWithDependencies(ws, followUp)
	if err == nil || !strings.Contains(err.Error(), "first assessment") {
		t.Fatalf("a task depending on an unstarted assessment => %v", err)
	}
	if len(handler.ran) != 0 {
		t.Fatalf("something was run: %v", handler.ran)
	}
	if after := storedTask(t, store, "assess"); after.Status != workspace.TaskStatusPending || after.Result != "" {
		t.Fatalf("the assessment was touched: status %s, result %q", after.Status, after.Result)
	}

	// A failed assessment is not tried again by a dependant either.
	changeAssessment(t, store, func(task *workspace.Task) { task.ForceStatus(workspace.TaskStatusFailed) })
	ws, _ = store.Get("ws-1")
	followUp, _ = ws.GetTask("follow-up")
	if _, err := th.executeTaskWithDependencies(ws, followUp); err == nil || len(handler.ran) != 0 {
		t.Fatalf("a task depending on a failed assessment => %v, ran %v", err, handler.ran)
	}

	// Once the user has run it, its report is an input like any other.
	changeAssessment(t, store, func(task *workspace.Task) {
		task.ForceStatus(workspace.TaskStatusCompleted)
		task.Result = "## What it is\nA service."
	})
	ws, _ = store.Get("ws-1")
	followUp, _ = ws.GetTask("follow-up")
	if _, err := th.executeTaskWithDependencies(ws, followUp); err != nil {
		t.Fatalf("a task depending on a finished assessment: %v", err)
	}
	if len(handler.ran) == 0 || handler.ran[0] != "follow-up" {
		t.Fatalf("ran = %v, want the dependant only", handler.ran)
	}
	for _, id := range handler.ran {
		if id == "assess" {
			t.Fatalf("the finished assessment was run again as an input: %v", handler.ran)
		}
	}
}

// Starting the assessment starts the assessment. Tasks listed as its inputs
// are ordinary agent tasks with tools; they are not run for it.
func TestRecipeTaskRunsNoneOfItsOwnInputs(t *testing.T) {
	handler := &recordingTaskHandler{result: "## What it is\nA service.\n\nNext task: Add a health check."}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	addTask(t, store, workspace.Task{
		ID: "with-tools", WorkspaceID: "ws-1", To: "Development Assistant", Description: "Run the test suite", Status: workspace.TaskStatusPending,
	})
	changeAssessment(t, store, func(task *workspace.Task) { task.InputTaskIDs = []string{"with-tools"} })

	if _, after, err := runRecipeTask(t, th, store); err != nil || !readsFinished(after) {
		t.Fatalf("the assessment: %v, state %s", err, after.CanonicalState())
	}
	if len(handler.ran) != 1 || handler.ran[0] != "assess" {
		t.Fatalf("ran = %v, want the assessment alone", handler.ran)
	}
	if other := storedTask(t, store, "with-tools"); other.Status != workspace.TaskStatusPending || other.Result != "" {
		t.Fatalf("the input task was run: status %s", other.Status)
	}
}

// A sub-task hung on the assessment is an ordinary agent task. Running the
// assessment does not run it, and its output is never recorded as the report.
func TestRecipeTaskIsNeverRunAsASequenceOfItsSubTasks(t *testing.T) {
	handler := &recordingTaskHandler{result: "## What it is\nA service.\n\nNext task: Add a health check."}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	addTask(t, store, workspace.Task{
		ID: "child", WorkspaceID: "ws-1", To: "Development Assistant", Description: "Run the test suite",
		Status: workspace.TaskStatusPending, ParentTaskID: "assess",
	})

	// The sequence itself refuses a recipe parent.
	th.executeParentTaskSequence("ws-1", "assess")
	if len(handler.ran) != 0 {
		t.Fatalf("the sub-task sequence ran for a recipe task: %v", handler.ran)
	}
	if after := storedTask(t, store, "assess"); after.Status != workspace.TaskStatusPending || after.Result != "" {
		t.Fatalf("the sequence touched the assessment: status %s, result %q", after.Status, after.Result)
	}

	// The manual run, which is what Try again sends, runs the recipe.
	rec := httptest.NewRecorder()
	th.ExecuteTaskHandler(rec, httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/execute", strings.NewReader(`{"task_id":"assess"}`)))
	if rec.Code != http.StatusAccepted || strings.Contains(rec.Body.String(), "sequence") {
		t.Fatalf("manual run => %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !readsFinished(storedTask(t, store, "assess")) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := storedTask(t, store, "assess"); !readsFinished(after) || !strings.HasPrefix(after.Result, "## What it is") {
		t.Fatalf("the assessment after a manual run: state %s, result %q", after.CanonicalState(), after.Result)
	}
	if len(handler.ran) != 1 || handler.ran[0] != "assess" {
		t.Fatalf("ran = %v, want the assessment alone", handler.ran)
	}
	if child := storedTask(t, store, "child"); child.Status != workspace.TaskStatusPending {
		t.Fatalf("the sub-task was run: status %s", child.Status)
	}
}

// The attempt loop an ordinary input task goes through gives a recipe task one
// call, whatever number of attempts its record asks for.
func TestTheAttemptLoopGivesARecipeTaskOneCall(t *testing.T) {
	handler := &scriptedTaskHandler{err: &workspace.RecipeRunError{Code: workspace.RecipeTimedOut, Sentence: "The assessment ran out of time."}}
	th, store := recipeTaskFixture(t, handler, "Assess this project and suggest a first task")
	changeAssessment(t, store, func(task *workspace.Task) { task.Context["max_attempts"] = 3 })
	ws, _ := store.Get("ws-1")
	task, _ := ws.GetTask("assess")
	if _, err := th.executeTaskIteratively(context.Background(), ws, task, *task, false); err == nil {
		t.Fatal("a failed run was reported as finished")
	}
	if handler.calls != 1 {
		t.Fatalf("the attempt loop called the recipe %d times", handler.calls)
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
