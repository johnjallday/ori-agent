package workspace

import "testing"

func TestResolveCoordinator(t *testing.T) {
	tests := []struct {
		name       string
		ws         *Workspace
		wantName   string
		wantSource CoordinatorSource
	}{
		{
			name: "explicit entry agent from shared data",
			ws: &Workspace{
				SharedData: map[string]any{sharedDataEntryAgentNameKey: "Manager"},
				AgentInstances: []AgentInstance{
					{Name: "Manager", NodeID: "manager-node-1"},
					{Name: "Writer", NodeID: "writer-node-1"},
				},
			},
			wantName:   "Manager",
			wantSource: CoordinatorSourceExplicitEntryAgent,
		},
		{
			name: "explicit entry agent from instance entry point",
			ws: &Workspace{
				AgentInstances: []AgentInstance{
					{Name: "Writer", NodeID: "writer-node-1"},
					{Name: "Manager", NodeID: "manager-node-1", EntryPoint: true},
				},
			},
			wantName:   "Manager",
			wantSource: CoordinatorSourceExplicitEntryAgent,
		},
		{
			name: "single agent default",
			ws: &Workspace{
				AgentInstances: []AgentInstance{
					{Name: "Solo", NodeID: "solo-node-1"},
				},
			},
			wantName:   "Solo",
			wantSource: CoordinatorSourceSingleAgentDefault,
		},
		{
			name: "multi agent missing entry",
			ws: &Workspace{
				AgentInstances: []AgentInstance{
					{Name: "Writer", NodeID: "writer-node-1"},
					{Name: "Researcher", NodeID: "researcher-node-1"},
				},
			},
			wantName:   "",
			wantSource: CoordinatorSourceMissing,
		},
		{
			name: "stale shared-data entry is ignored when not a member",
			ws: &Workspace{
				SharedData: map[string]any{sharedDataEntryAgentNameKey: "GhostManager"},
				AgentInstances: []AgentInstance{
					{Name: "Writer", NodeID: "writer-node-1"},
					{Name: "Researcher", NodeID: "researcher-node-1"},
				},
			},
			wantName:   "",
			wantSource: CoordinatorSourceMissing,
		},
		{
			name:       "empty workspace",
			ws:         &Workspace{},
			wantName:   "",
			wantSource: CoordinatorSourceMissing,
		},
		{
			name:       "nil workspace",
			ws:         nil,
			wantName:   "",
			wantSource: CoordinatorSourceMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotSource := tt.ws.ResolveCoordinator()
			if gotName != tt.wantName || gotSource != tt.wantSource {
				t.Fatalf("ResolveCoordinator() = (%q, %q), want (%q, %q)",
					gotName, gotSource, tt.wantName, tt.wantSource)
			}
		})
	}
}

// TestResolveCoordinatorDoesNotFallBackToFirstAgent guards the key difference
// from EntryAgentName(): a multi-agent workspace with no explicit entry agent
// must report "missing" rather than silently picking the first agent.
func TestResolveCoordinatorDoesNotFallBackToFirstAgent(t *testing.T) {
	ws := &Workspace{
		Agents: []string{"Alpha", "Beta"},
		AgentInstances: []AgentInstance{
			{Name: "Alpha", NodeID: "alpha-node-1"},
			{Name: "Beta", NodeID: "beta-node-1"},
		},
	}

	if got := ws.EntryAgentName(); got != "Alpha" {
		t.Fatalf("precondition: EntryAgentName() = %q, want legacy first-agent fallback %q", got, "Alpha")
	}

	name, source := ws.ResolveCoordinator()
	if name != "" || source != CoordinatorSourceMissing {
		t.Fatalf("ResolveCoordinator() = (%q, %q), want (\"\", %q)", name, source, CoordinatorSourceMissing)
	}
}
