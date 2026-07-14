package dailybrief

import (
	"testing"
	"time"
)

func refAt(entityType, id string, ts time.Time) SourceRef {
	return SourceRef{WorkspaceID: "ws-1", EntityType: entityType, EntityID: id, Timestamp: ts}
}

func TestComputeNeedsAttention_OrdersBySeverityAndCaps(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{
		WorkspaceID: "ws-1", Name: "One",
		OpenTasks: []TaskSnapshot{
			{Ref: refAt("task", "t-choice", now), Status: "waiting_for_choice", Description: "waiting"},
			{Ref: refAt("task", "t-failed", now), Status: "failed", Description: "failed"},
			{Ref: refAt("task", "t-timeout", now), Status: "timeout", Description: "timeout"},
			{Ref: refAt("task", "t-pending", now), Status: "pending", Description: "pending, not attention-worthy"},
		},
		Opportunities: []OpportunitySnapshot{
			{Ref: refAt("opportunity", "o-high", now), Priority: "high", Title: "high opp"},
			{Ref: refAt("opportunity", "o-low", now), Priority: "low", Title: "low opp, not attention-worthy"},
		},
		ScheduledTasks: []ScheduledTaskSnapshot{
			{Ref: refAt("scheduled_task", "st-failing", now), Enabled: true, FailureCount: 2, Name: "failing schedule"},
			{Ref: refAt("scheduled_task", "st-ok", now), Enabled: true, FailureCount: 0, Name: "healthy schedule"},
		},
	}}}

	items := ComputeNeedsAttention(snap)
	if len(items) != 5 {
		t.Fatalf("expected exactly the 5 attention-worthy items (cap not needed here), got %d: %+v", len(items), items)
	}
	// Failed must rank above timeout, which ranks above waiting_for_choice,
	// which ranks above high-priority opportunity, which ranks above a
	// failing schedule.
	wantOrder := []string{"failed", "timeout", "waiting_for_choice", "high_priority_opportunity", "schedule_failing"}
	for i, want := range wantOrder {
		if items[i].Reason != want {
			t.Fatalf("position %d: expected reason %q, got %q (full: %+v)", i, want, items[i].Reason, items)
		}
	}
}

func TestComputeNeedsAttention_CapsAtFive(t *testing.T) {
	now := time.Now()
	var tasks []TaskSnapshot
	for i := 0; i < 8; i++ {
		tasks = append(tasks, TaskSnapshot{Ref: refAt("task", string(rune('a'+i)), now), Status: "failed", Description: "failed"})
	}
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{WorkspaceID: "ws-1", OpenTasks: tasks}}}
	items := ComputeNeedsAttention(snap)
	if len(items) != maxAttentionItems {
		t.Fatalf("expected cap of %d, got %d", maxAttentionItems, len(items))
	}
}

func TestComputeNeedsAttention_EmptyWhenNothingNeedsAttention(t *testing.T) {
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{
		WorkspaceID: "ws-1",
		OpenTasks:   []TaskSnapshot{{Status: "pending"}, {Status: "in_progress"}},
	}}}
	if items := ComputeNeedsAttention(snap); len(items) != 0 {
		t.Fatalf("expected no attention items on a quiet day, got %+v", items)
	}
}

func TestComputeTodaysPlan_InProgressFirstThenDueSoon(t *testing.T) {
	now := time.Now()
	soon := now.Add(1 * time.Hour)
	later := now.Add(48 * time.Hour) // outside the 24h due-soon window
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{
		WorkspaceID: "ws-1",
		OpenTasks: []TaskSnapshot{
			{Ref: refAt("task", "t1", now), Status: "in_progress", Description: "in progress"},
		},
		ScheduledTasks: []ScheduledTaskSnapshot{
			{Ref: refAt("scheduled_task", "st-soon", now), Enabled: true, NextRun: &soon, Name: "due soon"},
			{Ref: refAt("scheduled_task", "st-later", now), Enabled: true, NextRun: &later, Name: "not due soon"},
		},
	}}}
	items := ComputeTodaysPlan(snap, now)
	if len(items) != 2 {
		t.Fatalf("expected 2 plan items (in_progress + due_soon, excluding the far-future one), got %+v", items)
	}
	if items[0].Reason != "in_progress" {
		t.Fatalf("expected in_progress ranked first, got %+v", items[0])
	}
}

