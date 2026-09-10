package dailybrief

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Bounds on per-workspace snapshot collection, keeping the eventual LLM
// prompt small and excluding unbounded transcripts/full files (PRD FR113).
const (
	maxTasksPerWorkspace         = 20
	maxOpportunitiesPerWorkspace = 10
	maxSessionsPerWorkspace      = 5
	// maxFollowUpsPerBrief bounds projected commitments globally across the
	// designated HQ and eligible Email Ops owners, never once per workspace.
	maxFollowUpsPerBrief = 10
)

// SourceRef is a stable reference to the entity a brief item came from:
// owning workspace, entity type/id, and a timestamp. Every factual item and
// actionable payload in the eventual synthesized brief must trace back to
// one of these (PRD FR81, task 6.8), and model output is validated against
// the set of refs actually present in the snapshot (task 6.12).
type SourceRef struct {
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceSlug string    `json:"workspace_slug,omitempty"`
	EntityType    string    `json:"entity_type"` // task | opportunity | scheduled_task | session | email_thread
	EntityID      string    `json:"entity_id"`
	Timestamp     time.Time `json:"timestamp"`
	// AccountID is set only for email_thread refs: the connected mailbox account
	// the provider thread belongs to, needed to build a validated open route
	// (task 4.1). Never a token — just the account's stable ID.
	AccountID string `json:"account_id,omitempty"`
}

// Key returns a stable string identity used for allowlist membership.
func (r SourceRef) Key() string {
	return r.EntityType + ":" + r.WorkspaceID + ":" + r.EntityID
}

// TaskSnapshot is a bounded, brief-relevant projection of an open task.
type TaskSnapshot struct {
	Ref           SourceRef
	Description   string
	Status        string
	AssignedAgent string
	Priority      int
}

// OpportunitySnapshot is a bounded projection of an open Action Center item.
type OpportunitySnapshot struct {
	Ref      SourceRef
	Title    string
	Priority string
	Status   string
}

// ScheduledTaskSnapshot is a bounded projection of a scheduled task.
type ScheduledTaskSnapshot struct {
	Ref          SourceRef
	Name         string
	Enabled      bool
	FailureCount int
	LastError    string
	NextRun      *time.Time
	LastRun      *time.Time
}

// SessionSnapshot is a bounded projection of a recent session — known
// metadata only (title, preview, message count), never a full transcript.
type SessionSnapshot struct {
	Ref          SourceRef
	Title        string
	Preview      string
	AgentName    string
	MessageCount int
	UpdatedAt    time.Time
}

// WorkspaceSnapshot is one eligible workspace's bounded projection.
type WorkspaceSnapshot struct {
	WorkspaceID    string
	WorkspaceSlug  string
	Name           string
	AgentCount     int
	OpenTasks      []TaskSnapshot
	Opportunities  []OpportunitySnapshot
	ScheduledTasks []ScheduledTaskSnapshot
	RecentSessions []SessionSnapshot
}

// EmailThreadSnapshot is a bounded projection of one email thread from the
// designated Personal HQ's connected account. Subject/From are sanitized
// upstream by the mailbox runtime; the brief treats them as untrusted display
// text (task 4.6). Email is HQ-scoped, so these live at the Snapshot top level
// rather than per-workspace.
// FollowUpSnapshot is a bounded read-only projection of a commitment owned by
// its referenced workspace. Detail and source content are intentionally
// excluded; OwnerName is hydrated from the authorized workspace, never from
// the follow-up row.
type FollowUpSnapshot struct {
	Ref       SourceRef
	OwnerName string
	Category  string
	Direction string
	Title     string
	Status    string
	DueAt     *time.Time
	Stale     bool
}

type EmailThreadSnapshot struct {
	Ref           SourceRef
	Subject       string
	From          string
	WaitingOnUser bool
	Unread        bool
}

// Snapshot is the bounded, read-only, authorized cross-workspace projection
// Daily Brief synthesis works from.
type Snapshot struct {
	GeneratedAt time.Time
	Workspaces  []WorkspaceSnapshot
	// EmailThreads is the bounded email attention projection for the designated
	// Personal HQ (empty when no HQ, no connected account, or a healthy-empty
	// inbox — distinct from an unreadable source, which appends a Gap).
	EmailThreads []EmailThreadSnapshot
	// FollowUps contains only active/reopened records owned by the current
	// user in the designated HQ or a scope-authorized Email Ops workspace.
	// Nil/empty is healthy when no follow-up source is configured.
	FollowUps []FollowUpSnapshot
	// Gaps names data sources that could not be read (an inaccessible
	// workspace, a failed opportunity/session query, a failed email read, ...)
	// so a missing source is never silently presented as "no activity"
	// (PRD FR86, task 4.3).
	Gaps []string
}

