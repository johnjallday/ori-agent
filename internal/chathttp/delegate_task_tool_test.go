package chathttp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func delegateToolWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		ID:     "ws-1",
		Status: workspace.StatusActive,
		AgentInstances: []workspace.AgentInstance{
			{Name: "Manager", NodeID: "manager-node-1", EntryPoint: true},
			{Name: "Writer", NodeID: "writer-node-1"},
		},
	}
}

func toolNames(p *WorkspaceToolProvider) map[string]bool {
	names := map[string]bool{}
	for _, tl := range p.Tools() {
		names[tl.Definition().Name] = true
	}
	return names
}

func TestDelegateTaskToolGatedToCoordinator(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(delegateToolWorkspace()); err != nil {
		t.Fatalf("save: %v", err)
	}

	coord := NewWorkspaceToolProvider(nil, store, "ws-1")
	coord.SetExecutingAgent("Manager")
	if !toolNames(coord)["delegate_task"] {
		t.Fatal("coordinator should be offered delegate_task")
	}

	specialist := NewWorkspaceToolProvider(nil, store, "ws-1")
	specialist.SetExecutingAgent("Writer")
	if toolNames(specialist)["delegate_task"] {
		t.Fatal("specialist must NOT be offered delegate_task (single-level enforcement)")
	}

	unknown := NewWorkspaceToolProvider(nil, store, "ws-1")
	if toolNames(unknown)["delegate_task"] {
		t.Fatal("provider with no executing agent must not expose delegate_task")
	}
}

func TestDelegateTaskToolCreatesSubtask(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := delegateToolWorkspace()
	ws.Tasks = []workspace.Task{{
		ID:          "p1",
		WorkspaceID: "ws-1",
		Description: "parent",
		Status:      workspace.TaskStatusInProgress,
	}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	p := NewWorkspaceToolProvider(nil, store, "ws-1")
	p.SetExecutingAgent("Manager")
	p.SetTaskID("p1")

	out, err := p.delegateTaskTool().Call(context.Background(),
		`{"agent":"Writer","instructions":"write the intro","reason":"Writer is the specialist"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode tool result: %v (out=%s)", err, out)
	}
	if res["error"] != nil {
		t.Fatalf("unexpected tool error: %v", res["error"])
	}
	if res["assigned_to"] != "Writer" || res["assignment_mode"] != "dynamic_delegation" {
		t.Fatalf("unexpected provenance in result: %v", res)
	}
	delegatedID, _ := res["delegated_task_id"].(string)
	if delegatedID == "" {
		t.Fatal("expected a delegated_task_id")
	}

	// Verify the subtask was persisted with parent link + provenance.
	reloaded, err := store.Get("ws-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var found *workspace.Task
	for i := range reloaded.Tasks {
		if reloaded.Tasks[i].ID == delegatedID {
			found = &reloaded.Tasks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("delegated task %s not persisted", delegatedID)
	}
	if found.ParentTaskID != "p1" || found.To != "Writer" ||
		found.AssignmentMode != workspace.TaskAssignmentModeDynamicDelegation || found.AssignedBy != "Manager" {
		t.Fatalf("persisted subtask has wrong fields: %+v", found)
	}
}
