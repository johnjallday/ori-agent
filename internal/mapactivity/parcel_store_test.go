package mapactivity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestParcelStore(t *testing.T) *SQLiteParcelStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSQLiteParcelStore(db)
}

var parcelClock = time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

func taskParcel(workspaceID, taskID, runKey string) Parcel {
	started := parcelClock.Add(-2 * time.Minute)
	return Parcel{
		WorkspaceID: workspaceID,
		Kind:        KindTask,
		RefID:       taskID,
		RunKey:      runKey,
		AgentName:   "Theo",
		Title:       "Compare launch notes",
		Summary:     "Two dates disagree.",
		Outcome:     OutcomeSucceeded,
		StartedAt:   &started,
		ProducedAt:  parcelClock,
		XP: XPReport{
			Awarded: 50, LevelBefore: 1, LevelAfter: 2,
			ProgressBefore: 0.8, ProgressAfter: 0.3,
			StageBefore: "spark", StageAfter: "infant",
		},
	}
}

func TestParcelStore_CreateIsIdempotentPerRun(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()

	first, created, err := store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))
	if err != nil || !created || first.ID == "" {
		t.Fatalf("Create() = %+v, %v, %v; want a new parcel", first, created, err)
	}
	again, created, err := store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("replayed Create() = %s, %v, %v; want the existing %s, not created", again.ID, created, err, first.ID)
	}
	if _, created, _ := store.Create(ctx, taskParcel("ws-1", "task-1", "run-2")); !created {
		t.Fatal("a later run of the same task did not get its own parcel")
	}

	got, err := store.Open(ctx, first.ID, parcelClock)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Title != "Compare launch notes" || got.Summary != "Two dates disagree." || got.AgentName != "Theo" ||
		got.StartedAt == nil || !got.StartedAt.Equal(parcelClock.Add(-2*time.Minute)) ||
		!got.ProducedAt.Equal(parcelClock) || got.XP.Awarded != 50 || got.XP.ProgressBefore != 0.8 ||
		got.XP.StageAfter != "infant" || got.Outcome != OutcomeSucceeded {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestParcelStore_CreateRequiresTheIdentity(t *testing.T) {
	store := newTestParcelStore(t)
	for _, broken := range []Parcel{
		{Kind: KindTask, RefID: "t", Outcome: OutcomeSucceeded},
		{WorkspaceID: "ws", RefID: "t", Outcome: OutcomeSucceeded},
		{WorkspaceID: "ws", Kind: KindTask, Outcome: OutcomeSucceeded},
		{WorkspaceID: "ws", Kind: KindTask, RefID: "t"},
	} {
		if _, _, err := store.Create(context.Background(), broken); err == nil {
			t.Errorf("Create(%+v) succeeded, want an error", broken)
		}
	}
}

func TestParcelStore_OpenSetsOpenedAtOnceAndIsHarmlessTwice(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()
	parcel, _, _ := store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))

	first, err := store.Open(ctx, parcel.ID, parcelClock.Add(time.Minute))
	if err != nil || first.OpenedAt == nil {
		t.Fatalf("Open() = %+v, %v; want opened", first, err)
	}
	second, err := store.Open(ctx, parcel.ID, parcelClock.Add(time.Hour))
	if err != nil || second.OpenedAt == nil || !second.OpenedAt.Equal(*first.OpenedAt) {
		t.Fatalf("second Open() = %+v, %v; want the first opened_at kept", second.OpenedAt, err)
	}
	if list, _ := store.ListUnopened(ctx); len(list) != 0 {
		t.Fatalf("unopened after open = %d, want 0", len(list))
	}
	if _, err := store.Open(ctx, "missing", parcelClock); !errors.Is(err, ErrParcelNotFound) {
		t.Fatalf("Open(missing) error = %v, want ErrParcelNotFound", err)
	}
}

