package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// OpportunityStatus tracks where an opportunity sits in the user's triage flow.
type OpportunityStatus string

const (
	OpportunityNew       OpportunityStatus = "new"
	OpportunitySnoozed   OpportunityStatus = "snoozed"
	OpportunityResolved  OpportunityStatus = "resolved"
	OpportunityDismissed OpportunityStatus = "dismissed"
	// OpportunityPlanned marks a finding promoted to a Backlog item via Add to
	// Backlog (PRD workspace-backlog FR26-29). Distinct from OpportunityResolved:
	// the underlying finding isn't fixed, it's just been turned into tracked
	// work — using "resolved" here would misdescribe the finding as addressed.
	OpportunityPlanned OpportunityStatus = "planned"
)

// DismissalReason captures why the user dismissed an opportunity. Used to power
// the duplicate-dismissal-rate success metric — high "duplicate" counts mean
// our dedup heuristic is missing real duplicates.
type DismissalReason string

const (
	DismissalNotUseful  DismissalReason = "not_useful"
	DismissalDuplicate  DismissalReason = "duplicate"
	DismissalOutOfScope DismissalReason = "out_of_scope"
	DismissalOther      DismissalReason = "other"
)

// Opportunity is a finding produced by a workspace mission run. Opportunities
// are observations (recorded by the system after parsing the run's structured
// output), not changes — they are never gated by the autonomy policy.
type Opportunity struct {
	ID                string            `json:"id"`
	WorkspaceID       string            `json:"workspace_id"`
	SourceRunID       string            `json:"source_run_id,omitempty"`
	Title             string            `json:"title"`
	Summary           string            `json:"summary,omitempty"`
	Evidence          string            `json:"evidence,omitempty"`
	Priority          string            `json:"priority,omitempty"`   // low | medium | high | critical
	Confidence        string            `json:"confidence,omitempty"` // low | medium | high
	Status            OpportunityStatus `json:"status"`
	RecommendedAction string            `json:"recommended_action,omitempty"`
	// SeenAt is set the first time the user opens the opportunity. nil means unseen.
	SeenAt *time.Time `json:"seen_at,omitempty"`
	// DismissalReason is captured when the user dismisses; absent otherwise.
	DismissalReason DismissalReason `json:"dismissal_reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DismissedAt     *time.Time      `json:"dismissed_at,omitempty"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
	SnoozedUntil    *time.Time      `json:"snoozed_until,omitempty"`
	// PlannedAt/LinkedTaskID/LinkedWorkspaceID are set by MarkPlanned when this
	// opportunity is promoted to a Backlog item (FR26-29). The link is a
	// reference, not a move — the opportunity record itself is untouched
	// besides these fields, so it retains its own evidence/history.
	// LinkedTaskID/LinkedWorkspaceID double as the idempotency check: a repeat
	// Add to Backlog call for the same opportunity returns the existing linked
	// item instead of creating a duplicate.
	PlannedAt         *time.Time `json:"planned_at,omitempty"`
	LinkedTaskID      string     `json:"linked_task_id,omitempty"`
	LinkedWorkspaceID string     `json:"linked_workspace_id,omitempty"`
}

// IsOpen reports whether the opportunity is in the active triage backlog.
// Resolved and dismissed opportunities are archived and not passed back to
// subsequent runs as context.
func (o Opportunity) IsOpen() bool {
	return o.Status == OpportunityNew || o.Status == OpportunitySnoozed
}

// DedupKey returns a deterministic key used to merge findings about the same
// underlying issue across runs. v1 uses sha256(normalized_title + workspace_id);
// semantic matching is deferred to v1.5.
func DedupKey(workspaceID, title string) string {
	normalized := normalizeTitle(title)
	sum := sha256.Sum256([]byte(normalized + "|" + workspaceID))
	return hex.EncodeToString(sum[:])
}

// normalizeTitle lowercases and strips punctuation, collapsing internal
// whitespace to single spaces. Used so trivial wording differences across runs
// ("Brand voice drift" vs "Brand-voice drift") collapse to one opportunity.
func normalizeTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true // collapse leading whitespace
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastSpace = false
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		default:
			// punctuation — drop, but treat as a word boundary so "drift-A" and
			// "drift A" collapse together.
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// ErrOpportunityNotFound is returned when an opportunity lookup misses.
var ErrOpportunityNotFound = errors.New("opportunity not found")

// ErrMissionTriggerNotConfigured is returned by TriggerMissionManually when
// the scheduler has no MissionTrigger wired in. Surfaces as a 503 from the
// manual-trigger HTTP endpoint so the user understands the missions feature
// is configured-but-incomplete rather than broken.
var ErrMissionTriggerNotConfigured = errors.New("mission trigger not configured on this server")

