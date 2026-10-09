package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

var ErrFolderReviewPending = errors.New("another folder setup review is pending; finish or close that review first")

// FolderConversationReview is provenance, not authority. Only the owning
// canonical conversation and a still-live selection can authorize a new setup.
// No source path is persisted here or exposed by FolderOfferView.
type FolderConversationReview struct {
	Target                 foldercontext.Target    `json:"target"`
	ObservationID          string                  `json:"observation_id"`
	CandidateID            string                  `json:"candidate_id"`
	CandidateIdentity      string                  `json:"candidate_identity"`
	Destination            *FolderSetupDestination `json:"destination,omitempty"`
	DestinationUnavailable bool                    `json:"destination_unavailable,omitempty"`
	Operation              string                  `json:"operation,omitempty"`
	DestinationID          string                  `json:"destination_id,omitempty"`
}

// ConfigureConversationReviews is wired once at host construction. The guard
// validates canonical state; the lease shares the turn/detach concurrency gate.
func (s *FolderObservationService) ConfigureConversationReviews(
	guard func(context.Context, FolderOffer) error,
	lease func(foldercontext.Target) (func(), bool),
) {
	s.reviewGuard, s.reviewLease = guard, lease
	s.digest.conversationSource = s.reviewSource
	s.digest.conversationLease = s.acquireReview
}

func (s *FolderObservationService) acquireReview(ctx context.Context, offer FolderOffer) (func(), error) {
	if offer.ConversationReview == nil {
		return func() {}, nil
	}
	if s.reviewGuard == nil || s.reviewLease == nil {
		return nil, ErrFolderSelection
	}
	release, ok := s.reviewLease(offer.ConversationReview.Target)
	if !ok {
		return nil, ErrFolderScanBusy
	}
	if err := s.reviewGuard(ctx, offer); err != nil {
		release()
		return nil, ErrFolderSelection
	}
	if _, ok := s.reviewSource(offer); !ok {
		release()
		return nil, ErrFolderSelection
	}
	return release, nil
}

func (s *FolderObservationService) reviewSource(offer FolderOffer) (string, bool) {
	review := offer.ConversationReview
	if review == nil || s.reviewGuard == nil {
		return "", false
	}
	ctx := context.Background()
	if err := s.reviewGuard(ctx, offer); err != nil {
		return "", false
	}
	held, _, err := s.resolve(ctx, review.Target, review.ObservationID)
	if err != nil || FolderKey(held.root) != offer.FolderKey || held.identity != offer.RootIdentity {
		return "", false
	}
	candidate, ok := observedReviewCandidate(held, review.CandidateID)
	if !ok || FolderKey(candidate.Path) != offer.Subject.Key {
		return "", false
	}
	canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
	identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
	if err != nil || canonical != candidate.Path || identityErr != nil || identity != review.CandidateIdentity {
		return "", false
	}
	return held.root, true
}

func observedReviewCandidate(held heldFolderObservation, id string) (folderdigest.Candidate, bool) {
	// Only candidates actually disclosed in the bounded preview are selectable.
	for _, shown := range held.observation.Projects {
		if shown.ID != id {
			continue
		}
		for index, candidate := range held.result.Candidates {
			if fmt.Sprintf("candidate-%d", index) == id {
				return candidate, true
			}
		}
	}
	return folderdigest.Candidate{}, false
}

