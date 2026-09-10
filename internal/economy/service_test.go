package economy

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeTasks is a TaskSource with no workspace store behind it, so the service's
// rules can be exercised without building a workspace tree.
type fakeTasks struct {
	farms          []FarmTask
	farmsErr       error
	completedTasks int64
	countErr       error
}

func (f *fakeTasks) Farms() ([]FarmTask, error) { return f.farms, f.farmsErr }

func (f *fakeTasks) Task(workspaceID, taskID string) (FarmTask, bool) {
	for _, farm := range f.farms {
		if farm.WorkspaceID == workspaceID && farm.TaskID == taskID {
			return farm, true
		}
	}
	return FarmTask{}, false
}

func (f *fakeTasks) CountCompletedTasks() (int64, error) { return f.completedTasks, f.countErr }

type fakeSettings struct {
	creativeMode bool
	dailyEnergy  int64
}

func (f *fakeSettings) EconomyCreativeMode() bool       { return f.creativeMode }
func (f *fakeSettings) EconomyDailyEnergyTokens() int64 { return f.dailyEnergy }

type fakeEnergy struct{ tokens int64 }

func (f *fakeEnergy) TokensUsedToday() int64 { return f.tokens }

// clock is a controllable time source, so the hourly cap and the duplicate
// window can be tested without sleeping.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newService(t *testing.T) (*Service, *clock, *fakeTasks) {
	t.Helper()
	store := newLedger(t)
	tasks := &fakeTasks{}
	service := NewService(store, tasks, nil, nil)
	c := &clock{at: time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC)}
	service.SetClock(c.now)
	return service, c, tasks
}

func messageSent(id, fingerprint string) workspace.Event {
	return workspace.Event{
		ID:          id,
		Type:        workspace.EventMessageSent,
		WorkspaceID: "ws1",
		Data:        map[string]any{"agent": "Atlas", "message_fingerprint": fingerprint},
	}
}

func taskCompleted(runID string, scheduled bool) workspace.Event {
	return workspace.Event{
		Type:        workspace.EventTaskCompleted,
		WorkspaceID: "ws1",
		Timestamp:   time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC),
		Data: map[string]any{
			"task_id":   "task-1",
			"scheduled": scheduled,
			"run_id":    runID,
		},
	}
}

func balancesOf(t *testing.T, s *Service) Balances {
	t.Helper()
	overview, err := s.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	return Balances{Craft: overview.Craft, Harvest: overview.Harvest}
}

func TestChatMessageEarnsCraft(t *testing.T) {
	service, _, _ := newService(t)

	service.HandleEvent(messageSent("event-1", "abc"))

	if got := balancesOf(t, service).Craft; got != CraftPerChatMessage {
		t.Fatalf("craft = %d, want %d", got, CraftPerChatMessage)
	}
}

// Replaying the same event pays once (FR3).
func TestChatMessageIsCreditedOncePerEvent(t *testing.T) {
	service, clk, _ := newService(t)

	service.HandleEvent(messageSent("event-1", "abc"))
	clk.advance(ChatDuplicateWindow + time.Second) // past the duplicate window
	service.HandleEvent(messageSent("event-1", "abc"))

	if got := balancesOf(t, service).Craft; got != CraftPerChatMessage {
		t.Fatalf("craft = %d after replaying one event, want %d", got, CraftPerChatMessage)
	}
}

// The same message twice in a row inside the window earns nothing (FR6).
func TestDuplicateChatMessageInsideTheWindowEarnsNothing(t *testing.T) {
	service, clk, _ := newService(t)

	service.HandleEvent(messageSent("event-1", "same"))
	clk.advance(ChatDuplicateWindow - time.Second)
	service.HandleEvent(messageSent("event-2", "same"))

	if got := balancesOf(t, service).Craft; got != CraftPerChatMessage {
		t.Fatalf("craft = %d, want %d — the duplicate should have earned nothing", got, CraftPerChatMessage)
	}

	// Past the window the same text is ordinary work again.
	clk.advance(ChatDuplicateWindow + time.Second)
	service.HandleEvent(messageSent("event-3", "same"))
	if got := balancesOf(t, service).Craft; got != 2*CraftPerChatMessage {
		t.Fatalf("craft = %d, want %d — a repeat outside the window earns", got, 2*CraftPerChatMessage)
	}
}

