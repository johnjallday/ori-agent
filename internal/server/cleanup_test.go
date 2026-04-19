package server

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestIsStaleWorkspaceManagerAgent(t *testing.T) {
	tests := []struct {
		name   string
		agent  *agent.Agent
		expect bool
	}{
		{
			name:   "nil agent",
			agent:  nil,
			expect: false,
		},
		{
			name:   "workspace-manager type",
			agent:  &agent.Agent{Type: "workspace-manager"},
			expect: true,
		},
		{
			name: "workspace-manager metadata tag",
			agent: &agent.Agent{
				Type:     "general",
				Metadata: &types.AgentMetadata{Tags: []string{"workspace-manager"}},
			},
			expect: true,
		},
		{
			name: "workspace-manager tag mixed case",
			agent: &agent.Agent{
				Type:     "general",
				Metadata: &types.AgentMetadata{Tags: []string{"Workspace-Manager"}},
			},
			expect: true,
		},
		{
			name:   "general agent no tags",
			agent:  &agent.Agent{Type: "general"},
			expect: false,
		},
		{
			name: "general agent with unrelated tags",
			agent: &agent.Agent{
				Type:     "general",
				Metadata: &types.AgentMetadata{Tags: []string{"travel", "specialist"}},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStaleWorkspaceManagerAgent(tt.agent); got != tt.expect {
				t.Fatalf("isStaleWorkspaceManagerAgent() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestCleanupStaleWorkspaceManagerAgents(t *testing.T) {
	dir := t.TempDir()
	agentStore, err := store.NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create a stale workspace-manager agent
	if err := agentStore.CreateAgent("Spain Manager", &store.CreateAgentConfig{
		Type: "workspace-manager",
		Role: "orchestrator",
	}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	// Create a normal agent that should survive
	if err := agentStore.CreateAgent("Travel Expert", &store.CreateAgentConfig{
		Type: "specialist",
		Role: "specialist",
	}); err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	s := &Server{
		Storage: &StorageSystemFacade{
			AgentStore: agentStore,
		},
	}

	s.cleanupStaleWorkspaceManagerAgents()

	if _, ok := agentStore.GetAgent("Spain Manager"); ok {
		t.Fatal("expected stale workspace-manager agent to be deleted")
	}
	if _, ok := agentStore.GetAgent("Travel Expert"); !ok {
		t.Fatal("expected normal agent to survive cleanup")
	}
}
