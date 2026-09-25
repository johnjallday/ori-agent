package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubTodayRelationship struct {
	projection *Projection
	err        error
}

func (s stubTodayRelationship) Get(context.Context, string) (*Projection, error) {
	return s.projection, s.err
}

type stubTodayBrief struct {
	revision *dailybrief.Revision
	err      error
}

func (s stubTodayBrief) GetCurrent(context.Context, string) (*dailybrief.Revision, error) {
	return s.revision, s.err
}

type stubTodayFollowUps struct {
	items             []*followup.FollowUp
	err               error
	itemsByWorkspace  map[string][]*followup.FollowUp
	errorsByWorkspace map[string]error
	filters           *[]followup.Filter
}

func (s stubTodayFollowUps) List(_ context.Context, filter followup.Filter) ([]*followup.FollowUp, error) {
	if s.filters != nil {
		*s.filters = append(*s.filters, filter)
	}
	if err := s.errorsByWorkspace[filter.WorkspaceID]; err != nil {
		return nil, err
	}
	if s.itemsByWorkspace != nil {
		return s.itemsByWorkspace[filter.WorkspaceID], nil
	}
	return s.items, s.err
}

type stubTodaySetup struct {
	projection *TodaySpecialistSetupProjection
	err        error
}

func (s stubTodaySetup) GetSpecialistSetup(context.Context, string) (*TodaySpecialistSetupProjection, error) {
	return s.projection, s.err
}

type stubFolderReceipts struct {
	offers []FolderOffer
	err    error
	userID string
	since  time.Time
	reads  []time.Time
}

func (s *stubFolderReceipts) RecentReceipts(_ context.Context, userID string, since time.Time) ([]FolderOffer, error) {
	s.userID, s.since = userID, since
	s.reads = append(s.reads, since)
	return s.offers, s.err
}

type stubTodayDigest struct {
	stubFolderReceipts
	view FolderDigestView
}

func (s *stubTodayDigest) Current(context.Context, string) (FolderDigestView, error) {
	return s.view, nil
}

func TestTodayService_ThreeSectionsOrderCardsAndSummarizeResults(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store, hq := newTodayWorkspace(t, now)
	hq.CreatedAt = now.Add(-2 * 24 * time.Hour)
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Thesis"})
	project.ID, project.FolderSlug = "project-1", "thesis"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "writing-project", TemplateName: "Writing Project"})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	resolved := now.Add(-time.Hour)
	digest := &stubTodayDigest{
		stubFolderReceipts: stubFolderReceipts{offers: []FolderOffer{{ID: "o1", ResolvedAt: &resolved, Outcome: &FolderOutcome{
			Kind: FolderChoiceProject, WorkspaceID: project.ID, Receipt: []FolderReceiptRow{{Kind: "workspace", Name: "Thesis"}, {Kind: "blueprint", Name: "Writing Project"}},
		}}}},
		view: FolderDigestView{Offer: &FolderOfferView{ID: "o2", Folder: "Documents", Status: FolderOfferPending}},
	}
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetFolderDigestReader(digest)
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.WorkingOn.Items) < 2 || got.WorkingOn.Items[0].ID != "hq-1" ||
		got.WorkingOn.Items[1].Title != "Thesis" || got.WorkingOn.Items[1].Detail != "Writing Project" ||
		len(got.NeedsYou.Items) == 0 || got.NeedsYou.Items[0].Kind != "folder_offer" ||
		len(got.Done.Items) < 3 || got.Done.Items[0].Kind != "hq_setup" || got.Done.Items[1].ID != "o1" {
		t.Fatalf("three Today sections: working=%+v needs=%+v done=%+v", got.WorkingOn.Items, got.NeedsYou.Items, got.Done.Items)
	}
	for _, section := range []TodaySection{got.WorkingOn, got.NeedsYou, got.Done} {
		for _, item := range section.Items {
			if strings.Contains(item.Title, "waiting_for_choice") || strings.Contains(item.Detail, "waiting_for_choice") {
				t.Fatalf("raw state in Today item: %+v", item)
			}
		}
	}
	digest.view.Offer = nil
	service.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	late, err := service.Get(context.Background(), "local")
	if err != nil || len(late.Done.Items) != 1 || late.Done.Items[0].Kind != "result" ||
		len(late.WorkingOn.Items) < 2 || len(late.NeedsYou.Items) != 0 {
		t.Fatalf("expired Done/retained Working on = %+v / %+v, err=%v", late.Done, late.WorkingOn, err)
	}
}

func TestTodayService_CandidatesAndTodaysCompletedFollowUpsHaveSeparateActions(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store, hq := newTodayWorkspace(t, now)
	hq.CreatedAt = now.Add(-30 * 24 * time.Hour)
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	completed := now.Add(-time.Hour)
	earlier := now.Add(-30 * time.Hour)
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store,
		stubTodayFollowUps{items: []*followup.FollowUp{
			{ID: "confirm-1", UserID: "local", WorkspaceID: "hq-1", Status: followup.StatusCandidate, Title: "Launch timing"},
			{ID: "done-1", UserID: "local", WorkspaceID: "hq-1", Status: followup.StatusCompleted, Title: "Call Morgan", CompletedAt: &completed},
			{ID: "old-1", UserID: "local", WorkspaceID: "hq-1", Status: followup.StatusCompleted, Title: "Old result", CompletedAt: &earlier},
		}},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.NeedsYou.Items) != 1 || got.NeedsYou.Items[0].Title != "Confirm Launch timing" ||
		got.NeedsYou.Items[0].Route != "/workspaces/personal-hq?follow_up=confirm-1" ||
		len(got.Done.Items) != 2 || got.Done.Items[1].Title != "Completed Call Morgan" {
		t.Fatalf("follow-up sections: needs=%+v done=%+v", got.NeedsYou.Items, got.Done.Items)
	}
}