// ErrMissionBindingsUnclassified is returned when a mission run is requested
// but the workspace has at least one enabled MCP/skill binding without a
// DefaultSideEffect set. The HTTP layer surfaces this as a 412 with a hint
// pointing to the classification UI.
var ErrMissionBindingsUnclassified = errors.New("workspace has unclassified bindings; classify them before enabling missions")

// OpportunityStore is the per-workspace CRUD surface for opportunities. v1
// persistence lives inline on the Workspace struct; this interface exists so
// callers depend on the API rather than on the underlying storage choice, and
// so v1.5 can move opportunities to a separate file or table without churn.
type OpportunityStore interface {
	List(workspaceID string) ([]Opportunity, error)
	Get(workspaceID, opportunityID string) (Opportunity, error)
	// Upsert inserts a new opportunity or merges into an existing one based on
	// dedup key. Returns the resulting opportunity (post-merge) and a bool
	// indicating whether a merge happened (true) or a new record was inserted
	// (false). Merge appends evidence, bumps UpdatedAt, and re-evaluates
	// priority/confidence (highest wins). Status is preserved on merge so a
	// snoozed opportunity stays snoozed.
	Upsert(opp Opportunity) (Opportunity, bool, error)
	Delete(workspaceID, opportunityID string) error

	// MarkSeen sets SeenAt to now if not already set. Idempotent: subsequent
	// calls are no-ops. Used by the Action Center's "implicit read" UX:
	// opening an opportunity counts as seeing it.
	MarkSeen(workspaceID, opportunityID string) error
	// Dismiss sets Status=dismissed, DismissedAt=now, and (optionally) the
	// DismissalReason. Pass empty string for no reason.
	Dismiss(workspaceID, opportunityID string, reason DismissalReason) error
	// Snooze sets Status=snoozed and SnoozedUntil. If until is zero, returns
	// an error — callers must pick a concrete snooze target.
	Snooze(workspaceID, opportunityID string, until time.Time) error
	// MarkResolved sets Status=resolved and ResolvedAt=now.
	MarkResolved(workspaceID, opportunityID string) error
	// MarkPlanned sets Status=planned, PlannedAt=now, and links the given
	// Backlog task (FR26-29). Overwrites any previous link — callers that need
	// idempotent "return the existing item" behavior should check
	// Status==OpportunityPlanned && LinkedTaskID!="" via Get before calling.
	MarkPlanned(workspaceID, opportunityID, taskID, taskWorkspaceID string) error
}

// workspaceOpportunityStore is the v1 implementation that reads and writes
// opportunities through the workspace Store, keeping them in the workspace's
// Opportunities slice. Cross-workspace queries are handled at the
// actioncenterhttp layer by iterating workspaces.
type workspaceOpportunityStore struct {
	store Store
}

// NewOpportunityStore returns an OpportunityStore backed by the given workspace
// Store. The opportunity records live on workspace.Opportunities; this wrapper
// presents a clean CRUD API and centralizes dedup-merge semantics.
func NewOpportunityStore(store Store) OpportunityStore {
	return &workspaceOpportunityStore{store: store}
}

func (s *workspaceOpportunityStore) List(workspaceID string) ([]Opportunity, error) {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	if len(ws.Opportunities) == 0 {
		return nil, nil
	}
	out := make([]Opportunity, len(ws.Opportunities))
	copy(out, ws.Opportunities)
	return out, nil
}

func (s *workspaceOpportunityStore) Get(workspaceID, opportunityID string) (Opportunity, error) {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return Opportunity{}, err
	}
	for _, o := range ws.Opportunities {
		if o.ID == opportunityID {
			return o, nil
		}
	}
	return Opportunity{}, ErrOpportunityNotFound
}

