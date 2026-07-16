package followup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// HomeProjectionCap bounds how many follow-ups the Home projection shows
// (contract §2.5); the full list lives in the Personal HQ management panel.
const HomeProjectionCap = 5

// NudgeWindow is the minimum spacing between reminders for one follow-up, so a
// stale item is nudged at most once per window (contract §2.4).
const NudgeWindow = 24 * time.Hour

// Store is the persistence contract the service needs. *SQLiteStore satisfies it.
type Store interface {
	Create(ctx context.Context, f *FollowUp) error
	Get(ctx context.Context, userID, id string) (*FollowUp, error)
	GetByDedupKey(ctx context.Context, userID, dedupKey string) (*FollowUp, error)
	List(ctx context.Context, f Filter) ([]*FollowUp, error)
	Update(ctx context.Context, f *FollowUp) error
	Delete(ctx context.Context, userID, id string) error
}

// Service is the follow-up domain service: capture, lifecycle, staleness, and
// projection.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService constructs the follow-up service.
func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// CaptureInput describes a follow-up to capture from a source or manual action.
type CaptureInput struct {
	UserID       string
	WorkspaceID  string
	Category     Category
	Direction    Direction
	Title        string
	Detail       string
	Counterparty string
	Source       SourceRef
	Provenance   Provenance
	Confidence   Confidence
	DueAt        *time.Time
}

// Capture creates a follow-up, or updates the existing one when the same source
// is reprocessed (source-based dedup, contract §2.1/§6.5). A sourced item that
// already exists and is still open has its title/detail refreshed; a
// completed/dismissed one is left as the user left it (reprocessing does not
// resurrect a closed loop).
func (s *Service) Capture(ctx context.Context, in CaptureInput) (*FollowUp, error) {
	if !ValidCategory(in.Category) {
		return nil, fmt.Errorf("followup: invalid category %q", in.Category)
	}
	title := Truncate(in.Title, MaxTitleLen)
	if title == "" {
		return nil, errors.New("followup: title is required")
	}
	now := s.now().UTC()

	dedupKey := DedupKey(in.UserID, in.Source.Type, in.Source.ID)
	if dedupKey != "" {
		existing, err := s.store.GetByDedupKey(ctx, in.UserID, dedupKey)
		if err == nil && existing != nil {
			if existing.IsOpen() {
				existing.Title = title
				if d := Truncate(in.Detail, MaxDetailLen); d != "" {
					existing.Detail = d
				}
				existing.UpdatedAt = now
				if err := s.store.Update(ctx, existing); err != nil {
					return nil, err
				}
			}
			return existing, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}

	f := &FollowUp{
		ID:           uuid.NewString(),
		UserID:       in.UserID,
		WorkspaceID:  in.WorkspaceID,
		Category:     in.Category,
		Direction:    in.Direction,
		Title:        title,
		Detail:       Truncate(in.Detail, MaxDetailLen),
		Counterparty: Truncate(in.Counterparty, MaxTitleLen),
		Source:       in.Source,
		DedupKey:     dedupKey,
		Provenance:   in.Provenance,
		Confidence:   in.Confidence,
		Status:       initialStatus(in.Provenance, in.Confidence),
		DueAt:        in.DueAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if f.Provenance == "" {
		f.Provenance = ProvenanceManual
	}
	if f.Direction == "" {
		f.Direction = DirectionNone
	}
	if err := s.store.Create(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// initialStatus decides whether a captured follow-up is immediately active or a
// review candidate: only an inferred item below high confidence enters as a
// candidate; explicit commitments and manual items are active (contract §2.3).
func initialStatus(prov Provenance, conf Confidence) Status {
	if prov == ProvenanceInferred && conf != ConfidenceHigh {
		return StatusCandidate
	}
	return StatusActive
}

// Get / List pass through to the store.
func (s *Service) Get(ctx context.Context, userID, id string) (*FollowUp, error) {
	return s.store.Get(ctx, userID, id)
}
func (s *Service) List(ctx context.Context, f Filter) ([]*FollowUp, error) {
	return s.store.List(ctx, f)
}

// transition applies fn to a loaded follow-up and persists it.
func (s *Service) transition(ctx context.Context, userID, id string, fn func(*FollowUp) error) (*FollowUp, error) {
	f, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if err := fn(f); err != nil {
		return nil, err
	}
	f.UpdatedAt = s.now().UTC()
	if err := s.store.Update(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// ConfirmCandidate promotes an inferred candidate to active.
func (s *Service) ConfirmCandidate(ctx context.Context, userID, id string) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		if f.Status != StatusCandidate {
			return fmt.Errorf("followup: only a candidate can be confirmed")
		}
		f.Status = StatusActive
		return nil
	})
}

// Edit updates the user-editable fields of a follow-up.
func (s *Service) Edit(ctx context.Context, userID, id, title, detail string, dueAt *time.Time) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		if t := Truncate(title, MaxTitleLen); t != "" {
			f.Title = t
		}
		f.Detail = Truncate(detail, MaxDetailLen)
		f.DueAt = dueAt
		return nil
	})
}

