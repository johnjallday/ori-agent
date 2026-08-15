package store

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// CreateAgentConfig holds optional configuration for creating a new agent
type CreateAgentConfig struct {
	Type            string // Agent type: "tool-calling", "general", "research"
	Role            types.AgentRole
	Model           string  // Model to use
	Temperature     float64 // Temperature (0.0-2.0)
	SystemPrompt    string  // Custom system prompt
	LLMProvider     string  // Provider backing the model (openai, anthropic, ollama, etc.)
	ReasoningEffort string  // Optional reasoning effort for providers that support it
	MaxOutputTokens int     // Optional max tokens for responses
	AllowWebSearch  *bool   // Optional web utility permission (nil defaults to allowed)
}

type Store interface {
	// Agents
	ListAgents() []string
	CreateAgent(name string, config *CreateAgentConfig) error
	DeleteAgent(name string) error

	// Get/Set/Update directly
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error
	UpdateAgent(name string, updateFn func(*agent.Agent) error) error

	// Management
	ClearAgents() error

	// Persistence
	Save() error
}

// AgentRenamer is an optional capability: a store that can move an agent record
// together with all of its on-disk sidecar state under a new name.
//
// It is deliberately not part of Store. Only the real file-backed store can move
// folders, and widening Store would force every in-memory test double to
// implement a filesystem operation it has no notion of. Callers type-assert and
// fall back to a non-destructive copy when the capability is absent.
type AgentRenamer interface {
	// RenameAgent moves oldName to newName. It must not overwrite an existing
	// agent, and must leave the source intact when it returns an error.
	RenameAgent(oldName, newName string) error
}

// FirstAgentName returns the first available agent name from the store.
func FirstAgentName(s Store) string {
	if s == nil {
		return ""
	}
	for _, candidate := range s.ListAgents() {
		if name := strings.TrimSpace(candidate); name != "" {
			return name
		}
	}
	return ""
}

// GetCurrentAgent is a legacy helper retained for single-agent code paths.
// It no longer reads any global current-agent state; it returns the first
// available agent instead.
func GetCurrentAgent(s Store) (*agent.Agent, string, bool) {
	name := FirstAgentName(s)
	if name == "" {
		return nil, "", false
	}

	ag, found := s.GetAgent(name)
	if !found || ag == nil {
		return nil, "", false
	}

	return ag, name, true
}
