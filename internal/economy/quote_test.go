package economy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func daily() *workspace.ScheduleConfig {
	return &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"}
}

func hourly() *workspace.ScheduleConfig {
	return &workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: time.Hour}
}

func weekly() *workspace.ScheduleConfig {
	return &workspace.ScheduleConfig{Type: workspace.ScheduleWeekly, TimeOfDay: "09:00", DayOfWeek: 3}
}

// existingFarm makes the fake task source report a task that is already a Farm
// on the given cadence, which is the "before" side of every upgrade quote.
func existingFarm(tasks *fakeTasks, schedule *workspace.ScheduleConfig) {
	tasks.farms = []FarmTask{{
		WorkspaceID:     "ws1",
		TaskID:          "task-1",
		Name:            "Inbox triage",
		Schedule:        schedule,
		ScheduleEnabled: true,
	}}
}

func stock(t *testing.T, service *Service, resource string, amount int64) {
	t.Helper()
	if amount <= 0 {
		return
	}
	if _, err := service.store.Credit(context.Background(), Entry{
		Resource: resource, Amount: amount, Reason: ReasonBackfill,
		RefKind: RefKindBackfill, RefID: "stock-" + resource,
	}, time.Now()); err != nil {
		t.Fatalf("stock %s: %v", resource, err)
	}
}

func TestQuoteBuildForANewTask(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	stock(t, service, ResourceCraft, 40)

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", Schedule: daily(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}

	if quote.Action != ActionBuild {
		t.Fatalf("action = %q, want build", quote.Action)
	}
	if quote.Resource != ResourceCraft {
		t.Fatalf("resource = %q, want craft", quote.Resource)
	}
	if quote.Cost != FarmBuildCost {
		t.Fatalf("cost = %d, want %d", quote.Cost, FarmBuildCost)
	}
	if quote.Balance != 40 || !quote.Affordable {
		t.Fatalf("balance = %d affordable = %v, want 40 and affordable", quote.Balance, quote.Affordable)
	}
	if quote.ToTierName != "Daily" {
		t.Fatalf("to tier name = %q, want Daily", quote.ToTierName)
	}
	// A build has no "before": there was no cadence to speed up from.
	if quote.FromTier != 0 || quote.FromTierName != "" {
		t.Fatalf("build quote carried a from-tier: %+v", quote)
	}
}

func TestQuoteBuildIsUnaffordableBelowThePrice(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	stock(t, service, ResourceCraft, FarmBuildCost-1)

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", Schedule: daily(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Affordable {
		t.Fatalf("quote reports affordable at %d craft against a %d cost", quote.Balance, quote.Cost)
	}
}

// Turning a schedule off, or editing a task that never had one, is free (FR20).
func TestQuoteIsFreeWhenTheResultIsNotAFarm(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)

	for _, change := range []ScheduleChange{
		{WorkspaceID: "ws1", TaskID: "task-1", Schedule: daily(), ScheduleEnabled: false},
		{WorkspaceID: "ws1", TaskID: "task-1", Schedule: nil, ScheduleEnabled: true},
		{WorkspaceID: "ws1", TaskID: "task-1",
			Schedule: &workspace.ScheduleConfig{Type: workspace.ScheduleOnce}, ScheduleEnabled: true},
	} {
		quote, err := service.Quote(ctx, change)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		if quote.Action != ActionNone || quote.Cost != 0 {
			t.Fatalf("quote = %+v, want a free no-op", quote)
		}
		if !quote.Affordable {
			t.Fatal("a free change reported itself unaffordable")
		}
	}
}

func TestQuoteUpgradeSumsTheStepsAndShowsWhatItBuys(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	stock(t, service, ResourceHarvest, 60)
	existingFarm(tasks, daily())
	buildEntry(t, service)

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", TaskID: "task-1", Schedule: hourly(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}

	if quote.Action != ActionUpgrade {
		t.Fatalf("action = %q, want upgrade", quote.Action)
	}
	if quote.Resource != ResourceHarvest {
		t.Fatalf("resource = %q, want harvest", quote.Resource)
	}
	// Daily (2) -> Hourly (4) is 20 + 30 (PRD FR19's own example).
	if quote.Cost != 50 {
		t.Fatalf("cost = %d, want 50", quote.Cost)
	}
	if quote.FromTierName != "Daily" || quote.ToTierName != "Hourly" {
		t.Fatalf("tiers = %q -> %q, want Daily -> Hourly", quote.FromTierName, quote.ToTierName)
	}
	if quote.RunsPerDayBefore != 1 || quote.RunsPerDayAfter != 24 {
		t.Fatalf("runs per day = %v -> %v, want 1 -> 24",
			quote.RunsPerDayBefore, quote.RunsPerDayAfter)
	}
}

func TestQuoteSlowingDownIsFree(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	existingFarm(tasks, hourly())
	buildEntry(t, service)

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", TaskID: "task-1", Schedule: weekly(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Action != ActionNone || quote.Cost != 0 {
		t.Fatalf("quote = %+v, want a free no-op", quote)
	}
}

func TestQuoteSameCadenceIsFree(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	existingFarm(tasks, daily())
	buildEntry(t, service)

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", TaskID: "task-1", Schedule: daily(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Action != ActionNone {
		t.Fatalf("action = %q, want none", quote.Action)
	}
}

