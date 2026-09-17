package mapactivity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func backgroundEvent(clock *fakeClock, eventType workspace.EventType, kind Kind, runID string, data map[string]any) workspace.Event {
	ev := workspace.NewActivityEvent(eventType, "ws-1", string(kind), runID, data)
	ev.Timestamp = clock.Now()
	return ev
}

func unopenedParcels(t *testing.T, store *SQLiteParcelStore) []Parcel {
	t.Helper()
	parcels, err := store.ListUnopened(context.Background())
	if err != nil {
		t.Fatalf("ListUnopened: %v", err)
	}
	return parcels
}

func TestBackground_AScheduledBriefLightsHQAndDeliversAFixedSentence(t *testing.T) {
	h := newParcelHarness(t)
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()

	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityStarted, KindDailyBrief, "claim-1", map[string]any{
		"trigger": "scheduled", "local_date": "2026-09-15",
	}))
	running := h.tracker.Snapshot().Running
	if len(running) != 1 || running[0].Kind != KindDailyBrief || running[0].ActivityID != "daily_brief:claim-1" || running[0].TaskID != "" {
		t.Fatalf("running = %+v", running)
	}

	h.clock.Advance(40 * time.Second)
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindDailyBrief, "claim-1", map[string]any{
		"trigger": "scheduled", "local_date": "2026-09-15", "outcome": "succeeded", "ref_id": "rev-7",
	}))

	if got := h.tracker.Snapshot().Running; len(got) != 0 {
		t.Fatalf("the building goes dark on finish: %+v", got)
	}
	parcels := unopenedParcels(t, h.store)
	if len(parcels) != 1 {
		t.Fatalf("parcels = %+v", parcels)
	}
	p := parcels[0]
	if p.Kind != KindDailyBrief || p.RefID != "rev-7" || p.Title != "Daily Brief" || p.Summary != "Your brief for Tuesday is ready." {
		t.Fatalf("parcel = %+v", p)
	}
	if p.StartedAt == nil || p.ProducedAt.Sub(*p.StartedAt) != 40*time.Second {
		t.Fatalf("a brief's card knows how long it took: %+v", p)
	}

	messages := messagesByName(t, ch)
	activities := messages["activity"]
	if len(activities) != 2 {
		t.Fatalf("a start and a finish, nothing between: %+v", activities)
	}
	finish := activities[1].Payload.(ActivityEvent)
	if finish.Phase != PhaseFinished || finish.Outcome != OutcomeSucceeded || finish.ParcelID != p.ID || finish.Count != nil {
		t.Fatalf("finish = %+v", finish)
	}
	if len(messages["parcel"]) != 1 {
		t.Fatalf("the new parcel is announced: %+v", messages["parcel"])
	}
}

func TestBackground_ABriefTheUserIsWatchingGetsNoParcel(t *testing.T) {
	for _, trigger := range []string{"first_open", "manual"} {
		t.Run(trigger, func(t *testing.T) {
			h := newParcelHarness(t)
			h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityStarted, KindDailyBrief, "claim-1", map[string]any{"trigger": trigger}))
			h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindDailyBrief, "claim-1", map[string]any{
				"trigger": trigger, "outcome": "succeeded", "ref_id": "rev-1",
			}))
			if got := unopenedParcels(t, h.store); len(got) != 0 {
				t.Fatalf("parcels = %+v", got)
			}
		})
	}
}

func TestBackground_AFailedScheduledBriefSaysSoWithoutItsError(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindDailyBrief, "claim-9", map[string]any{
		"trigger": "scheduled", "outcome": "failed", "error": "SECRET: model key rejected",
	}))
	parcels := unopenedParcels(t, h.store)
	if len(parcels) != 1 {
		t.Fatalf("parcels = %+v", parcels)
	}
	p := parcels[0]
	if p.Outcome != OutcomeFailed || p.FailureReason != "Your Daily Brief could not be prepared." || p.Summary != "" {
		t.Fatalf("parcel = %+v", p)
	}
	if p.RefID != "daily_brief:claim-9" {
		t.Fatalf("a brief with no revision is referenced by its run: %q", p.RefID)
	}
}

