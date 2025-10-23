package store

import "github.com/johnjallday/dolphin-agent/internal/agent"

type Store interface {
	// Agents
	ListAgents() (names []string, current string)
	CreateAgent(name string) error
	SwitchAgent(name string) error
	DeleteAgent(name string) error

	// Get/Set directly
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error

	// Persistence
	Save() error
}
