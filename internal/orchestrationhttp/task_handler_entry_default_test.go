package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// soloCoordinatorWorkspace builds a single-agent workspace whose lone member is
// the coordinator via the single-agent default, saved into store.
func soloCoordinatorWorkspace(t *testing.T, store workspace.Store, id string) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
	ws.ID = id
	ws.Agents = []string{"Solo"}
	ws.AgentInstances = []workspace.AgentInstance{{Name: "Solo", NodeID: "Solo-node-1"}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
}

type createdTaskResponse struct {
	Success bool `json:"success"`
	Task    struct {
		ID             string `json:"id"`
		To             string `json:"to"`
		AssignedBy     string `json:"assigned_by"`
		AssignmentMode string `json:"assignment_mode"`
	} `json:"task"`
}

func postCreateTask(t *testing.T, store workspace.Store, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.TasksHandler(rec, req)
	return rec
}

// TestHandleCreateTaskDefaultsToCoordinator: an unassigned create defaults to the
// coordinator and keeps entry_agent_default provenance (not clobbered to manual).
func TestHandleCreateTaskDefaultsToCoordinator(t *testing.T) {
	store := workspace.NewInMemoryStore()
	soloCoordinatorWorkspace(t, store, "ws-default-create")

	rec := postCreateTask(t, store, `{"workspace_id":"ws-default-create","from":"user","description":"do it"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createdTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.ID == "" {
		t.Fatal("created task was not returned (createdTask capture broke)")
	}
	if resp.Task.To != "Solo" ||
		resp.Task.AssignmentMode != string(workspace.TaskAssignmentModeEntryAgentDefault) ||
		resp.Task.AssignedBy != "Solo" {
		t.Fatalf("task not defaulted with entry_agent_default provenance: %+v", resp.Task)
	}
}

// TestHandleCreateTaskHonorsExplicitAssignee: an explicit assignee is not
// overridden by defaulting and stays manual.
func TestHandleCreateTaskHonorsExplicitAssignee(t *testing.T) {
	store := workspace.NewInMemoryStore()
	soloCoordinatorWorkspace(t, store, "ws-explicit")

	rec := postCreateTask(t, store, `{"workspace_id":"ws-explicit","from":"user","to":"Solo","description":"do it"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createdTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.To != "Solo" || resp.Task.AssignmentMode != string(workspace.TaskAssignmentModeManual) {
		t.Fatalf("explicit assignee should stay manual: %+v", resp.Task)
	}
}

// TestHandleCreateTaskNoCoordinatorStaysUnassigned: with no resolvable
// coordinator, an unassigned create stays unassigned (claimed later).
func TestHandleCreateTaskNoCoordinatorStaysUnassigned(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "multi"})
	ws.ID = "ws-multi"
	ws.Agents = []string{"Writer", "Researcher"}
	ws.AgentInstances = []workspace.AgentInstance{
		{Name: "Writer", NodeID: "Writer-node-1"},
		{Name: "Researcher", NodeID: "Researcher-node-1"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	rec := postCreateTask(t, store, `{"workspace_id":"ws-multi","from":"user","description":"do it"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createdTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.To != "" {
		t.Fatalf("task should stay unassigned with no coordinator: %+v", resp.Task)
	}
}

// TestHandleCreateScheduledTaskDefaultsBeforeValidation: a scheduled task with
// no assignee is defaulted to the coordinator before schedule validation, so it
// is accepted rather than rejected as unassigned (FR8).
func TestHandleCreateScheduledTaskDefaultsBeforeValidation(t *testing.T) {
	store := workspace.NewInMemoryStore()
	soloCoordinatorWorkspace(t, store, "ws-sched")

	body := `{"workspace_id":"ws-sched","from":"user","description":"morning briefing",` +
		`"schedule_enabled":true,"schedule":{"type":"interval","interval_minutes":60}}`
	rec := postCreateTask(t, store, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("scheduled task without assignee should default + pass validation; status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp createdTaskResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.To != "Solo" || resp.Task.AssignmentMode != string(workspace.TaskAssignmentModeEntryAgentDefault) {
		t.Fatalf("scheduled task not defaulted to coordinator: %+v", resp.Task)
	}
}
