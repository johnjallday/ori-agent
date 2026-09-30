package personalassistant

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// FolderContinuationState says whether an owner can carry a chosen collection
// on into a Home's library without choosing the folder again.
type FolderContinuationState string

const (
	// FolderContinuationReady means the original selection is still held, still
	// the same directory, and inside its hand-off window.
	FolderContinuationReady FolderContinuationState = "ready"
	// FolderContinuationNeedsPick means the collection was chosen for this Home
	// but the server can no longer vouch for the folder; one new pick is needed.
	FolderContinuationNeedsPick FolderContinuationState = "needs_pick"
)

// FolderContinuationReason distinguishes why a selection cannot be reused, so
// the browser never has to guess between a restart, an expiry, and a replaced
// directory. It is empty when the continuation is ready.
type FolderContinuationReason string

const (
	// FolderContinuationExpired: the 30-minute hand-off window has elapsed.
	FolderContinuationExpired FolderContinuationReason = "expired"
	// FolderContinuationLost: the server no longer holds the picked path (it
	// restarted; a dialog-picked folder is process memory only).
	FolderContinuationLost FolderContinuationReason = "lost"
	// FolderContinuationChanged: the folder is missing, no longer the same
	// directory, or otherwise fails the original identity check.
	FolderContinuationChanged FolderContinuationReason = "changed"
)

const (
	// portfolioContinuationLookback bounds how long after a Home was created an
	// unused collection offer is still worth explaining. Beyond it the offer is
	// history, not something to continue.
	portfolioContinuationLookback = 24 * time.Hour
	portfolioContinuationMax      = 5
)

// FolderContinuation is one collection an owner chose while creating a Home. It
// carries an opaque offer ID and the folder's display name, never a path, and
// grants nothing: the library's own pick-offer, root review, and commit still
// re-verify everything before any consequence.
type FolderContinuation struct {
	OfferID string                   `json:"offer_id"`
	Folder  string                   `json:"folder"`
	State   FolderContinuationState  `json:"state"`
	Reason  FolderContinuationReason `json:"reason,omitempty"`
}

// PortfolioContinuations lists, newest first, the collection offers whose
// verified outcome is exactly this Home for this owner. It is a pure read: it
// creates no Home, root, scan, or offer, and reads the filesystem only to
// re-check the server-held folder's identity. A Home ID that belongs to no
// offer of this owner yields an empty list, never an error that distinguishes
// "foreign" from "none".
func (s *FolderDigestService) PortfolioContinuations(ctx context.Context, userID, homeID string) ([]FolderContinuation, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	homeID = strings.TrimSpace(homeID)
	if userID == "" || homeID == "" {
		return nil, ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		// An owner with no assistant record has nothing to continue; that is the
		// same answer as an owner whose offers name another Home.
		return []FolderContinuation{}, nil
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	var offers []FolderOffer
	for _, offer := range doc.Offers {
		if offer.Status != FolderOfferResolved || offer.Portfolio == nil || offer.DecidedAt == nil ||
			offer.Outcome == nil || offer.Outcome.Kind != FolderChoiceHome || offer.Outcome.WorkspaceID != homeID ||
			!offer.Subject.IsRoot || offer.ResolvedAt == nil || offer.ResolvedAt.After(now) ||
			now.Sub(*offer.ResolvedAt) > portfolioContinuationLookback {
			continue
		}
		offers = append(offers, offer)
	}
	sort.SliceStable(offers, func(i, j int) bool { return offers[i].ResolvedAt.After(*offers[j].ResolvedAt) })
	if len(offers) > portfolioContinuationMax {
		offers = offers[:portfolioContinuationMax]
	}
	result := make([]FolderContinuation, 0, len(offers))
	for _, offer := range offers {
		item := FolderContinuation{OfferID: offer.ID, Folder: offer.FolderName, State: FolderContinuationReady}
		if _, _, loss := s.portfolioSource(offer); loss != "" {
			item.State, item.Reason = FolderContinuationNeedsPick, loss
		}
		result = append(result, item)
	}
	return result, nil
}
