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
