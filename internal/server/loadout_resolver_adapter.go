package server

import (
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// loadoutResolverAdapter resolves an agent's active-skill slot budget from the
// agent store so skills.Manager can enforce stage-based caps (PRD section C).
// It reads through the store on every call — no caching — so stage growth and
// expert-mode toggles take effect immediately.
type loadoutResolverAdapter struct {
	store store.Store
}

func newLoadoutResolverAdapter(s store.Store) *loadoutResolverAdapter {
	return &loadoutResolverAdapter{store: s}
}

func (a *loadoutResolverAdapter) ResolveAgentLoadout(agentName string) (skills.AgentLoadout, bool) {
	if a == nil || a.store == nil {
		return skills.AgentLoadout{}, false
	}
	ag, ok := a.store.GetAgent(agentName)
	if !ok || ag == nil {
		return skills.AgentLoadout{}, false
	}

	stage := types.AgentStageSpark
	if ag.Evolution != nil && ag.Evolution.Stage != "" {
		stage = ag.Evolution.Stage
	}

	return skills.AgentLoadout{
		SlotCap:    types.SkillSlotsForStage(stage),
		ExpertMode: ag.Metadata.IsExpertMode(ag.Role),
		Stage:      string(stage),
	}, true
}
