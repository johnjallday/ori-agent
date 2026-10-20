package resourcecatalog

import "time"

// WithdrawReview says what withdrawing one source's access does before the
// owner confirms it.
type WithdrawReview struct {
	Token       string    `json:"review_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	SourceID    string    `json:"source_id"`
	Label       string    `json:"label"`
	Location    string    `json:"location"`
	Consequence string    `json:"consequence"`
}

// ReviewWithdraw discloses a withdrawal. It works without the provider and
// on a detached Studio: an owner can always take access back.
func (s *Service) ReviewWithdraw(owner, studioID, sourceID string) (WithdrawReview, error) {
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return WithdrawReview{}, err
	}
	grants, hasGrants, err := readGrants(ref.paths)
	if err != nil {
		return WithdrawReview{}, err
	}
	var granted *GrantSource
	if hasGrants {
		granted = grantSource(grants, sourceID)
	}
	if granted == nil || !granted.Active() {
		return WithdrawReview{}, ErrInvalidRequest
	}
	review := pendingReview{
		kind: reviewWithdraw, owner: normalizedOwner(owner), studioID: ref.workspace.ID,
		sourceID: granted.SourceID, expiresAt: s.now().Add(ReviewTTL),
	}
	token := s.putReview(review)
	return WithdrawReview{
		Token: token, ExpiresAt: review.expiresAt, SourceID: granted.SourceID, Label: granted.Label, Location: granted.Location,
		Consequence: "Ori stops reading " + granted.Label + " now. Its entries leave the Home's counts and assistants' answers, " +
			"and stay visible here marked Access withdrawn, with your notes. Nothing in the folder changes. " +
			"Reading it again needs a new review.",
	}, nil
}

// CommitWithdraw withdraws the reviewed source on this computer. Once it
// returns, no page of that source is requested and nothing an inspection
// already read from it is published: every read and the publication
// re-check the grant under the same lock.
func (s *Service) CommitWithdraw(owner, studioID, token string) (OwnerView, error) {
	review, err := s.takeReview(token, reviewWithdraw, owner, studioID)
	if err != nil {
		return OwnerView{}, err
	}
	ref, err := s.resolveStudio(owner, studioID)
	if err != nil {
		return OwnerView{}, err
	}
	err = withLock(ref.paths.dataDir, func() error {
		grants, hasGrants, readErr := readGrants(ref.paths)
		if readErr != nil {
			return readErr
		}
		var granted *GrantSource
		if hasGrants {
			granted = grantSource(grants, review.sourceID)
		}
		if granted == nil {
			return ErrReviewStale
		}
		if !granted.Active() {
			return nil // already withdrawn: the same consequence
		}
		now := s.now()
		grants.ApprovalRevision++
		granted.WithdrawnAt = &now
		return writeDocument(ref.paths.grants(), grants)
	})
	if err != nil {
		return OwnerView{}, err
	}
	return s.Overview(owner, studioID)
}
