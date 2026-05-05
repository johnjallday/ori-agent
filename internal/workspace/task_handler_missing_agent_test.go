package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

func TestResolveExecutionAgent_UsesWorkspaceSnapshotWhenGlobalMissing(t *testing.T) {
	ws := &Workspace{
		ID:     "ws-local-agent",
		Name:   "Imported",
		Agents: []string{"Imported Manager"},
		AgentInstances: []AgentInstance{
			{
				ID:         "inst-1",
				Name:       "Imported Manager",
				NodeID:     "imported-manager-node-1",
				EntryPoint: true,
			},
		},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
	localAgent := &agent.Agent{Type: agent.TypeToolCalling}
	localAgent.Settings.Model = "workspace-local-model"
	if err := workspaceStore.SaveWorkspaceAgent(ws.ID, "Imported Manager", localAgent); err != nil {
		t.Fatalf("seed workspace-local agent: %v", err)
	}

	handler := &LLMTaskHandler{
		agentStore:     &resolverAgentStoreStub{agents: map[string]*agent.Agent{}},
		workspaceStore: workspaceStore,
	}

	resolved, err := handler.resolveExecutionAgent("Imported Manager", Task{
		ID:             "task-1",
		WorkspaceID:    ws.ID,
		AssignedNodeID: "imported-manager-node-1",
	})
	if err != nil {
		t.Fatalf("expected workspace-local snapshot to resolve, got error: %v", err)
	}
	if resolved == nil || resolved.Agent == nil {
		t.Fatal("expected resolved agent, got nil")
	}
	if resolved.Agent.Settings.Model != "workspace-local-model" {
		t.Fatalf("expected workspace-local model, got %q", resolved.Agent.Settings.Model)
	}
}

func TestResolveExecutionAgent_DoesNotReportMissingWhenWorkspaceSnapshotExists(t *testing.T) {
	ws := &Workspace{
		ID:     "ws-runtime-error",
		Name:   "Runtime Error",
		Agents: []string{"Runtime Manager"},
		AgentInstances: []AgentInstance{
			{
				ID:         "inst-1",
				Name:       "Runtime Manager",
				NodeID:     "runtime-manager-node-1",
				EntryPoint: true,
			},
		},
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-1", Path: t.TempDir()},
		},
	}
	workspaceStore := newTestWorkspaceStore(t, ws)
	localAgent := &agent.Agent{Type: agent.TypeToolCalling}
	localAgent.Settings.Model = "workspace-local-model"
	if err := workspaceStore.SaveWorkspaceAgent(ws.ID, "Runtime Manager", localAgent); err != nil {
		t.Fatalf("seed workspace-local agent: %v", err)
	}
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{}}

	handler := &LLMTaskHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		runtimeResolver: NewAgentRuntimeResolver(
			agentStore,
			workspaceStore,
			&runtimeRegistryStub{},
			&templateLookupStub{servers: map[string]mcp.ServerConfig{}},
		),
	}

	_, err := handler.resolveExecutionAgent("Runtime Manager", Task{
		ID:             "task-1",
		WorkspaceID:    ws.ID,
		AssignedNodeID: "runtime-manager-node-1",
	})
	if err == nil {
		t.Fatal("expected runtime template error")
	}
	if _, ok := AsTaskBlockedError(err); ok {
		t.Fatalf("expected original runtime error, got TaskBlockedError: %v", err)
	}
	if !strings.Contains(err.Error(), "load MCP template filesystem") {
		t.Fatalf("expected filesystem template error, got %v", err)
	}
}

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
