package agenthttp

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var snoozeWeekday = regexp.MustCompile(`^(?:next )?(sunday|monday|tuesday|wednesday|thursday|friday|saturday)(?:(?: at)? ([0-9]{1,2}):([0-9]{2}))?$`)

var (
	errSnoozeTime = errors.New("invalid snooze time")
	errSnoozeZone = errors.New("unavailable HQ timezone")
)

// resolveSnoozeTime resolves one calendar time once. Never infer a timezone
// from the process or the browser. A weekday is the next occurrence strictly
// after the current local day. An omitted time proposes 09:00 for review.
func resolveSnoozeTime(input, zone string, now time.Time) (time.Time, bool, error) {
	if zone == "" || zone == "Local" {
		return time.Time{}, false, errSnoozeZone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, false, errSnoozeZone
	}
	text := strings.TrimRight(strings.Join(strings.Fields(strings.ToLower(input)), " "), ".!")
	var wall time.Time
	var suggested bool
	if match := snoozeWeekday.FindStringSubmatch(text); match != nil {
		local := now.In(loc)
		day := local.Weekday()
		var offset int
		for offset = 1; offset <= 7; offset++ {
			if strings.EqualFold(time.Weekday((int(day)+offset)%7).String(), match[1]) {
				break
			}
		}
		hour, minute := 9, 0
		suggested = match[2] == ""
		if !suggested {
			hour, _ = strconv.Atoi(match[2])
			minute, _ = strconv.Atoi(match[3])
			if hour > 23 || minute > 59 {
				return time.Time{}, false, errSnoozeTime
			}
		}
		wall = time.Date(local.Year(), local.Month(), local.Day()+offset, hour, minute, 0, 0, time.UTC)
	} else {
		wall, err = time.Parse("2006-01-02 15:04", text)
		if err != nil {
			return time.Time{}, false, errSnoozeTime
		}
	}

	// A wall clock during a gap has no matching instant; a repeated one can
	// have two. Try the offsets on either side of this date rather than using
	// time.Date's unspecified choice on a transition.
	var instant time.Time
	for _, sample := range []time.Time{wall.Add(-24 * time.Hour), wall, wall.Add(24 * time.Hour)} {
		_, offset := sample.In(loc).Zone()
		candidate := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		if candidate.Format("2006-01-02 15:04:05") != wall.Format("2006-01-02 15:04:05") {
			continue
		}
		if !instant.IsZero() && !candidate.Equal(instant) {
			return time.Time{}, false, errSnoozeTime
		}
		instant = candidate
	}
	if instant.IsZero() || !instant.After(now) {
		return time.Time{}, false, errSnoozeTime
	}
	return instant, suggested, nil
}
