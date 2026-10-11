package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// preparedFor asks the assistant for a draft and returns the operation, the
// conversation it belongs to and the agent's own node in the workspace.
func (f *operationHostFixture) preparedFor(t *testing.T, agent, title string) (op map[string]any, node string) {
	t.Helper()
	f.prepareScript(agent, title)
	reply := f.operate(t, "", "Create a task for "+agent+" to "+title)
	op = operationOf(t, reply)
	if op["status"] != "prepared" {
		t.Fatalf("draft: %v", op)
	}
	ws, err := f.builder.workspaceStore.Get(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range ws.AgentInstances {
		if instance.Name == agent {
			node = instance.NodeID
		}
	}
	return op, node
}

// save is the task form's own create request for a prepared draft.
func (f *operationHostFixture) save(t *testing.T, op map[string]any, change func(map[string]any)) (int, map[string]any) {
	t.Helper()
	draft := op["draft"].(map[string]any)
	body := map[string]any{
		"workspace_id": f.project.ID, "description": draft["title"], "details": draft["brief"], "priority": 3,
		"to": draft["agent_name"], "assigned_node_id": draft["agent_node_id"],
		"assistant_operation_id": op["id"], "assistant_draft_revision": draft["revision"],
	}
	if change != nil {
		change(body)
	}
	return f.call(t, http.MethodPost, "/api/orchestration/tasks", body)
}

func (f *operationHostFixture) read(t *testing.T, id string) map[string]any {
	t.Helper()
	status, reply := f.call(t, http.MethodGet, "/api/home-assistant/operations/"+id+"?tab=tab-a", nil)
	if status != http.StatusOK {
		t.Fatalf("read: %d %v", status, reply)
	}
	return operationOf(t, reply)
}

func (f *operationHostFixture) task(t *testing.T, id string) *workspace.Task {
	t.Helper()
	ws, err := f.builder.workspaceStore.Get(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := ws.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func receiptCount(op map[string]any, kind string) int {
	raw, _ := op["receipts"].([]any)
	count := 0
	for _, item := range raw {
		if receipt, _ := item.(map[string]any); receipt["kind"] == kind {
			count++
		}
	}
	return count
}

// Through the real routes, stores and execution path: the reviewed save binds
// one task to its operation, a repeated save and a repeated Start change
// nothing, and the operation reports execution only from the task.
func TestAssistantOperations_RealHostCreatesAndStartsTheReviewedTaskOnce(t *testing.T) {
	f := newOperationHostFixture(t)
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.AddAgent("Engineer") }); err != nil {
		t.Fatal(err)
	}
	op, node := f.preparedFor(t, "Engineer", "bounce the stems")
	id := op["id"].(string)
	tasksBefore := f.taskCount(t)

	// The human edits the title in the form, then saves. The edit is what is created.
	status, created := f.save(t, op, func(body map[string]any) { body["description"] = "Bounce the stems at 48k" })
	if status != http.StatusCreated {
		t.Fatalf("save: %d %v", status, created)
	}
	taskID := created["task"].(map[string]any)["id"].(string)
	saved := f.task(t, taskID)
	if saved.Description != "Bounce the stems at 48k" || saved.To != "Engineer" || saved.AssignedNodeID != node || node == "" ||
		saved.AssistantOperationID != id || saved.Status != workspace.TaskStatusPending || saved.AssignmentMode != workspace.TaskAssignmentModeManual {
		t.Fatalf("saved task: %+v", saved)
	}
	if saved.CurrentRunID != "" || saved.StartedAt != nil || len(saved.ExecutionHistory) != 0 {
		t.Fatalf("saving must not start the task: %+v", saved)
	}
	reviewed := f.read(t, id)
	bound, _ := reviewed["task"].(map[string]any)
	if reviewed["status"] != "reviewed" || bound == nil || bound["task_id"] != taskID || receiptCount(reviewed, "task_created") != 1 || receiptCount(reviewed, "start_accepted") != 0 {
		t.Fatalf("operation after save: %v", reviewed)
	}
	if f.rows(t, `WHERE id=? AND status='reviewed' AND task_id=?`, id, taskID) != 1 {
		t.Fatal("the stored operation is not bound to the task")
	}

	// The browser never heard back and saves again; so does a second tab.
	for range 3 {
		status, repeat := f.save(t, op, nil)
		if status != http.StatusOK || repeat["already_created"] != true || repeat["task"].(map[string]any)["id"] != taskID {
			t.Fatalf("repeat: %d %v", status, repeat)
		}
	}
	if after := f.taskCount(t); after != tasksBefore+1 {
		t.Fatalf("one operation created %d tasks", after-tasksBefore)
	}

	// The user's own Start, then its duplicate deliveries.
	base := "/api/home-assistant/operations/" + id
	status, started := f.call(t, http.MethodPost, base+"/start", map[string]any{"tab_id": "tab-a"})
	if status != http.StatusOK {
		t.Fatalf("start: %d %v", status, started)
	}
	if state := operationOf(t, started)["status"]; state != "start_requested" && state != "started" {
		t.Fatalf("start: %v", started)
	}
	for _, tab := range []string{"tab-a", "tab-b"} {
		if status, again := f.call(t, http.MethodPost, base+"/start", map[string]any{"tab_id": tab}); status != http.StatusOK {
			t.Fatalf("repeat start from %s: %d %v", tab, status, again)
		}
	}

	// Execution is established by the task, and it ran exactly once.
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final = f.read(t, id)
		task := f.task(t, taskID)
		if final["status"] == "started" && task.Status != workspace.TaskStatusPending && task.Status != workspace.TaskStatusInProgress {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	ran := f.task(t, taskID)
	if final["status"] != "started" || receiptCount(final, "run_established") != 1 || receiptCount(final, "start_accepted") != 1 {
		t.Fatalf("operation after the run: %v (task %s: %s)", final, ran.Status, ran.Error)
	}
	if ran.Status == workspace.TaskStatusPending || ran.Status == workspace.TaskStatusInProgress || len(ran.ExecutionHistory) != 1 || ran.ExecutionCount > 1 {
		t.Fatalf("the task must have run exactly once: status=%s history=%d count=%d", ran.Status, len(ran.ExecutionHistory), ran.ExecutionCount)
	}

	// A late Start on the finished task is not a rerun.
	if status, late := f.call(t, http.MethodPost, base+"/start", map[string]any{"tab_id": "tab-a"}); status != http.StatusOK || operationOf(t, late)["status"] != "started" {
		t.Fatalf("late start: %d %v", status, late)
	}
	time.Sleep(100 * time.Millisecond)
	if again := f.task(t, taskID); len(again.ExecutionHistory) != 1 || again.Status != ran.Status {
		t.Fatalf("a finished task was replayed: history=%d status=%s", len(again.ExecutionHistory), again.Status)
	}
	if after := f.taskCount(t); after != tasksBefore+1 {
		t.Fatalf("task count drifted: %d", after-tasksBefore)
	}
}

// The reviewed assignee is checked against the workspace as it is at the save.
func TestAssistantOperations_RealHostSaveNeverSubstitutesOrAddsAnAgent(t *testing.T) {
	f := newOperationHostFixture(t)
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.AddAgent("Engineer") }); err != nil {
		t.Fatal(err)
	}
	op, _ := f.preparedFor(t, "Engineer", "bounce the stems")
	id := op["id"].(string)
	tasksBefore := f.taskCount(t)

	// Engineer leaves the workspace while the form is open.
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.RemoveAgent("Engineer") }); err != nil {
		t.Fatal(err)
	}
	status, refused := f.save(t, op, nil)
	if status != http.StatusConflict || refused["code"] != "operation_assignee" {
		t.Fatalf("removed assignee: %d %v", status, refused)
	}
	// An agent that was never here, and no agent at all.
	for _, to := range []string{"Editor", "", "unassigned"} {
		status, refused := f.save(t, op, func(body map[string]any) { body["to"], body["assigned_node_id"] = to, "" })
		if status != http.StatusConflict || refused["code"] != "operation_assignee" {
			t.Fatalf("assignee %q: %d %v", to, status, refused)
		}
	}
	ws, err := f.builder.workspaceStore.Get(f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.HasAgent("Engineer") || ws.HasAgent("Editor") || len(ws.Tasks) != tasksBefore {
		t.Fatalf("a refused save changed the workspace: agents=%d tasks=%d", len(ws.AgentInstances), len(ws.Tasks))
	}
	if still := f.read(t, id); still["status"] != "prepared" || still["task"] != nil {
		t.Fatalf("a wrong assignee asks for a corrected review; it ends nothing: %v", still)
	}

	// The user picks another agent that is in the workspace and saves again.
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.AddAgent("Drummer") }); err != nil {
		t.Fatal(err)
	}
	status, created := f.save(t, op, func(body map[string]any) { body["to"], body["assigned_node_id"] = "Drummer", "" })
	if status != http.StatusCreated {
		t.Fatalf("corrected save: %d %v", status, created)
	}
	task := f.task(t, created["task"].(map[string]any)["id"].(string))
	if task.To != "Drummer" || task.AssignedNodeID == "" || task.AssistantOperationID != id {
		t.Fatalf("corrected task: %+v", task)
	}
	if reviewed := f.read(t, id); reviewed["status"] != "reviewed" || reviewed["context"].(map[string]any)["agent_name"] != "Drummer" {
		t.Fatalf("the operation follows the reviewed assignee: %v", reviewed)
	}

	// Drummer leaves before Start: the start is refused, the task stays.
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { return ws.RemoveAgent("Drummer") }); err != nil {
		t.Fatal(err)
	}
	status, start := f.call(t, http.MethodPost, "/api/home-assistant/operations/"+id+"/start", map[string]any{"tab_id": "tab-a"})
	details, _ := start["details"].(map[string]any)
	if status != http.StatusConflict || start["code"] != "operation_assignee" || details == nil {
		t.Fatalf("start with a removed assignee: %d %v", status, start)
	}
	if after := operationOf(t, details); after["status"] != "reviewed" || receiptCount(after, "start_refused") != 1 || receiptCount(after, "start_accepted") != 0 {
		t.Fatalf("refused start: %v", after)
	}
	if kept := f.task(t, task.ID); kept.Status != workspace.TaskStatusPending || kept.StartedAt != nil || len(kept.ExecutionHistory) != 0 {
		t.Fatalf("a refused start touched the task: %+v", kept)
	}
}

