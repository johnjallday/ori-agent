package dailybrief

import (
	"context"
	"errors"
)

// Errors returned by Store implementations.
var (
	ErrConfigNotFound   = errors.New("dailybrief: config not found")
	ErrRevisionNotFound = errors.New("dailybrief: revision not found")
	ErrRequestNotFound  = errors.New("dailybrief: generation request not found")
)

// Store is the durable persistence contract for Daily Brief configuration,
// generation claims, revisions, and notifications. All operations are keyed
// by WorkspaceID (the designated HQ), never just UserID, per PRD FR68 —
// replacing or clearing an HQ must never carry configuration or history
// onto a different workspace.
type Store interface {
	// GetConfig returns the config for workspaceID, or ErrConfigNotFound.
	GetConfig(ctx context.Context, workspaceID string) (*Config, error)
	// UpsertConfig creates or updates the config, bumping ConfigRevision by
	// exactly 1 on every call (the store owns the monotonic counter, not the
	// caller) so a config revision can never be replayed or skipped.
	UpsertConfig(ctx context.Context, cfg *Config) error

	// ClaimGeneration attempts to record a new generation attempt. For
	// TriggerFirstOpen/TriggerScheduled, this is idempotent per
	// (workspaceID, localDate): if a non-manual, non-failed claim already
	// exists for that date, it is returned with isNew=false instead of
	// creating a duplicate. TriggerManual never dedupes — every call creates
	// a new claim.
	ClaimGeneration(ctx context.Context, req *GenerationRequest) (claim *GenerationRequest, isNew bool, err error)
	// UpdateGenerationStatus transitions a claim to a terminal or
	// in-progress status, optionally recording the resulting revision id.
	UpdateGenerationStatus(ctx context.Context, id string, status GenerationStatus, revisionID, errMsg string) error
	// GetGenerationRequest returns a claim by id, or ErrRequestNotFound.
	GetGenerationRequest(ctx context.Context, id string) (*GenerationRequest, error)
	// GetActiveClaim returns the current non-manual claim for
	// (workspaceID, localDate) if one exists (any status), or nil.
	GetActiveClaim(ctx context.Context, workspaceID, localDate string) (*GenerationRequest, error)

	// NextRevisionNumber returns the next monotonic revision number for
	// (workspaceID, localDate) — 1 for the first revision of that date.
	NextRevisionNumber(ctx context.Context, workspaceID, localDate string) (int, error)
	// CreateRevision persists a new revision. It never sets IsCurrent —
	// callers use SetCurrentRevision to flip that atomically, so a
	// revision can be persisted (for audit) even when it must not become
	// current (e.g. a failed attempt).
	CreateRevision(ctx context.Context, rev *Revision) error
	// SetCurrentRevision atomically clears any prior current revision for
	// workspaceID and marks revisionID current. The two are one statement
	// pair inside a transaction, so no intermediate "no current revision"
	// state is observable to a concurrent reader.
	SetCurrentRevision(ctx context.Context, workspaceID, revisionID string) error
	// GetCurrentRevision returns the current revision for workspaceID, or
	// ErrRevisionNotFound if none has ever been set current.
	GetCurrentRevision(ctx context.Context, workspaceID string) (*Revision, error)
	// GetRevision returns a revision by id, or ErrRevisionNotFound.
	GetRevision(ctx context.Context, id string) (*Revision, error)

	// ListHistory returns up to limit HistorySummary entries for
	// workspaceID, most recent local date first, collapsing same-day
	// revisions to their current (or latest) one.
	ListHistory(ctx context.Context, workspaceID string, limit int) ([]HistorySummary, error)
	// PruneHistory deletes revisions for workspaceID older than the most
	// recent keepDays distinct local dates. It never touches another
	// workspace's rows and never deletes the current revision.
	PruneHistory(ctx context.Context, workspaceID string, keepDays int) error

	// RecordNotification records that an Action Center notification was
	// created for revisionID. Idempotent: created is false if one already
	// existed (PRD FR65 — at most one notification per revision).
	RecordNotification(ctx context.Context, revisionID, workspaceID string) (created bool, err error)
}
