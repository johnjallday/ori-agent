package mapactivity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeTaskFacts map[string]TaskFacts

func (f fakeTaskFacts) TaskFacts(workspaceID, taskID string) (TaskFacts, bool) {
	facts, ok := f[workspaceID+"/"+taskID]
	return facts, ok
}

type parcelHarness struct {
	tracker *Tracker
	store   *SQLiteParcelStore
	clock   *fakeClock
	economy bool
	exists  []string
}

func newParcelHarness(t *testing.T) *parcelHarness {
	t.Helper()
	h := &parcelHarness{store: newTestParcelStore(t), clock: newFakeClock(), exists: []string{"ws-1"}}
	started := h.clock.Now().Add(-102 * time.Second)
	h.tracker = NewTracker(nil, WithClock(h.clock.Now))
	h.tracker.SetParcels(ParcelOptions{
		Store: h.store,
		Tasks: fakeTaskFacts{"ws-1/task-1": {
			Title:     "Compare launch notes",
			Summary:   "Two dates disagree.",
			StartedAt: &started,
		}},
		EconomyEnabled: func() bool { return h.economy },
		WorkspaceIDs:   func() ([]string, error) { return h.exists, nil },
	})
	return h
}

func completedTaskEvent(clock *fakeClock, data map[string]any) workspace.Event {
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["run_id"]; !ok {
		data["run_id"] = "run-1"
	}
	return taskEvent(clock, workspace.EventTaskCompleted, data)
}

func messagesByName(t *testing.T, ch <-chan StreamMessage) map[string][]StreamMessage {
	t.Helper()
	out := map[string][]StreamMessage{}
	for {
		select {
		case msg := <-ch:
			out[msg.Name] = append(out[msg.Name], msg)
		default:
			return out
		}
	}
}

func TestParcels_AManualRunGetsOneParcelAnnouncedBeforeItsFinish(t *testing.T) {
	h := newParcelHarness(t)
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()

	h.tracker.HandleEvent(taskEvent(h.clock, workspace.EventTaskStarted, nil))
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{
		"description": "SECRET DESCRIPTION", "result": "SECRET RESULT", "scheduled": false,
		"xp_awarded": int64(50), "level_before": 1, "level_after": 2,
		"progress_before": 0.8, "progress_after": 0.3, "stage_before": "spark", "stage_after": "infant",
	}))

	msgs := messagesByName(t, ch)
	finished := msgs["activity"][len(msgs["activity"])-1].Payload.(ActivityEvent)
	if finished.Phase != PhaseFinished || finished.ParcelID == "" {
		t.Fatalf("finished = %+v, want a parcel id on the finish", finished)
	}
	if len(msgs["parcel"]) != 1 {
		t.Fatalf("parcel messages = %d, want 1", len(msgs["parcel"]))
	}
	row := msgs["parcel"][0].Payload.(ParcelEvent)
	if row.ID != finished.ParcelID || row.Title != "Compare launch notes" || row.AgentName != "Theo" ||
		row.Outcome != OutcomeSucceeded || row.WorkspaceID != "ws-1" || row.Opened {
		t.Fatalf("parcel row = %+v", row)
	}
	raw, _ := json.Marshal(row)
	for _, banned := range []string{"SECRET", "Two dates disagree", "xp", "summary"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("parcel stream row %s leaks %q", raw, banned)
		}
	}

	stored, err := h.store.Open(context.Background(), row.ID, h.clock.Now())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if stored.Summary != "Two dates disagree." || stored.RunKey != "run-1" || stored.XP.Awarded != 50 ||
		stored.XP.StageAfter != "infant" || stored.StartedAt == nil {
		t.Fatalf("stored parcel = %+v", stored)
	}
}

func TestParcels_ReplayedCompletionMakesOneParcel(t *testing.T) {
	h := newParcelHarness(t)
	event := completedTaskEvent(h.clock, nil)
	h.tracker.HandleEvent(event)
	h.clock.Advance(time.Second)
	h.tracker.HandleEvent(event)

	list, _ := h.store.ListUnopened(context.Background())
	if len(list) != 1 {
		t.Fatalf("parcels = %d, want 1", len(list))
	}
}

func TestParcels_FarmRunsBelongToTheEconomyWhileItIsOn(t *testing.T) {
	h := newParcelHarness(t)
	h.economy = true
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{"scheduled": true}))
	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 0 {
		t.Fatalf("Farm run with the economy on made %d parcels, want 0", len(list))
	}

	h.economy = false
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{"scheduled": true, "run_id": "run-2"}))
	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 1 {
		t.Fatalf("Farm run with the economy off made %d parcels, want 1", len(list))
	}
}

