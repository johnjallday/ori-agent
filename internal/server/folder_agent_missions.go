package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// The current folder chooser uses folderagent, not the older folder-digest
// accept hook. Observe its durable report, never a browser claim or a start.
func (b *ServerBuilder) completeFolderAgentMissions(_ context.Context, userID, _ string) {
	if b == nil || b.progressionEngine == nil || userID != userprofile.LocalUserID {
		return
	}
	b.progressionEngine.Complete(progression.ShowFolderQuestID)
	b.progressionEngine.Complete(progression.FolderFirstLookQuestID)
}

func (b *ServerBuilder) reconcileFolderAgentMissions(ctx context.Context) {
	if b == nil || b.folderAgentService == nil || b.progressionEngine == nil {
		return
	}
	// List reads only records belonging to this user in workspaces that still
	// exist. These states all follow a persisted successful look; a failed or
	// interrupted first look is not evidence. This runs at startup, not on polls.
	for _, view := range b.folderAgentService.List(ctx, userprofile.LocalUserID) {
		switch view.State {
		case folderagent.StateReported, folderagent.StateKept, folderagent.StateSettingUp, folderagent.StateSetUp:
			b.completeFolderAgentMissions(ctx, userprofile.LocalUserID, view.WorkspaceID)
			return
		}
	}
}
