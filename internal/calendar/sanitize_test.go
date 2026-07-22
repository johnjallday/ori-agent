package calendar

import (
	"strings"
	"testing"
)

func TestSanitizeEvent_ValidatesTimestamps(t *testing.T) {
	e := SanitizeEvent(Event{
		ID:        "evt-1",
		StartTime: "2026-07-20T10:00:00Z",
		EndTime:   "not-a-timestamp",
	})
	if e.StartTime != "2026-07-20T10:00:00Z" {
		t.Errorf("valid RFC3339 start time should survive, got %q", e.StartTime)
	}
	if e.EndTime != "" {
		t.Errorf("malformed end time must be dropped, got %q", e.EndTime)
	}
}

func TestSanitizeEvent_DropsUnsafeLinks(t *testing.T) {
	cases := []struct {
		name  string
		link  string
		valid bool
	}{
		{"https ok", "https://meet.example.com/abc", true},
		{"http ok", "http://meet.example.com/abc", true},
		{"javascript URI rejected", "javascript:alert(1)", false},
		{"data URI rejected", "data:text/html,<script>alert(1)</script>", false},
		{"file URI rejected", "file:///etc/passwd", false},
		{"custom scheme rejected", "vbscript:msgbox(1)", false},
		{"protocol-relative rejected (no explicit scheme)", "//evil.example.com/abc", false},
		{"bare path rejected", "/relative/path", false},
		{"empty stays empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := SanitizeEvent(Event{ConferenceLink: tc.link, SourceLink: tc.link})
			gotValid := e.ConferenceLink != ""
			if gotValid != tc.valid {
				t.Errorf("ConferenceLink %q: got valid=%v, want %v (result=%q)", tc.link, gotValid, tc.valid, e.ConferenceLink)
			}
			if (e.SourceLink != "") != tc.valid {
				t.Errorf("SourceLink %q: got valid=%v, want %v (result=%q)", tc.link, e.SourceLink != "", tc.valid, e.SourceLink)
			}
		})
	}
}

func TestSanitizeEvent_StripsControlCharactersAndTruncates(t *testing.T) {
	malicious := "Team Sync\x1b[31m\x00" + strings.Repeat("A", maxTitleLen+100)
	e := SanitizeEvent(Event{Title: malicious})
	if strings.ContainsAny(e.Title, "\x1b\x00") {
		t.Fatalf("control characters must be stripped, got %q", e.Title)
	}
	if len([]rune(e.Title)) > maxTitleLen {
		t.Fatalf("title must be truncated to %d runes, got %d", maxTitleLen, len([]rune(e.Title)))
	}
}

func TestSanitizeEvent_PreservesNewlinesAndTabsInDescription(t *testing.T) {
	e := SanitizeEvent(Event{Description: "Line one\nLine two\tindented"})
	if e.Description != "Line one\nLine two\tindented" {
		t.Errorf("newlines/tabs should be preserved, got %q", e.Description)
	}
}

func TestSanitizeEvent_DropsAttendeesWithNoDisplay(t *testing.T) {
	e := SanitizeEvent(Event{
		Attendees: []Attendee{
			{Email: "a@example.com", DisplayName: "Alice"},
			{Email: "", DisplayName: ""}, // must be dropped: no way to display
			{Email: "bob@example.com"},   // email alone is a usable display
			{DisplayName: "Anonymous"},   // name alone is a usable display
		},
	})
	if len(e.Attendees) != 3 {
		t.Fatalf("expected 3 attendees to survive (the blank one dropped), got %d: %+v", len(e.Attendees), e.Attendees)
	}
}

func TestSanitizeEvent_RejectsMalformedEmail(t *testing.T) {
	e := SanitizeEvent(Event{Attendees: []Attendee{
		{Email: "not-an-email", DisplayName: "Carol"},
	}})
	if len(e.Attendees) != 1 || e.Attendees[0].Email != "" {
		t.Fatalf("malformed email must be dropped, name-only display preserved: %+v", e.Attendees)
	}
}

func TestSanitizeCalendar(t *testing.T) {
	c := SanitizeCalendar(Calendar{ID: "cal-1", Name: "Work\x00Calendar"})
	if strings.Contains(c.Name, "\x00") {
		t.Fatalf("control character must be stripped: %q", c.Name)
	}
}

func TestSanitizeTimeSlot(t *testing.T) {
	ts := SanitizeTimeSlot(TimeSlot{StartTime: "2026-07-20T09:00:00Z", EndTime: "garbage"})
	if ts.StartTime == "" {
		t.Error("valid start time must survive")
	}
	if ts.EndTime != "" {
		t.Error("invalid end time must be dropped")
	}
}

func TestSanitizeAccount(t *testing.T) {
	a := SanitizeAccount(Account{ID: "acct-1", Email: "not-an-email"})
	if a.Email != "" {
		t.Errorf("malformed email must be dropped, got %q", a.Email)
	}
}
