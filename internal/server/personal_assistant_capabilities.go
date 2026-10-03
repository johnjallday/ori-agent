package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// personalAssistantEmailCapability answers the Home email card from the
// workspace mail is actually linked to. Since the Email Ops spin-off that is the
// user's Email Ops workspace, not the HQ, and judging the HQ made the card say
// "Set up email" while the brief was listing the user's mail (#441).
type personalAssistantEmailCapability struct {
	readiness *emailReadinessEvaluator
	// emailOps must hydrate template provenance (the folder store). Nil judges
	// the HQ workspace, as before Email Ops existed.
	emailOps workspace.EmailOpsWorkspaceSource
}

func (a personalAssistantEmailCapability) EmailCapability(ctx context.Context, workspaceID string) personalassistant.EmailCapabilityStatus {
	if a.readiness == nil {
		return personalassistant.EmailCapabilityStatus{Status: personalassistant.CapabilityUnavailable, Reason: "source_unavailable"}
	}
	target, route := strings.TrimSpace(workspaceID), googleAccountCard
	if ws := a.emailOpsWorkspace(); ws != nil {
		target = ws.ID
		if r := questWorkspaceRoute(ws.FolderSlug); r != "" {
			route = r
		}
	}
	status := a.readiness.Evaluate(ctx, target)
	if status.Ready {
		return personalassistant.EmailCapabilityStatus{Status: personalassistant.CapabilityAvailable, Route: route}
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

// emailOpsWorkspace is the user's Email Ops workspace, or nil when there is none
// or it cannot be read.
func (a personalAssistantEmailCapability) emailOpsWorkspace() *workspace.Workspace {
	if a.emailOps == nil {
		return nil
	}
	id, err := workspace.ResolveEmailOpsWorkspace(a.emailOps, userprofile.LocalUserID)
	if err != nil || id == "" {
		return nil
	}
	ws, err := a.emailOps.Get(id)
	if err != nil {
		return nil
	}
	return ws
}
