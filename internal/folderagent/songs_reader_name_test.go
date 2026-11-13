package folderagent

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects 6.4 (FR44, FR45): the offer to learn the songs names
// the app plugin that reads the project files and says Ori keeps only the listed
// facts; with no plugin to name, the original promise stands.

// namedSongs is a Songs that also names the plugin that reads.
type namedSongs struct {
	*fakeSongs
	name string
}

func (n namedSongs) ReaderName() string { return n.name }

func TestSongsReadsBy(t *testing.T) {
	if got := songsReadsBy(""); got != songsReads {
		t.Fatalf("no plugin: %q", got)
	}
	if got := songsReadsBy("   "); got != songsReads {
		t.Fatalf("a blank name: %q", got)
	}
	got := songsReadsBy(" REAPER ")
	want := "REAPER reads tempo, length, track count, track names, markers and regions from each project file, and Ori keeps only those. Nothing is changed, and audio is not opened."
	if got != want {
		t.Fatalf("named:\n got %q\nwant %q", got, want)
	}
	// Both limits of the original promise are kept, and the reader is not Ori.
	for _, kept := range []string{"tempo, length, track count, track names, markers and regions", "Nothing is changed", "audio is not opened"} {
		if !strings.Contains(got, kept) || !strings.Contains(songsReads, kept) {
			t.Fatalf("%q is missing from one of the two wordings", kept)
		}
	}
	if strings.HasPrefix(got, "I'll read") {
		t.Fatalf("the named wording still says Ori reads: %q", got)
	}
}

func TestSongsOffer_NamesThePluginThatReads(t *testing.T) {
	base := &fakeSongs{state: workspace.SongDetailsNone}
	offer := songsService(namedSongs{fakeSongs: base, name: "REAPER"}).songsOffer(context.Background(), doneRecord())
	if offer == nil {
		t.Fatal("no offer")
	}
	if !strings.HasPrefix(offer.Reads, "REAPER reads ") || !strings.Contains(offer.Reads, "Ori keeps only those") {
		t.Fatalf("Reads = %q", offer.Reads)
	}
	// The counts and the question do not change with who reads.
	if offer.Count != 7 || offer.Ask != "Want me to learn the 7 songs?" {
		t.Fatalf("offer = %+v", offer)
	}
	// A Songs that names no plugin keeps the original words; so does one that
	// cannot say.
	for name, songs := range map[string]Songs{
		"names none":         namedSongs{fakeSongs: &fakeSongs{state: workspace.SongDetailsNone}},
		"cannot name anyone": &fakeSongs{state: workspace.SongDetailsNone},
	} {
		offer := songsService(songs).songsOffer(context.Background(), doneRecord())
		if offer == nil || offer.Reads != songsReads {
			t.Fatalf("%s: %+v", name, offer)
		}
	}
}
