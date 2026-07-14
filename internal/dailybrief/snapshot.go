package dailybrief

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Bounds on per-workspace snapshot collection, keeping the eventual LLM
// prompt small and excluding unbounded transcripts/full files (PRD FR113).
const (
	maxTasksPerWorkspace         = 20
	maxOpportunitiesPerWorkspace = 10
	maxSessionsPerWorkspace      = 5
)

// SourceRef is a stable reference to the entity a brief item came from:
// owning workspace, entity type/id, and a timestamp. Every factual item and
// actionable payload in the eventual synthesized brief must trace back to
// one of these (PRD FR81, task 6.8), and model output is validated against
// the set of refs actually present in the snapshot (task 6.12).
type SourceRef struct {
	WorkspaceID string    `json:"workspace_id"`
	EntityType  string    `json:"entity_type"` // task | opportunity | scheduled_task | session
	EntityID    string    `json:"entity_id"`
	Timestamp   time.Time `json:"timestamp"`
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
	Name           string
	AgentCount     int
	OpenTasks      []TaskSnapshot
	Opportunities  []OpportunitySnapshot
	ScheduledTasks []ScheduledTaskSnapshot
	RecentSessions []SessionSnapshot
}

// Snapshot is the bounded, read-only, authorized cross-workspace projection
// Daily Brief synthesis works from.
type Snapshot struct {
	GeneratedAt time.Time
	Workspaces  []WorkspaceSnapshot
	// Gaps names data sources that could not be read (an inaccessible
	// workspace, a failed opportunity/session query, ...) so a missing
	// source is never silently presented as "no activity" (PRD FR86).
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

// SnapshotSources bundles the read-only dependencies BuildSnapshot needs.
// Any field may be nil; a nil source degrades to a named gap rather than a
// panic or a silently-empty section (PRD FR136).
type SnapshotSources struct {
	Workspaces    WorkspaceSource
	Opportunities OpportunitySource
	Sessions      SessionSource
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
	if sources.Workspaces == nil {
		snap.Gaps = append(snap.Gaps, "workspace data is unavailable")
		return snap
	}

	var candidates []*workspace.Workspace
	if cfg.Scope == ScopeSelected {
		for _, id := range cfg.SelectedWorkspaceIDs {
			ws, err := sources.Workspaces.Get(id)
			if err != nil || ws == nil {
				snap.Gaps = append(snap.Gaps, fmt.Sprintf("workspace %s is unavailable", id))
				continue
			}
			candidates = append(candidates, ws)
		}
	} else {
		all, err := sources.Workspaces.ListActive()
		if err != nil {
			snap.Gaps = append(snap.Gaps, "workspace list is unavailable")
			return snap
		}
		candidates = all
	}

	for _, ws := range candidates {
		if ws == nil || isGroupWorkspace(ws) || ws.Status != workspace.StatusActive {
			continue
		}
		if ws.OwnerUserID != "" && ws.OwnerUserID != userID {
			continue
		}
		if cfg.Scope == ScopeAll && !cfg.IncludeFutureWorkspaces && !cfg.UpdatedAt.IsZero() && ws.CreatedAt.After(cfg.UpdatedAt) {
			continue
		}
		wsSnap, gaps := buildWorkspaceSnapshot(ctx, sources, ws)
		snap.Workspaces = append(snap.Workspaces, wsSnap)
		snap.Gaps = append(snap.Gaps, gaps...)
	}
	return snap
}

func buildWorkspaceSnapshot(ctx context.Context, sources SnapshotSources, ws *workspace.Workspace) (WorkspaceSnapshot, []string) {
	out := WorkspaceSnapshot{WorkspaceID: ws.ID, Name: ws.Name, AgentCount: len(ws.AgentInstances)}
	var gaps []string

	for _, task := range ws.Tasks {
		if !isOpenTaskStatus(task.Status) {
			continue
		}
		out.OpenTasks = append(out.OpenTasks, TaskSnapshot{
			Ref:           SourceRef{WorkspaceID: ws.ID, EntityType: "task", EntityID: task.ID, Timestamp: task.CreatedAt},
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
			Ref:          SourceRef{WorkspaceID: ws.ID, EntityType: "scheduled_task", EntityID: st.ID, Timestamp: st.UpdatedAt},
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
					Ref:      SourceRef{WorkspaceID: ws.ID, EntityType: "opportunity", EntityID: o.ID, Timestamp: o.UpdatedAt},
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
					Ref:          SourceRef{WorkspaceID: ws.ID, EntityType: "session", EntityID: item.ID, Timestamp: item.UpdatedAt},
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