// AllRefs returns every SourceRef present in the snapshot, keyed by
// SourceRef.Key() — the allowlist synthesis output is validated against.
func (s Snapshot) AllRefs() map[string]SourceRef {
	out := map[string]SourceRef{}
	for _, ws := range s.Workspaces {
		for _, t := range ws.OpenTasks {
			out[t.Ref.Key()] = t.Ref
		}
		for _, o := range ws.Opportunities {
			out[o.Ref.Key()] = o.Ref
		}
		for _, st := range ws.ScheduledTasks {
			out[st.Ref.Key()] = st.Ref
		}
		for _, sess := range ws.RecentSessions {
			out[sess.Ref.Key()] = sess.Ref
		}
	}
	for _, e := range s.EmailThreads {
		out[e.Ref.Key()] = e.Ref
	}
	for _, followUp := range s.FollowUps {
		out[followUp.Ref.Key()] = followUp.Ref
	}
	return out
}

// WorkspaceSource is the narrow workspace-listing contract the snapshot
// builder needs. workspace.Store (the folder-backed store already wired in
// production) satisfies it.
type WorkspaceSource interface {
	Get(id string) (*workspace.Workspace, error)
	ListActive() ([]*workspace.Workspace, error)
}

// OpportunitySource is the narrow Action Center contract needed here.
// workspace.OpportunityStore satisfies it.
type OpportunitySource interface {
	List(workspaceID string) ([]workspace.Opportunity, error)
}

// SessionSource is the narrow session-listing contract needed here.
// session.HybridStore satisfies it.
type SessionSource interface {
	ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) ([]session.SessionListItem, error)
}

// MailboxSource is the narrow email contract the brief needs: bounded,
// authorized email-thread projections for the designated Personal HQ. The
// implementation (server wiring) resolves the HQ workspace, the connected
// account, and the most-restrictive access, so the snapshot builder stays
// provider-neutral and never touches credentials.
//
// It distinguishes three outcomes so the brief can honor task 4.3:
//   - (threads, nil): a healthy read (possibly empty — a real "no mail" state).
//   - (nil, ErrEmailNotConfigured): no HQ / no connected account — NOT a gap,
//     the user simply has not set up email.
//   - (nil, other error): a selected source could not be read — the caller
//     appends a named data gap.
type MailboxSource interface {
	BriefEmailThreads(ctx context.Context, userID string) ([]EmailThreadSnapshot, error)
}

// FollowUpSource is the narrow owner-scoped canonical follow-up read used by
// snapshots. It exposes no mutation or ownership-transfer capability.
type FollowUpSource interface {
	List(ctx context.Context, filter followup.Filter) ([]*followup.FollowUp, error)
}

// ErrEmailNotConfigured signals that email is simply not set up for this user's
// HQ (no designation or no connected account), so the brief shows no email
// section and appends NO gap — distinct from an email source that failed to read.
var ErrEmailNotConfigured = errors.New("dailybrief: email is not configured for this personal hq")

// SnapshotSources bundles the read-only dependencies BuildSnapshot needs.
// Any field may be nil; a nil source degrades to a named gap rather than a
// panic or a silently-empty section (PRD FR136).
type SnapshotSources struct {
	Workspaces    WorkspaceSource
	Opportunities OpportunitySource
	Sessions      SessionSource
	// Mailbox is optional; nil means the brief has no email integration wired
	// (no email section, no gap).
	Mailbox MailboxSource
	// FollowUps is optional. Nil or an empty Config.WorkspaceID means not
	// configured and is distinct from a configured owner read failure. Every
	// authorized owner is filtered and validated independently.
	FollowUps FollowUpSource
}

func isGroupWorkspace(ws *workspace.Workspace) bool {
	return strings.EqualFold(strings.TrimSpace(ws.Kind), "group")
}

func isOpenTaskStatus(status workspace.TaskStatus) bool {
	switch status {
	case workspace.TaskStatusPending, workspace.TaskStatusAssigned, workspace.TaskStatusInProgress,
		workspace.TaskStatusWaitingForChoice, workspace.TaskStatusFailed, workspace.TaskStatusTimeout:
		return true
	default:
		return false
	}
}

