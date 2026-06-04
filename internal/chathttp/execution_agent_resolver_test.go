package chathttp

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type executionAgentSessionLookupStub struct {
	sessions map[string]*session.Session
}

func (s *executionAgentSessionLookupStub) GetSession(_ context.Context, id string) (*session.Session, error) {
	if s == nil {
		return nil, session.ErrSessionNotFound
	}
	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}
	return nil, session.ErrSessionNotFound
}

type workspaceEntryLookupStub struct {
	workspaces map[string]*workspace.Workspace
	// snapshots maps workspaceID -> agentName -> on-disk agent snapshot.
	snapshots map[string]map[string]*agent.Agent
}

func (s *workspaceEntryLookupStub) Get(id string) (*workspace.Workspace, error) {
	if s == nil {
		return nil, errors.New("workspace lookup unavailable")
	}
	if ws, ok := s.workspaces[id]; ok {
		return ws, nil
	}
	return nil, errors.New("workspace not found")
}

func (s *workspaceEntryLookupStub) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	if byWorkspace, ok := s.snapshots[workspaceID]; ok {
		if ag, ok := byWorkspace[agentName]; ok {
			return ag, true, nil
		}
	}
	return nil, false, nil
}

// newWorkspaceWithEntryAgent builds a workspace whose entry agent is the named
// agent, via an entry-point agent instance.
func newWorkspaceWithEntryAgent(entryAgent string) *workspace.Workspace {
	return &workspace.Workspace{
		AgentInstances: []workspace.AgentInstance{
			{Name: entryAgent, EntryPoint: true},
		},
		Agents: []string{entryAgent},
	}
}

func TestResolveExecutionAgentName_PrefersSessionBinding(t *testing.T) {
	sessionLookup := &executionAgentSessionLookupStub{
		sessions: map[string]*session.Session{
			"sess-1": {AgentName: "Session Agent"},
		},
	}
	agentStore := newPreflightStore("Session Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), sessionLookup, agentStore, nil, "sess-1", "Requested Agent", "")

	if resolution.Name != "Session Agent" {
		t.Fatalf("expected session-bound agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceSessionBinding {
		t.Fatalf("expected source %q, got %q", executionAgentSourceSessionBinding, resolution.Source)
	}
	if resolution.usesCompatibilityFallback() {
		t.Fatal("did not expect compatibility fallback for session binding")
	}
}

func TestResolveExecutionAgentName_UsesRequestOverrideBeforeCompatibilityFallback(t *testing.T) {
	agentStore := newPreflightStore("Requested Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "Requested Agent", "")

	if resolution.Name != "Requested Agent" {
		t.Fatalf("expected request agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceRequestOverride {
		t.Fatalf("expected source %q, got %q", executionAgentSourceRequestOverride, resolution.Source)
	}
	if resolution.usesCompatibilityFallback() {
		t.Fatal("did not expect compatibility fallback for request override")
	}
}

func TestResolveExecutionAgentName_SkipsMissingSessionBindingAndUsesRequestOverride(t *testing.T) {
	sessionLookup := &executionAgentSessionLookupStub{
		sessions: map[string]*session.Session{
			"sess-1": {AgentName: "Missing Agent"},
		},
	}
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			"Requested Agent":           {},
			assistantExecutionAgentName: {},
		},
		names: []string{"Requested Agent", assistantExecutionAgentName},
	}

	resolution := resolveExecutionAgentName(context.Background(), sessionLookup, agentStore, nil, "sess-1", "Requested Agent", "")

	if resolution.Name != "Requested Agent" {
		t.Fatalf("expected request override after stale session binding, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceRequestOverride {
		t.Fatalf("expected source %q, got %q", executionAgentSourceRequestOverride, resolution.Source)
	}
}

func TestResolveExecutionAgentName_SkipsMissingSessionBindingAndFallsBackToAssistant(t *testing.T) {
	sessionLookup := &executionAgentSessionLookupStub{
		sessions: map[string]*session.Session{
			"sess-1": {AgentName: "Missing Agent"},
		},
	}
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
		},
		names: []string{assistantExecutionAgentName},
	}

	resolution := resolveExecutionAgentName(context.Background(), sessionLookup, agentStore, nil, "sess-1", "", "")

	if resolution.Name != assistantExecutionAgentName {
		t.Fatalf("expected assistant fallback after stale session binding, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceAssistantDefault {
		t.Fatalf("expected source %q, got %q", executionAgentSourceAssistantDefault, resolution.Source)
	}
}

func TestResolveExecutionAgentName_SkipsMissingRequestOverrideAndFallsBackToAssistant(t *testing.T) {
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
		},
		names: []string{assistantExecutionAgentName},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "Missing Agent", "")

	if resolution.Name != assistantExecutionAgentName {
		t.Fatalf("expected assistant fallback after stale request override, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceAssistantDefault {
		t.Fatalf("expected source %q, got %q", executionAgentSourceAssistantDefault, resolution.Source)
	}
}

func TestResolveExecutionAgentName_PrefersAssistantDefaultOverCurrentAgent(t *testing.T) {
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
			"Specialist":                {},
		},
		names: []string{"Specialist", assistantExecutionAgentName},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "", "")

	if resolution.Name != assistantExecutionAgentName {
		t.Fatalf("expected Assistant runtime %q, got %q", assistantExecutionAgentName, resolution.Name)
	}
	if resolution.Source != executionAgentSourceAssistantDefault {
		t.Fatalf("expected source %q, got %q", executionAgentSourceAssistantDefault, resolution.Source)
	}
	if !resolution.isAssistantMode() {
		t.Fatal("expected Assistant default to be treated as Assistant mode")
	}
}

