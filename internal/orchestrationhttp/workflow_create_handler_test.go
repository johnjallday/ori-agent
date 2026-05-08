package orchestrationhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newWorkflowTestHandler(t *testing.T) (*TaskHandler, *workspace.Workspace, workspace.Store) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Workflow Tests"})
	ws.ID = "ws-workflows"
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	handler := &TaskHandler{
		workspaceStore: store,
		communicator:   agentcomm.NewCommunicator(store),
	}
	return handler, ws, store
}

func postJSON(t *testing.T, handler *TaskHandler, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/workflows", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleCreateWorkflow(rec, req)
	return rec
}

func TestWorkflowCreate_AtomicHappyPath(t *testing.T) {
	handler, ws, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id": ws.ID,
		"parent": map[string]interface{}{
			"id":          "parent-1",
			"description": "Onboarding workflow",
			"to":          "ori",
		},
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":             "step-1",
				"description":    "Collect requirements",
				"to":             "researcher",
				"subtask_index":  1,
				"input_task_ids": []string{},
			},
			map[string]interface{}{
				"id":             "step-2",
				"description":    "Draft plan",
				"to":             "writer",
				"subtask_index":  2,
				"input_task_ids": []string{"step-1"},
			},
		},
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success  bool              `json:"success"`
		Parent   *workspace.Task   `json:"parent"`
		Subtasks []*workspace.Task `json:"subtasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.Parent == nil || resp.Parent.ID != "parent-1" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if len(resp.Subtasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(resp.Subtasks))
	}
	if resp.Subtasks[1].InputTaskIDs[0] != "step-1" {
		t.Fatalf("expected step-2 to reference step-1 as input, got %v", resp.Subtasks[1].InputTaskIDs)
	}

	// Inspect the workspace state directly to confirm the batch persisted.
	if got := len(ws.Tasks); got != 3 {
		t.Fatalf("expected 3 tasks in workspace, got %d", got)
	}
}

func TestWorkflowCreate_RollsBackOnGraphCycle(t *testing.T) {
	handler, ws, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id": ws.ID,
		"parent": map[string]interface{}{
			"id":          "parent-cycle",
			"description": "Bad workflow",
		},
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":             "a",
				"description":    "A",
				"input_task_ids": []string{"b"},
			},
			map[string]interface{}{
				"id":             "b",
				"description":    "B",
				"input_task_ids": []string{"a"},
			},
		},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool                       `json:"success"`
		Error   string                     `json:"error"`
		Issues  []workspace.TaskGraphIssue `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if len(resp.Issues) == 0 {
		t.Fatal("expected at least one structured issue")
	}
	hasCycle := false
	for _, issue := range resp.Issues {
		if issue.Kind == workspace.TaskGraphIssueDependencyLoop {
			hasCycle = true
		}
	}
	if !hasCycle {
		t.Fatalf("expected a dependency_cycle issue, got %+v", resp.Issues)
	}
	if got := len(ws.Tasks); got != 0 {
		t.Fatalf("expected workspace to remain empty after rollback, got %d tasks", got)
	}
}

func TestWorkflowCreate_RejectsUnknownInputWithStructuredIssue(t *testing.T) {
	handler, _, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id": "ws-workflows",
		"parent": map[string]interface{}{
			"id":          "parent-unknown",
			"description": "Workflow",
		},
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":             "real",
				"description":    "Real step",
				"input_task_ids": []string{"never-existed"},
			},
		},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []workspace.TaskGraphIssue `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	hasUnknown := false
	for _, issue := range resp.Issues {
		if issue.Kind == workspace.TaskGraphIssueUnknownInput && issue.TaskID == "real" && issue.Reference == "never-existed" {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Fatalf("expected unknown_input issue pointing at task=real ref=never-existed, got %+v", resp.Issues)
	}
}

func TestWorkflowCreate_RejectsMissingParentID(t *testing.T) {
	handler, _, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id": "ws-workflows",
		"parent": map[string]interface{}{
			"description": "Has no client-supplied id",
		},
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":          "x",
				"description": "X",
			},
		},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing parent id, got %d", rec.Code)
	}
}

func TestWorkflowCreate_AttachToExistingParent(t *testing.T) {
	handler, ws, _ := newWorkflowTestHandler(t)
	if err := ws.AddTask(workspace.Task{ID: "existing-parent", Description: "Existing parent"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id":        ws.ID,
		"attach_to_parent_id": "existing-parent",
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":            "attached-1",
				"description":   "Step 1",
				"subtask_index": 1,
			},
			map[string]interface{}{
				"id":             "attached-2",
				"description":    "Step 2",
				"subtask_index":  2,
				"input_task_ids": []string{"attached-1"},
			},
		},
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := len(ws.Tasks); got != 3 {
		t.Fatalf("expected 1 existing + 2 attached = 3 tasks, got %d", got)
	}
	for _, id := range []string{"attached-1", "attached-2"} {
		task, err := ws.GetTask(id)
		if err != nil {
			t.Fatalf("expected task %s to exist: %v", id, err)
		}
		if task.ParentTaskID != "existing-parent" {
			t.Fatalf("expected attached subtask %s to have parent_task_id=existing-parent, got %q", id, task.ParentTaskID)
		}
	}
}

func TestWorkflowCreate_AttachRejectsMissingParent(t *testing.T) {
	handler, _, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id":        "ws-workflows",
		"attach_to_parent_id": "ghost-parent",
		"subtasks": []interface{}{
			map[string]interface{}{
				"id":          "x",
				"description": "X",
			},
		},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown attach parent, got %d", rec.Code)
	}
}

func TestWorkflowCreate_RejectsBothParentAndAttach(t *testing.T) {
	handler, _, _ := newWorkflowTestHandler(t)

	rec := postJSON(t, handler, map[string]interface{}{
		"workspace_id":        "ws-workflows",
		"attach_to_parent_id": "anything",
		"parent": map[string]interface{}{
			"id":          "p",
			"description": "P",
		},
		"subtasks": []interface{}{
			map[string]interface{}{"id": "x", "description": "X"},
		},
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when both parent and attach_to_parent_id are set, got %d", rec.Code)
	}
}

func TestWorkflowCreate_StructuredIssueOnSingleTaskEndpoint(t *testing.T) {
	// The /tasks endpoint should also surface structured issues now that
	// respondTaskGraphError is wired in. This exercises the path so a
	// future regression in the flat-error fallback gets caught.
	handler, ws, _ := newWorkflowTestHandler(t)
	if err := ws.AddTask(workspace.Task{ID: "anchor", Description: "anchor"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"workspace_id":   ws.ID,
		"description":    "child",
		"parent_task_id": "ghost",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.handleCreateTask(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []workspace.TaskGraphIssue `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Issues) == 0 {
		t.Fatalf("expected /tasks endpoint to surface structured issues, got body %s", rec.Body.String())
	}
}
