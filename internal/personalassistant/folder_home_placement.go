package personalassistant

import (
	"context"
	"fmt"
	"strings"
)

// FolderSetupHomeOperation is a server-only lookup into the existing managed
// Home journal. It is persisted before commit, never accepted from the browser.
// The enclosing document supplies the owner; the remaining fields bind the
// exact accepted source and managed-Home request. It conveys no execution grant.
type FolderSetupHomeOperation struct {
	Version         int    `json:"version"`
	OfferID         string `json:"offer_id"`
	RequestID       string `json:"request_id"`
	PlanDigest      string `json:"plan_digest"`
	GroupTemplateID string `json:"group_template_id"`
	Revision        string `json:"revision"`
	Name            string `json:"name"`
	ReviewToken     string `json:"review_token"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (w FolderSetupHomeOperation) Validate() error {
	if w.Version != 1 || len(w.PlanDigest) != 64 {
		return fmt.Errorf("%w: Home operation version/digest", errFolderDigestInvalid)
	}
	for _, value := range []string{w.OfferID, w.RequestID, w.PlanDigest, w.GroupTemplateID, w.Revision, w.Name, w.ReviewToken, w.IdempotencyKey} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsAny(value, "\x00\r\n/\\") {
			return fmt.Errorf("%w: Home operation lookup", errFolderDigestInvalid)
		}
	}
	return nil
}

// RecordSetupHomeOperation durably retains the lookup before the host invokes
// the managed-Home commit. A failed write must prevent that commit. No later
// pass may replace the original reference, even when its reply was lost.
func (s *FolderDigestService) RecordSetupHomeOperation(ctx context.Context, userID string, witness FolderSetupHomeOperation) error {
	if err := witness.Validate(); err != nil {
		return err
	}
	_, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		offer := d.Offer(witness.OfferID)
		if offer == nil || offer.Decision != FolderDecisionYes || offer.RequestID != witness.RequestID ||
			offer.Status != FolderOfferAwaitingOutcome || offer.Setup == nil || offer.Setup.Status != FolderSetupRunning ||
			offer.Setup.PlanDigest != witness.PlanDigest || !offer.Setup.Intent.CreatesHome {
			return ErrFolderPlanChanged
		}
		if prior := offer.Setup.HomeOperation; prior != nil && *prior != witness {
			return ErrFolderPlanChanged
		}
		offer.Setup.HomeOperation = &witness
		return nil
	})
	return err
}

// FolderHomeResult is read-only Home-phase evidence, separate from the later
// project's outcome. No managed-Home tokens or source paths reach the browser.
type FolderHomeResult struct {
	OfferID          string `json:"offer_id"`
	RequestID        string `json:"request_id"`
	PlanDigest       string `json:"plan_digest"`
	State            string `json:"state"` // ready, partial, unproven, unavailable
	HomeID           string `json:"home_id,omitempty"`
	Name             string `json:"name,omitempty"`
	Route            string `json:"route,omitempty"`
	Created          bool   `json:"created_by_this_operation"`
	LayoutSubjectNew bool   `json:"layout_subject_new"`
}

// FolderSetupHomeObserver must only read existing operation/canonical state.
// It must not call Plan, create, resume, staff, scan or open a project.
type FolderSetupHomeObserver interface {
	ObserveSetupHome(context.Context, FolderSetupRequest) FolderHomeResult
}

func (s *FolderDigestService) attachHomeResult(ctx context.Context, userID string, offer FolderOffer, view *FolderOfferView) {
	if offer.Setup == nil || !offer.Setup.Intent.Portfolio || offer.Decision != FolderDecisionYes {
		return
	}
	observer, ok := s.deps.Setup.(FolderSetupHomeObserver)
	if !ok {
		return
	}
	result := observer.ObserveSetupHome(ctx, FolderSetupRequest{UserID: userID, Offer: offer})
	result.OfferID, result.RequestID, result.PlanDigest = offer.ID, offer.RequestID, offer.Setup.PlanDigest
	ready := offer.Setup.Status == FolderSetupStopped && offer.Setup.StopReason == FolderStopHandoffReady && !offer.Setup.Opening
	ready = ready || (offer.Setup.Status == FolderSetupDone && offer.Status == FolderOfferResolved)
	if result.State == "verified" {
		result.State = "partial"
		if ready && result.HomeID != "" && result.HomeID == offer.Setup.HomeID {
			result.State = "ready"
		}
	}
	result.LayoutSubjectNew = result.State == "ready" && result.Created
	view.HomeResult = &result
}