func TestTodayService_UnavailableSourcesAppearOnceAndDoNotHideVerifiedRows(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store,
		stubTodayFollowUps{err: errors.New("unavailable")})
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, source := range got.UnavailableSources {
		if seen[source] {
			t.Fatalf("source reported twice: %+v", got.UnavailableSources)
		}
		seen[source] = true
	}
	if !seen["follow-ups"] || !seen["decisions"] || got.NeedsYou.Health.Status != TodaySectionUnavailable ||
		len(got.WorkingOn.Items) == 0 || got.WorkingOn.Items[0].Title != "Personal HQ" {
		t.Fatalf("partial Today lost verified rows: working=%+v needs=%+v unavailable=%+v", got.WorkingOn, got.NeedsYou, got.UnavailableSources)
	}
}

func TestTodayService_SevenDayFolderSetupReceiptsSurviveReloadAndStayBounded(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	for _, ws := range []struct{ id, slug, name string }{{"project-1", "thesis", "Thesis"}, {"janitor-1", "downloads", "Downloads"}} {
		w := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: ws.name})
		w.ID, w.FolderSlug = ws.id, ws.slug
		if err := store.Save(w); err != nil {
			t.Fatal(err)
		}
	}
	projectAt, tidyAt, oldAt := now.Add(-time.Hour), now.Add(-6*24*time.Hour), now.Add(-8*24*time.Hour)
	receipts := &stubFolderReceipts{offers: []FolderOffer{
		{ID: "project", ResolvedAt: &projectAt, Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "project-1", Receipt: []FolderReceiptRow{
			{Kind: "folder", Name: "Drafts"}, {Kind: "blueprint", Name: "Writing project"}, {Kind: "agent", Name: "Editor"}, {Kind: "task", Name: "Summarize drafts"},
		}}},
		{ID: "tidy", ResolvedAt: &tidyAt, Subject: FolderCandidateRecord{Name: "Downloads"}, Outcome: &FolderOutcome{Kind: FolderChoiceTidy, WorkspaceID: "janitor-1"}},
		{ID: "old", ResolvedAt: &oldAt, Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "project-1"}},
		{ID: "missing", ResolvedAt: &projectAt, Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "missing"}},
	}}
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.SetFolderReceiptReader(receipts)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if receipts.userID != "local" || len(receipts.reads) != 2 || !receipts.reads[0].Equal(now.Add(-todayFolderReceiptWindow)) || !receipts.reads[1].IsZero() {
		t.Fatalf("wrong receipt scope: user=%q since=%s", receipts.userID, receipts.since)
	}
	items := got.Results.Items
	if len(items) != 3 || items[0].ID != "project" || items[0].Kind != "folder_setup" ||
		items[0].Title != "Set up Thesis" || !strings.Contains(items[0].Detail, "First task: Summarize drafts") ||
		items[0].Route != "/workspaces/thesis" || items[1].Route != "/workspaces/downloads?panel=file-janitor" ||
		items[2].ID != "result-1" || got.Results.Health.Status != TodaySectionAvailable {
		t.Fatalf("Today setup receipts = %+v; health = %+v", items, got.Results.Health)
	}
	service.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	after, err := service.Get(context.Background(), "local")
	if err != nil || len(after.Results.Items) != 1 || after.Results.Items[0].ID != "result-1" {
		t.Fatalf("expired receipts = %+v, err=%v", after.Results.Items, err)
	}
	receipts.err = errors.New("unavailable")
	unavailable, err := service.Get(context.Background(), "local")
	if err != nil || len(unavailable.Results.Items) != 1 || unavailable.Results.Health.Status == TodaySectionUnavailable {
		t.Fatalf("failed receipt read changed Today: %+v, err=%v", unavailable.Results, err)
	}
}

func baseTodayProjection() *Projection {
	return &Projection{
		State: APIStateActive, StateVersion: 4, DisplayName: "Nova", Appearance: types.NewAgentAppearance(),
		HQWorkspaceID: "hq-1", Availability: Availability{Model: availableSource()},
		DailyBrief: &BriefConfigProjection{
			Timezone: "UTC", ScheduleDays: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			ScheduleTime: "08:00", ScheduleEnabled: true,
		},
	}
}

