package store

import "github.com/johnjallday/ori-agent/internal/agent"

// CreateAgentConfig holds optional configuration for creating a new agent
type CreateAgentConfig struct {
	Type            string  // Agent type: "tool-calling", "general", "research"
	Model           string  // Model to use
	Temperature     float64 // Temperature (0.0-2.0)
	SystemPrompt    string  // Custom system prompt
	LLMProvider     string  // Provider backing the model (openai, anthropic, ollama, etc.)
	MaxOutputTokens int     // Optional max tokens for responses
}

type Store interface {
	// Agents
	ListAgents() (names []string, current string)
	CreateAgent(name string, config *CreateAgentConfig) error
	DeleteAgent(name string) error

	// Get/Set directly
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error

	// Persistence
	Save() error
}

// GetCurrentAgent returns the current agent and its name from the store.
// If no current agent is set, it falls back to the first available agent.
// Returns nil, "", false if no agents exist.
func GetCurrentAgent(s Store) (*agent.Agent, string, bool) {
	names, current := s.ListAgents()
	if len(names) == 0 {
		return nil, "", false
	}

	// Fall back to first agent if no current is set
	if current == "" {
		current = names[0]
	}

	ag, found := s.GetAgent(current)
	if !found || ag == nil {
		return nil, "", false
	}

	return ag, current, true
}
