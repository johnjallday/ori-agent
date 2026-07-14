package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// homeAssistantHQAdapter satisfies agenthttp's narrow HQ-provider interface
// over the real personalhq.Service, so an untargeted "Ask Ori" prompt can
// default to the caller's designated Personal HQ (PRD FR102/FR103, task
// 7.10) without agenthttp importing internal/personalhq directly.
type homeAssistantHQAdapter struct {
	service  *personalhq.Service
	provider userprofile.UserProvider
}

func (a *homeAssistantHQAdapter) CurrentHQWorkspaceID(ctx context.Context) (string, bool) {
	if a == nil || a.service == nil {
		return "", false
	}
	userID := userprofile.LocalUserID
	if a.provider != nil {
		if id, err := a.provider.CurrentUserID(ctx); err == nil && id != "" {
			userID = id
		}
	}
	status, err := a.service.Status(ctx, userID)
	if err != nil || status == nil || !status.Valid {
		return "", false
	}
	return status.WorkspaceID, true
}
