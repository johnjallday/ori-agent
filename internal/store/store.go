package store

import "github.com/johnjallday/dolphin-agent/internal/types"

type Store interface {
	// Agents
	ListAgents() (names []string, current string)
	CreateAgent(name string) error
	SwitchAgent(name string) error
	DeleteAgent(name string) error

	// Get/Set directly
	GetAgent(name string) (*types.Agent, bool)
	SetAgent(name string, ag *types.Agent) error

	// Persistence
	Save() error
}
