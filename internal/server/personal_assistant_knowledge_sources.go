package server

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type personalKnowledgeSources struct {
	bindings *personalassistant.KnowledgeResolver
	apps     personalassistant.SavedAppProfileReader
	janitor  *janitorKnowledgeReader
}

// ReadSources is observational and fails closed per source. It never starts a
// detection, scan, proposal, provider operation or connection. A missing source
// is distinct from a configured source with no eligible evidence.
func (s personalKnowledgeSources) ReadSources(ctx context.Context, userID string) personalassistanthttp.KnowledgeSources {
	unavailable := personalassistanthttp.KnowledgeSourceCard{Status: "unavailable"}
	out := personalassistanthttp.KnowledgeSources{SavedApps: unavailable, FileJanitor: unavailable}
	if s.bindings == nil {
		return out
	}
	binding, err := s.bindings.Resolve(ctx, userID)
	if err != nil {
		return out
	}
	if s.apps != nil && binding.UserID == userprofile.LocalUserID {
		profile := s.apps.GetUserProfile()
		if profile != nil && !profile.InferredAt.IsZero() && len(profile.DetectedApps) > 0 {
			at := profile.InferredAt.UTC()
			out.SavedApps = personalassistanthttp.KnowledgeSourceCard{Status: "available", ObservedAt: &at}
		} else {
			out.SavedApps = personalassistanthttp.KnowledgeSourceCard{Status: "not_configured"}
		}
	}
	if s.janitor == nil {
		return out
	}
	now := time.Now
	if s.janitor.now != nil {
		now = s.janitor.now
	}
	var sourceCount, patternCount int
	err = s.janitor.visitJournals(ctx, userID, true, func(workspaceID, ownerID string, settings filejanitor.JanitorSettings, actions []filejanitor.FileAction) {
		sourceCount++
		patternCount += len(projectJanitorKnowledgeSupports(workspaceID, ownerID, settings, actions, now().UTC()))
	})
	if errors.Is(err, errJanitorKnowledgeAccessRevoked) {
		out.FileJanitor.Status = "revoked"
		return out
	}
	if err != nil {
		return out
	}
	switch {
	case sourceCount == 0:
		out.FileJanitor.Status = "not_configured"
	case patternCount == 0:
		out.FileJanitor.Status = "healthy_empty"
	default:
		out.FileJanitor.Status = "available"
	}
	return out
}