func TestParcels_AFailedRunKeepsItsReasonOffTheStream(t *testing.T) {
	h := newParcelHarness(t)
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()

	h.tracker.HandleEvent(taskEvent(h.clock, workspace.EventTaskFailed, map[string]any{"error": "the vendor API refused the key"}))

	msgs := messagesByName(t, ch)
	if len(msgs["parcel"]) != 1 {
		t.Fatalf("parcel messages = %d, want 1", len(msgs["parcel"]))
	}
	for _, msg := range append(msgs["activity"], msgs["parcel"]...) {
		raw, _ := json.Marshal(msg.Payload)
		if strings.Contains(string(raw), "vendor API") {
			t.Errorf("%s message leaks the failure reason: %s", msg.Name, raw)
		}
	}
	list, _ := h.store.ListUnopened(context.Background())
	if len(list) != 1 || list[0].Outcome != OutcomeFailed || list[0].FailureReason != "the vendor API refused the key" || list[0].Summary != "" {
		t.Fatalf("stored = %+v, want a failed parcel carrying its reason", list)
	}
}

func TestParcels_NoParcelForAcceptanceSilenceOrTheFlagOff(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{"accepted": true, "manual": true}))
	h.tracker.HandleEvent(taskEvent(h.clock, workspace.EventTaskStarted, nil))
	h.clock.Advance(DefaultStaleAfter + time.Second)
	h.tracker.SweepStale()

	t.Setenv("ORI_MAP_SHOW_ENABLED", "false")
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{"run_id": "run-9"}))

	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 0 {
		t.Fatalf("parcels = %+v, want none", list)
	}
}

func TestParcels_OpeningTellsEveryMapAndIsHarmlessTwice(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, nil))
	id := h.tracker.Snapshot().Parcels[0].ID

	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()
	first, err := h.tracker.OpenParcel(context.Background(), id)
	if err != nil || first.OpenedAt == nil {
		t.Fatalf("OpenParcel = %+v, %v", first, err)
	}
	h.clock.Advance(time.Hour)
	second, err := h.tracker.OpenParcel(context.Background(), id)
	if err != nil || !second.OpenedAt.Equal(*first.OpenedAt) {
		t.Fatalf("second OpenParcel = %+v, %v; want the same parcel", second, err)
	}
	msgs := messagesByName(t, ch)
	if len(msgs["parcel"]) == 0 || !msgs["parcel"][0].Payload.(ParcelEvent).Opened {
		t.Fatalf("parcel messages = %+v, want an opened notice", msgs["parcel"])
	}
	if got := len(h.tracker.Snapshot().Parcels); got != 0 {
		t.Fatalf("snapshot parcels = %d after open, want 0", got)
	}
}

func TestParcels_OpenByRefOpensTheTasksParcels(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, nil))
	h.clock.Advance(time.Second)
	h.tracker.HandleEvent(taskEvent(h.clock, workspace.EventTaskStarted, nil))
	h.clock.Advance(time.Second)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, map[string]any{"run_id": "run-2"}))

	opened, err := h.tracker.OpenParcelsByRef(context.Background(), KindTask, "ws-1", "task-1")
	if err != nil || len(opened) != 2 {
		t.Fatalf("OpenParcelsByRef = %d, %v; want both runs", len(opened), err)
	}
}

func TestParcels_DeletedTaskTakesItsParcels(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, nil))
	ch, unsubscribe := h.tracker.Subscribe()
	defer unsubscribe()

	h.tracker.HandleEvent(workspace.NewTaskEvent(workspace.EventTaskDeleted, "ws-1", "task-1", "", nil))

	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 0 {
		t.Fatalf("parcels after delete = %d, want 0", len(list))
	}
	if msgs := messagesByName(t, ch); len(msgs["parcel"]) != 1 || !msgs["parcel"][0].Payload.(ParcelEvent).Opened {
		t.Fatalf("parcel messages = %+v, want one removal", msgs["parcel"])
	}
}

func TestParcels_SweepClosesOldParcelsAndDeletedWorkspaces(t *testing.T) {
	h := newParcelHarness(t)
	h.tracker.HandleEvent(completedTaskEvent(h.clock, nil))
	gone := workspace.NewTaskEvent(workspace.EventTaskCompleted, "ws-gone", "task-9", "Ada", map[string]any{"run_id": "r"})
	gone.Timestamp = h.clock.Now()
	h.tracker.HandleEvent(gone)

	h.tracker.SweepParcels(context.Background())
	list, _ := h.store.ListUnopened(context.Background())
	if len(list) != 1 || list[0].WorkspaceID != "ws-1" {
		t.Fatalf("after the first sweep = %+v, want only the existing workspace's parcel", list)
	}

	h.clock.Advance(ParcelMaxAge - time.Minute)
	h.tracker.SweepParcels(context.Background())
	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 1 {
		t.Fatalf("swept a parcel younger than %s", ParcelMaxAge)
	}
	h.clock.Advance(2 * time.Minute)
	h.tracker.SweepParcels(context.Background())
	if list, _ := h.store.ListUnopened(context.Background()); len(list) != 0 {
		t.Fatalf("parcel older than %s still waiting", ParcelMaxAge)
	}
}

func TestParcels_SweepsStopWithTheTracker(t *testing.T) {
	h := newParcelHarness(t)
	tracker := NewTracker(nil, WithClock(h.clock.Now), WithParcelSweepInterval(time.Millisecond))
	tracker.SetParcels(ParcelOptions{Store: h.store})
	tracker.Start()
	done := make(chan struct{})
	go func() {
		tracker.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not end the parcel sweep")
	}
}
