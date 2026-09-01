package server

import (
	"errors"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
)

type personalAssistantAgentProfiles struct {
	store   store.Store
	renamer store.AgentRenamer
}

func newPersonalAssistantAgentProfiles(base store.Store) personalAssistantAgentProfiles {
	renamer, _ := base.(store.AgentRenamer)
	return personalAssistantAgentProfiles{store: base, renamer: renamer}
}

func (a personalAssistantAgentProfiles) ListAgents() []string {
	if a.store == nil {
		return nil
	}
	return a.store.ListAgents()
}

func (a personalAssistantAgentProfiles) GetAgent(name string) (*agent.Agent, bool) {
	if a.store == nil {
		return nil, false
	}
	return a.store.GetAgent(name)
}

func (a personalAssistantAgentProfiles) RenameAgent(oldName, newName string) error {
	if a.renamer == nil {
		return errors.New("agent profile rename is unavailable")
	}
	return a.renamer.RenameAgent(oldName, newName)
}
