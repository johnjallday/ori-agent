package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

func TestResolveExecutionAgent_BlocksWhenWorkspaceAgentDefinitionIsMissing(t *testing.T) {
	ws := &Workspace{
		ID:     "ws-missing-agent",
		Name:   "west new york",
		Agents: []string{"west new york Manager"},
		AgentInstances: []AgentInstance{
			{
				ID:         "inst-1",
				Name:       "west new york Manager",
				NodeID:     "west new york Manager-node-1",
				EntryPoint: true,
			},
		},
	}

	handler := &LLMTaskHandler{
		agentStore: &resolverAgentStoreStub{
			agents: map[string]*agent.Agent{
				"Ori": {},
			},
		},
		workspaceStore: newTestWorkspaceStore(t, ws),
	}

	_, err := handler.resolveExecutionAgent("west new york Manager", Task{
		ID:             "task-1",
		WorkspaceID:    ws.ID,
		AssignedNodeID: "west new york Manager-node-1",
	})
	if err == nil {
		t.Fatal("expected missing workspace agent to block execution")
	}

	blockedErr, ok := AsTaskBlockedError(err)
	if !ok {
		t.Fatalf("expected TaskBlockedError, got %v", err)
	}
	if blockedErr.ReasonCode != "assigned_agent_missing" {
		t.Fatalf("expected reason code assigned_agent_missing, got %q", blockedErr.ReasonCode)
	}
	if !strings.Contains(blockedErr.Reason, "workspace agent") {
		t.Fatalf("expected workspace-specific reason, got %q", blockedErr.Reason)
	}
	if !strings.Contains(blockedErr.Question, "Switch to another agent") {
		t.Fatalf("expected recovery guidance in question, got %q", blockedErr.Question)
	}
	if len(blockedErr.SuggestedActions) != 2 || blockedErr.SuggestedActions[0] != "switch_agent_retry" {
		t.Fatalf("expected switch_agent_retry suggested action first, got %#v", blockedErr.SuggestedActions)
	}
}