// A task with a build entry whose schedule is currently OFF has no cadence to
// step up from, so re-enabling it at any speed is free (FR17).
func TestQuoteReEnablingIsFreeAtAnyCadence(t *testing.T) {
	ctx := context.Background()
	service, _, tasks := newService(t)
	buildEntry(t, service)
	// The task exists but is not currently a Farm.
	tasks.farms = []FarmTask{{
		WorkspaceID: "ws1", TaskID: "task-1", Name: "Inbox triage",
		Schedule: daily(), ScheduleEnabled: false,
	}}

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", TaskID: "task-1",
		Schedule:        &workspace.ScheduleConfig{Type: workspace.ScheduleInterval, Interval: 15 * time.Minute},
		ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Action != ActionNone || quote.Cost != 0 {
		t.Fatalf("quote = %+v, want a free re-enable", quote)
	}
}

// Creative mode zeroes the cost but still names the action, so the editor can
// show "Build Farm" struck through rather than showing nothing (FR24, FR39).
func TestQuoteInCreativeModeIsFreeButStillNamesTheAction(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	service.SetSettingsSource(&fakeSettings{creativeMode: true})

	quote, err := service.Quote(ctx, ScheduleChange{
		WorkspaceID: "ws1", Schedule: daily(), ScheduleEnabled: true,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.Action != ActionBuild {
		t.Fatalf("action = %q, want build", quote.Action)
	}
	if quote.Cost != 0 {
		t.Fatalf("cost = %d in creative mode, want 0", quote.Cost)
	}
	if !quote.CreativeMode || !quote.Affordable {
		t.Fatalf("quote = %+v, want creative and affordable", quote)
	}
}

func TestPriceScheduleChangeRefusesAnUnaffordableSave(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	stock(t, service, ResourceCraft, 12)

	_, err := service.PriceScheduleChange(ctx, ScheduleChange{
		WorkspaceID: "ws1", Schedule: daily(), ScheduleEnabled: true,
	})

	var insufficient *InsufficientResourcesError
	if !errors.As(err, &insufficient) {
		t.Fatalf("error = %v, want InsufficientResourcesError", err)
	}
	if insufficient.Resource != ResourceCraft {
		t.Fatalf("resource = %q, want craft", insufficient.Resource)
	}
	if insufficient.Required != FarmBuildCost || insufficient.Balance != 12 {
		t.Fatalf("required/balance = %d/%d, want %d/12",
			insufficient.Required, insufficient.Balance, FarmBuildCost)
	}
	if insufficient.Action != ActionBuild {
		t.Fatalf("action = %q, want build", insufficient.Action)
	}
}

func TestChargeWritesExactlyOneBuildDebit(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	stock(t, service, ResourceCraft, 100)

	change := ScheduleChange{WorkspaceID: "ws1", TaskID: "task-1", Schedule: daily(), ScheduleEnabled: true}
	quote, err := service.Quote(ctx, change)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if err := service.Charge(ctx, change, quote); err != nil {
		t.Fatalf("charge: %v", err)
	}
	// A retried charge for the same build is the same reference, so it collapses
	// rather than charging twice.
	if err := service.Charge(ctx, change, quote); err != nil {
		t.Fatalf("second charge: %v", err)
	}

	if got := balancesOf(t, service).Craft; got != 100-FarmBuildCost {
		t.Fatalf("craft = %d, want %d", got, 100-FarmBuildCost)
	}
}

// Two upgrades to the same tier are two real charges; only a retry of one is
// collapsed. The reference carries the time for exactly that reason.
func TestChargeRecordsSuccessiveUpgradesSeparately(t *testing.T) {
	ctx := context.Background()
	service, clk, _ := newService(t)
	stock(t, service, ResourceHarvest, 100)

	change := ScheduleChange{WorkspaceID: "ws1", TaskID: "task-1", Schedule: hourly(), ScheduleEnabled: true}
	quote := Quote{Action: ActionUpgrade, Resource: ResourceHarvest, Cost: 20, ToTier: TierEverySixHour}

	if err := service.Charge(ctx, change, quote); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	clk.advance(time.Minute)
	if err := service.Charge(ctx, change, quote); err != nil {
		t.Fatalf("second charge: %v", err)
	}

	if got := balancesOf(t, service).Harvest; got != 60 {
		t.Fatalf("harvest = %d, want 60 (two 20-Harvest charges)", got)
	}
}

func TestChargeIsANoOpForAFreeChange(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newService(t)
	stock(t, service, ResourceCraft, 40)

	change := ScheduleChange{WorkspaceID: "ws1", TaskID: "task-1"}
	if err := service.Charge(ctx, change, Quote{Action: ActionNone}); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if got := balancesOf(t, service).Craft; got != 40 {
		t.Fatalf("craft = %d, want 40 untouched", got)
	}
}

// buildEntry marks the fixture's task as already built, which is what makes a
// later save an upgrade rather than a second build.
func buildEntry(t *testing.T, service *Service) {
	t.Helper()
	if _, err := service.store.Credit(context.Background(), Entry{
		Resource: ResourceCraft, Amount: 0, Reason: ReasonGrandfathered,
		RefKind: RefKindBuild, RefID: "task-1",
	}, time.Now()); err != nil {
		t.Fatalf("write build entry: %v", err)
	}
}