// BuildSnapshot assembles the bounded cross-workspace projection for
// userID/cfg. It applies the same eligibility rules as workspace navigation
// (current-user access, non-group, active status) plus the config's scope
// (all vs. selected) and future-workspace-inclusion choice (PRD FR106-111,
// task 6.2):
//   - Scope=Selected: only cfg.SelectedWorkspaceIDs, regardless of
//     IncludeFutureWorkspaces (an explicit list has nothing to "include
//     automatically").
//   - Scope=All + IncludeFutureWorkspaces=true (default): every eligible
//     active workspace, including ones created after the config was last
//     saved — no special handling needed, since "all" already means all.
//   - Scope=All + IncludeFutureWorkspaces=false: frozen to the workspaces
//     that existed when the config was last saved (CreatedAt <=
//     cfg.UpdatedAt), so a workspace created afterward is excluded until the
//     user opts back in — proving inclusion is never silent (PRD FR110).
func BuildSnapshot(ctx context.Context, sources SnapshotSources, cfg Config, userID string, now time.Time) Snapshot {
	snap := Snapshot{GeneratedAt: now}
	scope := ResolveWorkspaceScope(sources.Workspaces, cfg, userID)
	followUpOwners := ResolveFollowUpOwnerScope(sources.Workspaces, scope, cfg.WorkspaceID, userID)
	snap.Gaps = append(snap.Gaps, followUpOwners.Gaps...)
	for _, scoped := range scope.Workspaces {
		wsSnap, gaps := buildWorkspaceSnapshot(ctx, sources, scoped.Workspace)
		snap.Workspaces = append(snap.Workspaces, wsSnap)
		snap.Gaps = append(snap.Gaps, gaps...)
	}

	if sources.FollowUps != nil && strings.TrimSpace(cfg.WorkspaceID) != "" {
		failedOwners := newWorkspaceScopeGaps()
		seenRefs := make(map[string]struct{})
		for _, owner := range followUpOwners.Owners {
			items, err := sources.FollowUps.List(ctx, followup.Filter{
				UserID: strings.TrimSpace(userID), WorkspaceID: owner.WorkspaceID,
				Statuses: []followup.Status{followup.StatusActive, followup.StatusReopened},
			})
			if err != nil {
				if owner.WorkspaceID == strings.TrimSpace(cfg.WorkspaceID) {
					failedOwners.add("Personal HQ follow-ups could not be read")
				} else {
					failedOwners.add(fmt.Sprintf("follow-ups for %s could not be read", boundedWorkspaceLabel(owner.Name)))
				}
				continue
			}
			for _, item := range items {
				if item == nil || item.UserID != strings.TrimSpace(userID) || item.WorkspaceID != owner.WorkspaceID ||
					strings.TrimSpace(item.ID) == "" || item.ID != strings.TrimSpace(item.ID) ||
					(item.Status != followup.StatusActive && item.Status != followup.StatusReopened) {
					continue
				}
				ref := SourceRef{
					WorkspaceID: owner.WorkspaceID, WorkspaceSlug: owner.WorkspaceSlug,
					EntityType: "follow_up", EntityID: item.ID, Timestamp: item.UpdatedAt,
				}
				if _, duplicate := seenRefs[ref.Key()]; duplicate {
					continue
				}
				seenRefs[ref.Key()] = struct{}{}
				dueAt := item.DueAt
				snap.FollowUps = append(snap.FollowUps, FollowUpSnapshot{
					Ref: ref, OwnerName: owner.Name,
					Category: string(item.Category), Direction: string(item.Direction), Title: followup.Truncate(item.Title, followup.MaxTitleLen),
					Status: string(item.Status), DueAt: dueAt, Stale: item.IsStale(now),
				})
			}
		}
		snap.Gaps = append(snap.Gaps, failedOwners.values()...)
		sort.SliceStable(snap.FollowUps, func(i, j int) bool {
			left, right := snap.FollowUps[i], snap.FollowUps[j]
			if left.Stale != right.Stale {
				return left.Stale
			}
			if left.DueAt != nil && right.DueAt != nil && !left.DueAt.Equal(*right.DueAt) {
				return left.DueAt.Before(*right.DueAt)
			}
			if (left.DueAt != nil) != (right.DueAt != nil) {
				return left.DueAt != nil
			}
			if !left.Ref.Timestamp.Equal(right.Ref.Timestamp) {
				return left.Ref.Timestamp.Before(right.Ref.Timestamp)
			}
			if left.Ref.WorkspaceID != right.Ref.WorkspaceID {
				return left.Ref.WorkspaceID < right.Ref.WorkspaceID
			}
			return left.Ref.EntityID < right.Ref.EntityID
		})
		if len(snap.FollowUps) > maxFollowUpsPerBrief {
			snap.FollowUps = snap.FollowUps[:maxFollowUpsPerBrief]
		}
	}

	// Email is HQ-scoped and read through its own most-restrictive access
	// boundary, so it is collected once (not per workspace). A not-configured
	// mailbox is NOT a gap; only a selected source that fails to read is
	// (task 4.3). Email failure degrades only this source — it never blocks the
	// rest of the brief.
	if sources.Mailbox != nil {
		threads, err := sources.Mailbox.BriefEmailThreads(ctx, userID)
		switch {
		case errors.Is(err, ErrEmailNotConfigured):
			// No HQ email set up — no section, no gap.
		case err != nil:
			snap.Gaps = append(snap.Gaps, "email attention could not be read")
		default:
			snap.EmailThreads = threads
		}
	}
	return snap
}

