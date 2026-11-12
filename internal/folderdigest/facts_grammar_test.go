package folderdigest

import (
	"math/bits"
	"strings"
	"testing"
)

// factsRows is every marker row that declares a reading grammar.
func factsRows(t *testing.T) []Marker {
	t.Helper()
	var rows []Marker
	for _, marker := range Markers {
		if marker.Facts != nil {
			rows = append(rows, marker)
		}
	}
	if len(rows) == 0 {
		t.Fatal("no marker row declares a grammar")
	}
	return rows
}

// A grammar is data a generic reader interprets: it must never be half
// defined, or the reader would take the wrong value off a line.
func TestProjectFacts_EveryRowIsWholeOrAbsent(t *testing.T) {
	for _, row := range factsRows(t) {
		t.Run(row.ProjectFormat, func(t *testing.T) {
			g := row.Facts
			for name, key := range map[string]string{
				"Root": g.Root, "Tempo": g.Tempo, "TempoChanges": g.TempoChanges, "TempoPoint": g.TempoPoint,
				"Track": g.Track, "Item": g.Item, "ItemStart": g.ItemStart, "ItemLength": g.ItemLength,
			} {
				if key == "" || strings.ContainsAny(key, " \t") {
					t.Errorf("%s = %q: a key is one word", name, key)
				}
			}
			if !strings.HasPrefix(g.Root, "<") {
				t.Errorf("Root = %q: a block opens with <NAME", g.Root)
			}

			// Names: either the format gives them, with every position that picks
			// a value off a marker line, or it gives none of the marker fields.
			if g.TrackName != "" && (strings.ContainsAny(g.TrackName, " \t") || g.TrackName == g.Track) {
				t.Errorf("TrackName = %q: one word, and not the track block's own name", g.TrackName)
			}
			if g.Marker == "" {
				if g.MarkerIndex|g.MarkerPosition|g.MarkerName|g.MarkerFlags != 0 || g.MarkerRegionBit != 0 {
					t.Errorf("no Marker key but marker positions are set: %+v", g)
				}
				return
			}
			if strings.ContainsAny(g.Marker, " \t") {
				t.Errorf("Marker = %q: one word", g.Marker)
			}
			seen := map[int]string{}
			for name, position := range map[string]int{
				"MarkerIndex": g.MarkerIndex, "MarkerPosition": g.MarkerPosition,
				"MarkerName": g.MarkerName, "MarkerFlags": g.MarkerFlags,
			} {
				if position < 1 || position > 8 {
					t.Errorf("%s = %d: a 1-based position between 1 and 8", name, position)
				}
				if other, dup := seen[position]; dup {
					t.Errorf("%s and %s both read value %d", name, other, position)
				}
				seen[position] = name
			}
			if g.MarkerRegionBit == 0 || bits.OnesCount(g.MarkerRegionBit) != 1 {
				t.Errorf("MarkerRegionBit = %d: one flag bit", g.MarkerRegionBit)
			}
		})
	}
}

// The one row that declares a grammar today, pinned so that changing where
// names are read from is a deliberate edit and not a drift.
func TestProjectFacts_TheDeclaredRowNamesTracksAndMarkers(t *testing.T) {
	marker, ok := FactsMarker("reaper")
	if !ok {
		t.Fatal("the format that declares a grammar is no longer found")
	}
	g := marker.Facts
	if !g.HasNames() {
		t.Fatal("the declared grammar gives no names")
	}
	want := ProjectFacts{
		Root: "<REAPER_PROJECT", Tempo: "TEMPO", TempoChanges: "TEMPOENVEX", TempoPoint: "PT",
		Track: "TRACK", Item: "ITEM", ItemStart: "POSITION", ItemLength: "LENGTH",
		TrackName: "NAME", Marker: "MARKER", MarkerIndex: 1, MarkerPosition: 2, MarkerName: 3, MarkerFlags: 4,
		MarkerRegionBit: 1,
	}
	if *g != want {
		t.Fatalf("grammar = %+v\nwant      %+v", *g, want)
	}
	// Formats with no grammar give nothing, so nothing reads them.
	for _, format := range []string{"logic", "ableton", "", "nothing"} {
		if m, ok := FactsMarker(format); ok {
			t.Errorf("FactsMarker(%q) = %+v, want none", format, m)
		}
	}
}

func TestProjectFacts_WithoutNamesLeavesTheNumbersAndTheOriginalAlone(t *testing.T) {
	marker, _ := FactsMarker("reaper")
	original := marker.Facts
	before := *original

	numbers := original.WithoutNames()
	if numbers == original {
		t.Fatal("WithoutNames returned the shared grammar, not a copy")
	}
	if numbers.HasNames() || numbers.TrackName != "" || numbers.Marker != "" ||
		numbers.MarkerIndex|numbers.MarkerPosition|numbers.MarkerName|numbers.MarkerFlags != 0 || numbers.MarkerRegionBit != 0 {
		t.Fatalf("the numbers-only grammar still names something: %+v", *numbers)
	}
	// Everything a numbers-only reading needs is kept.
	if numbers.Root != before.Root || numbers.Tempo != before.Tempo || numbers.TempoChanges != before.TempoChanges ||
		numbers.TempoPoint != before.TempoPoint || numbers.Track != before.Track || numbers.Item != before.Item ||
		numbers.ItemStart != before.ItemStart || numbers.ItemLength != before.ItemLength {
		t.Fatalf("WithoutNames dropped a number's key: %+v", *numbers)
	}
	// The table itself is untouched, so the next read under a names consent works.
	if *original != before || !original.HasNames() {
		t.Fatalf("WithoutNames changed the shared grammar: %+v", *original)
	}
}

func TestProjectFacts_NilGrammarGivesNoNames(t *testing.T) {
	var g *ProjectFacts
	if g.HasNames() {
		t.Fatal("a nil grammar has names")
	}
	if g.WithoutNames() != nil {
		t.Fatal("a nil grammar became non-nil")
	}
	// Either half alone counts as naming something.
	if !(&ProjectFacts{TrackName: "NAME"}).HasNames() || !(&ProjectFacts{Marker: "MARKER"}).HasNames() {
		t.Fatal("a grammar with only track names or only markers reports no names")
	}
	if (&ProjectFacts{Root: "<X"}).HasNames() {
		t.Fatal("a grammar with no name keys reports names")
	}
}
