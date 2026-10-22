package workspace

import "testing"

func delegationPolicyWorkspace(id string, policy AutonomyPolicy) *Workspace {
	return &Workspace{
		ID:             id,
		Status:         StatusActive,
		AutonomyPolicy: policy,
		AgentInstances: []AgentInstance{{Name: "Writer", NodeID: "writer-node-1"}},
	}
}

func TestDelegatedSubtaskAutonomyGate(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(delegationPolicyWorkspace("ws", AutonomyWatch)); err != nil {
		t.Fatalf("save: %v", err)
	}
	h := &LLMTaskHandler{workspaceStore: store}

	delegated := Task{WorkspaceID: "ws", AssignmentMode: TaskAssignmentModeDynamicDelegation}
	manual := Task{WorkspaceID: "ws", AssignmentMode: TaskAssignmentModeManual}

	if h.delegatedSubtaskAutonomyPolicy(delegated) != AutonomyWatch {
		t.Fatal("a delegated subtask should inherit the workspace Watch policy")
	}
	if h.delegatedSubtaskAutonomyPolicy(manual) != "" {
		t.Fatal("a non-delegated task must not be gated")
	}

	if err := h.evaluateExecutionAutonomyGate(delegated, "delete_record"); err == nil {
		t.Fatal("Watch must block an unclassified/write tool inside a delegated subtask")
	}
	if err := h.evaluateExecutionAutonomyGate(delegated, "get_record"); err != nil {
		t.Fatalf("Watch must allow a read tool: %v", err)
	}
	if err := h.evaluateExecutionAutonomyGate(manual, "delete_record"); err != nil {
		t.Fatalf("a manual (non-delegated) task must not be gated: %v", err)
	}
}

// A task that asks for the workspace's gate (one started from a folder's first
// look) runs under the workspace's policy, and under Watch when the workspace
// has none or cannot be read: it reads and reports, and changes nothing.
func TestTaskAskingForTheWorkspaceGateRunsUnderWatch(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(delegationPolicyWorkspace("unset", "")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(delegationPolicyWorkspace("propose", AutonomyPropose)); err != nil {
		t.Fatal(err)
	}
	h := &LLMTaskHandler{workspaceStore: store}
	asked := func(workspaceID string) Task {
		return Task{WorkspaceID: workspaceID, AssignmentMode: TaskAssignmentModeManual,
			Context: map[string]any{TaskContextAutonomyGate: TaskAutonomyGateWorkspace}}
	}
	for _, id := range []string{"unset", "missing"} {
		if got := h.delegatedSubtaskAutonomyPolicy(asked(id)); got != AutonomyWatch {
			t.Fatalf("%s: policy = %q, want watch", id, got)
		}
		if err := h.evaluateExecutionAutonomyGate(asked(id), "delete_record"); err == nil {
			t.Fatalf("%s: Watch must block a write", id)
		}
		if err := h.evaluateExecutionAutonomyGate(asked(id), "get_record"); err != nil {
			t.Fatalf("%s: Watch must allow a read: %v", id, err)
		}
	}
	if got := h.delegatedSubtaskAutonomyPolicy(asked("propose")); got != AutonomyPropose {
		t.Fatalf("a workspace's own policy applies: %q", got)
	}
}

func TestDelegatedSubtaskGateNotAppliedWithoutPolicy(t *testing.T) {
	store := NewInMemoryStore()
	if err := store.Save(delegationPolicyWorkspace("ws", "")); err != nil {
		t.Fatalf("save: %v", err)
	}
	h := &LLMTaskHandler{workspaceStore: store}

	delegated := Task{WorkspaceID: "ws", AssignmentMode: TaskAssignmentModeDynamicDelegation}
	if err := h.evaluateExecutionAutonomyGate(delegated, "delete_record"); err != nil {
		t.Fatalf("with no workspace policy the delegation gate must not apply: %v", err)
	}
}
