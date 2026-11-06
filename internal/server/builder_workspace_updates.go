package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

func (b *ServerBuilder) wireWorkspaceUpdates() {
	store, ok := b.sessionStore.(workspaceupdates.Store)
	if !ok || b.workspaceStore == nil || b.chatHandler == nil || b.sessionHandler == nil {
		return
	}
	service := workspaceupdates.New(store, b.workspaceStore, b.chatHandler, b.resetWork)
	b.workspaceUpdates = service
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

func (b *ServerBuilder) notifyWorkspaceWelcomeReady(_ context.Context, _ string) {
	if b.workspaceUpdates != nil {
		b.workspaceUpdates.Notify()
	}
}
