// Package followup implements the Personal HQ structured follow-up domain
// (contract §2): personal commitments and dependencies with their own lifecycle
// and source-based deduplication. It is deliberately NOT built on Action Center
// opportunities — those are title-deduped mission findings with different
// semantics.
package followup

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Category is the kind of open loop a follow-up represents (v1 set only).
type Category string

const (
	CategoryIOwe             Category = "i_owe"
	CategoryWaitingOn        Category = "waiting_on"
	CategoryNeedsDecision    Category = "needs_decision"
	CategoryRecurringCheckIn Category = "recurring_check_in"
)

// Direction records who owes whom.
type Direction string

const (
	DirectionOutbound Direction = "outbound" // I owe them
	DirectionInbound  Direction = "inbound"  // waiting on them
	DirectionNone     Direction = "none"
)

// Provenance records how the follow-up was created.
type Provenance string

const (
	ProvenanceExplicit Provenance = "explicit" // an unambiguous commitment in a source
	ProvenanceInferred Provenance = "inferred" // model-inferred; enters as a candidate
	ProvenanceManual   Provenance = "manual"   // user-created
)

// Confidence is the inference confidence (empty for manual/explicit).
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// Status is the follow-up lifecycle state.
type Status string

const (
	StatusCandidate Status = "candidate" // inferred, awaiting user confirmation
	StatusActive    Status = "active"
	StatusSnoozed   Status = "snoozed"
	StatusCompleted Status = "completed"
	StatusDismissed Status = "dismissed"
	StatusReopened  Status = "reopened"
)

// SourceRef points at what a follow-up came from.
type SourceRef struct {
	Type      string `json:"type"` // email_thread | manual | journal
	ID        string `json:"id,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// TaskRef links a follow-up to a project task (a link, never ownership).
type TaskRef struct {
	WorkspaceID string `json:"workspace_id"`
	TaskID      string `json:"task_id"`
}

// FollowUp is one tracked personal commitment/dependency.
type FollowUp struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	WorkspaceID  string     `json:"workspace_id"`
	Category     Category   `json:"category"`
	Direction    Direction  `json:"direction"`
	Title        string     `json:"title"`
	Detail       string     `json:"detail,omitempty"`
	Counterparty string     `json:"counterparty,omitempty"`
	Source       SourceRef  `json:"source"`
	DedupKey     string     `json:"dedup_key,omitempty"`
	Provenance   Provenance `json:"provenance"`
	Confidence   Confidence `json:"confidence,omitempty"`
	Status       Status     `json:"status"`
	DueAt        *time.Time `json:"due_at,omitempty"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	LastNudgedAt *time.Time `json:"last_nudged_at,omitempty"`
	RelatedTask  *TaskRef   `json:"related_task,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DismissedAt  *time.Time `json:"dismissed_at,omitempty"`
}

// Bounds on stored text (contract §2.1).
const (
	MaxTitleLen  = 200
	MaxDetailLen = 1000
)

// StalenessWindow is the default age after which an active follow-up with no due
// date is considered stale (contract §2.4).
const StalenessWindow = 7 * 24 * time.Hour

// IsOpen reports whether the follow-up is in the active backlog.
func (f FollowUp) IsOpen() bool {
	switch f.Status {
	case StatusActive, StatusSnoozed, StatusCandidate, StatusReopened:
		return true
	default:
		return false
	}
}

// IsStale reports whether an active follow-up needs attention as of now: a due
// date in the past, or (with no due date) no update within the staleness window.
// Only active/reopened items go stale; snoozed items wait until their snooze.
func (f FollowUp) IsStale(now time.Time) bool {
	if f.Status != StatusActive && f.Status != StatusReopened {
		return false
	}
	if f.DueAt != nil {
		return !f.DueAt.After(now)
	}
	return now.Sub(f.UpdatedAt) >= StalenessWindow
}

// DedupKey returns the canonical source-dedup key for a sourced follow-up, or ""
// for a manual/unsourced one (which is never auto-deduped). Reprocessing the same
// source thread/message yields the same key so it updates rather than duplicates.
func DedupKey(userID, sourceType, sourceID string) string {
	sourceType = strings.TrimSpace(sourceType)
	sourceID = strings.TrimSpace(sourceID)
	if sourceType == "" || sourceType == "manual" || sourceID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(userID) + "|" + sourceType + "|" + sourceID))
	return hex.EncodeToString(sum[:])
}

// ValidCategory reports whether c is a supported v1 category.
func ValidCategory(c Category) bool {
	switch c {
	case CategoryIOwe, CategoryWaitingOn, CategoryNeedsDecision, CategoryRecurringCheckIn:
		return true
	default:
		return false
	}
}

// Truncate bounds a string to n runes without splitting a multi-byte rune.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}
