package workspaceplan

import (
	"context"
	"time"
)

// Retention: when a Plan moves to History, and what History means (FR-16).
//
// History is a placement, never a deletion. Every review version, approval,
// Task and Run link, and lifecycle entry survives archiving untouched — the
// Plan simply stops appearing in the active list. That distinction is the whole
// point: a user who let a draft go stale for a month should find their content
// intact when they look for it, not discover the app tidied it away.
//
// Placement happens on access rather than on a timer. A sweep would need a
// scheduler, a lock, and a story for what happens when it does not run; reading
// a workspace's plans already touches every row that could be stale, so doing
// it there is both cheaper and impossible to miss.

// InactiveRetention is how long a draft or needs_input Plan may sit without
// activity before it moves to History (FR-16).
const InactiveRetention = 30 * 24 * time.Hour

// archivableStatuses are the statuses that go to History through inactivity.
//
// Only unstarted thinking ages out. A Plan that was approved, is executing, or
// has finished is placed by what HAPPENED to it, not by how long nobody looked:
// archiving an executing Plan because a month passed would hide running work.
var archivableStatuses = map[Status]bool{
	StatusDraft:      true,
	StatusNeedsInput: true,
}

// ShouldArchiveForInactivity reports whether a Plan has aged out.
//
// It is a pure function so the rule can be tested without a clock or a store,
// and so the same predicate answers both the read path and any future sweep.
func ShouldArchiveForInactivity(plan *Plan, now time.Time) bool {
	if plan == nil || plan.ArchivedAt != nil {
		return false
	}
	if !archivableStatuses[plan.Status] {
		return false
	}
	// A Plan with no recorded activity is not assumed stale. An unset timestamp
	// means "unknown", and treating unknown as thirty days old would archive
	// plans created by a code path that forgot to stamp it.
	if plan.LastActivityAt.IsZero() {
		return false
	}
	return now.Sub(plan.LastActivityAt) >= InactiveRetention
}

// ArchiveInactive moves aged-out Plans to History and returns how many moved.
//
// Failures are counted, not returned: this runs inside a read, and one Plan
// that could not be archived must not fail the whole listing. The Plan simply
// stays active and gets another chance on the next read.
func (s *Service) ArchiveInactive(ctx context.Context, workspaceID string, plans []*Plan) int {
	now := s.now()
	archived := 0
	for _, plan := range plans {
		if !ShouldArchiveForInactivity(plan, now) {
			continue
		}
		if err := s.store.ArchivePlan(ctx, workspaceID, plan.ID, inactivityReason, now); err != nil {
			continue
		}
		entry := NewActivity(plan, ActivityArchived, SourceService, "", inactivityReason)
		entry.CreatedAt = now
		if _, err := s.store.AppendActivity(ctx, entry); err != nil {
			continue
		}
		plan.ArchivedAt = &now
		plan.ArchiveReason = inactivityReason
		archived++
	}
	return archived
}

// inactivityReason is what the history entry says. It names the rule rather
// than the moment, so a user reading it a year later knows why it happened
// rather than only when.
const inactivityReason = "moved to history after 30 days without activity"
