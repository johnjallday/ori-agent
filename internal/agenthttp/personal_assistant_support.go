package agenthttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const personalAssistantSupportPresentation = "Assistant support"

type personalAssistantSupportReader interface {
	Get(ctx context.Context, userID string) (*personalassistant.Projection, error)
}

type personalAssistantSupportClassifier struct {
	reader   personalAssistantSupportReader
	provider userprofile.UserProvider
}

func (c personalAssistantSupportClassifier) classify(ctx context.Context, name string, refs []workspace.WorkspaceRef) string {
	if c.reader == nil || !strings.EqualFold(strings.TrimSpace(name), "Journal") {
		return ""
	}
	userID := userprofile.LocalUserID
	if c.provider != nil {
		resolved, err := c.provider.CurrentUserID(ctx)
		if err != nil {
			return ""
		}
		if strings.TrimSpace(resolved) != "" {
			userID = resolved
		}
	}
	projection, err := c.reader.Get(ctx, userID)
	if err != nil || projection == nil || projection.AssistantID == "" || projection.HQWorkspaceID == "" {
		return ""
	}
	for _, ref := range refs {
		if ref.ID == projection.HQWorkspaceID {
			return personalAssistantSupportPresentation
		}
	}
	return ""
}