func TestComputeTodaysPlan_CapsAtThree(t *testing.T) {
	now := time.Now()
	var tasks []TaskSnapshot
	for i := 0; i < 5; i++ {
		tasks = append(tasks, TaskSnapshot{Ref: refAt("task", string(rune('a'+i)), now), Status: "in_progress"})
	}
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{WorkspaceID: "ws-1", OpenTasks: tasks}}}
	if items := ComputeTodaysPlan(snap, now); len(items) != maxTodaysPlanItems {
		t.Fatalf("expected cap of %d, got %d", maxTodaysPlanItems, len(items))
	}
}

func TestResolveCheckpoint_FirstBriefUsesBoundedWindow(t *testing.T) {
	now := time.Now()
	since, isFirst := ResolveCheckpoint(nil, now)
	if !isFirst {
		t.Fatal("expected isFirstBrief=true when there is no prior revision")
	}
	if !since.Equal(now.Add(-firstBriefWindow)) {
		t.Fatalf("expected the bounded first-brief window, got %v", since)
	}
}

func TestResolveCheckpoint_SubsequentBriefUsesPreviousGeneratedAt(t *testing.T) {
	now := time.Now()
	prevGenAt := now.Add(-3 * time.Hour)
	since, isFirst := ResolveCheckpoint(&Revision{GeneratedAt: prevGenAt}, now)
	if isFirst {
		t.Fatal("expected isFirstBrief=false when a prior revision exists")
	}
	if !since.Equal(prevGenAt) {
		t.Fatalf("expected checkpoint at the previous revision's GeneratedAt, got %v", since)
	}
}

func TestComputeSinceLastBrief_OnlyIncludesChangesAfterCheckpoint(t *testing.T) {
	now := time.Now()
	since := now.Add(-1 * time.Hour)
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{
		WorkspaceID: "ws-1",
		OpenTasks: []TaskSnapshot{
			{Ref: refAt("task", "old", now.Add(-2*time.Hour)), Description: "old, before checkpoint"},
			{Ref: refAt("task", "new", now.Add(-30*time.Minute)), Description: "new, after checkpoint"},
		},
	}}}
	items := ComputeSinceLastBrief(snap, since)
	if len(items) != 1 || items[0].Ref.EntityID != "new" {
		t.Fatalf("expected only the post-checkpoint change, got %+v", items)
	}
}

func TestComputeResumeCandidates_OrdersByRecencyAndCaps(t *testing.T) {
	now := time.Now()
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{
		WorkspaceID: "ws-1",
		RecentSessions: []SessionSnapshot{
			{Ref: refAt("session", "old", now.Add(-2*time.Hour)), Title: "old"},
			{Ref: refAt("session", "new", now.Add(-10*time.Minute)), Title: "new", Preview: "left off here"},
		},
	}}}
	items := ComputeResumeCandidates(snap, 5)
	if len(items) != 2 || items[0].Ref.EntityID != "new" {
		t.Fatalf("expected most recent session first, got %+v", items)
	}
	if !items[0].HasPreview || items[0].Preview != "left off here" {
		t.Fatalf("expected the preview to be carried through as grounded metadata, got %+v", items[0])
	}
	if items[1].HasPreview {
		t.Fatalf("expected no invented preview for a session with none, got %+v", items[1])
	}
}

func TestComputeResumeCandidates_DefaultsLimitWhenNonPositive(t *testing.T) {
	now := time.Now()
	var sessions []SessionSnapshot
	for i := 0; i < defaultResumeLimit+3; i++ {
		sessions = append(sessions, SessionSnapshot{Ref: refAt("session", string(rune('a'+i)), now.Add(-time.Duration(i)*time.Minute))})
	}
	snap := Snapshot{Workspaces: []WorkspaceSnapshot{{WorkspaceID: "ws-1", RecentSessions: sessions}}}
	if items := ComputeResumeCandidates(snap, 0); len(items) != defaultResumeLimit {
		t.Fatalf("expected default cap of %d, got %d", defaultResumeLimit, len(items))
	}
}
