package folderagent

import (
	"context"
	"errors"
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

// FR49: songs a reviewed plugin owns but cannot read yet are never a silent
// blank. The offer's place says to update the plugin, and offers nothing to
// answer; with no installed plugin that could read them it says nothing at all.
func TestSongsOffer_AnInstalledPluginWithoutAReaderSaysToUpdateIt(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, update: "REAPER"}
	service := songsService(songs)
	offer := service.songsOffer(context.Background(), doneRecord())
	if offer == nil || offer.Update != "REAPER" || offer.Ask != "Update REAPER to read the 7 songs." || offer.Count != 7 {
		t.Fatalf("offer = %+v", offer)
	}
	if offer.Reads != "" {
		t.Fatalf("an update sentence also promises a reading: %q", offer.Reads)
	}
	// There is nothing to answer: Learn them and Not now are refused as a wrong
	// state, and nothing is granted or read.
	asked := &fakeSongs{state: workspace.SongDetailsNone, update: "REAPER"}
	h, id := songsHarness(t, asked)
	if view, err := h.svc.View(context.Background(), "u1", id); err != nil || view.SongsOffer == nil || view.SongsOffer.Update != "REAPER" {
		t.Fatalf("the card's offer = %+v %v", view.SongsOffer, err)
	}
	for _, learn := range []bool{true, false} {
		if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, learn); !errors.Is(err, ErrWrongState) {
			t.Fatalf("learn=%v while no installed plugin reads the songs: %v", learn, err)
		}
	}
	if grants, learns := asked.counts(); grants != 0 || learns != 0 {
		t.Fatalf("songs nothing reads were granted %d and read %d times", grants, learns)
	}
	// A Home that already consented to names is not told anything it must answer.
	named := &fakeSongs{state: workspace.SongDetailsOn, names: true, update: "REAPER"}
	if offer := songsService(named).songsOffer(context.Background(), doneRecord()); offer != nil {
		t.Fatalf("a Home that already reads names got an offer: %+v", offer)
	}
	// With no installed plugin that could read them, the offer's place stays empty.
	none := &fakeSongs{state: workspace.SongDetailsNone, noReader: true}
	if offer := songsService(none).songsOffer(context.Background(), doneRecord()); offer != nil {
		t.Fatalf("songs nothing can read got an offer: %+v", offer)
	}
	// And a Songs that cannot say which plugin reads reads nothing: the host has no
	// reader of its own.
	if offer := songsService(blindSongs{}).songsOffer(context.Background(), doneRecord()); offer != nil {
		t.Fatalf("a Songs that cannot say got an offer: %+v", offer)
	}
}

// blindSongs is a Songs that implements no part of SongsReaders.
type blindSongs struct{ *fakeSongs }

func (blindSongs) ReaderFor() {} // a different signature: not the interface

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
