package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

// SpecialistOfferRequest is one answer to the post-hire domain offer.
//
// The offer is made after the relationship exists, not during the hire. That
// keeps the hire transaction exactly as heavy as it is — one assistant, one
// relationship — and means answering is an ordinary, reversible-looking edit
// to an existing working agreement rather than a condition of being set up.
type SpecialistOfferRequest struct {
	IfVersion int64
	// Decision is "accepted" or "declined". An empty decision is rejected:
	// silence is not an answer, and the offer stays open.
	Decision string
	// Slug is required when accepting and ignored when declining.
	Slug string
}

// SpecialistOfferService records the answer on the durable relationship.
type SpecialistOfferService struct {
	store Store
}

// NewSpecialistOfferService constructs the offer-answering boundary.
func NewSpecialistOfferService(store Store) *SpecialistOfferService {
	return &SpecialistOfferService{store: store}
}

// Answer records an accept or a decline.
//
// Accepting also replaces the working agreement's focus areas with the
// domain's, which is the whole visible point of saying yes: the assistant's
// stated focus becomes the user's actual work. Declining records only that the
// question was asked and answered, so it is never asked again.
//
// It creates no workspace and runs no setup wizard. The domain's workspace
// stays a suggestion the user acts on deliberately.
func (s *SpecialistOfferService) Answer(ctx context.Context, userID string, request SpecialistOfferRequest) (*State, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("personal assistant: specialist offer service is not configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", ErrValidation)
	}
	decision, err := NormalizeSpecialistOfferState(request.Decision)
	if err != nil || decision == SpecialistOfferUnanswered {
		return nil, fmt.Errorf("%w: answer must be accepted or declined", ErrValidation)
	}

	slug := ""
	var entry specialist.Entry
	if decision == SpecialistOfferAccepted {
		slug, err = NormalizeSpecialistSlug(request.Slug)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidation, err)
		}
		if slug == "" {
			return nil, fmt.Errorf("%w: accepting requires a specialist", ErrValidation)
		}
		found, ok := specialist.Get(slug)
		if !ok {
			return nil, fmt.Errorf("%w: unknown specialist", ErrValidation)
		}
		entry = found
	}

	state, err := s.store.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Only a real relationship can answer. Before a hire there is no working
	// agreement to shape and nobody to shape it for.
	switch state.Status {
	case StatusAwaitingHQ, StatusProvisioningHQ, StatusActive, StatusPaused:
	default:
		return nil, fmt.Errorf("%w: no assistant has been hired yet", ErrConflict)
	}
	if request.IfVersion != 0 && request.IfVersion != state.StateVersion {
		return nil, fmt.Errorf("%w: stale relationship version", ErrConflict)
	}
	// Answering twice with the same answer is a no-op, not a conflict: a double
	// click or a replayed request must not fail.
	if state.SpecialistOfferState == decision && state.SpecialistSlug == slug {
		return state.Clone(), nil
	}
	if state.SpecialistOfferState != SpecialistOfferUnanswered {
		return nil, fmt.Errorf("%w: the specialist offer was already answered", ErrConflict)
	}

	next := state.Clone()
	next.SpecialistOfferState = decision
	next.SpecialistSlug = slug
	if decision == SpecialistOfferAccepted {
		focus, focusErr := NormalizeFocusAreas(defaultFocusValues(entry))
		if focusErr != nil {
			// A mapping whose defaults are not valid enum members is a bug in the
			// mapping, not something to half-apply to a user's agreement.
			return nil, fmt.Errorf("%w: %v", ErrValidation, focusErr)
		}
		if len(focus) > 0 {
			next.FocusAreas = focus
		}
	}
	updated, err := s.store.UpdateState(ctx, next, state.StateVersion)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// defaultFocusValues returns the focus areas a domain pre-selects. A mapping
// that pre-selects nothing leaves the user's existing agreement alone rather
// than emptying it.
func defaultFocusValues(entry specialist.Entry) []string {
	out := make([]string, 0, len(entry.FocusAreas))
	for _, focus := range entry.FocusAreas {
		if focus.Selected {
			out = append(out, focus.Value)
		}
	}
	return out
}
