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
	}
	for index, raw := range cases {
		if _, _, err := ParseProposalOutput(raw, ticketRequirement(), "s", time.UTC, false); err == nil {
			t.Fatalf("case %d was accepted", index)
		}
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
