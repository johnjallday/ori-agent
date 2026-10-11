package orchestrationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// recordingBinder is the assistant-operation service as the create handler
// sees it. It answers the claim as scripted and records every call.
type recordingBinder struct {
	mu       sync.Mutex
	claim    func(assistantops.TaskCreate) (assistantops.CreateClaim, error)
	claims   []assistantops.TaskCreate
	confirms []string
	releases []string
	// tasksAtConfirm is how many tasks the workspace held when the create was
	// confirmed, which proves confirmation follows the write.
	store          workspace.Store
	tasksAtConfirm int
}

func (b *recordingBinder) ClaimTaskCreate(_ context.Context, req assistantops.TaskCreate) (assistantops.CreateClaim, error) {
	b.mu.Lock()
	b.claims = append(b.claims, req)
	claim := b.claim
	b.mu.Unlock()
	if claim == nil {
		return assistantops.CreateClaim{TaskID: req.TaskID, AgentName: req.AgentName, AgentNodeID: req.AgentNodeID}, nil
	}
	return claim(req)
}

func (b *recordingBinder) ConfirmTaskCreate(_ context.Context, operationID, taskID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.confirms = append(b.confirms, operationID+"/"+taskID)
	if b.store != nil {
		if ws, err := b.store.Get("ws-ops"); err == nil {
			b.tasksAtConfirm = len(ws.Tasks)
		}
	}
	return nil
}

func (b *recordingBinder) ReleaseTaskCreate(_ context.Context, operationID, taskID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releases = append(b.releases, operationID+"/"+taskID)
	return nil
}