// An operation that ended, or that belongs to another workspace, creates
// nothing: the save is refused, never turned into an ordinary create.
func TestAssistantOperations_RealHostEndedOperationsCreateNothing(t *testing.T) {
	f := newOperationHostFixture(t)
	op, _ := f.preparedFor(t, "Mixer", "mix the chorus")
	id := op["id"].(string)
	tasksBefore := f.taskCount(t)

	if status, refused := f.save(t, op, func(body map[string]any) { body["assistant_draft_revision"] = 7 }); status != http.StatusConflict || refused["code"] != "operation_conflict" {
		t.Fatalf("stale draft: %d %v", status, refused)
	}
	other := placementWorkspace(t, f.draftServerFixture, "Another workspace", "", "")
	if status, refused := f.save(t, op, func(body map[string]any) { body["workspace_id"] = other.ID }); status != http.StatusBadRequest || refused["code"] != "operation_invalid" {
		t.Fatalf("another workspace: %d %v", status, refused)
	}
	if status, refused := f.save(t, op, func(body map[string]any) { body["assistant_operation_id"] = "no-such-operation" }); status != http.StatusNotFound || refused["code"] != "operation_not_found" {
		t.Fatalf("unknown operation: %d %v", status, refused)
	}
	if status, refused := f.call(t, http.MethodPost, "/api/home-assistant/operations/"+id+"/start", map[string]any{"tab_id": "tab-a"}); status != http.StatusConflict || refused["code"] != "operation_ended" {
		t.Fatalf("starting an unsaved draft: %d %v", status, refused)
	}

	// The conversation is deleted: its operations go with it.
	if _, err := f.builder.sessionStore.DB().ExecContext(t.Context(), `DELETE FROM sessions WHERE id = (SELECT conversation_id FROM assistant_operations WHERE id = ?)`, id); err != nil {
		t.Fatal(err)
	}
	if status, refused := f.save(t, op, nil); status != http.StatusNotFound || refused["code"] != "operation_not_found" {
		t.Fatalf("deleted conversation: %d %v", status, refused)
	}
	if after := f.taskCount(t); after != tasksBefore || other.ID == f.project.ID {
		t.Fatalf("a refused save created a task: %d -> %d", tasksBefore, after)
	}
	otherWS, err := f.builder.workspaceStore.Get(other.ID)
	if err != nil || len(otherWS.Tasks) != 0 {
		t.Fatalf("the other workspace gained a task: %v", err)
	}
}