// A different message inside the window is not a duplicate.
func TestDifferentChatMessageInsideTheWindowEarns(t *testing.T) {
	service, clk, _ := newService(t)

	service.HandleEvent(messageSent("event-1", "first"))
	clk.advance(time.Second)
	service.HandleEvent(messageSent("event-2", "second"))

	if got := balancesOf(t, service).Craft; got != 2*CraftPerChatMessage {
		t.Fatalf("craft = %d, want %d", got, 2*CraftPerChatMessage)
	}
}

func TestChatCraftStopsAtTheHourlyCap(t *testing.T) {
	service, clk, _ := newService(t)

	// Well past the cap, each message distinct so the duplicate window is not
	// what is being measured.
	for i := 0; i < int(ChatCraftHourlyCap)+10; i++ {
		service.HandleEvent(messageSent(
			"event-"+time.Duration(i).String(),
			"message-"+time.Duration(i).String(),
		))
		clk.advance(time.Second)
	}

	if got := balancesOf(t, service).Craft; got != ChatCraftHourlyCap {
		t.Fatalf("craft = %d, want the cap %d", got, ChatCraftHourlyCap)
	}

	// The next hour starts a fresh allowance.
	clk.advance(time.Hour)
	service.HandleEvent(messageSent("event-next-hour", "fresh"))
	if got := balancesOf(t, service).Craft; got != ChatCraftHourlyCap+CraftPerChatMessage {
		t.Fatalf("craft = %d after the hour rolled over, want %d",
			got, ChatCraftHourlyCap+CraftPerChatMessage)
	}
}

func TestHandRunTaskEarnsCraft(t *testing.T) {
	service, _, _ := newService(t)

	service.HandleEvent(taskCompleted("run-1", false))

	balances := balancesOf(t, service)
	if balances.Craft != CraftPerManualTask {
		t.Fatalf("craft = %d, want %d", balances.Craft, CraftPerManualTask)
	}
	if balances.Harvest != 0 {
		t.Fatalf("harvest = %d, want 0 — a hand-run task is not Farm output", balances.Harvest)
	}
}

// A Farm run earns nothing until its result is opened (FR10, FR13).
func TestFarmRunCreatesPendingHarvestAndNoCraft(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	tasks.farms = []FarmTask{{
		WorkspaceID:     "ws1",
		TaskID:          "task-1",
		Name:            "Inbox triage",
		Schedule:        &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"},
		ScheduleEnabled: true,
	}}

	service.HandleEvent(taskCompleted("run-1", true))

	balances := balancesOf(t, service)
	if balances.Craft != 0 {
		t.Fatalf("craft = %d, want 0 — a Farm run never earns Craft", balances.Craft)
	}
	if balances.Harvest != 0 {
		t.Fatalf("harvest = %d, want 0 — nothing has been opened yet", balances.Harvest)
	}

	overview, err := service.Overview(ctx)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.PendingByWorkspace["ws1"] != 1 {
		t.Fatalf("pending for ws1 = %d, want 1", overview.PendingByWorkspace["ws1"])
	}
	if len(overview.Farms) != 1 || overview.Farms[0].PendingHarvest != 1 {
		t.Fatalf("farms = %+v, want one Farm with 1 pending", overview.Farms)
	}
	if overview.Farms[0].TierName != "Daily" {
		t.Fatalf("farm tier name = %q, want Daily", overview.Farms[0].TierName)
	}
}