func newTodayWorkspace(t *testing.T, now time.Time) (*workspace.InMemoryStore, *workspace.Workspace) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	ws.ID = "hq-1"
	ws.FolderSlug = "personal-hq"
	for i := 0; i < 12; i++ {
		due := now.Add(-time.Duration(i+1) * time.Hour)
		ws.Tasks = append(ws.Tasks, workspace.Task{
			ID: fmt.Sprintf("ticket-%02d", i), WorkspaceID: ws.ID, Description: fmt.Sprintf("Priority %02d", i),
			TicketState: workspace.TicketStateReady, DueDate: &due, Priority: i % 4,
			StateRank: int64(100 - i), CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	future := now.Add(48 * time.Hour)
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID: "future-ticket", WorkspaceID: ws.ID, Description: "Future commitment",
		TicketState: workspace.TicketStateReady, DueDate: &future, CreatedAt: now,
	})
	completed := now.Add(-time.Hour)
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID: "result-1", WorkspaceID: ws.ID, Description: "Review prepared notes",
		TicketState: workspace.TicketStateReview, Result: "prepared", CompletedAt: &completed, CreatedAt: now.Add(-2 * time.Hour),
	})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return store, ws
}

func addTodayEmailOpsWorkspace(t *testing.T, store *workspace.InMemoryStore, createdAt time.Time) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Email Ops"})
	ws.ID = "email-ops-1"
	ws.FolderSlug = "email-ops"
	ws.OwnerUserID = "local"
	ws.CreatedAt = createdAt
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func newTodayFollowUps(now time.Time) []*followup.FollowUp {
	items := make([]*followup.FollowUp, 0, 14)
	for i := 0; i < 12; i++ {
		category := followup.CategoryWaitingOn
		if i == 3 {
			category = followup.CategoryNeedsDecision
		}
		due := now.Add(time.Duration(i-4) * time.Hour)
		items = append(items, &followup.FollowUp{
			ID: fmt.Sprintf("follow-%02d", i), UserID: "local", WorkspaceID: "hq-1",
			Category: category, Title: fmt.Sprintf("Follow-up %02d", i), Status: followup.StatusActive,
			DueAt: &due, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-time.Duration(i+1) * time.Hour),
		})
	}
	items = append(items, &followup.FollowUp{ID: "foreign", UserID: "other", WorkspaceID: "hq-1", Category: followup.CategoryNeedsDecision, Title: "foreign", Status: followup.StatusActive})
	return items
}

func TestTodayService_AggregatesBoundedOwnedCanonicalRecordsAndDropsDeletedRefs(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	followUps := newTodayFollowUps(now)
	content := dailybrief.BriefContent{
		OpeningSummary: "There are confirmed items to review.", DataGaps: []string{"calendar unavailable"},
		NeedsAttention: []dailybrief.BriefAttentionItem{
			{Title: "Grounded follow-up", Reason: "due", Ref: dailybrief.SourceRef{WorkspaceID: "hq-1", EntityType: "follow_up", EntityID: "follow-00", Timestamp: now}},
			{Title: "Deleted task", Reason: "stale", Ref: dailybrief.SourceRef{WorkspaceID: "hq-1", EntityType: "task", EntityID: "deleted", Timestamp: now}},
		},
		TodaysPlan: []dailybrief.BriefPlanItem{
			{Title: "Grounded ticket", Reason: "ready", Ref: dailybrief.SourceRef{WorkspaceID: "hq-1", EntityType: "task", EntityID: "ticket-00", Timestamp: now}},
		},
	}
	encoded, _ := json.Marshal(content)
	revision := &dailybrief.Revision{
		ID: "brief-1", WorkspaceID: "hq-1", UserID: "local", ContentJSON: string(encoded), GeneratedAt: now,
	}
	service := NewTodayService(
		stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{revision: revision}, store,
		stubTodayFollowUps{items: followUps},
	)
	service.now = func() time.Time { return now }

	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "partial" || got.HQWorkspaceSlug != "personal-hq" || got.Links.PersonalHQ != "/workspaces/personal-hq" {
		t.Fatalf("unexpected Today identity/routes: %+v", got)
	}
	if len(got.Priorities.Items) != todayPriorityCap || len(got.FollowUps.Items) != todayFollowUpCap || len(got.Results.Items) != 1 {
		t.Fatalf("caps/results wrong: priorities=%d followups=%d results=%d", len(got.Priorities.Items), len(got.FollowUps.Items), len(got.Results.Items))
	}
	if got.Priorities.Items[0].ID != "ticket-11" || got.FollowUps.Items[0].ID != "follow-00" {
		t.Fatalf("deterministic due/stale order wrong: priority=%s followup=%s", got.Priorities.Items[0].ID, got.FollowUps.Items[0].ID)
	}
	if len(got.Decisions.Items) != 1 || got.Decisions.Items[0].ID != "follow-03" {
		t.Fatalf("owned decision projection wrong: %+v", got.Decisions.Items)
	}
	if len(got.Brief.Items) != 2 {
		t.Fatalf("deleted brief ref was not dropped: %+v", got.Brief.Items)
	}
	for _, section := range [][]TodayItem{got.Brief.Items, got.Priorities.Items, got.FollowUps.Items, got.Results.Items} {
		for _, item := range section {
			if !strings.HasPrefix(item.Route, "/workspaces/personal-hq?") || strings.Contains(item.Route, "foreign") {
				t.Fatalf("unsafe/non-canonical Today route: %+v", item)
			}
		}
	}
	if got.NextCheckIn == nil || !got.NextCheckIn.After(now) {
		t.Fatalf("next check-in missing: %+v", got.NextCheckIn)
	}
}

