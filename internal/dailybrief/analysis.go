package dailybrief

import (
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxAttentionItems caps Needs Attention at 5 primary items (PRD FR72).
const maxAttentionItems = 5

// maxTodaysPlanItems caps Today's Plan recommendations at 3 (PRD FR76).
const maxTodaysPlanItems = 3

// defaultResumeLimit is the default cap on Resume candidates.
const defaultResumeLimit = 5

// firstBriefWindow is the bounded lookback used when there is no prior
// successful brief to checkpoint against (PRD FR75).
const firstBriefWindow = 24 * time.Hour

// AttentionItem is one deterministically-computed "needs attention"
// candidate: waiting approvals/choices, blocked/failed/timed-out work,
// high-priority opportunities, or a failing schedule (PRD FR73).
type AttentionItem struct {
	Ref           SourceRef
	Title         string
	WorkspaceName string
	// Reason is a machine-readable tag: waiting_for_choice | failed |
	// timeout | high_priority_opportunity | schedule_failing.
	Reason string
}

func attentionRank(reason string) int {
	switch reason {
	case "failed":
		return 5
	case "timeout":
		return 4
	case "waiting_for_choice":
		return 3
	// An email thread awaiting the user's reply is a peer of a task waiting for a
	// choice — both are "you need to act". Ties break by timestamp (below), so
	// adding email never reorders existing non-email items of a different rank.
	case "email_waiting_on_user":
		return 3
	case "high_priority_opportunity":
		return 2
	case "schedule_failing":
		return 1
	// Unread inbound mail and threads waiting on someone else are the lowest
	// attention tier — surfaced only if higher-severity items don't fill the cap.
	case "email_unread":
		return 1
	default:
		return 0
	}
}

// ComputeNeedsAttention derives deterministic attention candidates from the
// snapshot, ordered by a fixed severity ranking (never LLM-decided, so
// results are reproducible) and capped at maxAttentionItems.
func ComputeNeedsAttention(snap Snapshot) []AttentionItem {
	var items []AttentionItem
	for _, ws := range snap.Workspaces {
		for _, t := range ws.OpenTasks {
			var reason string
			switch workspace.TaskStatus(t.Status) {
			case workspace.TaskStatusWaitingForChoice:
				reason = "waiting_for_choice"
			case workspace.TaskStatusFailed:
				reason = "failed"
			case workspace.TaskStatusTimeout:
				reason = "timeout"
			default:
				continue
			}
			items = append(items, AttentionItem{Ref: t.Ref, Title: t.Description, WorkspaceName: ws.Name, Reason: reason})
		}
		for _, o := range ws.Opportunities {
			if opportunityPriorityRank(o.Priority) >= opportunityPriorityRank("high") {
				items = append(items, AttentionItem{Ref: o.Ref, Title: o.Title, WorkspaceName: ws.Name, Reason: "high_priority_opportunity"})
			}
		}
		for _, st := range ws.ScheduledTasks {
			if st.Enabled && (st.FailureCount > 0 || st.LastError != "") {
				items = append(items, AttentionItem{Ref: st.Ref, Title: st.Name, WorkspaceName: ws.Name, Reason: "schedule_failing"})
			}
		}
	}
	// Email attention (HQ-scoped, top-level). Each thread keeps its own source
	// ref so an aggregate claim ("N threads waiting") can be traced back to every
	// underlying thread (task 4.5) — never a title-only count.
	for _, e := range snap.EmailThreads {
		switch {
		case e.WaitingOnUser:
			items = append(items, AttentionItem{Ref: e.Ref, Title: emailAttentionTitle(e), WorkspaceName: "Email", Reason: "email_waiting_on_user"})
		case e.Unread:
			items = append(items, AttentionItem{Ref: e.Ref, Title: emailAttentionTitle(e), WorkspaceName: "Email", Reason: "email_unread"})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := attentionRank(items[i].Reason), attentionRank(items[j].Reason)
		if ri != rj {
			return ri > rj
		}
		return items[i].Ref.Timestamp.After(items[j].Ref.Timestamp)
	})
	if len(items) > maxAttentionItems {
		items = items[:maxAttentionItems]
	}
	return items
}

// emailAttentionTitle renders a bounded, human title for an email attention
// item from its already-sanitized subject/sender. Falls back gracefully when a
// field is missing so a thread with no subject still shows something meaningful.
func emailAttentionTitle(e EmailThreadSnapshot) string {
	subject := strings.TrimSpace(e.Subject)
	from := strings.TrimSpace(e.From)
	switch {
	case subject != "" && from != "":
		return subject + " — " + from
	case subject != "":
		return subject
	case from != "":
		return "(no subject) — " + from
	default:
		return "(no subject)"
	}
}

// PlanItem is one Today's Plan recommendation: scheduled/in-progress work or
// a due commitment, with a machine-readable reason (PRD FR76/FR77).
type PlanItem struct {
	Ref           SourceRef
	Title         string
	WorkspaceName string
	// Reason: in_progress | due_soon.
	Reason string
}

// ComputeTodaysPlan derives deterministic plan candidates: work already in
// progress and schedules due within the next 24h, capped at
// maxTodaysPlanItems (in-progress work ranked first — it represents an
// explicit user commitment already underway).
func ComputeTodaysPlan(snap Snapshot, now time.Time) []PlanItem {
	var inProgress, dueSoon []PlanItem
	for _, ws := range snap.Workspaces {
		for _, t := range ws.OpenTasks {
			if workspace.TaskStatus(t.Status) == workspace.TaskStatusInProgress {
				inProgress = append(inProgress, PlanItem{Ref: t.Ref, Title: t.Description, WorkspaceName: ws.Name, Reason: "in_progress"})
			}
		}
		for _, st := range ws.ScheduledTasks {
			if st.Enabled && st.NextRun != nil && !st.NextRun.After(now.Add(24*time.Hour)) {
				dueSoon = append(dueSoon, PlanItem{Ref: st.Ref, Title: st.Name, WorkspaceName: ws.Name, Reason: "due_soon"})
			}
		}
	}
	items := append(inProgress, dueSoon...)
	if len(items) > maxTodaysPlanItems {
		items = items[:maxTodaysPlanItems]
	}
	return items
}

// ChangeItem is one "Since Last Brief" change: a task or session that
// updated after the checkpoint.
type ChangeItem struct {
	Ref           SourceRef
	Title         string
	WorkspaceName string
}

// ResolveCheckpoint returns the "since" boundary for Since Last Brief: the
// previous successful revision's GeneratedAt, or — when none exists — a
// clearly bounded default window so synthesis never implies a previous
// brief existed (PRD FR75).
func ResolveCheckpoint(previous *Revision, now time.Time) (since time.Time, isFirstBrief bool) {
	if previous == nil || previous.GeneratedAt.IsZero() {
		return now.Add(-firstBriefWindow), true
	}
	return previous.GeneratedAt, false
}

// ComputeSinceLastBrief finds tasks/sessions that changed after since.
func ComputeSinceLastBrief(snap Snapshot, since time.Time) []ChangeItem {
	var items []ChangeItem
	for _, ws := range snap.Workspaces {
		for _, t := range ws.OpenTasks {
			if t.Ref.Timestamp.After(since) {
				items = append(items, ChangeItem{Ref: t.Ref, Title: t.Description, WorkspaceName: ws.Name})
			}
		}
		for _, s := range ws.RecentSessions {
			if s.UpdatedAt.After(since) {
				items = append(items, ChangeItem{Ref: s.Ref, Title: s.Title, WorkspaceName: ws.Name})
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Ref.Timestamp.After(items[j].Ref.Timestamp) })
	return items
}

// ResumeItem is a bounded resume-summary candidate for a recent, possibly
// unfinished session. Known metadata only — a missing preview is left
// empty rather than inventing an objective or decision (PRD FR79).
type ResumeItem struct {
	Ref           SourceRef
	Title         string
	Preview       string
	WorkspaceName string
	HasPreview    bool
}

// ComputeResumeCandidates returns up to limit recent sessions across all
// snapshot workspaces, most recently updated first. limit<=0 defaults to
// defaultResumeLimit.
func ComputeResumeCandidates(snap Snapshot, limit int) []ResumeItem {
	if limit <= 0 {
		limit = defaultResumeLimit
	}
	var all []ResumeItem
	for _, ws := range snap.Workspaces {
		for _, s := range ws.RecentSessions {
			all = append(all, ResumeItem{
				Ref: s.Ref, Title: s.Title, Preview: s.Preview, WorkspaceName: ws.Name, HasPreview: s.Preview != "",
			})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Ref.Timestamp.After(all[j].Ref.Timestamp) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}