// Two runs of the same Farm pile up; opening the result banks both at once.
func TestOpeningAFarmResultBanksEveryPendingRun(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)

	service.HandleEvent(taskCompleted("run-1", true))
	service.HandleEvent(taskCompleted("run-2", true))

	result, err := service.BankPendingForTask(ctx, "ws1", "task-1")
	if err != nil {
		t.Fatalf("bank: %v", err)
	}
	if result.Banked != 2 {
		t.Fatalf("banked = %d, want 2", result.Banked)
	}
	if result.Harvest != 2*HarvestPerFarmRun {
		t.Fatalf("harvest = %d, want %d", result.Harvest, 2*HarvestPerFarmRun)
	}
}

func TestBankingAnAlreadyOpenedResultIsANoOp(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)

	service.HandleEvent(taskCompleted("run-1", true))
	if _, err := service.BankPendingForTask(ctx, "ws1", "task-1"); err != nil {
		t.Fatalf("first bank: %v", err)
	}

	result, err := service.BankPendingForTask(ctx, "ws1", "task-1")
	if err != nil {
		t.Fatalf("second bank: %v", err)
	}
	if result.Banked != 0 {
		t.Fatalf("banked = %d on a second open, want 0", result.Banked)
	}
	if result.Harvest != HarvestPerFarmRun {
		t.Fatalf("harvest = %d, want %d — the second open paid again", result.Harvest, HarvestPerFarmRun)
	}
}

// Banking a Farm that never ran is a success that pays nothing (FR26).
func TestBankingWithNothingPendingSucceeds(t *testing.T) {
	service, _, _ := newService(t)

	result, err := service.BankPendingForTask(context.Background(), "ws1", "task-unrun")
	if err != nil {
		t.Fatalf("bank: %v", err)
	}
	if result.Banked != 0 || result.Harvest != 0 {
		t.Fatalf("result = %+v, want an empty, successful harvest", result)
	}
}

// Deleting a task drops its uncollected runs without paying (FR12).
func TestDeletingATaskDropsItsPendingHarvest(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)

	service.HandleEvent(taskCompleted("run-1", true))
	service.HandleEvent(workspace.Event{
		Type:        workspace.EventTaskDeleted,
		WorkspaceID: "ws1",
		Data:        map[string]any{"task_id": "task-1"},
	})

	overview, err := service.Overview(ctx)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(overview.PendingByWorkspace) != 0 {
		t.Fatalf("pending = %v, want empty", overview.PendingByWorkspace)
	}
	if overview.Harvest != 0 {
		t.Fatalf("harvest = %d, want 0 — deleting a task paid out", overview.Harvest)
	}
}

// Failed, timed-out, cancelled, and blocked runs earn nothing (FR9). They earn
// nothing structurally: they publish their own event types, and the service only
// listens for completion.
func TestUnsuccessfulRunsEarnNothing(t *testing.T) {
	service, _, _ := newService(t)

	for _, eventType := range []workspace.EventType{
		workspace.EventTaskFailed,
		workspace.EventTaskTimeout,
		workspace.EventTaskBlocked,
	} {
		service.HandleEvent(workspace.Event{
			Type:        eventType,
			WorkspaceID: "ws1",
			Data:        map[string]any{"task_id": "task-1", "scheduled": true},
		})
	}

	balances := balancesOf(t, service)
	if balances.Craft != 0 || balances.Harvest != 0 {
		t.Fatalf("balances = %+v, want everything at zero", balances)
	}
}

func TestBackfillGrantsCraftForWorkAlreadyDone(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	tasks.completedTasks = 4 // 4 x 5 Craft = 20

	if err := service.Backfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if got := balancesOf(t, service).Craft; got != 4*CraftPerManualTask {
		t.Fatalf("craft = %d, want %d", got, 4*CraftPerManualTask)
	}
}

func TestBackfillIsCappedAndRunsOnce(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	tasks.completedTasks = 10_000

	if err := service.Backfill(ctx); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if got := balancesOf(t, service).Craft; got != BackfillGrantCap {
		t.Fatalf("craft = %d, want the cap %d", got, BackfillGrantCap)
	}

	// A restart must not grant again.
	tasks.completedTasks = 20_000
	if err := service.Backfill(ctx); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if got := balancesOf(t, service).Craft; got != BackfillGrantCap {
		t.Fatalf("craft = %d after a second backfill, want %d", got, BackfillGrantCap)
	}
}

