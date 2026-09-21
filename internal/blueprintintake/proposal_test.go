package blueprintintake

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func ticketRequirement() workspace.IntakeRequirement {
	return workspace.IntakeRequirement{Key: "materials", ProposalKinds: []string{"ticket"}}
}

func TestParseProposalOutputFiltersUnsupportedAndDisallowedItems(t *testing.T) {
	raw := `{"items":[
		{"kind":"ticket","key":"quiz-1","title":"Quiz 1","due_at":"2026-03-08","source":{"source_id":"source-1","quote":"Quiz 1 is due March 8"}},
		{"kind":"note","key":"outline","title":"Outline","source":{"source_id":"source-1","quote":"outline"}},
		{"kind":"ticket","key":"guess","title":"Guess","source":{"source_id":"source-1","quote":""}}
	]}`
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	items, notice, err := ParseProposalOutput(raw, ticketRequirement(), "source-1", location, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || notice.DroppedKind != 1 || notice.Unsupported != 1 {
		t.Fatalf("items/notice = %+v / %+v", items, notice)
	}
	item := items[0]
	if !item.NoTimeGiven || !item.PartlyRead || item.DueAt.Hour() != 0 || item.DueAt.Minute() != 1 {
		t.Fatalf("date flags/time = %+v", item)
	}
	if _, offset := item.DueAt.Zone(); offset != -5*60*60 {
		t.Fatalf("March 8 midnight should precede the DST transition, offset=%d", offset)
	}
}

func TestParseProposalOutputRejectsUnreadableOrUnboundedJSON(t *testing.T) {
	cases := []string{
		`not json`,
		`{"items":[],"commands":["create tickets"]}`,
		`{"items":[{"kind":"ticket","key":"x","title":"` + strings.Repeat("x", 241) + `","source":{"source_id":"s","quote":"q"}}]}`,
		`{"items":[{"kind":"ticket","key":"x","title":"X","due_at":"next friday","source":{"source_id":"s","quote":"q"}}]}`,
		`{"items":[{"kind":"ticket","key":"x","title":"X","recurrence":"weekly","source":{"source_id":"s","quote":"q"}}]}`,
	}
	for index, raw := range cases {
		if _, _, err := ParseProposalOutput(raw, ticketRequirement(), "s", time.UTC, false); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
	}
}

func TestParseProposalOutputValidatesMemoryNotesAndCalendarEvents(t *testing.T) {
	requirement := workspace.IntakeRequirement{Key: "materials", ProposalKinds: []string{"memory", "note", "calendar_event"}}
	raw := `{"items":[
		{"kind":"memory","key":"memory-good","text":"Office hours are Tuesdays","memory_type":"fact","source":{"source_id":"s","quote":"Office hours Tuesdays"}},
		{"kind":"memory","key":"memory-bad","text":"","source":{"source_id":"s","quote":"empty"}},
		{"kind":"note","key":"note","title":"Reading list","body":"Read chapters 1-3","source":{"source_id":"s","quote":"chapters 1-3"}},
		{"kind":"calendar_event","key":"event","title":"Seminar","start":"2026-04-03T10:00:00-04:00","end":"2026-04-03T11:00:00-04:00","location":"Room 2","source":{"source_id":"s","quote":"Seminar 10-11"}},
		{"kind":"calendar_event","key":"holiday","title":"Holiday","all_day":"2026-04-04","source":{"source_id":"s","quote":"Holiday April 4"}}
	]}`
	items, _, err := ParseProposalOutput(raw, requirement, "s", time.UTC, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 || items[0].Text != "Office hours are Tuesdays" || items[0].UnusableReason != "" {
		t.Fatalf("memory items = %+v", items)
	}
	if items[1].UnusableReason == "" || items[2].Body != "Read chapters 1-3" {
		t.Fatalf("memory/note validation = %+v", items)
	}
	if items[3].Start == nil || items[3].End == nil || items[3].Location != "Room 2" {
		t.Fatalf("calendar event = %+v", items[3])
	}
	if !items[4].AllDay || items[4].End.Sub(*items[4].Start) != 24*time.Hour {
		t.Fatalf("all-day event = %+v", items[4])
	}
}

func TestParseProposalOutputCapsItemsVisibly(t *testing.T) {
	var rows []string
	for index := 0; index < MaxProposalItems+5; index++ {
		rows = append(rows, fmt.Sprintf(`{"kind":"ticket","key":"item-%d","title":"Item","source":{"source_id":"s","quote":"q"}}`, index))
	}
	items, notice, err := ParseProposalOutput(`{"items":[`+strings.Join(rows, ",")+`]}`, ticketRequirement(), "s", time.UTC, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != MaxProposalItems || !notice.Truncated {
		t.Fatalf("items=%d notice=%+v", len(items), notice)
	}
}
