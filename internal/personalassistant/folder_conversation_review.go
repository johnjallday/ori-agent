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
	Target            foldercontext.Target `json:"target"`
	ObservationID     string               `json:"observation_id"`
	CandidateID       string               `json:"candidate_id"`
	CandidateIdentity string               `json:"candidate_identity"`
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
	held, _, err := s.resolve(ctx, source, observationID)
	if err != nil {
		return FolderOfferView{}, err
	}
	candidate, ok := observedReviewCandidate(held, candidateID)
	if !ok || conversationID == "" {
		return FolderOfferView{}, ErrFolderSelection
	}
	canonical, err := s.digest.deps.ValidateRoot(candidate.Path)
	identity, identityErr := portfolioDirectoryIdentity(candidate.Path)
	if err != nil || canonical != candidate.Path || identityErr != nil {
		return FolderOfferView{}, ErrFolderPathLost
	}
	d := s.digest
	now := d.now()
	target := source
	target.ConversationID, target.DraftID = conversationID, ""
	offer := FolderOffer{
		ID: d.deps.NewID(), Status: FolderOfferPending,
		FolderKey: FolderKey(held.root), FolderName: held.result.Name, RootIdentity: held.identity,
		Verdict: string(folderdigest.KindProject), Reason: folderdigest.DescribeCandidate(candidate, now),
		Partial: held.result.Partial, ScannedAt: held.observation.ScannedAt, CreatedAt: now,
		Subject:            folderCandidateRecord(candidate, FolderChoiceProject, folderdigest.DescribeCandidate(candidate, now)),
		ProjectsCount:      1,
		ConversationReview: &FolderConversationReview{Target: target, ObservationID: observationID, CandidateID: candidateID, CandidateIdentity: identity},
	}
	doc, err := d.store.Read(ctx, source.UserID)
	if err != nil {
		return FolderOfferView{}, err
	}
	if doc.Tombstoned(offer.Subject.Key) {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	// A collection is offered only when the user explicitly chose the root,
	// using the same observed signal and canonical Home availability as before.
	verdict := folderdigest.Exclude(held.result, now, func(c folderdigest.Candidate) bool { return doc.Tombstoned(FolderKey(c.Path)) })
	if candidate.IsRoot && verdict.Portfolio != nil && d.deps.HomeExists != nil {
		row, found := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		if found && row.Offer != nil && row.Offer.HomeProviderKey != "" {
			exists, homeErr := d.deps.HomeExists(ctx, source.UserID, row.Offer.HomeProviderKey)
			if homeErr != nil {
				return FolderOfferView{}, homeErr
			}
			if !exists || d.existingHomeReadable(ctx, source.UserID, row.Offer.HomeProviderKey) {
				offer = buildPortfolioOffer(offer, verdict, row.Offer.HomeProviderKey)
				offer.Portfolio.ExistingHome = exists
				offer.Portfolio.IntegrationKey, offer.Portfolio.IntegrationProjects = folderdigest.IntegrationProjects(held.result, verdict.Portfolio.Shape, now)
			}
		}
	}
	domain, _ := folderOfferDomainClass(offer)
	legacyDeclined := false
	if domain != "" && doc.DeclineFor(domain) == nil && d.deps.LegacyDeclined != nil {
		legacyDeclined, err = d.deps.LegacyDeclined(ctx, source.UserID, domain)
		if err != nil {
			return FolderOfferView{}, err
		}
	}
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
				prior.RootIdentity == offer.RootIdentity && prior.ConversationReview.CandidateIdentity == identity &&
				((prior.Status == FolderOfferPending && prior.ConversationReview.ObservationID == observationID && prior.ConversationReview.CandidateID == candidateID) || prior.Status == FolderOfferAwaitingOutcome || prior.Status == FolderOfferResolved) {
				prior.ConversationReview = offer.ConversationReview
				result = *prior
				return nil
			}
		}
		if pending := doc.Pending(); pending != nil {
			if pending.ConversationReview == nil || pending.ConversationReview.Target != target {
				return ErrFolderReviewPending
			}
			// The same conversation explicitly reviewed a replacement. No decline,
			// tombstone, memory learning or unrelated offer is produced by retirement.
			pending.Status = FolderOfferClosed
		}
		offer.CapabilitySuppressed = legacyDeclined || doc.DeclineFor(domain) != nil
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

func (s *FolderDigestService) acquireConversationReview(ctx context.Context, offer FolderOffer) (func(), error) {
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
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validateConversationReview(review *FolderConversationReview) error {
	if review == nil {
		return nil
	}
	if !review.Target.Valid() || review.Target.ConversationID == "" || review.Target.DraftID != "" {
		return errFolderDigestInvalid
	}
	for _, value := range []string{review.Target.UserID, review.Target.WorkspaceID, review.Target.AgentName, review.Target.ConversationID, review.ObservationID, review.CandidateID, review.CandidateIdentity} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsAny(value, "/\\\x00\r\n") {
			return errFolderDigestInvalid
		}
	}
	return nil
}