func TestBackground_AJanitorScanDeliversOnlyWhenThereIsSomethingToReview(t *testing.T) {
	h := newParcelHarness(t)
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()

	// Nothing new: the building goes dark with "Nothing to tidy." and no parcel.
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityStarted, KindFileJanitor, "ws-1:1", nil))
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindFileJanitor, "ws-1:1", map[string]any{
		"outcome": "succeeded", "count": 0,
	}))
	// A failed scan: no parcel either.
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindFileJanitor, "ws-1:2", map[string]any{
		"outcome": "failed", "count": 0,
	}))
	if got := unopenedParcels(t, h.store); len(got) != 0 {
		t.Fatalf("parcels = %+v", got)
	}

	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityStarted, KindFileJanitor, "ws-1:3", nil))
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindFileJanitor, "ws-1:3", map[string]any{
		"outcome": "succeeded", "count": 4, "ref_id": "batch-3",
	}))
	parcels := unopenedParcels(t, h.store)
	if len(parcels) != 1 || parcels[0].Summary != "4 files are ready to review." || parcels[0].RefID != "batch-3" || parcels[0].Title != "File Janitor" {
		t.Fatalf("parcels = %+v", parcels)
	}

	var counts []int
	for _, msg := range messagesByName(t, ch)["activity"] {
		ev := msg.Payload.(ActivityEvent)
		if ev.Phase != PhaseFinished {
			continue
		}
		if ev.Outcome == OutcomeFailed {
			if ev.Count != nil {
				t.Fatalf("a failed scan claims no count: %+v", ev)
			}
			continue
		}
		counts = append(counts, *ev.Count)
	}
	if len(counts) != 2 || counts[0] != 0 || counts[1] != 4 {
		t.Fatalf("finish counts = %v", counts)
	}
}

func TestBackground_StreamCarriesNoBriefOrFileDetails(t *testing.T) {
	h := newParcelHarness(t)
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()
	h.tracker.HandleEvent(backgroundEvent(h.clock, workspace.EventActivityFinished, KindFileJanitor, "ws-1:5", map[string]any{
		"outcome": "succeeded", "count": 1, "ref_id": "batch-5", "path": "/Users/me/SECRET.pdf",
	}))
	for _, messages := range messagesByName(t, ch) {
		for _, msg := range messages {
			raw, _ := json.Marshal(msg.Payload)
			if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "batch-5") {
				t.Fatalf("stream leaked %s", raw)
			}
		}
	}
	if got := unopenedParcels(t, h.store); len(got) != 1 || got[0].Summary != "1 file is ready to review." {
		t.Fatalf("parcels = %+v", got)
	}
}

// The tracker subscribes to a list of event types; a kind of work missing from
// it would light nothing however well HandleEvent handles it.
func TestBackground_ArrivesThroughTheRealBus(t *testing.T) {
	bus := workspace.NewEventBus(10, 10)
	tracker := NewTracker(bus)
	tracker.Start()
	defer tracker.Stop()
	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	bus.Publish(workspace.NewActivityEvent(workspace.EventActivityStarted, "hq", string(KindDailyBrief), "claim-1", map[string]any{"trigger": "scheduled"}))
	select {
	case msg := <-ch:
		ev := msg.Payload.(ActivityEvent)
		if ev.Kind != KindDailyBrief || ev.Phase != PhaseStarted || ev.WorkspaceID != "hq" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a background activity never reached the stream")
	}
}

func TestBackground_UnknownKindsAndLateStartsAreIgnored(t *testing.T) {
	clock := newFakeClock()
	tracker := newTestTracker(clock)
	tracker.HandleEvent(backgroundEvent(clock, workspace.EventActivityStarted, Kind("mystery"), "x", nil))
	if got := tracker.Snapshot().Running; len(got) != 0 {
		t.Fatalf("running = %+v", got)
	}

	// The bus does not order deliveries: a start that arrives after its own
	// finish must not relight the building.
	tracker.HandleEvent(backgroundEvent(clock, workspace.EventActivityFinished, KindDailyBrief, "claim-2", map[string]any{"outcome": "succeeded", "trigger": "manual"}))
	tracker.HandleEvent(backgroundEvent(clock, workspace.EventActivityStarted, KindDailyBrief, "claim-2", map[string]any{"trigger": "manual"}))
	if got := tracker.Snapshot().Running; len(got) != 0 {
		t.Fatalf("a late start relit the building: %+v", got)
	}
}
