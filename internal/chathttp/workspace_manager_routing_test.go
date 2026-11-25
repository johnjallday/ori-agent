package chathttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
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
		got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "", workspaceID, "")
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

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager 2", "ws-house", "")
	if got.Name != "House Manager" || got.Source != executionAgentSourceWorkspaceEntry {
		t.Fatalf("a request in ws-house naming ws-other's manager resolved %#v; want ws-house's own entry", got)
	}
}

func TestWorkspaceManagerRouting_AssignedManagerIsAddressableInItsNewWorkspace(t *testing.T) {
	agentStore, lookup := twoManagerFixture()
	// The person deliberately put ws-other's manager on ws-house's roster.
	lookup.workspaces["ws-house"].AgentInstances = append(lookup.workspaces["ws-house"].AgentInstances,
		workspace.AgentInstance{Name: "House Manager 2"})

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager 2", "ws-house", "")
	if got.Name != "House Manager 2" || got.Source != executionAgentSourceRequestOverride {
		t.Fatalf("an assigned manager resolved %#v; want the explicit roster choice", got)
	}
}

func TestWorkspaceManagerRouting_DirectChatOutsideWorkspaceStillReachesManager(t *testing.T) {
	agentStore, lookup := twoManagerFixture()

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "House Manager", "", "")
	if got.Name != "House Manager" || got.Source != executionAgentSourceRequestOverride {
		t.Fatalf("direct chat with a saved manager resolved %#v", got)
	}
}

func TestWorkspaceManagerRouting_MissingManagerNeverFallsBackAcrossWorkspaces(t *testing.T) {
	agentStore, lookup := twoManagerFixture()
	// ws-empty was created agentless; another workspace's manager exists.
	lookup.workspaces["ws-empty"] = &workspace.Workspace{}

	got := resolveExecutionAgentName(context.Background(), nil, agentStore, lookup, "", "", "ws-empty", "")
	if got.isResolved() || got.Source != executionAgentSourceUnavailable {
		t.Fatalf("an unstaffed workspace resolved %#v; want unresolved, never another workspace's manager", got)
	}
}

// A Blank workspace's own manager greets the person as its manager and offers
// both ways in; it never asks a goal question that must be answered first.
func TestComposeLeadWelcome_OwnManagerOffersHelpOrWorkWithoutAnInterview(t *testing.T) {
	h, _, ws, _ := folderWelcomeFixture(t)
	ag, _ := h.store.GetAgent("cli-agent")
	ag.WorkspaceManager = &agent.WorkspaceManagerProvenance{WorkspaceID: ws.ID}

	intro, state := composeIntro(t, h, ws)
	if state != workspaceupdates.StateReady {
		t.Fatalf("state = %q, want ready", state)
	}
	for _, want := range []string{"Hi, I’m cli-agent, the manager for", "Tell me what you’d like done", "help work out what this workspace is for"} {
		if !strings.Contains(intro, want) {
			t.Errorf("missing %q in %q", want, intro)
		}
	}
	if strings.Contains(intro, goalQuestion) {
		t.Fatalf("a manager welcome asked the goal question: %q", intro)
	}

	// The same agent bound to another workspace is an ordinary lead here.
	ag.WorkspaceManager = &agent.WorkspaceManagerProvenance{WorkspaceID: "ws-elsewhere"}
	intro, _ = composeIntro(t, h, ws)
	if !strings.Contains(intro, "the lead for") || !strings.Contains(intro, goalQuestion) {
		t.Fatalf("a foreign manager must greet as an ordinary lead: %q", intro)
	}
}

// No name makes an agent the global assistant — not a manager's, and not the
// retired assistant's old names. Only the Personal Assistant route does.
func TestWorkspaceManagerRouting_NoNameGrantsAssistantMode(t *testing.T) {
	for _, name := range []string{"House Manager", "Manager", "House Manager 2", "Ask Ori", "Workspace Manager", "Ori"} {
		if (executionAgentResolution{Name: name, Source: executionAgentSourceRequestOverride}).isAssistantMode() {
			t.Fatalf("%q must not be treated as the global assistant", name)
		}
	}
}

// Outside a workspace, cross-workspace work goes to the configured Personal
// Assistant. Without one nothing answers — not a user agent named "Ask Ori",
// not the first agent in the list.
func TestGlobalRoute_GoesToThePersonalAssistantOrNowhere(t *testing.T) {
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{"Ask Ori": {}, "Atlas": {}, "Aardvark": {}},
		names:  []string{"Aardvark", "Ask Ori", "Atlas"},
	}
	got := resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "", "", "Atlas")
	if got.Name != "Atlas" || got.Source != executionAgentSourceAssistantDefault || !got.isAssistantMode() {
		t.Fatalf("global route with a Personal Assistant = %#v", got)
	}
	got = resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "", "", "")
	if got.isResolved() || got.Source != executionAgentSourceUnavailable {
		t.Fatalf("global route without a Personal Assistant = %#v; want unresolved", got)
	}
	// A configured name that no longer resolves is not replaced by anyone.
	got = resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "", "", "Gone")
	if got.isResolved() {
		t.Fatalf("a missing Personal Assistant was replaced by %q", got.Name)
	}
}
