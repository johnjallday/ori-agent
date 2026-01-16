package review

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// mockAgentStore is a simple mock for store.Store
type mockAgentStore struct {
	agents map[string]*agent.Agent
}

func newMockAgentStore() *mockAgentStore {
	return &mockAgentStore{
		agents: make(map[string]*agent.Agent),
	}
}

func (m *mockAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := m.agents[name]
	return ag, ok
}

func (m *mockAgentStore) ListAgents() ([]string, string)                                 { return nil, "" }
func (m *mockAgentStore) CreateAgent(name string, config *store.CreateAgentConfig) error { return nil }
func (m *mockAgentStore) DeleteAgent(name string) error                                  { return nil }
func (m *mockAgentStore) SetAgent(name string, ag *agent.Agent) error                    { return nil }
func (m *mockAgentStore) Save() error                                                    { return nil }

func boolPtr(b bool) *bool {
	return &b
}

func TestGetAgentSensitivity(t *testing.T) {
	tests := []struct {
		name               string
		agentName          string
		defaultSensitivity string
		setupStore         func(*mockAgentStore)
		expected           string
	}{
		{
			name:               "no agent store returns default medium",
			agentName:          "test-agent",
			defaultSensitivity: "",
			setupStore:         nil, // No store
			expected:           "medium",
		},
		{
			name:               "no agent store returns provided default",
			agentName:          "test-agent",
			defaultSensitivity: "high",
			setupStore:         nil,
			expected:           "high",
		},
		{
			name:               "agent not found returns default medium",
			agentName:          "unknown-agent",
			defaultSensitivity: "",
			setupStore:         func(s *mockAgentStore) {},
			expected:           "medium",
		},
		{
			name:               "agent not found returns provided default",
			agentName:          "unknown-agent",
			defaultSensitivity: "low",
			setupStore:         func(s *mockAgentStore) {},
			expected:           "low",
		},
		{
			name:               "agent without metadata returns default",
			agentName:          "test-agent",
			defaultSensitivity: "high",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: nil,
				}
			},
			expected: "high",
		},
		{
			name:               "agent with review disabled returns empty",
			agentName:          "test-agent",
			defaultSensitivity: "medium",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: &types.AgentMetadata{
						ReviewEnabled: boolPtr(false),
					},
				}
			},
			expected: "",
		},
		{
			name:               "agent with review enabled uses default",
			agentName:          "test-agent",
			defaultSensitivity: "medium",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: &types.AgentMetadata{
						ReviewEnabled: boolPtr(true),
					},
				}
			},
			expected: "medium",
		},
		{
			name:               "agent with custom sensitivity uses it",
			agentName:          "test-agent",
			defaultSensitivity: "medium",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: &types.AgentMetadata{
						ReviewSensitivity: "high",
					},
				}
			},
			expected: "high",
		},
		{
			name:               "agent with custom sensitivity overrides default",
			agentName:          "test-agent",
			defaultSensitivity: "low",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: &types.AgentMetadata{
						ReviewEnabled:     boolPtr(true),
						ReviewSensitivity: "high",
					},
				}
			},
			expected: "high",
		},
		{
			name:               "agent with nil ReviewEnabled and custom sensitivity",
			agentName:          "test-agent",
			defaultSensitivity: "medium",
			setupStore: func(s *mockAgentStore) {
				s.agents["test-agent"] = &agent.Agent{
					Metadata: &types.AgentMetadata{
						ReviewEnabled:     nil, // Not explicitly set
						ReviewSensitivity: "low",
					},
				}
			},
			expected: "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &Runner{
				config: DefaultDetectionConfig(),
			}

			if tt.setupStore != nil {
				store := newMockAgentStore()
				tt.setupStore(store)
				runner.agentStore = store
			}

			result := runner.getAgentSensitivity(tt.agentName, tt.defaultSensitivity)
			if result != tt.expected {
				t.Errorf("getAgentSensitivity(%q, %q) = %q, want %q",
					tt.agentName, tt.defaultSensitivity, result, tt.expected)
			}
		})
	}
}
