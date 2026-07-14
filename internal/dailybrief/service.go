package dailybrief

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ErrGenerationInProgress is returned by RequestGeneration when another
// generation is already running for the same workspace (any trigger). The
// caller should poll rather than retry immediately.
var ErrGenerationInProgress = errors.New("dailybrief: a generation is already in progress for this workspace")

// MinRetentionDays is the minimum number of distinct local dates of brief
// history retained per workspace (PRD FR67: at least 30 days).
const MinRetentionDays = 30

// GenerationResult is what a Generator produces for one attempt. Content
// synthesis itself (the grounded snapshot + prompt/response contract) is a
// separate concern layered on top of this package; GenerationResult is the
// narrow contract this package needs to persist a revision.
type GenerationResult struct {
	ContentJSON       string
	Status            GenerationStatus // succeeded | partial | failed
	FailureReason     string
	SourceWindowStart time.Time
	SourceWindowEnd   time.Time
}

// Generator produces brief content for one generation attempt.
type Generator interface {
	Generate(ctx context.Context, req GenerationRequest, cfg Config) (GenerationResult, error)
}

// Service orchestrates Daily Brief configuration and the generation
// lifecycle: claiming, invoking the Generator, persisting the revision, and
// atomically flipping the current pointer.
type Service struct {
	store     Store
	generator Generator

	// mu/inFlight serializes ANY concurrent generation for the same
	// workspace regardless of trigger — stronger than the store's DB-level
	// dedup, which only covers first_open vs scheduled. Prevents two
	// simultaneous manual refreshes, or a manual refresh racing a first-open
	// request, from both invoking a (potentially slow, LLM-backed) Generator
	// at once for the same workspace (PRD 5.10).
	mu       sync.Mutex
	inFlight map[string]bool

	// onRevisionReady fires (outside any lock) after a revision successfully
	// or partially succeeds and becomes current, for any trigger. Set
	// post-construction (mirrors internal/personalhq.Service.SetOnDesignated)
	// so the server wiring layer can hook Action Center notifications
	// without this package depending on internal/workspace's opportunity
	// store.
	onRevisionReady func(cfg Config, rev *Revision)
}

// NewService constructs a Daily Brief service.
func NewService(store Store, generator Generator) *Service {
	return &Service{store: store, generator: generator, inFlight: map[string]bool{}}
}

// SetOnRevisionReady registers a callback fired whenever a generation
// succeeds or partially succeeds and becomes the current revision,
// regardless of trigger. The callback decides whether/how to notify (e.g.
// only for TriggerScheduled, per PRD FR63).
func (s *Service) SetOnRevisionReady(fn func(cfg Config, rev *Revision)) {
	s.onRevisionReady = fn
}

// GetConfig returns the current config for workspaceID, or ErrConfigNotFound
// if the HQ has never been configured.
func (s *Service) GetConfig(ctx context.Context, workspaceID string) (*Config, error) {
	return s.store.GetConfig(ctx, workspaceID)
}

// UpdateConfig validates/defaults cfg and persists it, bumping
// ConfigRevision. Source/schedule changes affect only future generations —
// this never rewrites prior revisions.
func (s *Service) UpdateConfig(ctx context.Context, cfg Config) (*Config, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpsertConfig(ctx, &normalized); err != nil {
		return nil, err
	}
	return s.store.GetConfig(ctx, normalized.WorkspaceID)
}

// TodayLocalDate returns the current local date key for cfg's timezone.
func TodayLocalDate(cfg Config) (string, error) {
	loc, err := ResolveTimezone(cfg.Timezone)
	if err != nil {
		return "", err
	}
	return LocalDateKey(time.Now().In(loc)), nil
}

