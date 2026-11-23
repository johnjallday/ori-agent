package sessionhttp

import (
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"testing"
)

func TestListedUnderCountsShelfInventoryNotNewlyReviewedProjects(t *testing.T) {
	doc := projectlibrary.Document{Entries: []projectlibrary.Entry{
		{Observations: []projectlibrary.Observation{{RootID: "fed", ScanID: "old", Availability: "unavailable"}}},
		{Observations: []projectlibrary.Observation{{RootID: "other"}, {RootID: "fed", ScanID: "now", Availability: "available"}}},
		{Observations: []projectlibrary.Observation{{RootID: "other"}}},
	}}
	if n := listedUnder(doc, "fed"); n != 2 {
		t.Fatalf("inventory = %d; want both the historical entry and today's observation", n)
	}
}