func TestParcelStore_OpenByRefOpensOnlyThatReference(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-1", "run-2"))
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-2", "run-1"))
	_, _, _ = store.Create(ctx, taskParcel("ws-2", "task-1", "run-9"))

	opened, err := store.OpenByRef(ctx, KindTask, "ws-1", "task-1", parcelClock)
	if err != nil || len(opened) != 2 {
		t.Fatalf("OpenByRef() = %d, %v; want both runs of task-1 in ws-1", len(opened), err)
	}
	list, _ := store.ListUnopened(ctx)
	if len(list) != 2 {
		t.Fatalf("unopened = %d, want task-2 and the other workspace's parcel", len(list))
	}
	if again, err := store.OpenByRef(ctx, KindTask, "ws-1", "task-1", parcelClock); err != nil || len(again) != 0 {
		t.Fatalf("second OpenByRef() = %d, %v; want nothing left to open", len(again), err)
	}
}

func TestParcelStore_SeeingTheBriefOrConsoleOpensEverythingOfThatKind(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()
	scan := func(workspaceID, batch string) Parcel {
		return Parcel{WorkspaceID: workspaceID, Kind: KindFileJanitor, RefID: batch, RunKey: batch,
			Title: "File Janitor", Summary: "2 files are ready to review.", Outcome: OutcomeSucceeded, ProducedAt: parcelClock}
	}
	_, _, _ = store.Create(ctx, scan("ws-1", "batch-1"))
	_, _, _ = store.Create(ctx, scan("ws-1", "batch-2"))
	_, _, _ = store.Create(ctx, scan("ws-2", "batch-3"))
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))

	opened, err := store.OpenByRef(ctx, KindFileJanitor, "ws-1", "", parcelClock)
	if err != nil || len(opened) != 2 {
		t.Fatalf("OpenByRef(no ref) = %d, %v; want both of ws-1's scans", len(opened), err)
	}
	if list, _ := store.ListUnopened(ctx); len(list) != 2 {
		t.Fatalf("unopened = %+v; want the other workspace's scan and the task", list)
	}
	if none, err := store.OpenByRef(ctx, KindTask, "ws-1", "", parcelClock); err != nil || len(none) != 0 {
		t.Fatalf("a task needs its reference: %d, %v", len(none), err)
	}
}

func TestParcelStore_DeletesAndSweep(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-1", "run-1"))
	_, _, _ = store.Create(ctx, taskParcel("ws-1", "task-2", "run-1"))
	old := taskParcel("ws-2", "task-3", "run-1")
	old.ProducedAt = parcelClock.Add(-15 * 24 * time.Hour)
	_, _, _ = store.Create(ctx, old)

	if n, err := store.DeleteForTask(ctx, "ws-1", "task-1"); err != nil || n != 1 {
		t.Fatalf("DeleteForTask = %d, %v; want 1", n, err)
	}
	if n, err := store.SweepUnopenedOlderThan(ctx, parcelClock.Add(-14*24*time.Hour), parcelClock); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want the 15-day-old parcel", n, err)
	}
	if n, err := store.DeleteForWorkspace(ctx, "ws-1"); err != nil || n != 1 {
		t.Fatalf("DeleteForWorkspace = %d, %v; want 1", n, err)
	}
	if list, _ := store.ListUnopened(ctx); len(list) != 0 {
		t.Fatalf("unopened = %+v, want none", list)
	}
}

func TestParcelStore_ListsOldestFirstAcrossFractionalSeconds(t *testing.T) {
	store := newTestParcelStore(t)
	ctx := context.Background()
	later := taskParcel("ws-1", "later", "r")
	later.ProducedAt = parcelClock.Add(500 * time.Millisecond)
	whole := taskParcel("ws-1", "whole", "r")
	whole.ProducedAt = parcelClock
	_, _, _ = store.Create(ctx, later)
	_, _, _ = store.Create(ctx, whole)

	list, err := store.ListUnopened(ctx)
	if err != nil || len(list) != 2 || list[0].RefID != "whole" {
		t.Fatalf("ListUnopened = %+v, %v; want the whole-second parcel first", list, err)
	}
}

func TestParcelStore_NilIsUnavailable(t *testing.T) {
	var store *SQLiteParcelStore
	if _, err := store.ListUnopened(context.Background()); !errors.Is(err, ErrParcelStoreUnavailable) {
		t.Fatalf("error = %v, want ErrParcelStoreUnavailable", err)
	}
}
