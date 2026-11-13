package foldermap

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// readsReaper stands in for an installed app plugin whose reader covers the one
// format of the demo layout's songs.
func readsReaper(format string) bool { return format == "reaper" }

func TestReadability_DemoFolderSplitsReadableFromNot(t *testing.T) {
	_, facts := walk(t, demoLayout(t))
	got := facts.Readability(readsReaper)
	if got.Shape != folderdigest.ShapeAudio {
		t.Fatalf("shape = %q, want audio", got.Shape)
	}
	if got.Readable != 7 {
		t.Fatalf("readable = %d, want 7 (facts %+v)", got.Readable, facts.Shapes)
	}
	want := []FormatCount{{Format: "logic", Name: "Logic Pro", Count: 1}}
	if !reflect.DeepEqual(got.Unreadable, want) {
		t.Fatalf("unreadable = %+v, want %+v", got.Unreadable, want)
	}
	if got.UnreadableTotal() != 1 {
		t.Fatalf("unreadable total = %d, want 1", got.UnreadableTotal())
	}
}

func TestReadability_NothingWithoutAPortfolio(t *testing.T) {
	// Four songs are a group, not a Home: no Home would hold them, so there is
	// no library to read into and nothing to offer.
	_, facts := walk(t, build(t, "a/Song.rpp", "b/Song.rpp", "c/Song.rpp", "d/Song.rpp"))
	if facts.Portfolio != nil {
		t.Fatalf("fixture drifted: portfolio = %+v", facts.Portfolio)
	}
	if got := facts.Readability(readsReaper); got.Readable != 0 || len(got.Unreadable) != 0 || got.Shape != "" {
		t.Fatalf("readability without a portfolio = %+v", got)
	}
	if got := (Facts{}).Readability(readsReaper); got.Readable != 0 || got.UnreadableTotal() != 0 {
		t.Fatalf("zero facts = %+v", got)
	}
}

func TestReadability_TheHostReadsNothingItself(t *testing.T) {
	// Ori has no reader of its own: with no installed plugin's answer, nothing
	// counts as readable, whatever the format.
	_, facts := walk(t, demoLayout(t))
	got := facts.Readability(nil)
	if got.Readable != 0 || got.UnreadableTotal() != 8 {
		t.Fatalf("readability with no readers = %+v, want 0 readable and all 8 unreadable", got)
	}
	if shape := facts.PortfolioShape(); shape != folderdigest.ShapeAudio {
		t.Fatalf("portfolio shape = %q, want audio", shape)
	}
	if shape := (Facts{}).PortfolioShape(); shape != "" {
		t.Fatalf("a map with no portfolio has shape %q", shape)
	}
}

func TestReadability_OnlyThePortfolioShapeCounts(t *testing.T) {
	facts := Facts{
		Portfolio: &Portfolio{Shape: folderdigest.ShapeAudio, Count: 6},
		Members: []Project{
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Marker: "*.rpp"},
			{Shape: folderdigest.ShapeAudio, Format: "ableton", Marker: "*.als"},
			{Shape: folderdigest.ShapeAudio, Marker: "mystery"},
			{Shape: folderdigest.ShapeCode, Marker: "go.mod"},
		},
		Shapes: []ShapeCount{
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 4},
			{Shape: folderdigest.ShapeCode, Count: 9},
			{Shape: folderdigest.ShapeAudio, Format: "ableton", Count: 2},
			{Shape: folderdigest.ShapeAudio, Count: 1},
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 0},
		},
	}
	got := facts.Readability(readsReaper)
	if got.Readable != 4 {
		t.Fatalf("readable = %d, want 4 (other shapes and empty rows do not count)", got.Readable)
	}
	// Largest-first order is the map's; a format the table does not name has an
	// empty Name rather than a guessed one.
	want := []FormatCount{
		{Format: "ableton", Name: "Ableton Live", Count: 2},
		{Format: "", Name: "", Count: 1},
	}
	if !reflect.DeepEqual(got.Unreadable, want) {
		t.Fatalf("unreadable = %+v, want %+v", got.Unreadable, want)
	}
	if got.UnreadableTotal() != 3 {
		t.Fatalf("unreadable total = %d, want 3", got.UnreadableTotal())
	}
}

func TestReadability_TheRootProjectNamesItsFormat(t *testing.T) {
	root := Project{Shape: folderdigest.ShapeAudio, Format: "logic", Marker: "*.logicx"}
	facts := Facts{
		Portfolio:   &Portfolio{Shape: folderdigest.ShapeAudio, Count: 1},
		RootProject: &root,
		Shapes:      []ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 1}},
	}
	got := facts.Readability(readsReaper)
	want := []FormatCount{{Format: "logic", Name: "Logic Pro", Count: 1}}
	if got.Readable != 0 || !reflect.DeepEqual(got.Unreadable, want) {
		t.Fatalf("readability = %+v, want only %+v unreadable", got, want)
	}
}
