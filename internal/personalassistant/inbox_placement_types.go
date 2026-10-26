package personalassistant

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// InboxPlacementSource binds a build to one canonical conversation review.
// Provenance is not permission: callers must still validate the current source
// context, build version and live destination selection before acceptance.
// It survives Settle so source history can resolve only this operation.
type InboxPlacementSource struct {
	OfferID       string               `json:"offer_id"`
	Target        foldercontext.Target `json:"target"`
	ObservationID string               `json:"observation_id"`
	Revision      string               `json:"revision"`
	PlanDigest    string               `json:"plan_digest"`
}

// InboxPlacementHandoff retires the original confirmation owner before a build
// is saved. A crash may leave a missing destination; never reopen the source or
// allocate another build identity to recover it. This record is not acceptance.
type InboxPlacementHandoff struct {
	BuildID    string `json:"build_id"`
	Revision   string `json:"revision"`
	PlanDigest string `json:"plan_digest"`
	// DraftSaved distinguishes interrupted seed from pruned/removed history.
	// Once true, a missing build is never reconstructed. Acceptance requires it.
	DraftSaved bool `json:"draft_saved,omitempty"`
}

func placementReference(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && boundedText(value, 128) && !strings.ContainsAny(value, "/\\\r\n")
}

func (s *InboxPlacementSource) validate() error {
	if s == nil {
		return nil
	}
	if !s.Target.Valid() || s.Target.ConversationID == "" || s.Target.DraftID != "" || IsBuildReview(s.Target) ||
		!placementReference(s.OfferID) || !placementReference(s.ObservationID) || !placementReference(s.Revision) || !buildPreparationDigest(s.PlanDigest) {
		return fmt.Errorf("%w: placement source", ErrWorkspaceBuildInvalid)
	}
	for _, value := range []string{s.Target.UserID, s.Target.WorkspaceID, s.Target.AgentName, s.Target.ConversationID} {
		if !placementReference(value) {
			return fmt.Errorf("%w: placement owner", ErrWorkspaceBuildInvalid)
		}
	}
	return nil
}

func validatePlacementHandoff(offer FolderOffer) error {
	handoff := offer.PlacementHandoff
	if handoff == nil {
		return nil
	}
	if !placementReference(handoff.BuildID) || !placementReference(handoff.Revision) || !buildPreparationDigest(handoff.PlanDigest) ||
		(offer.Status != FolderOfferClosed && offer.Status != FolderOfferResolved) || offer.Setup != nil || offer.DecidedAt != nil ||
		offer.ConversationReview == nil || !IsInboxOffer(offer) {
		return fmt.Errorf("%w: placement handoff", errFolderDigestInvalid)
	}
	return nil
}