func TestTodayService_AggregatesEmailOpsDecisionsAndGroundsBriefToOwningRoute(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	emailOps := addTodayEmailOpsWorkspace(t, store, now.Add(-24*time.Hour))
	projection := baseTodayProjection()
	projection.DailyBrief.Scope = dailybrief.ScopeSelected
	projection.DailyBrief.SelectedWorkspaceIDs = []string{emailOps.ID}
	projection.DailyBrief.UpdatedAt = now

	hqItem := &followup.FollowUp{
		ID: "same-id", UserID: "local", WorkspaceID: "hq-1", Category: followup.CategoryWaitingOn,
		Title: "HQ follow-up", Status: followup.StatusActive, UpdatedAt: now.Add(-2 * time.Hour),
	}
	emailDecision := &followup.FollowUp{
		ID: "same-id", UserID: "local", WorkspaceID: emailOps.ID, Category: followup.CategoryNeedsDecision,
		Title: "Approve the signed agreement", Counterparty: "Alex", Status: followup.StatusActive,
		UpdatedAt: now.Add(-time.Hour),
	}
	wrongOwner := &followup.FollowUp{
		ID: "wrong-owner", UserID: "local", WorkspaceID: "unrelated", Category: followup.CategoryNeedsDecision,
		Title: "Must not leak", Status: followup.StatusActive, UpdatedAt: now,
	}
	closed := &followup.FollowUp{
		ID: "closed", UserID: "local", WorkspaceID: emailOps.ID, Category: followup.CategoryNeedsDecision,
		Title: "Already completed", Status: followup.StatusCompleted, UpdatedAt: now,
	}
	content := dailybrief.BriefContent{NeedsAttention: []dailybrief.BriefAttentionItem{
		{Title: "Grounded Email Ops decision", Ref: dailybrief.SourceRef{WorkspaceID: emailOps.ID, EntityType: "follow_up", EntityID: emailDecision.ID}},
		{Title: "Wrong-owner collision", Ref: dailybrief.SourceRef{WorkspaceID: "hq-1", EntityType: "follow_up", EntityID: "missing"}},
	}}
	encoded, _ := json.Marshal(content)
	var filters []followup.Filter
	service := NewTodayService(
		stubTodayRelationship{projection: projection},
		stubTodayBrief{revision: &dailybrief.Revision{
			ID: "brief-1", WorkspaceID: "hq-1", UserID: "local", ContentJSON: string(encoded), GeneratedAt: now,
		}},
		store,
		stubTodayFollowUps{
			itemsByWorkspace: map[string][]*followup.FollowUp{
				"hq-1":      {hqItem},
				emailOps.ID: {emailDecision, emailDecision, wrongOwner, closed},
			},
			filters: &filters,
		},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 6 {
		t.Fatalf("filters = %+v, want one read per owner per lifecycle", filters)
	}
	for i, filter := range filters {
		wantStatuses := 2
		if i >= 2 {
			wantStatuses = 1
		}
		if filter.UserID != "local" || filter.WorkspaceID == "" || len(filter.Statuses) != wantStatuses {
			t.Fatalf("unbounded follow-up filter: %+v", filter)
		}
	}
	if len(got.FollowUps.Items) != 2 {
		t.Fatalf("mixed-owner follow-ups = %+v", got.FollowUps)
	}
	if len(got.Decisions.Items) != 1 {
		t.Fatalf("Email Ops decision missing or duplicated: %+v", got.Decisions)
	}
	decision := got.Decisions.Items[0]
	if decision.ID != emailDecision.ID || decision.Ref.WorkspaceID != emailOps.ID || decision.Attribution != "Email Ops" ||
		decision.Ref.WorkspaceSlug != "email-ops" || decision.Route != "/workspaces/email-ops?follow_up=same-id" {
		t.Fatalf("decision not grounded to Email Ops owner: %+v", decision)
	}
	if len(got.Brief.Items) != 1 || got.Brief.Items[0].Route != decision.Route ||
		got.Brief.Items[0].Ref.WorkspaceSlug != "email-ops" || got.Brief.Items[0].Attribution != "Email Ops" {
		t.Fatalf("brief follow-up did not use canonical live owner: %+v", got.Brief)
	}
	for _, item := range append(append([]TodayItem{}, got.FollowUps.Items...), got.Decisions.Items...) {
		if item.ID == wrongOwner.ID || item.ID == closed.ID {
			t.Fatalf("wrong-owner or completed item leaked: %+v", item)
		}
	}
}

func TestTodayService_EmailOpsFailureIsPartialAndRetainsHealthyHQItems(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	emailOps := addTodayEmailOpsWorkspace(t, store, now.Add(-24*time.Hour))
	projection := baseTodayProjection()
	projection.DailyBrief.Scope = dailybrief.ScopeSelected
	projection.DailyBrief.SelectedWorkspaceIDs = []string{emailOps.ID}
	hqDecision := &followup.FollowUp{
		ID: "hq-decision", UserID: "local", WorkspaceID: "hq-1", Category: followup.CategoryNeedsDecision,
		Title: "Healthy HQ decision", Status: followup.StatusActive, UpdatedAt: now,
	}
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store,
		stubTodayFollowUps{
			itemsByWorkspace:  map[string][]*followup.FollowUp{"hq-1": {hqDecision}},
			errorsByWorkspace: map[string]error{emailOps.ID: errors.New("private details")},
		},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "partial" || got.FollowUps.Health.Status != TodaySectionPartial ||
		got.Decisions.Health.Status != TodaySectionPartial {
		t.Fatalf("partial source health = %+v", got)
	}
	if len(got.FollowUps.Items) != 1 || got.FollowUps.Items[0].ID != hqDecision.ID ||
		len(got.Decisions.Items) != 1 || got.Decisions.Items[0].ID != hqDecision.ID {
		t.Fatalf("healthy HQ items erased by Email Ops failure: followups=%+v decisions=%+v", got.FollowUps, got.Decisions)
	}
}

func TestTodayService_UsesPersistedCutoffForFutureEmailOpsScope(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)
	store, _ := newTodayWorkspace(t, now)
	emailOps := addTodayEmailOpsWorkspace(t, store, now.Add(-time.Hour))
	projection := baseTodayProjection()
	projection.DailyBrief.Scope = dailybrief.ScopeAll
	projection.DailyBrief.IncludeFutureWorkspaces = false
	projection.DailyBrief.UpdatedAt = cutoff
	var filters []followup.Filter
	reader := stubTodayFollowUps{itemsByWorkspace: map[string][]*followup.FollowUp{
		emailOps.ID: {{
			ID: "future", UserID: "local", WorkspaceID: emailOps.ID, Title: "Future source",
			Status: followup.StatusActive, UpdatedAt: now,
		}},
	}, filters: &filters}
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store, reader,
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FollowUps.Items) != 0 {
		t.Fatalf("post-cutoff Email Ops leaked with future inclusion disabled: %+v", got.FollowUps)
	}
	for _, filter := range filters {
		if filter.WorkspaceID == emailOps.ID {
			t.Fatalf("post-cutoff Email Ops was queried: %+v", filters)
		}
	}

	projection.DailyBrief.IncludeFutureWorkspaces = true
	filters = nil
	got, err = service.Get(context.Background(), "local")
	if err != nil || len(got.FollowUps.Items) != 1 || got.FollowUps.Items[0].Ref.WorkspaceID != emailOps.ID {
		t.Fatalf("future-enabled Email Ops missing: followups=%+v filters=%+v err=%v", got.FollowUps, filters, err)
	}
}

