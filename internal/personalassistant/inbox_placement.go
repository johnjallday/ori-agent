package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// InboxPlacementInput names a source review, never a client-authored plan/path.
// Optional card edits apply on first seed only; a replay returns current edits.
type InboxPlacementInput struct {
	OfferID        string  `json:"offer_id"`
	PlanDigest     string  `json:"plan_digest"`
	ConversationID string  `json:"conversation_id"`
	ObservationID  string  `json:"observation_id"`
	SourceRevision string  `json:"source_revision"`
	Name           *string `json:"name,omitempty"`
	Brief          *string `json:"brief,omitempty"`
	Monitoring     bool    `json:"monitoring,omitempty"`
}

func (r InboxPlacementInput) Validate() error {
	if !placementReference(r.OfferID) || !placementReference(r.ConversationID) || !placementReference(r.ObservationID) || !placementReference(r.SourceRevision) || !buildPreparationDigest(r.PlanDigest) {
		return ErrValidation
	}
	if r.Name != nil && !boundedText(*r.Name, workspaceBuildMaxName) {
		return ErrValidation
	}
	if r.Brief != nil && !boundedText(*r.Brief, WorkspaceBuildMaxText) {
		return ErrValidation
	}
	return nil
}

type InboxPlacementBuildStore interface {
	Read(context.Context, string) (WorkspaceBuildDocument, error)
	Mutate(context.Context, string, func(*WorkspaceBuildDocument) error) (WorkspaceBuildDocument, error)
	Now() time.Time
}

type InboxPlacementSeed func(context.Context, FolderSetupPlan, InboxPlacementInput) (BuildDraft, error)

type InboxPlacementResult struct {
	Session WorkspaceBuildSession
	Resumed bool
}

type InboxPlacementError struct {
	Code    string
	BuildID string
}

func (e *InboxPlacementError) Error() string { return "inbox placement: " + e.Code }
func placementError(code string) error       { return &InboxPlacementError{Code: code} }

// ConfigureInboxPlacementRevision supplies the canonical, local folder event
// revision reader. It is wired alongside the conversation guard/lease, not from
// a browser-provided projection.
func (s *FolderObservationService) ConfigureInboxPlacementRevision(reader func(context.Context, FolderOffer) (string, error)) {
	s.placementRevision = reader
}

func (s *FolderObservationService) placementSourceCurrent(ctx context.Context, offer FolderOffer, revision string, accepted bool) error {
	if s.reviewGuard == nil || s.placementRevision == nil || offer.ConversationReview == nil {
		return placementError("source_changed")
	}
	if accepted {
		// Accepted work may recover after detach, but its original local event
		// and relationship must still exist. The guard already owns that rule.
		now := s.digest.now()
		offer.DecidedAt = &now
	}
	if err := s.reviewGuard(ctx, offer); err != nil {
		return placementError("source_changed")
	}
	if !accepted {
		current, err := s.placementRevision(ctx, offer)
		if err != nil || current != revision {
			return placementError("source_changed")
		}
	}
	return nil
}

