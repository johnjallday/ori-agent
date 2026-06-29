package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func memberWorkspace(names ...string) *Workspace {
	ws := &Workspace{}
	insts := make([]AgentInstance, 0, len(names))
	for _, n := range names {
		insts = append(insts, AgentInstance{Name: n, NodeID: n + "-node-1"})
	}
	ws.AgentInstances = insts
	return ws
}

func TestApplyTaskAssignment(t *testing.T) {
	ws := memberWorkspace("Writer", "Researcher")

	t.Run("member assignee stamps provenance", func(t *testing.T) {
		task := &Task{ID: "t1"}
		err := ws.ApplyTaskAssignment(task, TaskAssignment{
			AgentName:  "Writer",
			Mode:       TaskAssignmentModeStaticPlan,
			AssignedBy: "Manager",
			Reason:     "best fit",
		})
		if err != nil {
			t.Fatalf("ApplyTaskAssignment() error = %v", err)
		}
		if task.To != "Writer" || task.AssignmentMode != TaskAssignmentModeStaticPlan ||
			task.AssignedBy != "Manager" || task.AssignmentReason != "best fit" {
			t.Fatalf("provenance not stamped: %+v", task)
		}
	})

	t.Run("non-member assignee rejected", func(t *testing.T) {
		task := &Task{ID: "t2"}
		err := ws.ApplyTaskAssignment(task, TaskAssignment{AgentName: "Ghost", Mode: TaskAssignmentModeStaticPlan})
		if !errors.Is(err, ErrAssigneeNotInWorkspace) {
			t.Fatalf("ApplyTaskAssignment() error = %v, want ErrAssigneeNotInWorkspace", err)
		}
		if task.To != "" {
			t.Fatalf("task.To = %q, want unchanged on rejection", task.To)
		}
	})

	t.Run("empty assignee leaves task unassigned but stamps provenance", func(t *testing.T) {
		task := &Task{ID: "t3", To: "stale"}
		err := ws.ApplyTaskAssignment(task, TaskAssignment{
			AgentName:  "",
			Mode:       TaskAssignmentModeManual,
			AssignedBy: TaskAssignedByManual,
		})
		if err != nil {
			t.Fatalf("ApplyTaskAssignment() error = %v", err)
		}
		if task.To != "" || task.AssignmentMode != TaskAssignmentModeManual {
			t.Fatalf("unexpected state: %+v", task)
		}
	})

	t.Run("nil task errors", func(t *testing.T) {
		if err := ws.ApplyTaskAssignment(nil, TaskAssignment{}); err == nil {
			t.Fatal("ApplyTaskAssignment(nil) = nil, want error")
		}
	})
}

func TestResolveCoordinatorForAssignment(t *testing.T) {
	t.Run("explicit entry resolves", func(t *testing.T) {
		ws := memberWorkspace("Manager", "Writer")
		ws.SharedData = map[string]any{sharedDataEntryAgentNameKey: "Manager"}
		name, err := ws.ResolveCoordinatorForAssignment()
		if err != nil || name != "Manager" {
			t.Fatalf("ResolveCoordinatorForAssignment() = (%q, %v), want (Manager, nil)", name, err)
		}
	})

	t.Run("single agent default resolves", func(t *testing.T) {
		ws := memberWorkspace("Solo")
		name, err := ws.ResolveCoordinatorForAssignment()
		if err != nil || name != "Solo" {
			t.Fatalf("ResolveCoordinatorForAssignment() = (%q, %v), want (Solo, nil)", name, err)
		}
	})

	t.Run("multi-agent missing entry blocks", func(t *testing.T) {
		ws := memberWorkspace("Writer", "Researcher")
		name, err := ws.ResolveCoordinatorForAssignment()
		if !errors.Is(err, ErrCoordinatorMissing) || name != "" {
			t.Fatalf("ResolveCoordinatorForAssignment() = (%q, %v), want (\"\", ErrCoordinatorMissing)", name, err)
		}
	})
}