func TestTodayService_MixedOwnersShareOneDeterministicFollowUpCap(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	emailOps := addTodayEmailOpsWorkspace(t, store, now.Add(-24*time.Hour))
	projection := baseTodayProjection()
	projection.DailyBrief.Scope = dailybrief.ScopeSelected
	projection.DailyBrief.SelectedWorkspaceIDs = []string{emailOps.ID}
	byWorkspace := map[string][]*followup.FollowUp{}
	for _, ownerID := range []string{"hq-1", emailOps.ID} {
		for i := 5; i >= 0; i-- {
			byWorkspace[ownerID] = append(byWorkspace[ownerID], &followup.FollowUp{
				ID: fmt.Sprintf("item-%02d", i), UserID: "local", WorkspaceID: ownerID,
				Title: "Follow-up", Status: followup.StatusActive, UpdatedAt: now,
			})
		}
	}
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store,
		stubTodayFollowUps{itemsByWorkspace: byWorkspace},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FollowUps.Items) != todayFollowUpCap {
		t.Fatalf("cap=%d want=%d", len(got.FollowUps.Items), todayFollowUpCap)
	}
	for i := 0; i < 6; i++ {
		if got.FollowUps.Items[i].Ref.WorkspaceID != emailOps.ID || got.FollowUps.Items[i].ID != fmt.Sprintf("item-%02d", i) {
			t.Fatalf("deterministic mixed-owner order at %d: %+v", i, got.FollowUps.Items)
		}
	}
}

func TestTodayService_IndependentFailuresAndStatesRemainTruthful(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	projection := baseTodayProjection()
	projection.Availability.Model = SourceAvailability{Status: AvailabilityNotConfigured, Reason: "not_configured"}
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: errors.New("brief down")}, store,
		stubTodayFollowUps{err: errors.New("follow-ups down")},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "partial" || got.Brief.Health.Status != TodaySectionUnavailable || got.FollowUps.Health.Status != TodaySectionUnavailable {
		t.Fatalf("failed source was reported as empty/all-clear: %+v", got)
	}
	if got.Priorities.Health.Status != TodaySectionAvailable || len(got.Priorities.Items) == 0 {
		t.Fatalf("successful source was erased by another failure: %+v", got.Priorities)
	}

	projection.State = APIStatePaused
	service = NewTodayService(stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	got, err = service.Get(context.Background(), "local")
	if err != nil || got.State != "paused" || got.NextCheckIn != nil {
		t.Fatalf("paused state scheduled proactive work: state=%s next=%v err=%v", got.State, got.NextCheckIn, err)
	}
}

// panicTodayBrief/panicTodayFollowUps fail the test loudly if the pre-HQ path
// ever reaches a canonical store it has no HQ workspace ID to read from.
type panicTodayBrief struct{}

