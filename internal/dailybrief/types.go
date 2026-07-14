// Package dailybrief implements Daily Brief configuration, durable
// revisions/history, and timezone-aware scheduling for the designated
// Personal HQ workspace. Content synthesis (grounded snapshots, the actual
// brief prose) is a separate concern layered on top in a later package —
// this package owns storage, config, generation lifecycle/concurrency, and
// the recurrence calculation.
package dailybrief

import "time"

// Trigger identifies what caused a generation attempt.
type Trigger string

const (
	TriggerFirstOpen Trigger = "first_open"
	TriggerScheduled Trigger = "scheduled"
	TriggerManual    Trigger = "manual"
)

// GenerationStatus is the lifecycle status of a single generation attempt or
// the revision it produced.
type GenerationStatus string

const (
	GenerationPending   GenerationStatus = "pending"
	GenerationRunning   GenerationStatus = "running"
	GenerationSucceeded GenerationStatus = "succeeded"
	GenerationPartial   GenerationStatus = "partial"
	GenerationFailed    GenerationStatus = "failed"
)

// Scope selects which workspaces a brief covers.
type Scope string

const (
	ScopeAll      Scope = "all"
	ScopeSelected Scope = "selected"
)

// defaultScheduleDays is Monday-Friday, matching the Build My HQ default
// (internal/personalhq.SetupCoordinator).
var defaultScheduleDays = []string{"mon", "tue", "wed", "thu", "fri"}

// validDays is the accepted set of ScheduleDays values.
var validDays = map[string]bool{
	"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true,
}

// Config is the Daily Brief configuration for one HQ workspace. It is keyed
// by WorkspaceID (the designated HQ), not just UserID, so replacing or
// clearing the HQ designation never carries settings onto a different
// workspace (PRD FR68) — a new HQ starts with fresh defaults.
type Config struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	// Timezone is an IANA zone name, resolved via time.LoadLocation. Never
	// derived from server-local time.Now().Location().
	Timezone string `json:"timezone"`
	// ScheduleDays are lowercase 3-letter day codes (mon..sun).
	ScheduleDays []string `json:"schedule_days"`
	// ScheduleTime is 24-hour "HH:MM" local time in Timezone.
	ScheduleTime            string   `json:"schedule_time"`
	ScheduleEnabled         bool     `json:"schedule_enabled"`
	Scope                   Scope    `json:"scope"`
	SelectedWorkspaceIDs    []string `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool     `json:"include_future_workspaces"`
	NotifyOnReady           bool     `json:"notify_on_ready"`
	// ConfigRevision increments on every update. A Revision records the
	// ConfigRevision that produced it, so config changes affect future
	// generations only and never retroactively relabel prior history.
	ConfigRevision int       `json:"config_revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GenerationRequest is one attempt (successful or not) to produce a brief
// for a given local calendar date. First-open and scheduled attempts dedupe
// against each other per (WorkspaceID, LocalDate); manual never dedupes.
type GenerationRequest struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	// LocalDate is "YYYY-MM-DD" in the config's timezone at claim time.
	LocalDate  string           `json:"local_date"`
	Trigger    Trigger          `json:"trigger"`
	Status     GenerationStatus `json:"status"`
	RevisionID string           `json:"revision_id"`
	Error      string           `json:"error,omitempty"`
	ClaimedAt  time.Time        `json:"claimed_at"`
	FinishedAt time.Time        `json:"finished_at"`
}

// Revision is one generated brief document for a local date. Multiple
// revisions can exist for the same date (each manual refresh adds one);
// IsCurrent marks the single authoritative revision for the whole
// workspace, enforced by a partial unique database index.
type Revision struct {
	ID                string           `json:"id"`
	WorkspaceID       string           `json:"workspace_id"`
	UserID            string           `json:"user_id"`
	LocalDate         string           `json:"local_date"`
	RevisionNumber    int              `json:"revision_number"`
	IsCurrent         bool             `json:"is_current"`
	Trigger           Trigger          `json:"trigger"`
	Status            GenerationStatus `json:"status"`
	ConfigRevision    int              `json:"config_revision"`
	ContentJSON       string           `json:"content_json"`
	SourceWindowStart time.Time        `json:"source_window_start"`
	SourceWindowEnd   time.Time        `json:"source_window_end"`
	FailureReason     string           `json:"failure_reason,omitempty"`
	GeneratedAt       time.Time        `json:"generated_at"`
	CreatedAt         time.Time        `json:"created_at"`
}

// HistorySummary is one calendar date's collapsed entry for the history
// list: same-day revisions collapse to their current (or latest) one, while
// the full revision rows remain queryable for audit/debug.
type HistorySummary struct {
	LocalDate         string           `json:"local_date"`
	CurrentRevisionID string           `json:"current_revision_id"`
	RevisionCount     int              `json:"revision_count"`
	Status            GenerationStatus `json:"status"`
	GeneratedAt       time.Time        `json:"generated_at"`
}

// NotificationRecord tracks whether an Action Center notification was
// already created for a revision (at most one per revision, PRD FR65).
type NotificationRecord struct {
	RevisionID  string    `json:"revision_id"`
	WorkspaceID string    `json:"workspace_id"`
	NotifiedAt  time.Time `json:"notified_at"`
}