func TestBuildCoordinatorRoster(t *testing.T) {
	t.Run("explicit entry excludes coordinator from specialists", func(t *testing.T) {
		ws := memberWorkspace("Manager", "Writer", "Researcher")
		ws.SharedData = map[string]any{sharedDataEntryAgentNameKey: "Manager"}
		r := ws.BuildCoordinatorRoster()
		if r.Coordinator != "Manager" || r.CoordinatorSource != CoordinatorSourceExplicitEntryAgent {
			t.Fatalf("coordinator = (%q, %q), want (Manager, explicit)", r.Coordinator, r.CoordinatorSource)
		}
		if len(r.Specialists) != 2 {
			t.Fatalf("specialists = %v, want 2 excluding coordinator", r.Specialists)
		}
		for _, s := range r.Specialists {
			if s == "Manager" {
				t.Fatalf("coordinator leaked into specialists: %v", r.Specialists)
			}
		}
	})

	t.Run("single agent has no specialists", func(t *testing.T) {
		ws := memberWorkspace("Solo")
		r := ws.BuildCoordinatorRoster()
		if r.Coordinator != "Solo" || r.CoordinatorSource != CoordinatorSourceSingleAgentDefault || len(r.Specialists) != 0 {
			t.Fatalf("roster = %+v, want Solo/single_agent_default/no specialists", r)
		}
	})

	t.Run("missing coordinator keeps all members as specialists", func(t *testing.T) {
		ws := memberWorkspace("Writer", "Researcher")
		r := ws.BuildCoordinatorRoster()
		if r.Coordinator != "" || r.CoordinatorSource != CoordinatorSourceMissing || len(r.Specialists) != 2 {
			t.Fatalf("roster = %+v, want empty coordinator/missing/2 specialists", r)
		}
	})
}

// --- HTTP handler provenance (2.3) ---

type taskProvenanceResponse struct {
	Task struct {
		To             string `json:"to"`
		AssignedBy     string `json:"assigned_by"`
		AssignmentMode string `json:"assignment_mode"`
	} `json:"task"`
}

func TestCreateTaskStampsManualProvenance(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	handler := NewHTTPHandler(store, nil, nil)

	ws := memberWorkspace("Writer")
	ws.ID = "ws-create"
	ws.Name = "Create"
	ws.Status = StatusActive
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-create/tasks",
		strings.NewReader(`{"description":"write a post","to":"Writer"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp taskProvenanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.To != "Writer" || resp.Task.AssignmentMode != string(TaskAssignmentModeManual) ||
		resp.Task.AssignedBy != TaskAssignedByManual {
		t.Fatalf("unexpected provenance: %+v", resp.Task)
	}
}

func TestCreateTaskRejectsNonMemberAssignee(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	handler := NewHTTPHandler(store, nil, nil)

	ws := memberWorkspace("Writer")
	ws.ID = "ws-reject"
	ws.Name = "Reject"
	ws.Status = StatusActive
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-reject/tasks",
		strings.NewReader(`{"description":"do it","to":"Ghost"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateTask(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateTaskReassignmentStampsManualProvenance(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	handler := NewHTTPHandler(store, nil, nil)

	ws := memberWorkspace("Writer", "Researcher")
	ws.ID = "ws-update"
	ws.Name = "Update"
	ws.Status = StatusActive
	// Seed a task that looks coordinator-assigned, to prove reassignment overrides it.
	ws.Tasks = []Task{{
		ID:             "task-1",
		WorkspaceID:    "ws-update",
		Description:    "reassign me",
		To:             "Writer",
		AssignedBy:     "Manager",
		AssignmentMode: TaskAssignmentModeStaticPlan,
		Status:         TaskStatusPending,
	}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-update/tasks/task-1",
		strings.NewReader(`{"to":"Researcher"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.UpdateTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp taskProvenanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Task.To != "Researcher" || resp.Task.AssignmentMode != string(TaskAssignmentModeManual) ||
		resp.Task.AssignedBy != TaskAssignedByManual {
		t.Fatalf("reassignment did not stamp manual provenance: %+v", resp.Task)
	}
}