func (panicTodayBrief) GetCurrent(context.Context, string) (*dailybrief.Revision, error) {
	panic("today: brief store reached with no hq workspace")
}

type panicTodayFollowUps struct{}

func (panicTodayFollowUps) List(context.Context, followup.Filter) ([]*followup.FollowUp, error) {
	panic("today: follow-up store reached with no hq workspace")
}

func TestTodayService_HiredAssistantWithNoHQNeverFetchesOrImpliesAnEmptyHQ(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	// No workspace store row for hq-1 at all: this proves the service never
	// tries to load one for a relationship that has no HQ yet.
	store := workspace.NewInMemoryStore()
	briefs := panicTodayBrief{}
	followUps := panicTodayFollowUps{}

	for _, state := range []APIState{APIStateNeedsHQ, APIStateProvisioningHQ} {
		t.Run(string(state), func(t *testing.T) {
			projection := &Projection{
				State: state, StateVersion: 3, DisplayName: "Atlas",
				Appearance: types.NewAgentAppearance(), Availability: Availability{Model: availableSource()},
			}
			service := NewTodayService(stubTodayRelationship{projection: projection}, briefs, store, followUps)
			service.now = func() time.Time { return now }
			got, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.State != "needs_hq" {
				t.Fatalf("state = %q; want needs_hq", got.State)
			}
			if got.DisplayName != "Atlas" || got.StateVersion != 3 {
				t.Fatalf("hired identity dropped: %+v", got)
			}
			if got.HQWorkspaceID != "" || got.HQWorkspaceSlug != "" {
				t.Fatalf("a nonexistent hq was implied: %+v", got)
			}
			if got.Links.PersonalHQ != "/?quest=build-hq" {
				t.Fatalf("links = %+v; want the guided quest route", got.Links)
			}
			// The relationship never had an HQ workspace, so nothing here may
			// have touched the brief or follow-up stores.
			if len(got.Brief.Items) != 0 {
				t.Fatalf("brief fabricated items: %+v", got.Brief)
			}
		})
	}
}

func TestTodayService_DistinguishesHealthyEmptyAndModelUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	ws.ID, ws.FolderSlug = "hq-1", "personal-hq"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	projection := baseTodayProjection()
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound},
		store, stubTodayFollowUps{},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil || got.State != "healthy_empty" {
		t.Fatalf("healthy empty state=%+v err=%v", got, err)
	}

	projection.Availability.Model = SourceAvailability{Status: AvailabilityNotConfigured, Reason: "not_configured"}
	got, err = service.Get(context.Background(), "local")
	if err != nil || got.State != "model_unavailable" {
		t.Fatalf("model-unavailable state=%+v err=%v", got, err)
	}
}

func TestTodayServiceIncludesCanonicalSpecialistSetupWithoutChangingHQAuthority(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, hq := newTodayWorkspace(t, now)
	projection := baseTodayProjection()
	projection.SpecialistSlug = "music_production"
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound},
		store, stubTodayFollowUps{},
	)
	service.SetSpecialistSetupReader(stubTodaySetup{projection: &TodaySpecialistSetupProjection{
		Health: TodaySourceHealth{Status: TodaySectionAvailable}, JourneyID: "reaper_setup",
		Title: "Set up REAPER", Lifecycle: "ready", ConnectedProjectCount: 2,
		ChildRunCount: 1, UnfinishedChildCount: 1,
		Runs:    []TodaySpecialistSetupRun{{RunID: "root-1", RunKind: "root", Lifecycle: "ready", ProjectWorkspaceID: "project-1"}, {RunID: "child-1", RunKind: "child", Lifecycle: "in_progress"}},
		Actions: []TodaySpecialistSetupAction{{ID: "review_setup", Label: "Review setup"}},
	}})
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.SpecialistSetup == nil || got.SpecialistSetup.ConnectedProjectCount != 2 || got.SpecialistSetup.UnfinishedChildCount != 1 || len(got.SpecialistSetup.Runs) != 2 {
		t.Fatalf("specialist setup = %+v", got.SpecialistSetup)
	}
	persisted, err := store.Get(hq.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.GetAssistantProgramState() != nil || persisted.GetAssistantProjectLink() != nil || len(persisted.DirectoryReferences) != 0 || persisted.GetRuntimeState() != nil {
		t.Fatalf("Today reporting broadened Personal HQ authority: %+v", persisted)
	}
}

func TestTodayServiceSpecialistSetupFailureIsBoundedAndDoesNotHideOtherSources(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	projection := baseTodayProjection()
	projection.SpecialistSlug = "music_production"
	service := NewTodayService(
		stubTodayRelationship{projection: projection}, stubTodayBrief{err: dailybrief.ErrRevisionNotFound},
		store, stubTodayFollowUps{},
	)
	service.SetSpecialistSetupReader(stubTodaySetup{err: errors.New("/Users/private/song.rpp secret-token")})
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "partial" || got.SpecialistSetup == nil || got.SpecialistSetup.Health.Reason != "read_failed" || got.Priorities.Health.Status != TodaySectionAvailable {
		t.Fatalf("setup source failure was not isolated: %+v", got)
	}
	// FR 21: without a read, the card names the integration's install quest,
	// the one quest that resolves whatever the plugin's state.
	if got.SpecialistSetup.JourneyID != "install_ori_reaper" || got.SpecialistSetup.Title != "Install Ori REAPER Plugin" {
		t.Fatalf("read-failed fallback = %q / %q", got.SpecialistSetup.JourneyID, got.SpecialistSetup.Title)
	}
	encoded, _ := json.Marshal(got.SpecialistSetup)
	if strings.Contains(string(encoded), "/Users/") || strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("setup error leaked through Today: %s", encoded)
	}
}

