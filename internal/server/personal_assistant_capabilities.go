package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type personalAssistantEmailCapability struct {
	readiness *emailReadinessEvaluator
}

func (a personalAssistantEmailCapability) EmailCapability(ctx context.Context, workspaceID string) personalassistant.EmailCapabilityStatus {
	if a.readiness == nil {
		return personalassistant.EmailCapabilityStatus{Status: personalassistant.CapabilityUnavailable, Reason: "source_unavailable"}
	}
	status := a.readiness.Evaluate(ctx, strings.TrimSpace(workspaceID))
	if status.Ready {
		return personalassistant.EmailCapabilityStatus{
			Status: personalassistant.CapabilityAvailable,
			Route:  "/settings#google-account",
		}
	}
	result := personalassistant.EmailCapabilityStatus{
		Status: personalassistant.CapabilityNotConfigured, Reason: status.Reason, Route: status.ActionURL,
	}
	switch status.Reason {
	case workspace.BlockedReasonReconnectRequired, workspace.BlockedReasonAccountUnavailable:
		result.Status = personalassistant.CapabilityRevoked
	}
	return result
}
