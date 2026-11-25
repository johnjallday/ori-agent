package chathttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Local work goes to the workspace's own manager. These tests pin the routing
// contract for workspace-owned managers: a request inside one workspace cannot
// pick another workspace's manager, a manager carries none of the former
// protected assistant's chat carve-outs, and two agents with similar labels
// stay distinct.

func managerAgent(workspaceID string) *agent.Agent {
	return &agent.Agent{WorkspaceManager: &agent.WorkspaceManagerProvenance{WorkspaceID: workspaceID}}
}

func twoManagerFixture() (*preflightStore, *workspaceEntryLookupStub) {
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			"House Manager":   managerAgent("ws-house"),
			"House Manager 2": managerAgent("ws-other"),
		},
		names: []string{"House Manager", "House Manager 2"},
	}
	lookup := &workspaceEntryLookupStub{workspaces: map[string]*workspace.Workspace{
		"ws-house": newWorkspaceWithEntryAgent("House Manager"),
		"ws-other": newWorkspaceWithEntryAgent("House Manager 2"),
	}}
	return agentStore, lookup
}

func TestWorkspaceManagerRouting_EachWorkspaceReachesOnlyItsOwnManager(t *testing.T) {
	agentStore, lookup := twoManagerFixture()

	for workspaceID, want := range map[string]string{"ws-house": "House Manager", "ws-other": "House Manager 2"} {
		got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "", workspaceID)
		if got.Name != want || got.Source != executionAgentSourceWorkspaceEntry {
			t.Fatalf("workspace %s resolved %#v, want its own manager %q", workspaceID, got, want)
		}
		if got.isAssistantMode() {
			t.Fatalf("workspace %s manager must not be treated as a global assistant", workspaceID)
		}
	}
}

func TestWorkspaceManagerRouting_RequestCannotChooseAnotherWorkspacesManager(t *testing.T) {
	agentStore, lookup := twoManagerFixture()

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager 2", "ws-house")
	if got.Name != "House Manager" || got.Source != executionAgentSourceWorkspaceEntry {
		t.Fatalf("a request in ws-house naming ws-other's manager resolved %#v; want ws-house's own entry", got)
	}
}

func TestWorkspaceManagerRouting_AssignedManagerIsAddressableInItsNewWorkspace(t *testing.T) {
	agentStore, lookup := twoManagerFixture()
	// The person deliberately put ws-other's manager on ws-house's roster.
	lookup.workspaces["ws-house"].AgentInstances = append(lookup.workspaces["ws-house"].AgentInstances,
		workspace.AgentInstance{Name: "House Manager 2"})

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager 2", "ws-house")
	if got.Name != "House Manager 2" || got.Source != executionAgentSourceRequestOverride {
		t.Fatalf("an assigned manager resolved %#v; want the explicit roster choice", got)
	}
}

func TestWorkspaceManagerRouting_DirectChatOutsideWorkspaceStillReachesManager(t *testing.T) {
	agentStore, lookup := twoManagerFixture()

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager", "")
	if got.Name != "House Manager" || got.Source != executionAgentSourceRequestOverride {
		t.Fatalf("direct chat with a saved manager resolved %#v", got)
	}
}

func TestWorkspaceManagerRouting_MissingManagerNeverFallsBackAcrossWorkspaces(t *testing.T) {
	agentStore, lookup := twoManagerFixture()
	// ws-empty was created agentless; another workspace's manager exists.
	lookup.workspaces["ws-empty"] = &workspace.Workspace{}

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "", "ws-empty")
	if got.isResolved() || got.Source != executionAgentSourceUnavailable {
		t.Fatalf("an unstaffed workspace resolved %#v; want unresolved, never another workspace's manager", got)
	}
}

func TestWorkspaceManagerRouting_ManagerHasNoProtectedAssistantCarveOuts(t *testing.T) {
	for _, name := range []string{"House Manager", "Manager", "House Manager 2"} {
		if isSystemAssistantForPreflight(name) {
			t.Fatalf("%q must not get the protected assistant's MCP preflight carve-out", name)
		}
		if (executionAgentResolution{Name: name, Source: executionAgentSourceRequestOverride}).isAssistantMode() {
			t.Fatalf("%q must not be treated as the global assistant", name)
		}
	}
}