func TestResolveExecutionAgentName_ReturnsUnresolvedWhenAssistantIsMissing(t *testing.T) {
	agentStore := newPreflightStore("Fallback Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, nil, "", "", "")

	if resolution.isResolved() {
		t.Fatalf("expected unresolved result, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceUnavailable {
		t.Fatalf("expected source %q, got %q", executionAgentSourceUnavailable, resolution.Source)
	}
}

func TestResolveExecutionAgentName_ReturnsUnresolvedWhenNoAgentIsAvailable(t *testing.T) {
	resolution := resolveExecutionAgentName(context.Background(), nil, nil, nil, "", "", "")

	if resolution.isResolved() {
		t.Fatalf("expected unresolved result, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceUnavailable {
		t.Fatalf("expected source %q, got %q", executionAgentSourceUnavailable, resolution.Source)
	}
}

func TestResolveExecutionAgentName_WorkspaceDefaultsToEntryAgent(t *testing.T) {
	// Both the entry agent and the global assistant exist in the store; inside a
	// workspace the entry agent must win.
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			"Workspace Manager":         {},
			assistantExecutionAgentName: {},
		},
		names: []string{"Workspace Manager", assistantExecutionAgentName},
	}
	wsLookup := &workspaceEntryLookupStub{
		workspaces: map[string]*workspace.Workspace{
			"ws-1": newWorkspaceWithEntryAgent("Workspace Manager"),
		},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, wsLookup, "", "", "ws-1")

	if resolution.Name != "Workspace Manager" {
		t.Fatalf("expected workspace entry agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceWorkspaceEntry {
		t.Fatalf("expected source %q, got %q", executionAgentSourceWorkspaceEntry, resolution.Source)
	}
	if resolution.isAssistantMode() {
		t.Fatal("workspace entry agent must not be treated as Assistant mode")
	}
	if !resolution.isWorkspaceEntryDefault() {
		t.Fatal("expected isWorkspaceEntryDefault to be true")
	}
}

func TestResolveExecutionAgentName_WorkspaceNeverFallsBackToAssistant(t *testing.T) {
	// The global assistant exists, but the workspace's entry agent is missing
	// from the store AND has no on-disk snapshot. Inside a workspace we must NOT
	// silently answer as Ori.
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
		},
		names: []string{assistantExecutionAgentName},
	}
	wsLookup := &workspaceEntryLookupStub{
		workspaces: map[string]*workspace.Workspace{
			"ws-1": newWorkspaceWithEntryAgent("Missing Manager"),
		},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, wsLookup, "", "", "ws-1")

	if resolution.isResolved() {
		t.Fatalf("expected unresolved inside workspace with missing entry agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceUnavailable {
		t.Fatalf("expected source %q, got %q", executionAgentSourceUnavailable, resolution.Source)
	}
}

func TestResolveExecutionAgentName_WorkspaceResolvesEntryAgentFromSnapshot(t *testing.T) {
	// The entry agent is absent from the global registry but the workspace has
	// an on-disk snapshot. The chat runtime resolves workspace agents
	// snapshot-first, so the entry agent must still resolve (not be reported as
	// missing) — this is the testjdas/test123 case.
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
		},
		names: []string{assistantExecutionAgentName},
	}
	wsLookup := &workspaceEntryLookupStub{
		workspaces: map[string]*workspace.Workspace{
			"ws-1": newWorkspaceWithEntryAgent("Workspace Manager"),
		},
		snapshots: map[string]map[string]*agent.Agent{
			"ws-1": {"Workspace Manager": {}},
		},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, wsLookup, "", "", "ws-1")

	if resolution.Name != "Workspace Manager" {
		t.Fatalf("expected snapshot-backed entry agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceWorkspaceEntry {
		t.Fatalf("expected source %q, got %q", executionAgentSourceWorkspaceEntry, resolution.Source)
	}
	if resolution.isAssistantMode() {
		t.Fatal("snapshot-backed entry agent must not be treated as Assistant mode")
	}
}

func TestResolveExecutionAgentName_WorkspaceSessionBindingWinsOverEntryAgent(t *testing.T) {
	sessionLookup := &executionAgentSessionLookupStub{
		sessions: map[string]*session.Session{
			"sess-1": {AgentName: "Pinned Specialist"},
		},
	}
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			"Pinned Specialist": {},
			"Workspace Manager": {},
		},
		names: []string{"Pinned Specialist", "Workspace Manager"},
	}
	wsLookup := &workspaceEntryLookupStub{
		workspaces: map[string]*workspace.Workspace{
			"ws-1": newWorkspaceWithEntryAgent("Workspace Manager"),
		},
	}

	resolution := resolveExecutionAgentName(context.Background(), sessionLookup, agentStore, wsLookup, "sess-1", "", "ws-1")

	if resolution.Name != "Pinned Specialist" {
		t.Fatalf("expected pinned session agent to win over entry agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceSessionBinding {
		t.Fatalf("expected source %q, got %q", executionAgentSourceSessionBinding, resolution.Source)
	}
}