// operationWorkspace is a two-agent workspace, so an unassigned task has no
// single coordinator to fall back on and any substitution would be visible.
func operationWorkspace(t *testing.T, store workspace.Store) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album"})
	ws.ID = "ws-ops"
	ws.AgentInstances = []workspace.AgentInstance{
		{Name: "Mixer", NodeID: "Mixer-node-1"},
		{Name: "Engineer", NodeID: "Engineer-node-1"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

func postOperationCreate(t *testing.T, store workspace.Store, binder TaskOperationBinder, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := &TaskHandler{workspaceStore: store, communicator: agentcomm.NewCommunicator(store)}
	if binder != nil {
		handler.SetTaskOperationBinder(binder)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.TasksHandler(rec, req)
	return rec
}

func workspaceTasks(t *testing.T, store workspace.Store) []workspace.Task {
	t.Helper()
	ws, err := store.Get("ws-ops")
	if err != nil {
		t.Fatal(err)
	}
	return ws.Tasks
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Code
}

const operationCreateBody = `{"workspace_id":"ws-ops","description":"Mix the chorus","details":"Two passes.",
	"to":"mixer","assigned_node_id":"Mixer-node-1","assistant_operation_id":"op-1","assistant_draft_revision":3}`

func TestCreateTask_OrdinaryCreateNeverTouchesTheBinder(t *testing.T) {
	store := workspace.NewInMemoryStore()
	operationWorkspace(t, store)
	binder := &recordingBinder{}
	rec := postOperationCreate(t, store, binder, `{"workspace_id":"ws-ops","description":"Mix the chorus","to":"Mixer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	tasks := workspaceTasks(t, store)
	if len(tasks) != 1 || tasks[0].AssistantOperationID != "" || tasks[0].AssignmentMode != workspace.TaskAssignmentModeManual {
		t.Fatalf("ordinary create changed: %+v", tasks)
	}
	if len(binder.claims)+len(binder.confirms)+len(binder.releases) != 0 {
		t.Fatalf("an ordinary create must not involve operations: %+v", binder)
	}
}

func TestCreateTask_BindsTheReviewedSaveToItsOperation(t *testing.T) {
	store := workspace.NewInMemoryStore()
	operationWorkspace(t, store)
	binder := &recordingBinder{store: store, claim: func(req assistantops.TaskCreate) (assistantops.CreateClaim, error) {
		// The service answers with the roster's own instance.
		return assistantops.CreateClaim{TaskID: req.TaskID, AgentName: "Mixer", AgentNodeID: "Mixer-node-1"}, nil
	}}
	rec := postOperationCreate(t, store, binder, operationCreateBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Task workspace.Task `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	tasks := workspaceTasks(t, store)
	if len(tasks) != 1 {
		t.Fatalf("tasks: %+v", tasks)
	}
	saved := tasks[0]
	if saved.ID != resp.Task.ID || saved.AssistantOperationID != "op-1" || resp.Task.AssistantOperationID != "op-1" {
		t.Fatalf("the task must carry its operation: %+v", saved)
	}
	if saved.To != "Mixer" || saved.AssignedNodeID != "Mixer-node-1" {
		t.Fatalf("the assignee is the roster's own instance: %q %q", saved.To, saved.AssignedNodeID)
	}
	// The human reviewed and saved it: the assignment is theirs.
	if saved.AssignmentMode != workspace.TaskAssignmentModeManual || saved.Status != workspace.TaskStatusPending || saved.CurrentRunID != "" || saved.StartedAt != nil {
		t.Fatalf("a saved task is a waiting, manually assigned task: %+v", saved)
	}
	if len(binder.claims) != 1 {
		t.Fatalf("claims: %+v", binder.claims)
	}
	claim := binder.claims[0]
	if claim.OperationID != "op-1" || claim.DraftRevision != 3 || claim.WorkspaceID != "ws-ops" || claim.AgentName != "mixer" ||
		claim.AgentNodeID != "Mixer-node-1" || claim.TaskID != saved.ID {
		t.Fatalf("the claim must carry the submitted values and the pre-assigned task: %+v", claim)
	}
	if len(binder.confirms) != 1 || binder.confirms[0] != "op-1/"+saved.ID || len(binder.releases) != 0 {
		t.Fatalf("confirms=%v releases=%v", binder.confirms, binder.releases)
	}
	if binder.tasksAtConfirm != 1 {
		t.Fatalf("the create was confirmed with %d tasks written", binder.tasksAtConfirm)
	}
}

func TestCreateTask_ARepeatedSaveReturnsTheFirstTask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	operationWorkspace(t, store)
	first := &recordingBinder{}
	if rec := postOperationCreate(t, store, first, operationCreateBody); rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := workspaceTasks(t, store)[0]

	repeat := &recordingBinder{claim: func(assistantops.TaskCreate) (assistantops.CreateClaim, error) {
		return assistantops.CreateClaim{TaskID: created.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-1", Existing: true}, nil
	}}
	rec := postOperationCreate(t, store, repeat, operationCreateBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success        bool           `json:"success"`
		AlreadyCreated bool           `json:"already_created"`
		Task           workspace.Task `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || !resp.AlreadyCreated || resp.Task.ID != created.ID {
		t.Fatalf("a repeat must return the first task: %+v", resp)
	}
	if tasks := workspaceTasks(t, store); len(tasks) != 1 {
		t.Fatalf("a repeat created another task: %d", len(tasks))
	}
	if len(repeat.confirms)+len(repeat.releases) != 0 {
		t.Fatalf("a repeat reserved nothing, so it settles nothing: %+v", repeat)
	}

	// The first task was deleted since: the operation is not given another.
	gone := &recordingBinder{claim: func(assistantops.TaskCreate) (assistantops.CreateClaim, error) {
		return assistantops.CreateClaim{TaskID: "deleted-task", Existing: true}, nil
	}}
	rec = postOperationCreate(t, store, gone, operationCreateBody)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "operation_ended" || len(workspaceTasks(t, store)) != 1 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateTask_ARefusedBindingWritesNothing(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"assignee not in the roster": {fmt.Errorf("%w: Ghost", assistantops.ErrAssignee), http.StatusConflict, "operation_assignee"},
		"another save in flight":     {assistantops.ErrCreateInFlight, http.StatusConflict, "operation_create_in_flight"},
		"stale draft":                {fmt.Errorf("%w: changed", assistantops.ErrConflict), http.StatusConflict, "operation_conflict"},
		"expired":                    {assistantops.ErrExpired, http.StatusGone, "operation_expired"},
		"invalidated":                {assistantops.ErrInvalidated, http.StatusGone, "operation_invalidated"},
		"not this user's":            {assistantops.ErrNotFound, http.StatusNotFound, "operation_not_found"},
		"dismissed":                  {assistantops.ErrTransition, http.StatusConflict, "operation_ended"},
		"mismatched":                 {fmt.Errorf("%w: other workspace", assistantops.ErrInvalidCommand), http.StatusBadRequest, "operation_invalid"},
		"could not check":            {assistantops.ErrUnavailable, http.StatusServiceUnavailable, "operation_unavailable"},
		"storage error":              {errors.New("disk I/O error at /Users/x/ops.db"), http.StatusServiceUnavailable, "operation_unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := workspace.NewInMemoryStore()
			operationWorkspace(t, store)
			binder := &recordingBinder{claim: func(assistantops.TaskCreate) (assistantops.CreateClaim, error) {
				return assistantops.CreateClaim{}, tc.err
			}}
			// The form names an agent that is not in this workspace at all.
			rec := postOperationCreate(t, store, binder, `{"workspace_id":"ws-ops","description":"Mix","to":"Ghost",
				"assistant_operation_id":"op-1","assistant_draft_revision":1}`)
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "/Users/") || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("refusal leaked or was cacheable: %s", rec.Body.String())
			}
			ws, err := store.Get("ws-ops")
			if err != nil {
				t.Fatal(err)
			}
			if len(ws.Tasks) != 0 {
				t.Fatalf("a refused save wrote a task: %+v", ws.Tasks)
			}
			// Neither added to the workspace nor replaced by another agent.
			if ws.HasAgent("Ghost") || len(ws.AgentInstances) != 2 {
				t.Fatalf("a refused assignee was added: %+v", ws.AgentInstances)
			}
			if len(binder.confirms)+len(binder.releases) != 0 {
				t.Fatalf("nothing was reserved, so nothing is settled: %+v", binder)
			}
		})
	}
}

func TestCreateTask_AnOperationSaveIsNeverAnOrdinaryCreateByAccident(t *testing.T) {
	t.Run("no binder wired", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		operationWorkspace(t, store)
		rec := postOperationCreate(t, store, nil, operationCreateBody)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "operation_unavailable" || len(workspaceTasks(t, store)) != 0 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	notPlain := map[string]string{
		"schedule": `"schedule_enabled":true,"schedule":{"type":"interval","interval":"1h"}`,
		"parent":   `"parent_task_id":"parent-1"`,
		"inputs":   `"input_task_ids":["other-task"]`,
	}
	for name, extra := range notPlain {
		t.Run("with a "+name, func(t *testing.T) {
			store := workspace.NewInMemoryStore()
			operationWorkspace(t, store)
			binder := &recordingBinder{}
			rec := postOperationCreate(t, store, binder, `{"workspace_id":"ws-ops","description":"Mix","to":"Mixer",
				"assistant_operation_id":"op-1","assistant_draft_revision":1,`+extra+`}`)
			if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "operation_not_plain" {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(binder.claims) != 0 || len(workspaceTasks(t, store)) != 0 {
				t.Fatalf("nothing may be reserved or written: %+v", binder.claims)
			}
		})
	}

	t.Run("ordinary validation still comes first", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		operationWorkspace(t, store)
		binder := &recordingBinder{}
		for _, body := range []string{
			`{"workspace_id":"ws-ops","to":"Mixer","assistant_operation_id":"op-1","assistant_draft_revision":1}`,
			`{"workspace_id":"ws-ops","description":"Mix","to":"Mixer","reference_url":"javascript:alert(1)","assistant_operation_id":"op-1","assistant_draft_revision":1}`,
			`{"workspace_id":"ws-missing","description":"Mix","to":"Mixer","assistant_operation_id":"op-1","assistant_draft_revision":1}`,
		} {
			rec := postOperationCreate(t, store, binder, body)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		}
		if len(binder.claims) != 0 || len(workspaceTasks(t, store)) != 0 {
			t.Fatalf("an invalid request reached the operation: %+v", binder.claims)
		}
	})
}

// failingSaveStore refuses the workspace save, as a full disk would.
type failingSaveStore struct {
	workspace.Store
	fail bool
}

func (s *failingSaveStore) Save(ws *workspace.Workspace) error {
	if s.fail {
		return errors.New("disk full")
	}
	return s.Store.Save(ws)
}

func TestCreateTask_AFailedSaveGivesTheReservationBack(t *testing.T) {
	base := workspace.NewInMemoryStore()
	operationWorkspace(t, base)
	store := &failingSaveStore{Store: base, fail: true}
	binder := &recordingBinder{}
	rec := postOperationCreate(t, store, binder, operationCreateBody)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(binder.claims) != 1 || len(binder.confirms) != 0 || len(binder.releases) != 1 ||
		binder.releases[0] != "op-1/"+binder.claims[0].TaskID {
		t.Fatalf("claims=%v confirms=%v releases=%v", binder.claims, binder.confirms, binder.releases)
	}
	if len(workspaceTasks(t, base)) != 0 {
		t.Fatal("a failed save left a task behind")
	}
	// The same reviewed form saves again once the disk recovers.
	store.fail = false
	if rec := postOperationCreate(t, store, binder, operationCreateBody); rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(binder.confirms) != 1 || len(binder.releases) != 1 || len(workspaceTasks(t, base)) != 1 {
		t.Fatalf("confirms=%v releases=%v", binder.confirms, binder.releases)
	}
}

func TestCreateTask_TheOutcomeIsRecordedEvenIfTheBrowserHungUp(t *testing.T) {
	store := workspace.NewInMemoryStore()
	operationWorkspace(t, store)
	var confirmCtxErr error
	binder := &contextBinder{recordingBinder: &recordingBinder{}, onConfirm: func(ctx context.Context) { confirmCtxErr = ctx.Err() }}
	handler := &TaskHandler{workspaceStore: store, communicator: agentcomm.NewCommunicator(store)}
	handler.SetTaskOperationBinder(binder)
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // the browser went away before the server answered
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(operationCreateBody)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	handler.TasksHandler(httptest.NewRecorder(), req)
	if len(binder.confirms) != 1 || confirmCtxErr != nil {
		t.Fatalf("confirm must run on a live context: confirms=%v err=%v", binder.confirms, confirmCtxErr)
	}
}

type contextBinder struct {
	*recordingBinder
	onConfirm func(context.Context)
}

func (b *contextBinder) ConfirmTaskCreate(ctx context.Context, operationID, taskID string) error {
	b.onConfirm(ctx)
	return b.recordingBinder.ConfirmTaskCreate(ctx, operationID, taskID)
}

// reviewedTaskFixture is a workspace with one saved, waiting task and an
// executor that records what it ran.
func reviewedTaskFixture(t *testing.T, change func(*workspace.Task)) (*TaskHandler, workspace.Store, *countingExecutor) {
	t.Helper()
	ws := &workspace.Workspace{ID: "ws-ops", Name: "Album", AgentInstances: []workspace.AgentInstance{{ID: "mixer-id", Name: "Mixer", NodeID: "Mixer-node-1"}}}
	task := workspace.Task{ID: "task-1", WorkspaceID: "ws-ops", To: "Mixer", AssignedNodeID: "Mixer-node-1",
		Description: "Mix the chorus", Status: workspace.TaskStatusPending, AssistantOperationID: "op-1"}
	if err := ws.AddTasks([]workspace.Task{task}); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		stored, err := ws.GetTask("task-1")
		if err != nil {
			t.Fatal(err)
		}
		change(stored)
		if err := ws.UpdateTask(*stored); err != nil {
			t.Fatal(err)
		}
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	executor := &countingExecutor{done: make(chan struct{}, 4)}
	return NewTaskHandler(store, nil, executor, workspace.NewEventBus(16, 64)), store, executor
}

type countingExecutor struct {
	mu    sync.Mutex
	calls int
	done  chan struct{}
}

func (e *countingExecutor) ExecuteTask(context.Context, string, workspace.Task) (string, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	e.done <- struct{}{}
	return "Mixed.", nil
}

func (e *countingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func waitForTask(t *testing.T, store workspace.Store, done func(*workspace.Task) bool) *workspace.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ws, err := store.Get("ws-ops")
		if err == nil {
			if task, taskErr := ws.GetTask("task-1"); taskErr == nil && done(task) {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the task did not reach the expected state")
	return nil
}

func TestStartReviewedTask_RunsAWaitingTaskOnceThroughTheExecutionPath(t *testing.T) {
	handler, store, executor := reviewedTaskFixture(t, nil)
	host := operationTaskHost{handler: &Handler{taskHandlerSub: handler}}

	before, err := host.Task(t.Context(), "ws-ops", "task-1")
	if err != nil || !before.Startable || before.Ran || before.RunID != "" || before.OperationID != "op-1" ||
		before.AgentName != "Mixer" || before.AgentNodeID != "Mixer-node-1" {
		t.Fatalf("a saved task is waiting: %+v %v", before, err)
	}
	if err := host.Start(t.Context(), "ws-ops", "task-1"); err != nil {
		t.Fatal(err)
	}
	finished := waitForTask(t, store, func(task *workspace.Task) bool { return task.Status == workspace.TaskStatusCompleted })
	if executor.count() != 1 || finished.Result != "Mixed." || finished.ExecutionMode != workspace.TaskExecutionModeAuto {
		t.Fatalf("calls=%d task=%+v", executor.count(), finished)
	}
	after, err := host.Task(t.Context(), "ws-ops", "task-1")
	if err != nil || !after.Ran || after.Startable {
		t.Fatalf("a finished task reports that it ran: %+v %v", after, err)
	}

	// The same call again, however late, is never a rerun.
	var refusal *assistantops.StartRefusal
	if err := host.Start(t.Context(), "ws-ops", "task-1"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "already run") {
		t.Fatalf("got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	still := waitForTask(t, store, func(task *workspace.Task) bool { return true })
	if executor.count() != 1 || still.Status != workspace.TaskStatusCompleted || still.Result != "Mixed." {
		t.Fatalf("a completed task was replayed: calls=%d %+v", executor.count(), still)
	}
}

func TestStartReviewedTask_RefusesWhatIsNotAFirstStart(t *testing.T) {
	started := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		change func(*workspace.Task)
		want   string
	}{
		"in progress":     {func(task *workspace.Task) { task.Status = workspace.TaskStatusInProgress }, "already run"},
		"has a run":       {func(task *workspace.Task) { task.CurrentRunID = "run-1" }, "already run"},
		"started before":  {func(task *workspace.Task) { task.StartedAt = &started }, "already run"},
		"completed":       {func(task *workspace.Task) { task.Status, task.Result = workspace.TaskStatusCompleted, "Done." }, "already run"},
		"failed":          {func(task *workspace.Task) { task.Status, task.Error = workspace.TaskStatusFailed, "boom" }, "already run"},
		"has history":     {func(task *workspace.Task) { workspace.RecordTaskExecution(task, "success", "ok", started, 5) }, "already run"},
		"cancelled":       {func(task *workspace.Task) { task.Status = workspace.TaskStatusCancelled }, "cancelled"},
		"backlog":         {func(task *workspace.Task) { task.Status = workspace.TaskStatusBacklog }, "backlog"},
		"unassigned":      {func(task *workspace.Task) { task.To, task.AssignedNodeID = "", "" }, "Assign the task"},
		"word unassigned": {func(task *workspace.Task) { task.To = "unassigned" }, "Assign the task"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler, store, executor := reviewedTaskFixture(t, tc.change)
			before, _ := store.Get("ws-ops")
			snapshot, _ := before.GetTask("task-1")
			var refusal *assistantops.StartRefusal
			err := handler.StartReviewedTask(t.Context(), "ws-ops", "task-1")
			if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, tc.want) {
				t.Fatalf("got %v, want a refusal mentioning %q", err, tc.want)
			}
			time.Sleep(30 * time.Millisecond)
			after, _ := store.Get("ws-ops")
			task, _ := after.GetTask("task-1")
			if executor.count() != 0 || task.Status != snapshot.Status || task.Result != snapshot.Result || task.Error != snapshot.Error || task.ExecutionMode != snapshot.ExecutionMode {
				t.Fatalf("a refused start changed the task: calls=%d %+v", executor.count(), task)
			}
		})
	}

	t.Run("a task with steps of its own", func(t *testing.T) {
		handler, store, executor := reviewedTaskFixture(t, nil)
		ws, _ := store.Get("ws-ops")
		if err := ws.AddTask(workspace.Task{ID: "step-1", WorkspaceID: "ws-ops", ParentTaskID: "task-1", SubtaskIndex: 1, To: "Mixer", Description: "Step", Status: workspace.TaskStatusPending}); err != nil {
			t.Fatal(err)
		}
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
		var refusal *assistantops.StartRefusal
		if err := handler.StartReviewedTask(t.Context(), "ws-ops", "task-1"); !errors.As(err, &refusal) || executor.count() != 0 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing task", func(t *testing.T) {
		handler, _, _ := reviewedTaskFixture(t, nil)
		if err := handler.StartReviewedTask(t.Context(), "ws-ops", "task-9"); !errors.Is(err, assistantops.ErrTaskMissing) {
			t.Fatalf("got %v", err)
		}
		host := operationTaskHost{handler: &Handler{taskHandlerSub: handler}}
		if _, err := host.Task(t.Context(), "ws-ops", "task-9"); !errors.Is(err, assistantops.ErrTaskMissing) {
			t.Fatalf("got %v", err)
		}
		// An unreadable workspace is not a missing task.
		if _, err := host.Task(t.Context(), "ws-nowhere", "task-1"); err == nil || errors.Is(err, assistantops.ErrTaskMissing) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("no executor", func(t *testing.T) {
		_, store, _ := reviewedTaskFixture(t, nil)
		handler := NewTaskHandler(store, nil, nil, workspace.NewEventBus(16, 64))
		var refusal *assistantops.StartRefusal
		if err := handler.StartReviewedTask(t.Context(), "ws-ops", "task-1"); !errors.As(err, &refusal) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("during a reset", func(t *testing.T) {
		handler, store, executor := reviewedTaskFixture(t, nil)
		gate := &resetstate.WorkGate{}
		handler.SetAdmissionGate(gate)
		if err := gate.TryFence(t.Context()); err != nil {
			t.Fatal(err)
		}
		var refusal *assistantops.StartRefusal
		if err := handler.StartReviewedTask(t.Context(), "ws-ops", "task-1"); !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "reset") {
			t.Fatalf("got %v", err)
		}
		ws, _ := store.Get("ws-ops")
		task, _ := ws.GetTask("task-1")
		if executor.count() != 0 || task.Status != workspace.TaskStatusPending {
			t.Fatalf("a fenced start ran: %+v", task)
		}
	})
}

func TestOperationTaskState_TheFirstRunIsTheOperationsRun(t *testing.T) {
	first := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	task := &workspace.Task{ID: "task-1", Status: workspace.TaskStatusInProgress, CurrentRunID: "run-1"}
	if state := operationTaskState(task); !state.Ran || state.RunID != "run-1" || state.Startable {
		t.Fatalf("running: %+v", state)
	}
	// It finished, then the user ran it again: the oldest record is the first run.
	task.ExecutionHistory = []workspace.TaskExecution{
		{TaskID: "task-1", RunID: "run-1", ExecutedAt: first, Status: "success"},
		{TaskID: "task-1", RunID: "run-2", ExecutedAt: first.Add(time.Hour), Status: "success"},
	}
	task.CurrentRunID, task.Status = "run-3", workspace.TaskStatusInProgress
	if state := operationTaskState(task); state.RunID != "run-1" {
		t.Fatalf("a rerun replaced the first run: %+v", state)
	}
	waiting := &workspace.Task{ID: "task-2", Status: workspace.TaskStatusAssigned}
	if state := operationTaskState(waiting); state.Ran || !state.Startable || state.RunID != "" {
		t.Fatalf("waiting: %+v", state)
	}
}

// The outcome an operation reports is resolved from what the task itself
// recorded through the production recorder.
func TestOperationTaskState_CarriesTheTasksOwnRunRecords(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	claimed := at.Add(-time.Second)
	now := at.Add(6 * time.Hour)
	binding := &assistantops.TaskBinding{TaskID: "task-1", RunID: "attempt-1", ClaimedAt: &claimed}
	task := &workspace.Task{ID: "task-1", Status: workspace.TaskStatusInProgress, CurrentRunID: "attempt-1"}
	outcome := func() *assistantops.Outcome {
		return assistantops.ResolveOutcome(binding, operationTaskState(task), now, assistantops.DefaultStartGrace)
	}

	// A task nobody launched for the operation has no outcome to attribute.
	if got := outcome(); got != nil {
		t.Fatalf("unmarked task: %+v", got)
	}
	workspace.MarkOperationRunLaunched(task)
	if got := outcome(); got == nil || *got != (assistantops.Outcome{Run: assistantops.RunRunning, Result: assistantops.ResultNone}) {
		t.Fatalf("running: %+v", got)
	}

	// The execution retried itself: by the time it is recorded, the task is on
	// another run identity. The record is still this start's, because the
	// recorder binds the mark and the record in one write.
	task.CurrentRunID = "attempt-3"
	workspace.RecordTaskExecution(task, "success", "Mixed.", at, time.Second)
	task.Status, task.Result = workspace.TaskStatusCompleted, "Mixed."
	state := operationTaskState(task)
	if len(state.Runs) != 1 || !state.Runs[0].At.Equal(at) || state.Runs[0].Status != "success" || !state.Runs[0].HasResult ||
		!state.HasResult || state.Active || state.Waiting || !state.Launched || state.LaunchPending || !state.LaunchedRunAt.Equal(at) {
		t.Fatalf("finished state: %+v", state)
	}
	if got := outcome(); *got != (assistantops.Outcome{Run: assistantops.RunSucceeded, Result: assistantops.ResultCurrent}) {
		t.Fatalf("finished after internal retries: %+v", got)
	}

	// The user reruns it: a new record and a new current result. The mark
	// stays on the first.
	task.CurrentRunID = "rerun-1"
	workspace.RecordTaskExecution(task, "success", "Mixed again.", at.Add(time.Hour), time.Second)
	task.Result = "Mixed again."
	if got := outcome(); *got != (assistantops.Outcome{Run: assistantops.RunSucceeded, Result: assistantops.ResultHistory, LaterRuns: 1}) {
		t.Fatalf("after a rerun: %+v", got)
	}
	if !operationTaskState(task).LaunchedRunAt.Equal(at) {
		t.Fatal("a rerun moved the mark")
	}
	// Enough reruns push the first record out of the task's own history.
	for i := range 200 {
		workspace.RecordTaskExecution(task, "success", "Again.", at.Add(time.Duration(i+2)*time.Hour), time.Second)
	}
	if got := outcome(); len(operationTaskState(task).Runs) >= 202 || got.Run != assistantops.RunUnknown || got.Result != assistantops.ResultGone {
		t.Fatalf("after the record was dropped: %+v", got)
	}

	// A start that stopped for the user is waiting, not failed.
	blocked := &workspace.Task{ID: "task-3", Status: workspace.TaskStatusInProgress}
	workspace.MarkOperationRunLaunched(blocked)
	workspace.RecordTaskExecution(blocked, "blocked", "connection required", at, 0)
	blocked.Status = workspace.TaskStatusWaitingForChoice
	got := assistantops.ResolveOutcome(&assistantops.TaskBinding{TaskID: "task-3", ClaimedAt: &claimed}, operationTaskState(blocked), now, assistantops.DefaultStartGrace)
	if got.Run != assistantops.RunWaiting || got.Result != assistantops.ResultNone {
		t.Fatalf("waiting: %+v", got)
	}

	// A start that never launched takes its mark back, so a later run from the
	// task's own controls is not recorded as the operation's.
	store := workspace.NewInMemoryStore()
	ws := &workspace.Workspace{ID: "ws-ops", Name: "Album", AgentInstances: []workspace.AgentInstance{{ID: "mixer-id", Name: "Mixer", NodeID: "Mixer-node-1"}}}
	marked := workspace.Task{ID: "task-5", WorkspaceID: "ws-ops", To: "Mixer", Description: "Mix", Status: workspace.TaskStatusPending}
	workspace.MarkOperationRunLaunched(&marked)
	if err := ws.AddTasks([]workspace.Task{marked}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := NewTaskHandler(store, nil, &countingExecutor{done: make(chan struct{}, 1)}, workspace.NewEventBus(16, 64))
	handler.unmarkOperationRun("ws-ops", "task-5")
	saved, _ := store.Get("ws-ops")
	unmarked, _ := saved.GetTask("task-5")
	if unmarked.AssistantOperationRun != nil || unmarked.Description != "Mix" || unmarked.To != "Mixer" {
		t.Fatalf("unmark must remove only the mark: %+v", unmarked)
	}
}

// The real first-start path marks the task, and the execution it runs is the
// one recorded as the operation's. A fenced start leaves no mark behind.
func TestStartReviewedTask_MarksTheExecutionItLaunches(t *testing.T) {
	handler, store, _ := reviewedTaskFixture(t, nil)
	host := operationTaskHost{handler: &Handler{taskHandlerSub: handler}}
	if err := host.Start(t.Context(), "ws-ops", "task-1"); err != nil {
		t.Fatal(err)
	}
	finished := waitForTask(t, store, func(task *workspace.Task) bool { return task.Status == workspace.TaskStatusCompleted })
	run := finished.AssistantOperationRun
	if run == nil || run.Pending || run.ExecutedAt == nil || len(finished.ExecutionHistory) != 1 ||
		!run.ExecutedAt.Equal(finished.ExecutionHistory[0].ExecutedAt) {
		t.Fatalf("the launched execution must be marked as recorded: %+v history=%d", run, len(finished.ExecutionHistory))
	}
	claimed := finished.ExecutionHistory[0].ExecutedAt.Add(-time.Second)
	state, err := host.Task(t.Context(), "ws-ops", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := assistantops.ResolveOutcome(&assistantops.TaskBinding{TaskID: "task-1", ClaimedAt: &claimed}, state, time.Now(), assistantops.DefaultStartGrace)
	if got == nil || *got != (assistantops.Outcome{Run: assistantops.RunSucceeded, Result: assistantops.ResultCurrent}) {
		t.Fatalf("outcome through the real path: %+v", got)
	}

	fenced, fencedStore, executor := reviewedTaskFixture(t, nil)
	gate := &resetstate.WorkGate{}
	fenced.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	var refusal *assistantops.StartRefusal
	if err := fenced.StartReviewedTask(t.Context(), "ws-ops", "task-1"); !errors.As(err, &refusal) {
		t.Fatalf("got %v", err)
	}
	ws, _ := fencedStore.Get("ws-ops")
	task, _ := ws.GetTask("task-1")
	if task.AssistantOperationRun != nil || executor.count() != 0 {
		t.Fatalf("a start that never launched left a mark: %+v", task.AssistantOperationRun)
	}
}