// RequestGenerationNow resolves "today" from cfg's timezone and requests a
// generation for it. Used by first-open and manual refresh.
func (s *Service) RequestGenerationNow(ctx context.Context, workspaceID, userID string, trigger Trigger) (*Revision, error) {
	cfg, err := s.store.GetConfig(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	localDate, err := TodayLocalDate(*cfg)
	if err != nil {
		return nil, err
	}
	return s.RequestGeneration(ctx, *cfg, userID, trigger, localDate)
}

// RequestGeneration claims and (if newly claimed) runs a generation for
// (cfg.WorkspaceID, localDate). When a first-open/scheduled claim already
// exists for that date, this returns the outcome of that existing claim
// instead of starting a duplicate (PRD FR55/FR59):
//   - if it already succeeded/partial, the current revision is returned;
//   - if it is still pending/running, ErrGenerationInProgress is returned
//     so the caller polls rather than duplicating work.
//
// Manual refresh (Trigger=TriggerManual) always creates a new revision.
func (s *Service) RequestGeneration(ctx context.Context, cfg Config, userID string, trigger Trigger, localDate string) (*Revision, error) {
	if !s.tryLockWorkspace(cfg.WorkspaceID) {
		return nil, ErrGenerationInProgress
	}
	defer s.unlockWorkspace(cfg.WorkspaceID)

	claim, isNew, err := s.store.ClaimGeneration(ctx, &GenerationRequest{
		WorkspaceID: cfg.WorkspaceID,
		UserID:      userID,
		LocalDate:   localDate,
		Trigger:     trigger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to claim daily brief generation: %w", err)
	}
	if !isNew {
		switch claim.Status {
		case GenerationSucceeded, GenerationPartial:
			if claim.RevisionID != "" {
				return s.store.GetRevision(ctx, claim.RevisionID)
			}
			return s.store.GetCurrentRevision(ctx, cfg.WorkspaceID)
		default:
			return nil, ErrGenerationInProgress
		}
	}

	return s.runGeneration(ctx, cfg, claim)
}

func (s *Service) runGeneration(ctx context.Context, cfg Config, claim *GenerationRequest) (*Revision, error) {
	if err := s.store.UpdateGenerationStatus(ctx, claim.ID, GenerationRunning, "", ""); err != nil {
		logger.Warn("dailybrief: failed to mark generation running", logger.Fields{"claim_id": claim.ID, "error": err})
	}

	if s.generator == nil {
		failMsg := "no brief generator configured"
		_ = s.store.UpdateGenerationStatus(ctx, claim.ID, GenerationFailed, "", failMsg)
		return nil, errors.New("dailybrief: " + failMsg)
	}

	result, genErr := s.generator.Generate(ctx, *claim, cfg)
	if genErr != nil {
		result = GenerationResult{Status: GenerationFailed, FailureReason: genErr.Error()}
	}
	if result.Status == "" {
		result.Status = GenerationFailed
	}

	revisionNumber, err := s.store.NextRevisionNumber(ctx, cfg.WorkspaceID, claim.LocalDate)
	if err != nil {
		_ = s.store.UpdateGenerationStatus(ctx, claim.ID, GenerationFailed, "", err.Error())
		return nil, fmt.Errorf("failed to compute next revision number: %w", err)
	}
	rev := &Revision{
		WorkspaceID:       cfg.WorkspaceID,
		UserID:            claim.UserID,
		LocalDate:         claim.LocalDate,
		RevisionNumber:    revisionNumber,
		Trigger:           claim.Trigger,
		Status:            result.Status,
		ConfigRevision:    cfg.ConfigRevision,
		ContentJSON:       result.ContentJSON,
		SourceWindowStart: result.SourceWindowStart,
		SourceWindowEnd:   result.SourceWindowEnd,
		FailureReason:     result.FailureReason,
		GeneratedAt:       time.Now().UTC(),
	}
	if err := s.store.CreateRevision(ctx, rev); err != nil {
		_ = s.store.UpdateGenerationStatus(ctx, claim.ID, GenerationFailed, "", err.Error())
		return nil, fmt.Errorf("failed to persist daily brief revision: %w", err)
	}

	if rev.Status == GenerationSucceeded || rev.Status == GenerationPartial {
		// Preserve the last successful brief on failure: current is only
		// ever flipped forward on a successful/partial result (PRD 5.12).
		if err := s.store.SetCurrentRevision(ctx, cfg.WorkspaceID, rev.ID); err != nil {
			logger.Warn("dailybrief: failed to set current revision", logger.Fields{"revision_id": rev.ID, "error": err})
		} else {
			rev.IsCurrent = true
			if s.onRevisionReady != nil {
				s.onRevisionReady(cfg, rev)
			}
		}
	}

	if err := s.store.UpdateGenerationStatus(ctx, claim.ID, rev.Status, rev.ID, rev.FailureReason); err != nil {
		logger.Warn("dailybrief: failed to finalize generation status", logger.Fields{"claim_id": claim.ID, "error": err})
	}

	if genErr != nil {
		return rev, genErr
	}
	return rev, nil
}

// GetCurrent returns the current revision for workspaceID, or
// ErrRevisionNotFound if no brief has ever been generated.
func (s *Service) GetCurrent(ctx context.Context, workspaceID string) (*Revision, error) {
	return s.store.GetCurrentRevision(ctx, workspaceID)
}

// GetHistory returns up to limit collapsed per-date history entries.
func (s *Service) GetHistory(ctx context.Context, workspaceID string, limit int) ([]HistorySummary, error) {
	return s.store.ListHistory(ctx, workspaceID, limit)
}

// PruneHistory prunes workspaceID's history down to MinRetentionDays,
// never touching another workspace or the current revision.
func (s *Service) PruneHistory(ctx context.Context, workspaceID string) error {
	return s.store.PruneHistory(ctx, workspaceID, MinRetentionDays)
}

// GetActiveGeneration returns today's most recently claimed generation
// attempt for workspaceID (first-open, scheduled, or manual), or nil if none
// has been claimed yet — used by the HTTP layer to let a client poll
// generation status, including a manual refresh, without blocking on it
// synchronously (PRD FR56/FR57/task 7.4).
func (s *Service) GetActiveGeneration(ctx context.Context, workspaceID string) (*GenerationRequest, error) {
	cfg, err := s.store.GetConfig(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	localDate, err := TodayLocalDate(*cfg)
	if err != nil {
		return nil, err
	}
	return s.store.GetLatestClaim(ctx, workspaceID, localDate)
}

// RecordNotificationIfEnabled creates an Action Center notification record
// for rev, but only when cfg has opted in AND rev came from a scheduled
// trigger (PRD FR63: manual/first-open generations never notify) AND rev
// succeeded. Idempotent per revision (PRD FR65). Returns whether a
// notification should be surfaced to the user (the Action Center item
// itself is created by the caller — task 7's integration layer).
func (s *Service) RecordNotificationIfEnabled(ctx context.Context, cfg Config, rev *Revision) (bool, error) {
	if !cfg.NotifyOnReady || rev == nil || rev.Trigger != TriggerScheduled {
		return false, nil
	}
	if rev.Status != GenerationSucceeded && rev.Status != GenerationPartial {
		return false, nil
	}
	return s.store.RecordNotification(ctx, rev.ID, cfg.WorkspaceID)
}

func (s *Service) tryLockWorkspace(workspaceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[workspaceID] {
		return false
	}
	s.inFlight[workspaceID] = true
	return true
}

func (s *Service) unlockWorkspace(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, workspaceID)
}