// Review creates/reuses an ordinary canonical offer, but never decides it. The
// caller must append the returned ID to the canonical conversation before its
// controls can authorize anything. A failed append can close this pending offer.
func (s *FolderObservationService) Review(ctx context.Context, source foldercontext.Target, conversationID, observationID, candidateID string) (FolderOfferView, error) {
	if conversationID == "" {
		return FolderOfferView{}, ErrFolderSelection
	}
	held, _, err := s.resolve(ctx, source, observationID)
	if err != nil {
		return FolderOfferView{}, err
	}
	target := source
	target.ConversationID, target.DraftID = conversationID, ""
	offer, err := s.prepareReviewOffer(ctx, held, target, candidateID)
	if err != nil {
		return FolderOfferView{}, err
	}
	d := s.digest
	offer.ID = d.deps.NewID()
	var result FolderOffer
	_, err = d.store.Mutate(ctx, source.UserID, func(doc *FolderDigestDocument) error {
		// Reuse a review/result for this exact conversation and candidate. A fresh
		// explicit pick can recover a confirmed setup, never adopt a replacement dir.
		for i := range doc.Offers {
			prior := &doc.Offers[i]
			if prior.ConversationReview == nil || prior.ConversationReview.Target != target {
				continue
			}
			if prior.FolderKey == offer.FolderKey && prior.Subject.Key == offer.Subject.Key &&
				prior.RootIdentity == offer.RootIdentity && prior.ConversationReview.CandidateIdentity == offer.ConversationReview.CandidateIdentity &&
				prior.ConversationReview.Operation == offer.ConversationReview.Operation && prior.ConversationReview.DestinationID == offer.ConversationReview.DestinationID &&
				((prior.Status == FolderOfferPending && prior.ConversationReview.ObservationID == observationID && prior.ConversationReview.CandidateID == candidateID) || prior.Status == FolderOfferAwaitingOutcome || prior.Status == FolderOfferResolved) {
				// Recovering the folder is not consent to retarget an already
				// confirmed run or completed outcome. An explicit pending Review
				// may refresh material disclosure, changing its confirmation digest.
				if prior.Status != FolderOfferPending {
					offer.ConversationReview.Destination = prior.ConversationReview.Destination
					offer.ConversationReview.DestinationUnavailable = prior.ConversationReview.DestinationUnavailable
				}
				prior.ConversationReview = offer.ConversationReview
				result = *prior
				return nil
			}
		}
		if pending := doc.Pending(); pending != nil {
			if pending.ConversationReview == nil || pending.ConversationReview.Target != target {
				return ErrFolderReviewPending
			}
			// Retirement is not a decline, tombstone or memory preference.
			pending.Status = FolderOfferClosed
		}
		domain, _ := folderOfferDomainClass(offer)
		offer.CapabilitySuppressed = offer.CapabilitySuppressed || doc.DeclineFor(domain) != nil
		doc.Offers = append(doc.Offers, offer)
		pruneFolderDigest(doc)
		result = offer
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	return d.viewFor(ctx, source.UserID, result, held.binding.Paused), nil
}

// prepareReviewOffer is shared by explicit Review and read-only suggestions.
// It validates the observed candidate but neither allocates nor stores an offer.
func (s *FolderObservationService) prepareReviewOffer(ctx context.Context, held heldFolderObservation, target foldercontext.Target, candidateID string) (FolderOffer, error) {
	candidate, ok := observedReviewCandidate(held, candidateID)
	if !ok {
		return FolderOffer{}, ErrFolderSelection
	}
	canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
	identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
	if err != nil || canonical != candidate.Path || identityErr != nil {
		return FolderOffer{}, ErrFolderPathLost
	}
	d := s.digest
	now := d.now()
	offer := FolderOffer{
		Status:    FolderOfferPending,
		FolderKey: FolderKey(held.root), FolderName: held.result.Name, RootIdentity: held.identity,
		Verdict: string(folderdigest.KindProject), Reason: folderdigest.DescribeCandidate(candidate, now),
		Partial: held.result.Partial, ScannedAt: held.observation.ScannedAt, CreatedAt: now,
		Subject:            folderCandidateRecord(candidate, FolderChoiceProject, folderdigest.DescribeCandidate(candidate, now)),
		ProjectsCount:      1,
		ConversationReview: &FolderConversationReview{Target: target, ObservationID: held.observation.ID, CandidateID: candidateID, CandidateIdentity: identity},
	}
	placement := folderReviewPlacement(ctx)
	offer.ConversationReview.Operation, offer.ConversationReview.DestinationID = placement.Operation, placement.DestinationID
	if placement.Operation == FolderOperationSupport {
		offer.CapabilitySuppressed = true
	}
	doc, err := d.store.Read(ctx, target.UserID)
	if err != nil {
		return FolderOffer{}, err
	}
	if doc.Tombstoned(offer.Subject.Key) {
		return FolderOffer{}, ErrFolderOfferDecided
	}
	// Only the root option can describe a collection. Review still requires
	// the user to choose it explicitly, with the same canonical Home checks.
	verdict := folderdigest.Exclude(held.result, now, func(c folderdigest.Candidate) bool { return doc.Tombstoned(FolderKey(c.Path)) })
	if candidate.IsRoot && verdict.Portfolio != nil && d.deps.HomeExists != nil {
		row, found := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		if found && row.Offer != nil && row.Offer.HomeProviderKey != "" {
			exists, homeErr := d.deps.HomeExists(ctx, target.UserID, row.Offer.HomeProviderKey)
			if homeErr != nil {
				return FolderOffer{}, homeErr
			}
			if !exists || d.existingHomeReadable(ctx, target.UserID, row.Offer.HomeProviderKey) {
				offer = buildPortfolioOffer(offer, verdict, row.Offer.HomeProviderKey)
				offer.Portfolio.ExistingHome = exists
				offer.Portfolio.IntegrationKey, offer.Portfolio.IntegrationProjects = folderdigest.IntegrationProjects(held.result, verdict.Portfolio.Shape, now)
			}
		}
	}
	domain, _ := folderOfferDomainClass(offer)
	legacyDeclined := false
	if domain != "" && doc.DeclineFor(domain) == nil && d.deps.LegacyDeclined != nil {
		legacyDeclined, err = d.deps.LegacyDeclined(ctx, target.UserID, domain)
		if err != nil {
			return FolderOffer{}, err
		}
	}
	offer.CapabilitySuppressed = offer.CapabilitySuppressed || legacyDeclined || doc.DeclineFor(domain) != nil
	if reader, ok := d.deps.Setup.(FolderSetupDestinationReader); ok {
		destination, err := reader.ReadSetupDestination(ctx, d.setupRequest(ctx, target.UserID, offer))
		if err != nil {
			offer.ConversationReview.DestinationUnavailable = true
		} else if destination != nil {
			if destination.Validate() != nil {
				offer.ConversationReview.DestinationUnavailable = true
			} else {
				copy := *destination
				offer.ConversationReview.Destination = &copy
			}
		}
	}
	return offer, nil
}

// FolderReviewOption is a path-free description, never a plan or execution grant.
type FolderReviewOption struct {
	CandidateID   string                    `json:"candidate_id"`
	WorkspaceType string                    `json:"workspace_type"`
	Presentation  *FolderReviewPresentation `json:"presentation,omitempty"`
}

// ReviewOptions reads the same candidate and setup availability as Review. It
// does not scan, create an offer, advance a journey, or disturb pending work.
func (s *FolderObservationService) ReviewOptions(ctx context.Context, target foldercontext.Target, observationID string) []FolderReviewOption {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return nil
	}
	doc, err := s.digest.store.Read(ctx, target.UserID)
	if err != nil || doc.Pending() != nil {
		return nil
	}
	var options []FolderReviewOption
	for _, project := range held.observation.Projects {
		offer, err := s.prepareReviewOffer(ctx, held, target, project.ID)
		if err != nil || offer.CapabilitySuppressed {
			continue
		}
		// Describe without a canonical review/root grant: that grant can only
		// exist after the user's later Review click is successfully persisted.
		view := s.digest.describeOffer(ctx, offer, held.binding.Paused)
		s.digest.attachSetupPlan(ctx, target.UserID, offer, &view)
		kind := "Blank workspace"
		if view.Capability != nil {
			if view.Plan == nil {
				continue
			}
			kind = view.Capability.Workspace
		} else if !view.CreateAvailable {
			continue
		} else if view.BlueprintLabel != "" {
			kind = view.BlueprintLabel + " workspace"
		}
		options = append(options, FolderReviewOption{CandidateID: project.ID, WorkspaceType: kind, Presentation: folderReviewPresentation(offer, view)})
	}
	return options
}

// CloseReview closes only an unconfirmed review owned by this conversation.
// It is not No thanks: no preference, decision, memory, or task is written.
func (s *FolderObservationService) CloseReview(ctx context.Context, target foldercontext.Target, offerID string) error {
	_, err := s.digest.store.Mutate(ctx, target.UserID, func(doc *FolderDigestDocument) error {
		offer := doc.Offer(offerID)
		if offer == nil || offer.ConversationReview == nil || offer.ConversationReview.Target != target {
			return ErrFolderOfferNotFound
		}
		if offer.Status == FolderOfferClosed {
			return errFolderReplay
		}
		if offer.Status != FolderOfferPending || offer.DecidedAt != nil {
			return ErrFolderOfferDecided
		}
		offer.Status = FolderOfferClosed
		return nil
	})
	if errors.Is(err, errFolderReplay) {
		return nil
	}
	return err
}

// ReadReview projects an exact stored reference, never promoting offers or
// executing their outcome. Even completed receipts remain owned by the thread.
func (s *FolderObservationService) ReadReview(ctx context.Context, target foldercontext.Target, offerID string) (*FolderOfferView, error) {
	offer, err := s.digest.StoredOffer(ctx, target.UserID, offerID)
	if err != nil {
		return nil, err
	}
	if offer.ConversationReview == nil || offer.ConversationReview.Target != target {
		return nil, ErrFolderOfferNotFound
	}
	binding, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	view := s.digest.viewFor(ctx, target.UserID, offer, binding.Paused)
	return &view, nil
}

func (s *FolderDigestService) checkReviewDestination(ctx context.Context, offer FolderOffer) error {
	review := offer.ConversationReview
	if review == nil || (offer.Status != FolderOfferPending && offer.Status != FolderOfferAwaitingOutcome) {
		return nil
	}
	if review.DestinationUnavailable {
		return ErrFolderOutcomeUnavailable
	}
	if review.Destination == nil {
		return nil
	} // unbound historical/unsupported placement is not invented
	if review.Destination.Status == "new" && offer.Setup != nil {
		if offer.Setup.Intent.Destination == nil || *offer.Setup.Intent.Destination != *review.Destination {
			return ErrFolderPlanChanged
		}
		guard, ok := s.deps.Setup.(FolderSetupDestinationGuard)
		if !ok {
			return ErrFolderOutcomeUnavailable
		}
		request := s.setupRequest(ctx, review.Target.UserID, offer)
		request.Plan = FolderSetupPlan{Digest: offer.Setup.PlanDigest, Destination: offer.Setup.Intent.Destination, Intent: offer.Setup.Intent}
		return guard.ValidateSetupDestination(ctx, request)
	}
	reader, ok := s.deps.Setup.(FolderSetupDestinationReader)
	if !ok {
		return ErrFolderOutcomeUnavailable
	}
	actual, err := reader.ReadSetupDestination(ctx, s.setupRequest(ctx, review.Target.UserID, offer))
	if err != nil {
		return ErrFolderOutcomeUnavailable
	}
	if actual == nil || actual.Validate() != nil {
		return ErrFolderPlanChanged
	}
	copy := *actual
	if offer.Setup != nil {
		// Own progress revisions cannot retarget the material witness.
		copy.RecordVersion, copy.ProgramRevision = review.Destination.RecordVersion, review.Destination.ProgramRevision
	}
	if copy != *review.Destination {
		return ErrFolderPlanChanged
	}
	return nil
}

func (s *FolderDigestService) acquireConversationReview(ctx context.Context, offer FolderOffer) (func(), error) {
	if err := s.checkReviewDestination(ctx, offer); err != nil {
		return nil, ErrFolderPlanChanged
	}
	if offer.ConversationReview == nil {
		return func() {}, nil
	}
	if s.conversationLease == nil {
		return nil, ErrFolderSelection
	}
	return s.conversationLease(ctx, offer)
}

func conversationReviewDigest(offer FolderOffer, view FolderOfferView) string {
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t", offer.ID, offer.ConversationReview.ObservationID,
		offer.Subject.Key, view.Subject.Name, view.Subject.Shape, view.Blueprint, view.Remember)
	value += "\x00" + offer.ConversationReview.Operation
	if offer.ConversationReview.Destination != nil {
		value += "\x00" + DestinationPlanDigest(nil, offer.ConversationReview.Destination)
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validateConversationReview(review *FolderConversationReview) error {
	if review == nil {
		return nil
	}
	if review.Destination != nil && review.Destination.Validate() != nil {
		return errFolderDigestInvalid
	}
	if !review.Target.Valid() || review.Target.ConversationID == "" || review.Target.DraftID != "" {
		return errFolderDigestInvalid
	}
	if review.Operation != "" && review.Operation != FolderOperationCreate && review.Operation != FolderOperationSupport {
		return errFolderDigestInvalid
	}
	if len(review.DestinationID) > 200 || strings.ContainsAny(review.DestinationID, "/\\\x00\r\n") {
		return errFolderDigestInvalid
	}
	for _, value := range []string{review.Target.UserID, review.Target.WorkspaceID, review.Target.AgentName, review.Target.ConversationID, review.ObservationID, review.CandidateID, review.CandidateIdentity} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsAny(value, "/\\\x00\r\n") {
			return errFolderDigestInvalid
		}
	}
	return nil
}
