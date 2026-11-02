package secondbrain

import (
	"errors"
	"testing"
	"time"
)

// nudgeRecord is a connected record whose last look finished at lookAt and kept
// items.
func nudgeRecord(lookAt time.Time, items ...LookItem) *Record {
	source := testSource("/Users/sam/Brain")
	finished := lookAt
	source.LastLook = &Look{ID: "look-1", State: LookFinished, StartedAt: lookAt, FinishedAt: &finished, Items: items}
	return &Record{Sources: []Source{source}}
}

func datedItem(text, due string) LookItem {
	item := testItem(testSource("/Users/sam/Brain"), "Plans.md", text, 1)
	item.Due = due
	return item
}

func TestNudge_StaleAfterSevenDaysAndDatesThatCameDue(t *testing.T) {
	lookAt := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name    string
		now     time.Time
		items   []LookItem
		stale   bool
		days    int
		cameDue int
	}{
		{name: "fresh look, nothing due", now: lookAt.Add(day)},
		{name: "exactly seven days is not stale", now: lookAt.Add(StaleLookAfter)},
		{name: "a minute past seven days is stale", now: lookAt.Add(StaleLookAfter + time.Minute), stale: true, days: 7},
		{name: "eight days", now: lookAt.Add(8 * day), stale: true, days: 8},
		{
			name: "due today came due", now: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
			items: []LookItem{datedItem("Pay rent", "2026-10-22")}, cameDue: 1,
		},
		{
			name: "due on the look's own day did not come due since", now: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
			items: []LookItem{datedItem("Pay rent", "2026-10-20")},
		},
		{
			name: "due before the look did not come due since", now: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
			items: []LookItem{datedItem("Pay rent", "2026-10-01")},
		},
		{
			name: "due tomorrow has not come due", now: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
			items: []LookItem{datedItem("Pay rent", "2026-10-23")},
		},
		{
			name: "stale and came due together", now: lookAt.Add(9 * day),
			items: []LookItem{datedItem("Pay rent", "2026-10-25"), datedItem("Call Ana", "2026-10-26"), datedItem("No date", "")},
			stale: true, days: 9, cameDue: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Nudge(nudgeRecord(lookAt, tc.items...), tc.now)
			if got.Stale != tc.stale || got.Days != tc.days || got.CameDue != tc.cameDue {
				t.Fatalf("Nudge = %+v, want stale %v days %d came due %d", got, tc.stale, tc.days, tc.cameDue)
			}
			if got.Any() != (tc.stale || tc.cameDue > 0) {
				t.Fatalf("Any = %v", got.Any())
			}
			if got.Any() && (got.LookID != "look-1" || !got.LookAt.Equal(lookAt)) {
				t.Fatalf("report names look %q at %v", got.LookID, got.LookAt)
			}
		})
	}
}

func TestNudge_AnsweredItemsDoNotCountAsComingDue(t *testing.T) {
	lookAt := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	done := datedItem("Done one", "2026-10-21")
	done.ActedAs, done.ActedAt = ActedDone, &now
	followed := datedItem("Follow-up one", "2026-10-21")
	followed.ActedAs, followed.ActedAt = ActedFollowUp, &now
	seeded := datedItem("Seeded one", "2026-10-21")
	seeded.Seeded = true
	if got := Nudge(nudgeRecord(lookAt, done, followed, seeded), now); got.Any() {
		t.Fatalf("answered items nudged: %+v", got)
	}
}

func TestNudge_DismissalHoldsUntilTheNextLook(t *testing.T) {
	lookAt := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	now := lookAt.Add(10 * 24 * time.Hour)

	record := nudgeRecord(lookAt)
	before := lookAt.Add(-time.Hour)
	record.NudgeDismissedAt = &before
	if !Nudge(record, now).Stale {
		t.Fatal("a dismissal before the last look hid the nudge")
	}

	if err := record.DismissNudge(now, "nudge-1"); err != nil {
		t.Fatal(err)
	}
	if got := Nudge(record, now.Add(time.Hour)); got.Any() {
		t.Fatalf("a dismissal after the last look still nudges: %+v", got)
	}

	// A new look after the dismissal brings the nudge back once it is stale.
	later := now.Add(time.Hour)
	record.Sources[0].LastLook.FinishedAt = &later
	if got := Nudge(record, later.Add(8*24*time.Hour)); !got.Stale {
		t.Fatalf("after a newer look: %+v, want stale", got)
	}
}

func TestNudge_NothingWithoutAFinishedLook(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if got := Nudge(&Record{}, now); got.Any() {
		t.Fatalf("nothing connected: %+v", got)
	}
	record := &Record{Sources: []Source{testSource("/Users/sam/Brain")}}
	if got := Nudge(record, now); got.Any() {
		t.Fatalf("no look: %+v", got)
	}
}

func TestRecord_DismissNudgeIsIdempotentByRequestID(t *testing.T) {
	record := nudgeRecord(storeNow)
	if err := record.DismissNudge(storeNow, "nudge-1"); err != nil {
		t.Fatal(err)
	}
	first := *record.NudgeDismissedAt
	if err := record.DismissNudge(storeNow.Add(time.Hour), "nudge-1"); err != nil {
		t.Fatal(err)
	}
	if !record.NudgeDismissedAt.Equal(first) || len(record.Receipts) != 1 {
		t.Fatalf("a replay changed the record: %v, %d receipts", record.NudgeDismissedAt, len(record.Receipts))
	}
	record.Remember(Receipt{RequestID: "other-1", Action: "look", At: storeNow})
	if err := record.DismissNudge(storeNow, "other-1"); !errors.Is(err, ErrRequestReused) {
		t.Fatalf("reused id: err = %v", err)
	}
	if err := record.DismissNudge(storeNow, "has space"); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("bad id: err = %v", err)
	}
	var empty Record
	if err := empty.DismissNudge(storeNow, "nudge-2"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("not connected: err = %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("record after dismissal does not validate: %v", err)
	}
}