func (s *workspaceOpportunityStore) Upsert(opp Opportunity) (Opportunity, bool, error) {
	var result Opportunity
	var merged bool
	err := s.store.Update(opp.WorkspaceID, func(ws *Workspace) error {
		now := time.Now()
		key := DedupKey(ws.ID, opp.Title)
		for i := range ws.Opportunities {
			existing := ws.Opportunities[i]
			if DedupKey(ws.ID, existing.Title) != key {
				continue
			}
			// Merge: append new evidence, bump UpdatedAt, re-evaluate
			// priority/confidence (highest wins).
			if opp.Evidence != "" {
				if existing.Evidence != "" {
					existing.Evidence += "\n---\n" + opp.Evidence
				} else {
					existing.Evidence = opp.Evidence
				}
			}
			if comparePriority(opp.Priority, existing.Priority) > 0 {
				existing.Priority = opp.Priority
			}
			if compareConfidence(opp.Confidence, existing.Confidence) > 0 {
				existing.Confidence = opp.Confidence
			}
			if opp.RecommendedAction != "" {
				existing.RecommendedAction = opp.RecommendedAction
			}
			if opp.SourceRunID != "" {
				existing.SourceRunID = opp.SourceRunID
			}
			// Status handling on recurrence: a finding the user had marked
			// resolved has come back, so re-open it for triage (and mark it
			// unseen again so it reads as new). snoozed is a deferral the user
			// chose and dismissed is an explicit "don't surface this" — both
			// keep their status.
			if existing.Status == OpportunityResolved {
				existing.Status = OpportunityNew
				existing.ResolvedAt = nil
				existing.SeenAt = nil
			}
			existing.UpdatedAt = now
			ws.Opportunities[i] = existing
			result = existing
			merged = true
			return nil
		}
		// New record.
		if opp.ID == "" {
			opp.ID = newOpportunityID()
		}
		if opp.Status == "" {
			opp.Status = OpportunityNew
		}
		if opp.CreatedAt.IsZero() {
			opp.CreatedAt = now
		}
		opp.UpdatedAt = now
		ws.Opportunities = append(ws.Opportunities, opp)
		result = opp
		merged = false
		return nil
	})
	if err != nil {
		return Opportunity{}, false, err
	}
	return result, merged, nil
}

func (s *workspaceOpportunityStore) Delete(workspaceID, opportunityID string) error {
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		for i, o := range ws.Opportunities {
			if o.ID == opportunityID {
				ws.Opportunities = append(ws.Opportunities[:i], ws.Opportunities[i+1:]...)
				return nil
			}
		}
		return ErrOpportunityNotFound
	})
}

// updateOpportunity is a shared helper for the status-mutation methods. Walks
// ws.Opportunities, hands the matching record to fn, and persists.
func (s *workspaceOpportunityStore) updateOpportunity(workspaceID, opportunityID string, fn func(*Opportunity)) error {
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		for i := range ws.Opportunities {
			if ws.Opportunities[i].ID == opportunityID {
				fn(&ws.Opportunities[i])
				ws.Opportunities[i].UpdatedAt = time.Now()
				return nil
			}
		}
		return ErrOpportunityNotFound
	})
}

func (s *workspaceOpportunityStore) MarkSeen(workspaceID, opportunityID string) error {
	return s.updateOpportunity(workspaceID, opportunityID, func(o *Opportunity) {
		if o.SeenAt == nil {
			now := time.Now()
			o.SeenAt = &now
		}
	})
}

func (s *workspaceOpportunityStore) Dismiss(workspaceID, opportunityID string, reason DismissalReason) error {
	return s.updateOpportunity(workspaceID, opportunityID, func(o *Opportunity) {
		now := time.Now()
		o.Status = OpportunityDismissed
		o.DismissedAt = &now
		o.DismissalReason = reason
	})
}

func (s *workspaceOpportunityStore) Snooze(workspaceID, opportunityID string, until time.Time) error {
	if until.IsZero() {
		return errors.New("snooze target is required")
	}
	return s.updateOpportunity(workspaceID, opportunityID, func(o *Opportunity) {
		o.Status = OpportunitySnoozed
		o.SnoozedUntil = &until
	})
}

func (s *workspaceOpportunityStore) MarkResolved(workspaceID, opportunityID string) error {
	return s.updateOpportunity(workspaceID, opportunityID, func(o *Opportunity) {
		now := time.Now()
		o.Status = OpportunityResolved
		o.ResolvedAt = &now
	})
}

func (s *workspaceOpportunityStore) MarkPlanned(workspaceID, opportunityID, taskID, taskWorkspaceID string) error {
	return s.updateOpportunity(workspaceID, opportunityID, func(o *Opportunity) {
		now := time.Now()
		o.Status = OpportunityPlanned
		o.PlannedAt = &now
		o.LinkedTaskID = taskID
		o.LinkedWorkspaceID = taskWorkspaceID
	})
}

// priorityRank returns a numeric rank so we can compare priority strings
// without an enum type. Unknown values are treated as zero (lowest).
func priorityRank(p string) int {
	switch p {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	}
	return 0
}

func comparePriority(a, b string) int { return priorityRank(a) - priorityRank(b) }

func confidenceRank(c string) int {
	switch c {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return 0
}

func compareConfidence(a, b string) int { return confidenceRank(a) - confidenceRank(b) }

// newOpportunityID returns a unique opportunity ID.
func newOpportunityID() string { return "opp-" + uuid.New().String() }