func TestTodayService_RefusesForeignBriefInvalidSlugAndReplacedHQ(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, ws := newTodayWorkspace(t, now)
	service := NewTodayService(
		stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{revision: &dailybrief.Revision{ID: "foreign", WorkspaceID: "hq-1", UserID: "other", ContentJSON: `{}`, GeneratedAt: now}},
		store, stubTodayFollowUps{},
	)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil || got.Brief.Health.Reason != "ownership_mismatch" || got.State != "partial" {
		t.Fatalf("foreign brief not isolated: %+v err=%v", got, err)
	}

	// InMemoryStore intentionally retains the saved pointer, letting this test
	// simulate corrupt persisted presentation data without bypassing the store.
	ws.FolderSlug = "../foreign"
	got, err = service.Get(context.Background(), "local")
	if err != nil || got.State != "partial" || got.Links.PersonalHQ != "" {
		t.Fatalf("invalid slug became a route: %+v err=%v", got, err)
	}

	replaced := baseTodayProjection()
	replaced.HQWorkspaceID = "replaced-hq"
	service = NewTodayService(stubTodayRelationship{projection: replaced}, stubTodayBrief{}, store, stubTodayFollowUps{})
	got, err = service.Get(context.Background(), "local")
	if err != nil || got.State != "partial" || got.Links.PersonalHQ != "" {
		t.Fatalf("replaced HQ leaked stale routes: %+v err=%v", got, err)
	}
}

// todayBriefRevision is a minimal owned brief for hq-1.
func todayBriefRevision(now time.Time) *dailybrief.Revision {
	encoded, _ := json.Marshal(dailybrief.BriefContent{OpeningSummary: "Here is today."})
	return &dailybrief.Revision{ID: "brief-1", WorkspaceID: "hq-1", UserID: "local", ContentJSON: string(encoded), GeneratedAt: now}
}

// Mission 04 completes the first time Today is served with a brief.
func TestTodayService_OnBriefSeenFiresOnceWhenABriefIsServed(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)

	var seen []string
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{revision: todayBriefRevision(now)}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetOnBriefSeen(func(userID string) { seen = append(seen, userID) })

	for range 3 {
		if _, err := service.Get(context.Background(), "local"); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1 || seen[0] != "local" {
		t.Fatalf("brief seen fired %v, want once for local", seen)
	}

	// Paused still shows the brief, so it counts too.
	paused := baseTodayProjection()
	paused.State = APIStatePaused
	pausedService := NewTodayService(stubTodayRelationship{projection: paused},
		stubTodayBrief{revision: todayBriefRevision(now)}, store, stubTodayFollowUps{})
	pausedService.now = func() time.Time { return now }
	var pausedSeen int
	pausedService.SetOnBriefSeen(func(string) { pausedSeen++ })
	if _, err := pausedService.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if pausedSeen != 1 {
		t.Fatalf("paused relationship brief seen = %d, want 1", pausedSeen)
	}
}

func TestTodayService_OnBriefSeenNeverFiresWithoutAServedBrief(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	var fires int
	hook := func(string) { fires++ }

	noBrief := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store, stubTodayFollowUps{})
	noBrief.now = func() time.Time { return now }
	noBrief.SetOnBriefSeen(hook)
	if _, err := noBrief.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}

	needsHQ := &Projection{State: APIStateNeedsHQ, Availability: Availability{Model: availableSource()}}
	noHQ := NewTodayService(stubTodayRelationship{projection: needsHQ},
		stubTodayBrief{revision: todayBriefRevision(now)}, store, stubTodayFollowUps{})
	noHQ.SetOnBriefSeen(hook)
	if _, err := noHQ.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if fires != 0 {
		t.Fatalf("brief seen fired %d times without a served brief", fires)
	}

	// Unset, a served brief is simply not observed.
	unset := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{revision: todayBriefRevision(now)}, store, stubTodayFollowUps{})
	unset.now = func() time.Time { return now }
	if _, err := unset.Get(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
}

type stubJanitorResults struct {
	results []JanitorResult
	err     error
}

func (s stubJanitorResults) JanitorResults(context.Context, string) ([]JanitorResult, error) {
	return s.results, s.err
}

func todayJanitorService(t *testing.T, now time.Time, reader JanitorResultReader) *TodayService {
	t.Helper()
	store, _ := newTodayWorkspace(t, now)
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()},
		stubTodayBrief{err: dailybrief.ErrRevisionNotFound}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	if reader != nil {
		service.SetJanitorResultReader(reader)
	}
	return service
}

func janitorItems(items []TodayItem) []TodayItem {
	var out []TodayItem
	for _, item := range items {
		if item.Kind == "janitor_result" {
			out = append(out, item)
		}
	}
	return out
}

