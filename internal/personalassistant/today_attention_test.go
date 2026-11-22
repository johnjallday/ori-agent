package personalassistant

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestHQInputRequestsUseCanonicalTaskOwner(t *testing.T) {
	hq := &workspace.Workspace{ID: "hq", Tasks: []workspace.Task{
		{ID: "pause", Status: workspace.TaskStatusInProgress, Context: map[string]any{"execution_step_waiting": true}},
		{ID: "closed", Status: workspace.TaskStatusCompleted, Context: map[string]any{"execution_step_waiting": true}},
		{ID: "backlog", Status: workspace.TaskStatusBacklog, Context: map[string]any{"execution_step_waiting": true}},
	}}
	items := hqInputItems(hq, "/workspaces/my-hq")
	if len(items) != 1 {
		t.Fatalf("want one input request, got %+v", items)
	}
	item := items[0]
	if item.Ref.WorkspaceID != "hq" || item.Ref.EntityID != "pause" || item.Route != "/workspaces/my-hq?ticket=pause" || item.State != "waiting_for_choice" {
		t.Fatalf("lost owner: %+v", item)
	}
}

func TestNeedsYouPreservesOrderAcrossOwnersAndPartialReads(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	store, hq := newTodayWorkspace(t, now)
	email := addTodayEmailOpsWorkspace(t, store, now.Add(-time.Hour))
	projection := baseTodayProjection()
	projection.DailyBrief.SelectedWorkspaceIDs = []string{email.ID}
	hq.Tasks = []workspace.Task{{ID: "review", Description: "Review export", Status: workspace.TaskStatusInProgress,
		Context: map[string]any{"execution_step_waiting": true}}}
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	failed := map[string]error{}
	rows := map[string][]*followup.FollowUp{
		hq.ID:    {{ID: "follow-hq", UserID: "local", WorkspaceID: hq.ID, Title: "Reply to Mina", Status: followup.StatusActive, UpdatedAt: now}},
		email.ID: {{ID: "follow-email", UserID: "local", WorkspaceID: email.ID, Title: "Reply to Mina", Status: followup.StatusReopened, UpdatedAt: now.Add(time.Minute)}},
	}
	today := NewTodayService(stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store,
		stubTodayFollowUps{itemsByWorkspace: rows, errorsByWorkspace: failed})
	today.now = func() time.Time { return now }
	out, err := today.Get(t.Context(), "local")
	if err != nil || out.NeedsYou.Health.Status != TodaySectionAvailable {
		t.Fatalf("available: %+v %v", out, err)
	}
	items := out.NeedsYou.Items
	if len(items) != 3 || items[0].Ref.EntityID != "review" || items[1].Ref.EntityID != "follow-hq" || items[2].Ref.EntityID != "follow-email" ||
		items[1].Ref.WorkspaceID != hq.ID || items[2].Ref.WorkspaceID != email.ID || items[1].Title != items[2].Title {
		t.Fatalf("Needs you chronology and identity: %+v", items)
	}
	failed[email.ID] = errors.New("failed in Email Ops")
	out, err = today.Get(t.Context(), "local")
	if err != nil || out.NeedsYou.Health.Status != TodaySectionPartial || len(out.NeedsYou.Items) != 2 || out.NeedsYou.Items[1].Ref != items[1].Ref {
		t.Fatalf("partial source lost good rows: %+v %v", out, err)
	}
	delete(failed, email.ID)
	delete(rows, hq.ID)
	delete(rows, email.ID)
	hq.Tasks = nil
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	out, err = today.Get(t.Context(), "local")
	if err != nil || out.NeedsYou.Health.Status != TodaySectionHealthyEmpty || len(out.NeedsYou.Items) != 0 {
		t.Fatalf("healthy empty: %+v %v", out, err)
	}
}

func TestTodayDeduplicatesCanonicalOwnerNotTitleOrBareID(t *testing.T) {
	a := TodayItem{ID: "same", Kind: "ticket", Title: "Respond", Ref: dailybrief.SourceRef{WorkspaceID: "a", EntityType: "task", EntityID: "same"}}
	brief := a
	brief.Kind = "brief"
	b := a
	b.Ref.WorkspaceID = "b"
	items := uniqueTodayItems([]TodayItem{a, brief, b})
	if len(items) != 2 || items[0].Ref.WorkspaceID != "a" || items[1].Ref.WorkspaceID != "b" {
		t.Fatalf("bad dedup: %+v", items)
	}
}