// Snooze puts an open follow-up to sleep until until.
func (s *Service) Snooze(ctx context.Context, userID, id string, until time.Time) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		if !f.IsOpen() {
			return fmt.Errorf("followup: only an open item can be snoozed")
		}
		u := until.UTC()
		f.Status = StatusSnoozed
		f.SnoozedUntil = &u
		return nil
	})
}

// Complete closes a follow-up as done.
func (s *Service) Complete(ctx context.Context, userID, id string) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		now := s.now().UTC()
		f.Status = StatusCompleted
		f.CompletedAt = &now
		return nil
	})
}

// Dismiss closes a follow-up without completing it.
func (s *Service) Dismiss(ctx context.Context, userID, id string) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		now := s.now().UTC()
		f.Status = StatusDismissed
		f.DismissedAt = &now
		return nil
	})
}

// Reopen returns a completed/dismissed follow-up to the active backlog.
func (s *Service) Reopen(ctx context.Context, userID, id string) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		if f.Status != StatusCompleted && f.Status != StatusDismissed {
			return fmt.Errorf("followup: only a closed item can be reopened")
		}
		f.Status = StatusActive
		f.CompletedAt = nil
		f.DismissedAt = nil
		return nil
	})
}

// LinkTask attaches a project task reference to a follow-up (a link, never a
// move — the follow-up and the task are independent, contract §1).
func (s *Service) LinkTask(ctx context.Context, userID, id string, ref TaskRef) (*FollowUp, error) {
	return s.transition(ctx, userID, id, func(f *FollowUp) error {
		f.RelatedTask = &ref
		return nil
	})
}

// Wake re-activates snoozed follow-ups whose snooze has elapsed. Returns how
// many were woken. Intended to run before stale evaluation.
func (s *Service) Wake(ctx context.Context) (int, error) {
	items, err := s.store.List(ctx, Filter{Statuses: []Status{StatusSnoozed}})
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	woken := 0
	for _, f := range items {
		if f.SnoozedUntil != nil && !f.SnoozedUntil.After(now) {
			f.Status = StatusActive
			f.SnoozedUntil = nil
			f.UpdatedAt = now
			if err := s.store.Update(ctx, f); err != nil {
				return woken, err
			}
			woken++
		}
	}
	return woken, nil
}

// DueForNudge returns stale follow-ups for userID that have not been nudged
// within NudgeWindow, so a reminder fires at most once per window (contract
// §2.4). Callers mark each nudged via MarkNudged after delivering.
func (s *Service) DueForNudge(ctx context.Context, userID string) ([]*FollowUp, error) {
	items, err := s.store.List(ctx, Filter{UserID: userID, Statuses: []Status{StatusActive, StatusReopened}})
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	var out []*FollowUp
	for _, f := range items {
		if !f.IsStale(now) {
			continue
		}
		if f.LastNudgedAt != nil && now.Sub(*f.LastNudgedAt) < NudgeWindow {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// MarkNudged stamps a follow-up as nudged now (idempotency for reminders).
func (s *Service) MarkNudged(ctx context.Context, userID, id string) error {
	_, err := s.transition(ctx, userID, id, func(f *FollowUp) error {
		now := s.now().UTC()
		f.LastNudgedAt = &now
		return nil
	})
	return err
}

// HomeProjection returns the bounded set of follow-ups Home surfaces: due or
// stale active items first, then other open items, capped at HomeProjectionCap
// (contract §2.5). Deterministic ordering: stale-first, then oldest update.
func (s *Service) HomeProjection(ctx context.Context, userID string) ([]*FollowUp, error) {
	items, err := s.store.List(ctx, Filter{UserID: userID, OpenOnly: true})
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	var stale, other []*FollowUp
	for _, f := range items {
		if f.Status == StatusCandidate {
			continue // candidates await review; not projected as active items
		}
		if f.IsStale(now) {
			stale = append(stale, f)
		} else {
			other = append(other, f)
		}
	}
	projected := append(stale, other...)
	if len(projected) > HomeProjectionCap {
		projected = projected[:HomeProjectionCap]
	}
	return projected, nil
}