// post sends one request through the production routes without touching the
// test from another goroutine.
func (f *operationHostFixture) post(path string, body any) (int, map[string]any) {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

// Many saves and many Starts at once, through the real routes, stores and
// execution path: one task is written and it runs once.
func TestAssistantOperations_RealHostConcurrentSavesAndStartsStayAtOne(t *testing.T) {
	f := newOperationHostFixture(t)
	op, _ := f.preparedFor(t, "Mixer", "mix the chorus")
	id := op["id"].(string)
	draft := op["draft"].(map[string]any)
	tasksBefore := f.taskCount(t)
	body := map[string]any{
		"workspace_id": f.project.ID, "description": draft["title"], "to": draft["agent_name"],
		"assigned_node_id": draft["agent_node_id"], "assistant_operation_id": id, "assistant_draft_revision": draft["revision"],
	}

	const savers = 10
	type outcome struct {
		status int
		reply  map[string]any
	}
	saves := make(chan outcome, savers)
	var wg sync.WaitGroup
	for range savers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, reply := f.post("/api/orchestration/tasks", body)
			saves <- outcome{status, reply}
		}()
	}
	wg.Wait()
	close(saves)
	created, taskID := 0, ""
	for save := range saves {
		switch {
		case save.status == http.StatusCreated:
			created++
			taskID = save.reply["task"].(map[string]any)["id"].(string)
		case save.status == http.StatusOK && save.reply["already_created"] == true:
		case save.status == http.StatusConflict && (save.reply["code"] == "operation_create_in_flight" || save.reply["code"] == "operation_conflict"):
		default:
			t.Errorf("unexpected save outcome: %d %v", save.status, save.reply)
		}
	}
	if created != 1 || f.taskCount(t) != tasksBefore+1 {
		t.Fatalf("%d concurrent saves created %d tasks (%d created responses)", savers, f.taskCount(t)-tasksBefore, created)
	}
	// Every later save, whatever it was told while racing, gets that one task.
	if status, repeat := f.save(t, op, nil); status != http.StatusOK || repeat["task"].(map[string]any)["id"] != taskID {
		t.Fatalf("repeat after the race: %d %v", status, repeat)
	}
	if reviewed := f.read(t, id); reviewed["status"] != "reviewed" || receiptCount(reviewed, "task_created") != 1 {
		t.Fatalf("operation after the race: %v", reviewed)
	}

	const clicks = 10
	starts := make(chan outcome, clicks)
	for i := range clicks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, reply := f.post("/api/home-assistant/operations/"+id+"/start", map[string]any{"tab_id": fmt.Sprintf("tab-%d", i)})
			starts <- outcome{status, reply}
		}()
	}
	wg.Wait()
	close(starts)
	for start := range starts {
		// A click that lost the swap may be told to read again; none is refused
		// for any other reason and none starts a second run.
		if start.status != http.StatusOK && (start.status != http.StatusConflict || start.reply["code"] != "operation_conflict") {
			t.Errorf("unexpected start outcome: %d %v", start.status, start.reply)
		}
	}
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final = f.read(t, id)
		if task := f.task(t, taskID); final["status"] == "started" && task.Status != workspace.TaskStatusPending && task.Status != workspace.TaskStatusInProgress {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	ran := f.task(t, taskID)
	if final["status"] != "started" || receiptCount(final, "start_accepted") != 1 || receiptCount(final, "run_established") != 1 {
		t.Fatalf("operation after concurrent starts: %v", final)
	}
	if len(ran.ExecutionHistory) != 1 || ran.ExecutionCount > 1 {
		t.Fatalf("%d concurrent starts ran the task %d times (count %d, status %s)", clicks, len(ran.ExecutionHistory), ran.ExecutionCount, ran.Status)
	}
}

