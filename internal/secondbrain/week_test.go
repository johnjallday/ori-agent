package secondbrain

import (
	"testing"
	"time"
)

func TestWeekSentence_CountsInTheOrderTheUserKnowsThem(t *testing.T) {
	for name, tc := range map[string]struct {
		counts WeekCounts
		want   string
	}{
		"the PRD's example": {
			WeekCounts{Facts: 4, People: 2, Projects: 1, Captures: 5},
			"This week your second brain gained 4 facts, 2 people, 1 project and 5 captures.",
		},
		"one of one kind": {
			WeekCounts{Captures: 1},
			"This week your second brain gained 1 capture.",
		},
		"singular everywhere": {
			WeekCounts{Facts: 1, People: 1, Projects: 1, FollowUps: 1, Captures: 1, Journals: 1},
			"This week your second brain gained 1 fact, 1 person, 1 project, 1 follow-up, 1 capture and 1 journal entry.",
		},
		"plural journal": {
			WeekCounts{Journals: 3, FollowUps: 2},
			"This week your second brain gained 2 follow-ups and 3 journal entries.",
		},
		"a quiet week is said plainly": {
			WeekCounts{},
			"Your second brain had a quiet week: nothing new was added.",
		},
	} {
		if got := WeekSentence(tc.counts); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, tc.want)
		}
	}
}

// The week is a calendar count in the user's own zone: a setup late in the evening
// is due on the same morning as one made at breakfast, and the clock moving an hour
// does not move the day.
func TestWeekDue_IsTheStartOfTheLocalDaySevenDaysLater(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip("no tz database")
	}
	breakfast := time.Date(2026, 10, 25, 8, 0, 0, 0, seoul)
	late := time.Date(2026, 10, 25, 23, 50, 0, 0, seoul)
	want := time.Date(2026, 11, 1, 0, 0, 0, 0, seoul)
	for name, at := range map[string]time.Time{"breakfast": breakfast, "late evening": late, "the same instant in UTC": late.UTC()} {
		if got := WeekDue(at, seoul); !got.Equal(want) {
			t.Errorf("%s: due %v, want %v", name, got, want)
		}
	}
	// Across the end of daylight saving time the day still starts at midnight.
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	before := time.Date(2026, 10, 30, 12, 0, 0, 0, newYork)
	if got, want := WeekDue(before, newYork), time.Date(2026, 11, 6, 0, 0, 0, 0, newYork); !got.Equal(want) {
		t.Errorf("across the clock change: due %v, want %v", got, want)
	}
}

func TestMirrorCount_CountsEveryStateAndIsSafeOnNothing(t *testing.T) {
	var none *FillIn
	if none.MirrorCount(NoteKindPerson) != 0 {
		t.Fatal("a missing ledger counted notes")
	}
	fillIn := &FillIn{Mirrors: []Mirror{
		{Ref: MirrorRef{Kind: NoteKindPerson}, State: MirrorCurrent},
		{Ref: MirrorRef{Kind: NoteKindPerson, ID: "b"}, State: MirrorUserOwned},
		{Ref: MirrorRef{Kind: NoteKindPerson, ID: "c"}, State: MirrorStale},
		{Ref: MirrorRef{Kind: NoteKindProject}, State: MirrorCurrent},
	}}
	if got := fillIn.MirrorCount(NoteKindPerson); got != 3 {
		t.Fatalf("people = %d, want 3: a note the user took over was still one Ori added", got)
	}
	if got := fillIn.MirrorCount(NoteKindFollowUp); got != 0 {
		t.Fatalf("follow-ups = %d", got)
	}
}
