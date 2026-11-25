package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// personalAssistantProjector is the slice of the personal assistant service
// chat routing needs.
type personalAssistantProjector interface {
	Get(ctx context.Context, userID string) (*personalassistant.Projection, error)
}

// activePersonalAssistantProfile names the profile chat outside any workspace
// talks to: the hired Personal Assistant, and only while it is active. A
// paused, unhired or unrepaired assistant yields "", and chat then says how to
// set one up rather than answering as anyone else.
func activePersonalAssistantProfile(service personalAssistantProjector, users userprofile.UserProvider) chathttp.PersonalAssistantProfileReader {
	return func(ctx context.Context) string {
		if service == nil {
			return ""
		}
		userID := userprofile.LocalUserID
		if users != nil {
			if resolved, err := users.CurrentUserID(ctx); err == nil && strings.TrimSpace(resolved) != "" {
				userID = strings.TrimSpace(resolved)
			}
		}
		projection, err := service.Get(ctx, userID)
		if err != nil || projection == nil || projection.State != personalassistant.APIStateActive {
			return ""
		}
		return strings.TrimSpace(projection.GlobalAgentProfile)
	}
}