// The user starts the task from the task's own controls while the drawer's
// Start is on its way: one run, and the operation reports it without claiming it.
func TestAssistantOperations_RealHostManualStartRacingTheOperationRunsOnce(t *testing.T) {
	f := newOperationHostFixture(t)
	op, _ := f.preparedFor(t, "Mixer", "mix the chorus")
	id := op["id"].(string)
	status, created := f.save(t, op, nil)
	if status != http.StatusCreated {
		t.Fatalf("save: %d %v", status, created)
	}
	taskID := created["task"].(map[string]any)["id"].(string)

	var wg sync.WaitGroup
	results := make(chan int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		code, _ := f.post("/api/orchestration/tasks/execute", map[string]any{"task_id": taskID})
		results <- code
	}()
	go func() {
		defer wg.Done()
		code, _ := f.post("/api/home-assistant/operations/"+id+"/start", map[string]any{"tab_id": "tab-a"})
		results <- code
	}()
	wg.Wait()
	close(results)
	for code := range results {
		// Whichever lost is told so (already in progress, or refused); neither
		// answer is a server error.
		if code >= http.StatusInternalServerError {
			t.Errorf("unexpected status %d", code)
		}
	}
	var final map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		final = f.read(t, id)
		if task := f.task(t, taskID); final["status"] == "started" && task.Status != workspace.TaskStatusPending && task.Status != workspace.TaskStatusInProgress {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	ran := f.task(t, taskID)
	if final["status"] != "started" || receiptCount(final, "run_established") != 1 {
		t.Fatalf("operation after the race: %v (task %s)", final, ran.Status)
	}
	if len(ran.ExecutionHistory) != 1 {
		t.Fatalf("a manual start racing the operation ran the task %d times (status %s)", len(ran.ExecutionHistory), ran.Status)
	}
}
