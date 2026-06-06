package agentcomm

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func delegationWorkspace(id string, names ...string) *workspace.Workspace {
	insts := make([]workspace.AgentInstance, 0, len(names))
	for i, n := range names {
		insts = append(insts, workspace.AgentInstance{
			Name:       n,
			NodeID:     n + "-node-1",
			EntryPoint: i == 0, // first agent is the coordinator/entry agent
		})
	}
	return &workspace.Workspace{
		ID:             id,
		Status:         workspace.StatusActive,
		AgentInstances: insts,
		Agents:         append([]string(nil), names...),
	}
}

func TestDelegateTaskStampsDynamicDelegationProvenance(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := delegationWorkspace("ws-1", "Manager", "Writer")
	// The triggering parent task must exist; the task graph validates parent refs.
	ws.Tasks = []workspace.Task{{
		ID:          "parent-1",
		WorkspaceID: "ws-1",
		Description: "parent task",
		Status:      workspace.TaskStatusInProgress,
	}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	c := NewCommunicator(store)
	task, err := c.DelegateTask(DelegationRequest{
		WorkspaceID:  "ws-1",
		From:         "Manager",
		To:           "Writer",
		Description:  "write a section",
		ParentTaskID: "parent-1",
		Reason:       "Writer is the specialist",
	})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}

	if task.To != "Writer" {
		t.Fatalf("To = %q, want Writer", task.To)
	}
	if task.AssignmentMode != workspace.TaskAssignmentModeDynamicDelegation {
		t.Fatalf("AssignmentMode = %q, want dynamic_delegation", task.AssignmentMode)
	}
	if task.AssignedBy != "Manager" {
		t.Fatalf("AssignedBy = %q, want Manager (the delegating coordinator)", task.AssignedBy)
	}
	if task.ParentTaskID != "parent-1" {
		t.Fatalf("ParentTaskID = %q, want parent-1", task.ParentTaskID)
	}
	if task.AssignmentReason != "Writer is the specialist" {
		t.Fatalf("AssignmentReason = %q", task.AssignmentReason)
	}
}

func TestDelegateTaskRejectsNonMemberTarget(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.Save(delegationWorkspace("ws-2", "Manager")); err != nil {
		t.Fatalf("save: %v", err)
	}

	c := NewCommunicator(store)
	if _, err := c.DelegateTask(DelegationRequest{
		WorkspaceID: "ws-2",
		From:        "Manager",
		To:          "Ghost",
		Description: "x",
	}); err == nil {
		t.Fatal("expected error delegating to a non-member target, got nil")
	}
}
