package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ErrAgentChangedOnDisk reports that an agent's definition file was edited
// outside Ori after the store last read or wrote it. SetAgent refuses to
// overwrite such a file; the store reloads the agent from disk instead, so the
// caller can show the user what is actually there and let them try again.
var ErrAgentChangedOnDisk = errors.New("agent definition changed on disk")

// ErrAgentRootUnavailable reports a write to the workspace root's agents
// folder while that root is missing or unreadable (an unmounted drive, say).
// Ori never creates the root to satisfy the write.
var ErrAgentRootUnavailable = errors.New("your Workspace Directory was not found, so your agents are unavailable")

// ErrAgentUnreadable refuses any write to an agent whose definition file is not
// valid JSON. Ori never overwrites or deletes such a file: the user fixes it
// in a text editor and rescans.
var ErrAgentUnreadable = errors.New("agent definition could not be read")

// UnreadableAgent is an agent folder whose definition file could not be read.
type UnreadableAgent struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Error string `json:"error"`
}

// UnreadableAgentError is ErrAgentUnreadable naming the file.
type UnreadableAgentError struct {
	UnreadableAgent
}

func (e *UnreadableAgentError) Error() string {
	return fmt.Sprintf("agent %q could not be read from %s", e.Name, e.File)
}

func (e *UnreadableAgentError) Unwrap() error { return ErrAgentUnreadable }

// AgentChangedOnDiskMessage is the user-facing explanation for
// ErrAgentChangedOnDisk. HTTP handlers answer it with 409 Conflict.
const AgentChangedOnDiskMessage = "This agent was changed on disk. Ori reloaded it. Review it and try again."

// CreateAgentConfig holds optional configuration for creating a new agent
type CreateAgentConfig struct {
	Role            types.AgentRole
	Model           string                 // Model to use
	Temperature     float64                // Temperature (0.0-2.0)
	SystemPrompt    string                 // Custom system prompt
	LLMProvider     string                 // Provider backing the model (openai, anthropic, ollama, etc.)
	ReasoningEffort string                 // Optional reasoning effort for providers that support it
	MaxOutputTokens int                    // Optional max tokens for responses
	AllowWebSearch  *bool                  // Optional web utility permission (nil defaults to allowed)
	Appearance      *types.AgentAppearance // Optional validated visual identity
	// AssistantSetup is written atomically with a newly created root profile.
	// Callers must leave it nil for an existing/reused profile.
	AssistantSetup *agent.AssistantSetupProvenance
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
