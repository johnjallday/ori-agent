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
	WorkspaceID string
	UserID      string
	// Timezone is an IANA zone name, resolved via time.LoadLocation. Never
	// derived from server-local time.Now().Location().
	Timezone string
	// ScheduleDays are lowercase 3-letter day codes (mon..sun).
	ScheduleDays []string
	// ScheduleTime is 24-hour "HH:MM" local time in Timezone.
	ScheduleTime            string
	ScheduleEnabled         bool
	Scope                   Scope
	SelectedWorkspaceIDs    []string
	IncludeFutureWorkspaces bool
	NotifyOnReady           bool
	// ConfigRevision increments on every update. A Revision records the
	// ConfigRevision that produced it, so config changes affect future
	// generations only and never retroactively relabel prior history.
	ConfigRevision int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GenerationRequest is one attempt (successful or not) to produce a brief
// for a given local calendar date. First-open and scheduled attempts dedupe
// against each other per (WorkspaceID, LocalDate); manual never dedupes.
type GenerationRequest struct {
	ID          string
	WorkspaceID string
	UserID      string
	// LocalDate is "YYYY-MM-DD" in the config's timezone at claim time.
	LocalDate  string
	Trigger    Trigger
	Status     GenerationStatus
	RevisionID string
	Error      string
	ClaimedAt  time.Time
	FinishedAt time.Time
}

// Revision is one generated brief document for a local date. Multiple
// revisions can exist for the same date (each manual refresh adds one);
// IsCurrent marks the single authoritative revision for the whole
// workspace, enforced by a partial unique database index.
type Revision struct {
	ID                string
	WorkspaceID       string
	UserID            string
	LocalDate         string
	RevisionNumber    int
	IsCurrent         bool
	Trigger           Trigger
	Status            GenerationStatus
	ConfigRevision    int
	ContentJSON       string
	SourceWindowStart time.Time
	SourceWindowEnd   time.Time
	FailureReason     string
	GeneratedAt       time.Time
	CreatedAt         time.Time
}

// HistorySummary is one calendar date's collapsed entry for the history
// list: same-day revisions collapse to their current (or latest) one, while
// the full revision rows remain queryable for audit/debug.
type HistorySummary struct {
	LocalDate         string
	CurrentRevisionID string
	RevisionCount     int
	Status            GenerationStatus
	GeneratedAt       time.Time
}

// NotificationRecord tracks whether an Action Center notification was
// already created for a revision (at most one per revision, PRD FR65).
type NotificationRecord struct {
	RevisionID  string
	WorkspaceID string
	NotifiedAt  time.Time
}
