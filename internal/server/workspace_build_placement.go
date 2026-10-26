package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Resolve the observation service at call time, as ordinary build folders do:
// the assistant conversation service is wired later than the workspace handler.
type inboxBuildPlacement struct{ builder *ServerBuilder }

func (p inboxBuildPlacement) Handoff(ctx context.Context, owner string, input personalassistant.InboxPlacementInput, seed personalassistant.InboxPlacementSeed) (personalassistant.InboxPlacementResult, error) {
	service := (buildFolderObservations{builder: p.builder}).service()
	if service == nil || p.builder.workspaceBuildStore == nil {
		return personalassistant.InboxPlacementResult{}, personalassistant.ErrRepairNeeded
	}
	return service.HandoffInbox(ctx, owner, input, p.builder.workspaceBuildStore, seed)
}

func (p inboxBuildPlacement) Acquire(ctx context.Context, owner string, build personalassistant.WorkspaceBuildSession) (func(), error) {
	service := (buildFolderObservations{builder: p.builder}).service()
	if service == nil {
		return nil, personalassistant.ErrRepairNeeded
	}
	return service.AcquireInboxPlacement(ctx, owner, build)
}
