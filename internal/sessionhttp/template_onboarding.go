package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
)

func (h *Handler) resumeTemplateOnboardingForEntryAgent(ctx context.Context, workspace *session.Workspace) (*templateonboarding.Summary, bool, error) {
	if h == nil || h.templateOnboarding == nil || workspace == nil {
		return nil, false, nil
	}
	entryAgentName := strings.TrimSpace(currentWorkspaceEntryAgentName(workspace))
	if entryAgentName == "" {
		return nil, false, nil
	}
	return h.templateOnboarding.ResumeForEntryAgent(ctx, workspace.ID, entryAgentName)
}
