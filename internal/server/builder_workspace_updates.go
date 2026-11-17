package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

func (b *ServerBuilder) wireWorkspaceUpdates() {
	store, ok := b.sessionStore.(workspaceupdates.Store)
	if !ok || b.workspaceStore == nil || b.chatHandler == nil || b.sessionHandler == nil {
		return
	}
	service := workspaceupdates.New(store, b.workspaceStore, b.chatHandler, b.resetWork)
	if b.fileJanitorService != nil {
		service.SetReviewReader(b.fileJanitorService)
	}
	b.workspaceUpdates = service
	// A workspace made by feeding a folder is first asked about by its own
	// Commander: the lead's welcome waits until the folder is set up or kept.
	b.chatHandler.SetFolderWelcomeReader(folderWelcomeHold{builder: b})
	b.sessionHandler.SetWorkspaceUpdates(service)
	b.sessionHandler.SetWorkspaceWelcomeCreated(func(ctx context.Context, id string) error {
		owner := userprofile.LocalUserID
		if b.userProvider != nil {
			var err error
			owner, err = b.userProvider.CurrentUserID(ctx)
			if err != nil || owner == "" {
				return workspaceupdates.ErrUnavailable
			}
		}
		return service.Enroll(ctx, owner, id)
	})
}

// folderWelcomeHold answers the chat handler's question "does this workspace's
// welcome wait on its folder agent?" from the folder agent service. The service
// is wired in another phase, so it is looked up when asked; until then (or when
// the feature is not wired) nothing is held. The welcome worker, the chat
// handler and the session store never import the folder agent.
type folderWelcomeHold struct{ builder *ServerBuilder }

func (h folderWelcomeHold) FolderWelcome(workspaceID string) chathttp.FolderWelcome {
	if h.builder == nil || h.builder.folderAgentService == nil {
		return chathttp.FolderWelcomeNone
	}
	return folderWelcomeOf(h.builder.folderAgentService.FolderWelcome(workspaceID))
}

// folderWelcomeOf translates the folder agent's hold into the chat handler's.
func folderWelcomeOf(hold folderagent.WelcomeHold) chathttp.FolderWelcome {
	switch hold {
	case folderagent.WelcomeHeld:
		return chathttp.FolderWelcomeHold
	case folderagent.WelcomeAfterSetUp:
		return chathttp.FolderWelcomeAfterSetUp
	case folderagent.WelcomeAfterKept:
		return chathttp.FolderWelcomeAfterKept
	}
	return chathttp.FolderWelcomeNone
}

func (b *ServerBuilder) notifyWorkspaceWelcomeReady(_ context.Context, _ string) {
	if b.workspaceUpdates != nil {
		b.workspaceUpdates.Notify()
	}
}
