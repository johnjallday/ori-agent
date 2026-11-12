package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// folderAgentTicketSongFacts gives a ticket worked by a folder's own agent what
// the Home learned about that folder's songs. The folder agent service is looked
// up when a ticket runs, so wiring order does not matter.
type folderAgentTicketSongFacts struct{ builder *ServerBuilder }

var _ workspace.SongFactsReader = folderAgentTicketSongFacts{}

func (f folderAgentTicketSongFacts) SongFacts(ctx context.Context, workspaceID, agentName string) string {
	if f.builder == nil || f.builder.folderAgentService == nil {
		return ""
	}
	return f.builder.folderAgentService.TicketSongFacts(ctx, workspaceID, agentName)
}
