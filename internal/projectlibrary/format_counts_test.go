package projectlibrary

import (
	"reflect"
	"testing"
)

func TestDocumentFormatCountsCountsEachEntryOncePerFormat(t *testing.T) {
	doc := Document{Entries: []Entry{
		{ID: "a", Observations: []Observation{{Format: "reaper"}, {Format: "reaper"}}},
		{ID: "b", Observations: []Observation{{Format: "reaper"}, {Format: "logic"}}},
		{ID: "c", Observations: []Observation{{Format: "logic"}}},
		{ID: "d", Observations: []Observation{{Format: ""}}},
		{ID: "e"},
	}}
	if got := doc.FormatCounts(); !reflect.DeepEqual(got, map[string]int{"reaper": 2, "logic": 2}) {
		t.Fatalf("counts = %v", got)
	}
	if got := (Document{}).FormatCounts(); len(got) != 0 {
		t.Fatalf("an empty library counted %v", got)
	}
}
