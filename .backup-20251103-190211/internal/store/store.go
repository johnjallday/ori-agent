package store

import "github.com/johnjallday/ori-agent/internal/agent"

// CreateAgentConfig holds optional configuration for creating a new agent
type CreateAgentConfig struct {
	Type         string  // Agent type: "tool-calling", "general", "research"
	Model        string  // Model to use
	Temperature  float64 // Temperature (0.0-2.0)
	SystemPrompt string  // Custom system prompt
}

type Store interface {
	// Agents
	ListAgents() (names []string, current string)
	CreateAgent(name string, config *CreateAgentConfig) error
	SwitchAgent(name string) error
	DeleteAgent(name string) error

	// Get/Set directly
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error

	// Persistence
	Save() error
}
