package calendar

import (
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Field length bounds applied to untrusted connector strings before they ever
// reach the browser (FR41). These are generous enough for legitimate calendar
// data and exist only to stop a misbehaving or malicious connector from
// flooding the UI/response payload.
const (
	maxTitleLen    = 500
	maxTextLen     = 5000 // description
	maxLocationLen = 500
	maxNameLen     = 200
	maxEmailLen    = 320 // RFC 5321 mailbox length limit
	maxIDLen       = 512
)

// SanitizeEvent returns a copy of e with every connector-provided string
// bounded/stripped of control characters, timestamps validated as RFC3339 (an
// unparseable timestamp is dropped rather than passed through), and links
// validated as http(s) URLs (anything else, including a javascript: URI, is
// dropped). Called by the gateway on every value it applies from a connector
// result, before the value is ever returned to the browser (FR41, task 4.3).
func SanitizeEvent(e Event) Event {
	return Event{
		ID:             sanitizeID(e.ID),
		CalendarID:     sanitizeID(e.CalendarID),
		Title:          sanitizeText(e.Title, maxTitleLen),
		Description:    sanitizeText(e.Description, maxTextLen),
		Location:       sanitizeText(e.Location, maxLocationLen),
		StartTime:      sanitizeTimestamp(e.StartTime),
		EndTime:        sanitizeTimestamp(e.EndTime),
		TimeZone:       sanitizeText(e.TimeZone, maxNameLen),
		AllDay:         e.AllDay,
		Private:        e.Private,
		Canceled:       e.Canceled,
		ResponseStatus: sanitizeText(e.ResponseStatus, maxNameLen),
		Attendees:      sanitizeAttendees(e.Attendees),
		ConferenceLink: sanitizeURL(e.ConferenceLink),
		SourceLink:     sanitizeURL(e.SourceLink),
		Recurring:      e.Recurring,
	}
}

// SanitizeCalendar returns a copy of c with connector-provided strings bounded.
func SanitizeCalendar(c Calendar) Calendar {
	return Calendar{
		ID:       sanitizeID(c.ID),
		Name:     sanitizeText(c.Name, maxNameLen),
		Primary:  c.Primary,
		TimeZone: sanitizeText(c.TimeZone, maxNameLen),
		Color:    sanitizeText(c.Color, maxNameLen),
	}
}

// SanitizeAccount returns a copy of a with connector-provided strings bounded.
func SanitizeAccount(a Account) Account {
	return Account{
		ID:    sanitizeID(a.ID),
		Label: sanitizeText(a.Label, maxNameLen),
		Email: sanitizeEmail(a.Email),
	}
}

// SanitizeTimeSlot returns a copy of t with timestamps validated as RFC3339.
func SanitizeTimeSlot(t TimeSlot) TimeSlot {
	return TimeSlot{
		StartTime: sanitizeTimestamp(t.StartTime),
		EndTime:   sanitizeTimestamp(t.EndTime),
	}
}

func sanitizeAttendees(in []Attendee) []Attendee {
	if len(in) == 0 {
		return nil
	}
	out := make([]Attendee, 0, len(in))
	for _, a := range in {
		email := sanitizeEmail(a.Email)
		name := sanitizeText(a.DisplayName, maxNameLen)
		if email == "" && name == "" {
			continue
		}
		out = append(out, Attendee{
			Email:          email,
			DisplayName:    name,
			ResponseStatus: sanitizeText(a.ResponseStatus, maxNameLen),
			Organizer:      a.Organizer,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeText strips ASCII/Unicode control characters (which have no
// business inside calendar text and can be used to smuggle terminal escapes
// or confuse downstream rendering), trims surrounding whitespace, and
// truncates to maxLen runes.
func sanitizeText(s string, maxLen int) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > maxLen {
		cleaned = strings.TrimSpace(string(runes[:maxLen]))
	}
	return cleaned
}

// sanitizeID trims/bounds an opaque connector id. Ids are not otherwise
// format-validated -- connectors mint them in whatever shape they choose --
// but an empty result after trimming means "no usable id."
func sanitizeID(s string) string {
	return sanitizeText(s, maxIDLen)
}

// sanitizeTimestamp requires s to parse as RFC3339 (the calendar contract's
// only accepted wire format for times, see Event's doc comment); anything
// else -- garbage, a bare date, an out-of-range offset -- is dropped rather
// than passed through, since a downstream date parser choking on a malformed
// value is worse than a missing one.
func sanitizeTimestamp(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// sanitizeURL requires s to be an absolute http(s) URL; anything else
// (javascript:, data:, a bare path, malformed input) is dropped so it can
// never reach an <a href> unescaped.
func sanitizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.String()
	default:
		return ""
	}
}

func sanitizeEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "@") {
		return ""
	}
	return sanitizeText(s, maxEmailLen)
}