// BuildAllScopeSnapshot assembles the live, bounded cross-workspace projection
// used by Personal HQ surfaces that must never inherit a user's saved Daily
// Brief scope. It deliberately includes every currently eligible workspace,
// including workspaces created after any Daily Brief configuration was saved.
func BuildAllScopeSnapshot(ctx context.Context, sources SnapshotSources, userID string, now time.Time) Snapshot {
	return BuildSnapshot(ctx, sources, Config{
		Scope:                   ScopeAll,
		IncludeFutureWorkspaces: true,
	}, userID, now)
}

func buildWorkspaceSnapshot(ctx context.Context, sources SnapshotSources, ws *workspace.Workspace) (WorkspaceSnapshot, []string) {
	out := WorkspaceSnapshot{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, Name: ws.Name, AgentCount: len(ws.AgentInstances)}
	var gaps []string

	for _, task := range ws.Tasks {
		if !isOpenTaskStatus(task.Status) {
			continue
		}
		out.OpenTasks = append(out.OpenTasks, TaskSnapshot{
			Ref:           SourceRef{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, EntityType: "task", EntityID: task.ID, Timestamp: task.CreatedAt},
			Description:   task.Description,
			Status:        string(task.Status),
			AssignedAgent: task.To,
			Priority:      task.Priority,
		})
	}
	sort.Slice(out.OpenTasks, func(i, j int) bool { return out.OpenTasks[i].Ref.Timestamp.After(out.OpenTasks[j].Ref.Timestamp) })
	if len(out.OpenTasks) > maxTasksPerWorkspace {
		out.OpenTasks = out.OpenTasks[:maxTasksPerWorkspace]
	}

	for _, st := range ws.ScheduledTasks {
		nextRun, lastRun := st.NextRun, st.LastRun
		out.ScheduledTasks = append(out.ScheduledTasks, ScheduledTaskSnapshot{
			Ref:          SourceRef{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, EntityType: "scheduled_task", EntityID: st.ID, Timestamp: st.UpdatedAt},
			Name:         st.Name,
			Enabled:      st.Enabled,
			FailureCount: st.FailureCount,
			LastError:    st.LastError,
			NextRun:      nextRun,
			LastRun:      lastRun,
		})
	}

	if sources.Opportunities != nil {
		opps, err := sources.Opportunities.List(ws.ID)
		if err != nil {
			gaps = append(gaps, fmt.Sprintf("opportunities for workspace %s are unavailable", ws.Name))
		} else {
			for _, o := range opps {
				if !o.IsOpen() {
					continue
				}
				out.Opportunities = append(out.Opportunities, OpportunitySnapshot{
					Ref:      SourceRef{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, EntityType: "opportunity", EntityID: o.ID, Timestamp: o.UpdatedAt},
					Title:    o.Title,
					Priority: o.Priority,
					Status:   string(o.Status),
				})
			}
			sort.Slice(out.Opportunities, func(i, j int) bool {
				return opportunityPriorityRank(out.Opportunities[i].Priority) > opportunityPriorityRank(out.Opportunities[j].Priority)
			})
			if len(out.Opportunities) > maxOpportunitiesPerWorkspace {
				out.Opportunities = out.Opportunities[:maxOpportunitiesPerWorkspace]
			}
		}
	} else {
		gaps = append(gaps, fmt.Sprintf("opportunities for workspace %s are unavailable", ws.Name))
	}

	if sources.Sessions != nil {
		folderID := ws.ID
		items, err := sources.Sessions.ListSessions(ctx, &session.SessionFilter{FolderID: &folderID}, &session.ListOptions{
			Limit: maxSessionsPerWorkspace, Sort: session.SortByUpdatedDesc,
		})
		if err != nil {
			gaps = append(gaps, fmt.Sprintf("recent sessions for workspace %s are unavailable", ws.Name))
		} else {
			for _, item := range items {
				out.RecentSessions = append(out.RecentSessions, SessionSnapshot{
					Ref:          SourceRef{WorkspaceID: ws.ID, WorkspaceSlug: ws.FolderSlug, EntityType: "session", EntityID: item.ID, Timestamp: item.UpdatedAt},
					Title:        item.Title,
					Preview:      item.Preview,
					AgentName:    item.AgentName,
					MessageCount: item.MessageCount,
					UpdatedAt:    item.UpdatedAt,
				})
			}
		}
	} else {
		gaps = append(gaps, fmt.Sprintf("recent sessions for workspace %s are unavailable", ws.Name))
	}

	return out, gaps
}

func opportunityPriorityRank(p string) int {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
