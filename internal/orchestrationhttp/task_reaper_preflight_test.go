package orchestrationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type recordingFallbackPreparer struct {
	mu      sync.Mutex
	prepare int
	commits int
	aborts  int
}

type recordingFallbackRun struct {
	owner *recordingFallbackPreparer
	task  workspace.Task
}

func (p *recordingFallbackPreparer) PrepareTaskFileFallback(_ context.Context, _ string, task workspace.Task, capability string) (workspace.TaskFileFallbackRun, error) {
	p.mu.Lock()
	p.prepare++
	p.mu.Unlock()
	task.RequiredCapabilities = capabilitiesWithout(task.RequiredCapabilities, capability)
	task.RuntimeExecution = &workspace.TaskRuntimeExecution{WorkspaceRoot: "/trusted/stage", FileOnly: true, DisableTools: true, Filename: "song.rpp"}
	return &recordingFallbackRun{owner: p, task: task}, nil
}
func (r *recordingFallbackRun) PreparedTask() workspace.Task { return r.task }
func (r *recordingFallbackRun) Commit() error {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	r.owner.commits++
	return nil
}
func (r *recordingFallbackRun) Abort() {
	r.owner.mu.Lock()
	defer r.owner.mu.Unlock()
	r.owner.aborts++
}

func reaperBlockedError(reason, action, label string) *workspace.TaskBlockedError {
	return &workspace.TaskBlockedError{
		CapabilityKey: "reaper_live_control",
		ReasonCode:    reason,
		Reason:        "Live REAPER control is unavailable.",
		Repair:        &workspace.TaskRepairAction{Code: action, Label: label, URL: "/workspaces/ws-reaper?runtime_setup=1"},
	}
}

func TestReaperPreflightBlocksBeforeRunTraceOrProviderCall(t *testing.T) {
	ws := &workspace.Workspace{ID: "ws-reaper", Name: "Song", AgentInstances: []workspace.AgentInstance{{ID: "producer", Name: "Producer"}}}
	task := workspace.Task{
		ID: "live-task", WorkspaceID: ws.ID, To: "Producer", Description: "Adjust the live session",
		Status: workspace.TaskStatusPending, RequiredCapabilities: []string{"reaper_live_control"},
		FileFallbackFor: []string{"reaper_live_control"}, Context: map[string]any{},
	}
	if err := ws.AddTasks([]workspace.Task{task}); err != nil {
		t.Fatal(err)
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedTaskHandler{result: "should not run"}
	handler := NewTaskHandler(store, agentcomm.NewCommunicator(store), provider, workspace.NewEventBus(16, 64))
	handler.SetCapabilityGate(stubGate{blocked: reaperBlockedError("reaper_offline", "open_check_reaper", "Open or check REAPER")})

	loaded, _ := store.Get(ws.ID)
	pending, _ := loaded.GetTask(task.ID)
	if _, err := handler.executeTaskWithDependencies(loaded, pending); err == nil {
		t.Fatal("offline live task should block")
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d", provider.calls)
	}
	saved, _ := store.Get(ws.ID)
	blocked, _ := saved.GetTask(task.ID)
	if blocked.CurrentRunID != "" || blocked.StartedAt != nil || len(blocked.ExecutionHistory) != 0 || len(blocked.ExecutionTrace) != 0 {
		t.Fatalf("preflight created run metadata: %+v", blocked)
	}
	loop, ok := blocked.Context["human_loop"].(map[string]any)
	if !ok {
		t.Fatalf("human_loop type=%T value=%#v", blocked.Context["human_loop"], blocked.Context["human_loop"])
	}
	workflow, _ := loop["workflow_step"].(*workspace.TaskBlockedWorkflowStep)
	if loop["reason_code"] != "reaper_offline" || loop["repair"] == nil || workflow == nil || len(workflow.Choices) != 1 || workflow.Choices[0].ID != "use_file_fallback" {
		t.Fatalf("blocked context = %+v", loop)
	}
}

func TestExplicitReaperFileFallbackRunsOnceAndReportsFileChange(t *testing.T) {
	ws := &workspace.Workspace{ID: "ws-reaper", Name: "Song", AgentInstances: []workspace.AgentInstance{{ID: "producer", Name: "Producer"}}}
	task := workspace.Task{
		ID: "live-task", WorkspaceID: ws.ID, To: "Producer", Description: "Adjust the live session",
		Status: workspace.TaskStatusPending, RequiredCapabilities: []string{"reaper_live_control"},
		FileFallbackFor: []string{"reaper_live_control"}, Context: map[string]any{},
	}
	if err := ws.AddTasks([]workspace.Task{task}); err != nil {
		t.Fatal(err)
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedTaskHandler{result: "Updated the project."}
	handler := NewTaskHandler(store, agentcomm.NewCommunicator(store), provider, workspace.NewEventBus(32, 128))
	handler.SetCapabilityGate(stubGate{blocked: reaperBlockedError("wrong_project", "open_correct_project", "Open the workspace project")})
	fallback := &recordingFallbackPreparer{}
	handler.SetFileFallbackPreparer(fallback)

	loaded, _ := store.Get(ws.ID)
	pending, _ := loaded.GetTask(task.ID)
	if _, err := handler.executeTaskWithDependencies(loaded, pending); err == nil {
		t.Fatal("first attempt should block")
	}
	blockedWS, _ := store.Get(ws.ID)
	blockedTask, _ := blockedWS.GetTask(task.ID)
	loop, _ := blockedTask.Context["human_loop"].(map[string]any)
	blockID, _ := loop["block_id"].(string)
	body, _ := json.Marshal(map[string]any{
		"block_id": blockID, "action": "continue_with_instruction",
		"choice_id": "use_file_fallback", "choice_label": "Use project-file fallback", "choice_number": "F",
	})
	recorder := httptest.NewRecorder()
	handler.handleAssistTask(recorder, httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/"+task.ID+"/assist", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("assist status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		saved, _ := store.Get(ws.ID)
		completed, _ := saved.GetTask(task.ID)
		if strings.Contains(completed.Result, "project-file change") {
			if provider.calls != 1 {
				t.Fatalf("provider calls = %d", provider.calls)
			}
			if completed.ApprovedRuntimeFileFallback() != "" {
				t.Fatal("one-shot approval survived execution")
			}
			fallback.mu.Lock()
			commits, aborts, prepares := fallback.commits, fallback.aborts, fallback.prepare
			fallback.mu.Unlock()
			if commits != 1 || aborts != 1 || prepares != 1 {
				t.Fatalf("fallback lifecycle prepare=%d commit=%d abort=%d", prepares, commits, aborts)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fallback did not complete; provider calls=%d", provider.calls)
}

func TestFileFallbackChoiceCannotBeForgedOnUnrelatedBlock(t *testing.T) {
	handler, store, _ := blockedTaskFixture(t)
	handler.communicator = agentcomm.NewCommunicator(store)
	ws, _ := store.Get("ws-1")
	task, _ := ws.GetTask("task-1")
	task.RequiredCapabilities = []string{"reaper_live_control"}
	task.FileFallbackFor = []string{"reaper_live_control"}
	if err := ws.UpdateTask(*task); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	body := `{"action":"continue_with_instruction","choice_id":"use_file_fallback","choice_label":"Use project-file fallback"}`
	recorder := httptest.NewRecorder()
	handler.handleAssistTask(recorder, httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/task-1/assist", strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
