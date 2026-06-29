package workspace

import (
	"context"
	"testing"
)

// subtaskCreatingExecutor simulates a coordinator run: when executed it appends
// the configured subtasks to the workspace (as delegate_task would) and returns
// a result, so the adapter's before/after detection can be exercised.
type subtaskCreatingExecutor struct {
	store   Store
	wsID    string
	created []string
	result  string
}

func (e *subtaskCreatingExecutor) ExecuteTask(_ context.Context, _ string, _ Task) (string, error) {
	if len(e.created) > 0 {
		ws, err := e.store.Get(e.wsID)
		if err != nil {
			return "", err
		}
		for _, id := range e.created {
			ws.Tasks = append(ws.Tasks, Task{ID: id, WorkspaceID: e.wsID, To: "Writer", Description: "sub " + id})
		}
		if err := e.store.Save(ws); err != nil {
			return "", err
		}
	}
	return e.result, nil
}

func adapterWorkspace() *Workspace {
	return &Workspace{
		ID:     "ws",
		Status: StatusActive,
		AgentInstances: []AgentInstance{
			{Name: "Manager", NodeID: "manager-node-1", EntryPoint: true},
			{Name: "Writer", NodeID: "writer-node-1"},
		},
		Tasks: []Task{{ID: "f1", WorkspaceID: "ws", Description: "do x", Status: TaskStatusFailed}},
	}
}

func TestCoordinatorAdapterDetectsDelegatedSubtasks(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(adapterWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	adapter := NewCoordinatorAdapter(store, &subtaskCreatingExecutor{
		store: store, wsID: "ws", created: []string{"sub-a", "sub-b"},
	})

	res, err := adapter.Adapt(context.Background(), CoordinatorAdaptRequest{
		WorkspaceID: "ws",
		Coordinator: "Manager",
		FailedTask:  Task{ID: "f1", Description: "do x"},
		Trigger:     DelegationTrigger{Trigger: true, Code: DelegationTriggerFailed, Reason: "boom"},
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if res.Resolved {
		t.Fatalf("expected not resolved when subtasks were delegated, got %+v", res)
	}
	if len(res.DelegatedTaskIDs) != 2 || res.DelegatedTaskIDs[0] != "sub-a" || res.DelegatedTaskIDs[1] != "sub-b" {
		t.Fatalf("expected [sub-a sub-b] in order, got %v", res.DelegatedTaskIDs)
	}
}

func TestCoordinatorAdapterResolvesWhenNoDelegation(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(adapterWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}
	adapter := NewCoordinatorAdapter(store, &subtaskCreatingExecutor{
		store: store, wsID: "ws", result: "I handled it myself",
	})

	res, err := adapter.Adapt(context.Background(), CoordinatorAdaptRequest{
		WorkspaceID: "ws",
		Coordinator: "Manager",
		FailedTask:  Task{ID: "f1", Description: "do x"},
		Trigger:     DelegationTrigger{Trigger: true},
	})
	if err != nil {
		t.Fatalf("Adapt: %v", err)
	}
	if !res.Resolved || res.DirectResult != "I handled it myself" || len(res.DelegatedTaskIDs) != 0 {
		t.Fatalf("expected self-resolution, got %+v", res)
	}
}
