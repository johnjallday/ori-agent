package secondbrain

import "time"

// StaleLookAfter is how old the last look may get before Needs you says the
// notes deserve another look.
const StaleLookAfter = 7 * 24 * time.Hour

// NudgeReport says whether the notes deserve another look, and why.
type NudgeReport struct {
	// Stale says the last finished look is older than StaleLookAfter; Days is
	// its age in whole days.
	Stale bool
	Days  int
	// CameDue counts the last look's untouched items whose date has arrived
	// since that look: after the look's day, and on or before today.
	CameDue int
	// LookID and LookAt name the look the report is about.
	LookID string
	LookAt time.Time
}

// Any reports whether there is anything to nudge about.
func (n NudgeReport) Any() bool { return n.Stale || n.CameDue > 0 }

// Nudge decides the stale-notes nudge from the record alone: no folder is read
// and no look is started. Nothing connected, no finished look, or a "Not now"
// given after the last look all mean no nudge. Days are the local days of now's
// location, the same days the look's dates are written in.
func Nudge(record *Record, now time.Time) NudgeReport {
	source := record.Connected()
	if source == nil || source.LastLook == nil || source.LastLook.FinishedAt == nil {
		return NudgeReport{}
	}
	look := source.LastLook
	at := *look.FinishedAt
	if record.NudgeDismissedAt != nil && record.NudgeDismissedAt.After(at) {
		return NudgeReport{}
	}
	report := NudgeReport{LookID: look.ID, LookAt: at}
	if age := now.Sub(at); age > StaleLookAfter {
		report.Stale = true
		report.Days = int(age / (24 * time.Hour))
	}
	location := now.Location()
	lookDay := at.In(location).Format(time.DateOnly)
	today := now.Format(time.DateOnly)
	for _, item := range look.Items {
		// Done or dismissed items, and items already follow-ups, have been
		// answered; follow-ups keep their own due dates.
		if item.Due == "" || item.ActedAs != "" || item.Seeded {
			continue
		}
		if item.Due > lookDay && item.Due <= today {
			report.CameDue++
		}
	}
	return report
}
