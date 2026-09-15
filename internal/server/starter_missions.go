package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// starterMissionContext supplies the per-user state the starter missions
// resolve their Quests card from (tasks/prd-starter-missions.md FR7).
//
// It runs on every GET /api/progression, which the Home widget polls, so every
// read here is local and cheap. Each source is read at call time rather than
// captured, because several of them are wired after progression is, and a nil
// source leaves its field zero.
func (b *ServerBuilder) starterMissionContext() progression.MissionContext {
	var mission progression.MissionContext
	ctx := context.Background()

	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(ctx, userprofile.LocalUserID); err == nil && state != nil {
			for _, focus := range state.FocusAreas {
				mission.FocusAreas = append(mission.FocusAreas, string(focus))
			}
		}
	}

	return mission
}