func TestTodayService_JanitorResultLine(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	result := JanitorResult{
		WorkspaceID: "janitor-1", WorkspaceName: "File Janitor", Slug: "file-janitor", FolderName: "Downloads",
		Moved: 14, Trashed: 2, NewestActionID: "action-9", NewestAt: now.Add(-time.Hour),
	}
	service := todayJanitorService(t, now, stubJanitorResults{results: []JanitorResult{result}})
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	items := got.Results.Items
	if len(items) != 2 || items[0].Kind != "result" {
		t.Fatalf("janitor line must follow the HQ results: %+v", items)
	}
	line := items[1]
	want := TodayItem{
		ID: "action-9", Kind: "janitor_result", Title: "Filed 14 files into Downloads/Filed",
		Detail: "2 sent to Trash · Undo from History", Attribution: "File Janitor",
		Route: "/workspaces/file-janitor?panel=file-janitor&tab=history",
		Ref: dailybrief.SourceRef{
			WorkspaceID: "janitor-1", EntityType: "file_janitor_batch", EntityID: "action-9", Timestamp: now.Add(-time.Hour),
		},
		SourceAt: now.Add(-time.Hour),
	}
	if line.ID != want.ID || line.Kind != want.Kind || line.Title != want.Title || line.Detail != want.Detail ||
		line.Attribution != want.Attribution || line.Route != want.Route || line.Ref != want.Ref || !line.SourceAt.Equal(want.SourceAt) {
		t.Fatalf("janitor line = %+v\nwant %+v", line, want)
	}
	if got.Results.Health.Status != TodaySectionAvailable {
		t.Fatalf("results health changed: %+v", got.Results.Health)
	}
}

func TestTodayService_JanitorResultCopyAndWindow(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	base := JanitorResult{
		WorkspaceID: "janitor-1", WorkspaceName: "File Janitor", Slug: "file-janitor", FolderName: "Downloads",
		NewestActionID: "action-1", NewestAt: now.Add(-time.Hour),
	}
	cases := []struct {
		name          string
		mutate        func(*JanitorResult)
		title, detail string
		shown         bool
	}{
		{"one file", func(r *JanitorResult) { r.Moved = 1 }, "Filed 1 file into Downloads/Filed", "Undo from History", true},
		{"many files", func(r *JanitorResult) { r.Moved = 3 }, "Filed 3 files into Downloads/Filed", "Undo from History", true},
		{"moves and trash", func(r *JanitorResult) { r.Moved = 2; r.Trashed = 1 }, "Filed 2 files into Downloads/Filed", "1 sent to Trash · Undo from History", true},
		{"trash only", func(r *JanitorResult) { r.Trashed = 1 }, "Sent 1 file to Trash from Downloads", "Undo from History", true},
		{"older than a day", func(r *JanitorResult) { r.Moved = 2; r.NewestAt = now.Add(-25 * time.Hour) }, "", "", false},
		{"nothing applied", func(r *JanitorResult) {}, "", "", false},
		{"unsafe slug", func(r *JanitorResult) { r.Moved = 1; r.Slug = "../escape" }, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := base
			tc.mutate(&result)
			service := todayJanitorService(t, now, stubJanitorResults{results: []JanitorResult{result}})
			got, err := service.Get(context.Background(), "local")
			if err != nil {
				t.Fatal(err)
			}
			lines := janitorItems(got.Results.Items)
			if !tc.shown {
				if len(lines) != 0 {
					t.Fatalf("unexpected janitor line: %+v", lines)
				}
				return
			}
			if len(lines) != 1 || lines[0].Title != tc.title || lines[0].Detail != tc.detail {
				t.Fatalf("janitor line = %+v, want %q / %q", lines, tc.title, tc.detail)
			}
		})
	}
}

func TestTodayService_JanitorResultsRespectTheCapAndFailQuietly(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	var many []JanitorResult
	for i := range 8 {
		many = append(many, JanitorResult{
			WorkspaceID: fmt.Sprintf("janitor-%d", i), WorkspaceName: "File Janitor", Slug: fmt.Sprintf("janitor-%d", i),
			FolderName: "Downloads", Moved: 1, NewestActionID: fmt.Sprintf("action-%d", i),
			NewestAt: now.Add(-time.Duration(i+1) * time.Minute),
		})
	}
	got, err := todayJanitorService(t, now, stubJanitorResults{results: many}).Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results.Items) != todayResultCap {
		t.Fatalf("results = %d, want the cap %d", len(got.Results.Items), todayResultCap)
	}
	if lines := janitorItems(got.Results.Items); len(lines) == 0 || lines[0].ID != "action-0" {
		t.Fatalf("janitor lines are not newest first: %+v", lines)
	}

	baseline, err := todayJanitorService(t, now, nil).Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	failing, err := todayJanitorService(t, now, stubJanitorResults{err: errors.New("journal unreadable")}).Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(failing.Results.Items) != len(baseline.Results.Items) || failing.Results.Health != baseline.Results.Health ||
		failing.State != baseline.State {
		t.Fatalf("a reader failure changed Today: results=%+v state=%s, baseline results=%+v state=%s",
			failing.Results, failing.State, baseline.Results, baseline.State)
	}
	if len(janitorItems(baseline.Results.Items)) != 0 {
		t.Fatal("no reader still produced a janitor line")
	}
}