// AcquireInboxPlacement holds the source context stable through a material
// mutation or the accepted preparation claim. It intentionally does not resolve
// the OLD folder hold: the current build may have an explicitly replaced folder.
func (s *FolderObservationService) AcquireInboxPlacement(ctx context.Context, userID string, build WorkspaceBuildSession) (func(), error) {
	source := build.PlacementSource
	if s == nil || s.digest == nil || s.reviewLease == nil || source == nil || source.validate() != nil || source.Target.UserID != userID {
		return nil, placementError("source_changed")
	}
	release, ok := s.reviewLease(source.Target)
	if !ok {
		return nil, ErrFolderScanBusy
	}
	offer, err := s.digest.StoredOffer(ctx, userID, source.OfferID)
	if err == nil {
		handoff, review := offer.PlacementHandoff, offer.ConversationReview
		if handoff == nil || !handoff.DraftSaved || review == nil || handoff.BuildID != build.ID || handoff.Revision != source.Revision || handoff.PlanDigest != source.PlanDigest ||
			review.Target != source.Target || review.ObservationID != source.ObservationID {
			err = placementError("source_changed")
		} else {
			err = s.placementSourceCurrent(ctx, offer, source.Revision, build.Preparation != nil)
		}
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// HandoffInbox transfers one unaccepted source to the edited-build owner. The
// lock order is conversation lease -> build store -> folder store. Persist the
// source fence first: a failed destination write is recoverable under the SAME
// reserved ID and never leaves the original card independently confirmable.
func (s *FolderObservationService) HandoffInbox(ctx context.Context, userID string, input InboxPlacementInput, builds InboxPlacementBuildStore, seed InboxPlacementSeed) (InboxPlacementResult, error) {
	var result InboxPlacementResult
	if input.Validate() != nil {
		return result, ErrValidation
	}
	if s == nil || s.digest == nil || s.digest.store == nil || s.digest.deps.Setup == nil || s.reviewLease == nil || builds == nil || seed == nil {
		return result, ErrRepairNeeded
	}
	d := s.digest
	offer, err := d.StoredOffer(ctx, userID, input.OfferID)
	if err != nil {
		return result, err
	}
	review := offer.ConversationReview
	if review == nil || review.Target.UserID != userID || review.Target.ConversationID != input.ConversationID || review.ObservationID != input.ObservationID ||
		!IsInboxOffer(offer) || review.Destination != nil || review.DestinationID != "" || review.Operation == FolderOperationSupport || offer.Setup != nil || offer.DecidedAt != nil {
		return result, placementError("source_changed")
	}
	release, ok := s.reviewLease(review.Target)
	if !ok {
		return result, ErrFolderScanBusy
	}
	defer release()
	// Read again after acquiring the shared source lease.
	offer, err = d.StoredOffer(ctx, userID, input.OfferID)
	if err != nil {
		return result, err
	}
	if offer.ConversationReview == nil || offer.ConversationReview.Target != review.Target || offer.ConversationReview.ObservationID != input.ObservationID ||
		!IsInboxOffer(offer) || offer.Setup != nil || offer.DecidedAt != nil {
		return result, placementError("source_changed")
	}
	review = offer.ConversationReview
	if err := s.placementSourceCurrent(ctx, offer, input.SourceRevision, false); err != nil {
		return result, err
	}
	buildID := d.deps.NewID()
	if handoff := offer.PlacementHandoff; handoff != nil {
		if handoff.Revision != input.SourceRevision || handoff.PlanDigest != input.PlanDigest {
			return result, placementError("source_changed")
		}
		buildID = handoff.BuildID
	} else if offer.Status != FolderOfferPending || offer.Setup != nil || offer.DecidedAt != nil {
		return result, placementError("source_changed")
	}
	source := InboxPlacementSource{OfferID: offer.ID, Target: review.Target, ObservationID: review.ObservationID, Revision: input.SourceRevision, PlanDigest: input.PlanDigest}
	if source.validate() != nil || !placementReference(buildID) {
		return result, ErrValidation
	}
	// Keep the prepared seed through optimistic retries. It has no effects and
	// may not adopt newer card edits or allocate a second source identity.
	var draft *BuildDraft
	var folder *foldercontext.Observation
	_, err = builds.Mutate(ctx, userID, func(doc *WorkspaceBuildDocument) error {
		if existing := doc.Session(buildID); existing != nil {
			if existing.PlacementSource == nil || *existing.PlacementSource != source {
				return placementError("handoff_incomplete")
			}
			result = InboxPlacementResult{Session: *existing, Resumed: true}
			return nil
		}
		if offer.PlacementHandoff != nil && offer.PlacementHandoff.DraftSaved {
			// Completed handoff history may have been pruned; absence is not
			// evidence that its accepted operation never created resources.
			return placementError("handoff_incomplete")
		}
		if open := doc.Open(); open != nil {
			return &InboxPlacementError{Code: "build_conflict", BuildID: open.ID}
		}
		if draft == nil {
			path, available := s.reviewSource(offer)
			if !available {
				return placementError("selection_expired")
			}
			plan, err := d.deps.Setup.Plan(ctx, d.planRequest(withFolderSubjectPath(ctx, path), userID, offer))
			if err != nil {
				return err
			}
			plan = plan.Stamped()
			if plan.Intent.Inbox == nil || plan.Intent.Placement != "standalone" || plan.Digest != input.PlanDigest {
				return placementError("source_changed")
			}
			prepared, err := seed(ctx, plan, input)
			if err != nil {
				return err
			}
			draft = &prepared
			folder, err = s.copyInboxPlacementHold(ctx, review.Target, review.ObservationID, buildID)
			if err != nil {
				return placementError("selection_expired")
			}
		}
		// The store-level fence also stops a legacy request which read pending
		// before this lease was acquired. No decision/acceptance is recorded.
		_, err := d.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
			current := doc.Offer(offer.ID)
			if current == nil || !reflect.DeepEqual(current.ConversationReview, review) {
				return placementError("source_changed")
			}
			if prior := current.PlacementHandoff; prior != nil {
				if prior.BuildID == buildID && prior.Revision == source.Revision && prior.PlanDigest == source.PlanDigest {
					return nil
				}
				return placementError("handoff_incomplete")
			}
			if current.Status != FolderOfferPending || current.Setup != nil || current.DecidedAt != nil || doc.Tombstoned(current.Subject.Key) {
				return placementError("source_changed")
			}
			current.PlacementHandoff = &InboxPlacementHandoff{BuildID: buildID, Revision: source.Revision, PlanDigest: source.PlanDigest}
			current.Status = FolderOfferClosed
			return nil
		})
		if err != nil {
			return err
		}
		now := builds.Now()
		build := WorkspaceBuildSession{ID: buildID, Status: WorkspaceBuildOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
			EntryPoint: "assistant_inbox_placement", PlacementSource: &source, PlacementMonitoring: input.Monitoring,
			Draft: *draft, Folder: &BuildFolder{Observation: *folder}, FurthestStep: 4, Transcript: []BuildTranscriptEntry{}}
		if err := validateBuildSession(build); err != nil {
			return err
		}
		doc.Add(build)
		result.Session = build
		return nil
	})
	if err != nil {
		var known *InboxPlacementError
		if errors.As(err, &known) || errors.Is(err, ErrValidation) || errors.Is(err, ErrFolderScanBusy) {
			return InboxPlacementResult{}, err
		}
		return InboxPlacementResult{}, fmt.Errorf("%w: %v", placementError("handoff_incomplete"), err)
	}
	// Acceptance is forbidden until both durable records are proven. This
	// acknowledgment also prevents a later pruned build being reconstructed.
	_, err = d.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		current := doc.Offer(offer.ID)
		if current == nil || current.PlacementHandoff == nil || current.PlacementHandoff.BuildID != buildID {
			return placementError("handoff_incomplete")
		}
		current.PlacementHandoff.DraftSaved = true
		return nil
	})
	if err != nil {
		return InboxPlacementResult{}, placementError("handoff_incomplete")
	}
	return result, nil
}