// An install with no history still gets a backfill entry, so the guard holds and
// the next restart does not try again (FR32).
func TestBackfillOnAFreshInstallGrantsNothingButStillGuards(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)

	if err := service.Backfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := balancesOf(t, service).Craft; got != 0 {
		t.Fatalf("craft = %d on a fresh install, want 0", got)
	}

	has, err := service.store.HasReason(ctx, ReasonBackfill)
	if err != nil {
		t.Fatalf("has reason: %v", err)
	}
	if !has {
		t.Fatal("a zero grant left no guard entry, so the backfill would run again")
	}
}

// Every Farm that predates the feature gets a free build entry, so it shows its
// badge and is never charged for a build it made before there was a price (FR33).
func TestBackfillGrandfathersExistingFarms(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	tasks.farms = []FarmTask{
		{WorkspaceID: "ws1", TaskID: "task-1", Name: "Inbox triage"},
		{WorkspaceID: "ws2", TaskID: "task-2", Name: "Weekly report"},
	}

	if err := service.Backfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for _, taskID := range []string{"task-1", "task-2"} {
		has, err := service.store.HasEntry(ctx, ResourceCraft, RefKindBuild, taskID)
		if err != nil {
			t.Fatalf("has entry for %s: %v", taskID, err)
		}
		if !has {
			t.Fatalf("%s was not grandfathered", taskID)
		}
	}
	if got := balancesOf(t, service).Craft; got != 0 {
		t.Fatalf("craft = %d, want 0 — grandfathering grants nothing", got)
	}
}

func TestOverviewReportsCreativeModeAndEnergy(t *testing.T) {
	service, _, _ := newService(t)
	service.SetSettingsSource(&fakeSettings{creativeMode: true, dailyEnergy: 2_000_000})
	service.SetEnergySource(&fakeEnergy{tokens: 142_000})

	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if !overview.CreativeMode {
		t.Fatal("creative mode is not reported")
	}
	if overview.Energy.UsedToday != 142_000 {
		t.Fatalf("energy used today = %d, want 142000", overview.Energy.UsedToday)
	}
	if overview.Energy.DailyFigure != 2_000_000 {
		t.Fatalf("energy daily figure = %d, want 2000000", overview.Energy.DailyFigure)
	}
}

// Without settings or a cost tracker the gauge reads zero against the default
// figure rather than dividing by nothing.
func TestOverviewEnergyFallsBackToTheDefaultFigure(t *testing.T) {
	service, _, _ := newService(t)

	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.Energy.UsedToday != 0 {
		t.Fatalf("energy used today = %d, want 0", overview.Energy.UsedToday)
	}
	if overview.Energy.DailyFigure != DefaultDailyEnergyTokens {
		t.Fatalf("energy daily figure = %d, want %d", overview.Energy.DailyFigure, DefaultDailyEnergyTokens)
	}
}

// One unreadable workspace must cost the map its Farm badges, not the whole HUD.
func TestOverviewSurvivesAFailedFarmListing(t *testing.T) {
	service, _, tasks := newService(t)
	tasks.farmsErr = context.DeadlineExceeded

	service.HandleEvent(messageSent("event-1", "abc"))

	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.Craft != CraftPerChatMessage {
		t.Fatalf("craft = %d, want %d — balances must still render", overview.Craft, CraftPerChatMessage)
	}
	if len(overview.Farms) != 0 {
		t.Fatalf("farms = %+v, want none", overview.Farms)
	}
}

// A nil service is the "economy switched off" state at every call site.
func TestNilServiceDoesNothing(t *testing.T) {
	var service *Service

	if service.Available() {
		t.Fatal("a nil service reports itself available")
	}
	if service.CreativeMode() {
		t.Fatal("a nil service reports creative mode")
	}
	service.HandleEvent(messageSent("event-1", "abc")) // must not panic
	if _, err := service.Overview(context.Background()); err == nil {
		t.Fatal("a nil service returned an overview")
	}
}
