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
	items []*followup.FollowUp
	err   error
}

func (s stubTodayFollowUps) List(context.Context, followup.Filter) ([]*followup.FollowUp, error) {
	return s.items, s.err
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
