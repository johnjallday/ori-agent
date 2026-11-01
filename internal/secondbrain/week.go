package secondbrain

import (
	"fmt"
	"strings"
	"time"
)

const (
	// WeekDays is how long after setup the first-week recap is due (first-week FR25).
	WeekDays = 7
	// WeekShowsFor is how long the recap row waits to be seen once it is due, and
	// then how long it stays once it has been: a week is a week, not a backlog, so
	// a setup older than two weeks never gets a recap of its "first week".
	WeekShowsFor = 7 * 24 * time.Hour
)

// WeekDue is the moment the recap becomes due: the start of the local day seven
// days after the day the second brain was set up. Dates are calendar dates in
// loc, so a setup at 23:50 is due on the same morning as one at 08:00.
func WeekDue(connectedAt time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	year, month, day := connectedAt.In(loc).Date()
	return time.Date(year, month, day+WeekDays, 0, 0, 0, 0, loc)
}

// MirrorCount is how many notes of one kind the ledger holds, whatever their
// state: a note the user took over was still one Ori added. It never reads a note.
func (f *FillIn) MirrorCount(kind string) int {
	if f == nil {
		return 0
	}
	count := 0
	for _, mirror := range f.Mirrors {
		if mirror.Ref.Kind == kind {
			count++
		}
	}
	return count
}

// WeekCounts are the first week's gains: Facts are the facts the one facts note
// holds, the others are notes.
type WeekCounts struct {
	Facts, People, Projects, FollowUps, Captures, Journals int
}

// Total is everything the week gained.
func (c WeekCounts) Total() int {
	return c.Facts + c.People + c.Projects + c.FollowUps + c.Captures + c.Journals
}

// WeekSentence is the recap row: "This week your second brain gained 4 facts,
// 2 people, 1 project and 5 captures." A week with nothing says so.
func WeekSentence(counts WeekCounts) string {
	var parts []string
	for _, part := range []struct {
		count     int
		one, many string
	}{
		{counts.Facts, "fact", "facts"},
		{counts.People, "person", "people"},
		{counts.Projects, "project", "projects"},
		{counts.FollowUps, "follow-up", "follow-ups"},
		{counts.Captures, "capture", "captures"},
		{counts.Journals, "journal entry", "journal entries"},
	} {
		switch {
		case part.count == 1:
			parts = append(parts, "1 "+part.one)
		case part.count > 1:
			parts = append(parts, fmt.Sprintf("%d %s", part.count, part.many))
		}
	}
	switch len(parts) {
	case 0:
		return "Your second brain had a quiet week: nothing new was added."
	case 1:
		return "This week your second brain gained " + parts[0] + "."
	default:
		return "This week your second brain gained " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + "."
	}
}
