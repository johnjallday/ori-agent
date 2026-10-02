package projectlibrary

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// summaryDoc is a library of n songs under one root: song i was saved i days
// before now, and, when withFacts, has tempo 80+i%70, i%20+1 tracks and a
// length of 60+i*7 seconds.
func summaryDoc(n int, withFacts bool, now time.Time) Document {
	doc := Document{Roots: []Root{{ID: "root", Path: "/Users/me/Music/Songs", FileIdentity: "1:1", Revision: 1}}}
	for i := 0; i < n; i++ {
		saved := now.AddDate(0, 0, -i)
		observation := Observation{RootID: "root", RelativeFolder: fmt.Sprintf("Song %04d", i), FileIdentity: fmt.Sprintf("1:%d", i+2),
			Format: "reaper", Alternates: []string{"Song.rpp"}, Availability: "available", FileModifiedAt: saved}
		if withFacts {
			observation.Facts = &ObservedFacts{SongFacts: SongFacts{TempoBPM: float64(80 + i%70), TempoVaries: i%10 == 0,
				LengthSeconds: float64(60 + i*7%900), TrackCount: i%20 + 1}}
		}
		doc.Entries = append(doc.Entries, Entry{ID: fmt.Sprintf("e%05d", i), Revision: 1, Observations: []Observation{observation}})
	}
	return doc
}

func TestSummarizeCollection_SpreadsAndTheSixMostRecent(t *testing.T) {
	now := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	doc := summaryDoc(12, true, now)
	summary := summarizeCollection(doc, nil, true, now)
	if summary.Songs != 12 || summary.WithFacts != 12 || summary.TempoVaries != 2 ||
		summary.SavedLast7Days != 8 || summary.SavedLast30Days != 12 {
		t.Fatalf("counts: %+v", summary)
	}
	wantTempo := []SummaryBand{{"80–89 BPM", 10}, {"90–99 BPM", 2}}
	if !reflect.DeepEqual(summary.Tempo, wantTempo) {
		t.Fatalf("tempo bands = %+v", summary.Tempo)
	}
	if want := []SummaryBand{{"under 2:00", 9}, {"2:00–2:59", 3}}; !reflect.DeepEqual(summary.Length, want) {
		t.Fatalf("length bands = %+v, want %+v", summary.Length, want)
	}
	if want := []SummaryBand{{"1–4 tracks", 4}, {"5–8 tracks", 4}, {"9–16 tracks", 4}}; !reflect.DeepEqual(summary.Tracks, want) {
		t.Fatalf("track bands = %+v, want %+v", summary.Tracks, want)
	}
	if len(summary.Recent) != 6 || summary.Recent[0].Name != "Song 0000" || summary.Recent[0].SavedOn != "2026-10-02" ||
		summary.Recent[5].Name != "Song 0005" || summary.Recent[0].Facts == nil {
		t.Fatalf("recent = %+v", summary.Recent)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "/Users") || strings.Contains(string(encoded), "read_from") ||
		strings.Contains(string(encoded), "Song.rpp") {
		t.Fatalf("the summary carries a path or file: %s", encoded)
	}
	// Facts hidden (switch off): the same songs, no spreads.
	off := summarizeCollection(doc, nil, false, now)
	if off.Songs != 12 || off.WithFacts != 0 || off.Tempo != nil || off.Recent[0].Facts != nil {
		t.Fatalf("switched off: %+v", off)
	}
}

func TestSummarizeCollection_CountsOnlyUsableSources(t *testing.T) {
	now := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	doc := summaryDoc(4, true, now)
	revoked := now
	doc.Roots = append(doc.Roots, Root{ID: "gone", Path: "/Volumes/Old", FileIdentity: "9:9", Revision: 2, RevokedAt: &revoked},
		Root{ID: "paused", Path: "/Volumes/Paused", FileIdentity: "8:8", Revision: 1})
	doc.Entries[0].Observations[0].Availability = "unavailable"
	doc.Entries[1].Observations[0].RootID = "gone"
	doc.Entries[2].Observations[0].RootID = "paused"
	summary := summarizeCollection(doc, map[string]bool{"paused": true}, true, now)
	if summary.Songs != 1 || summary.WithFacts != 1 || len(summary.Recent) != 1 || summary.Recent[0].Name != "Song 0003" {
		t.Fatalf("only the usable song counts: %+v", summary)
	}
}

func TestSummarizeCollection_ALibraryWithoutFacts(t *testing.T) {
	now := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	summary := summarizeCollection(summaryDoc(9, false, now), nil, true, now)
	if summary.Songs != 9 || summary.WithFacts != 0 || summary.Tempo != nil || summary.Length != nil || summary.Tracks != nil ||
		len(summary.Recent) != 6 || summary.Recent[0].Facts != nil {
		t.Fatalf("no facts: %+v", summary)
	}
	empty := summarizeCollection(Document{}, nil, true, now)
	if empty.Songs != 0 || empty.Recent == nil || len(empty.Recent) != 0 {
		t.Fatalf("empty library: %+v", empty)
	}
}

func TestSummarizeCollection_SameShapeAtFiveThousandSongs(t *testing.T) {
	now := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	small, err := json.Marshal(summarizeCollection(summaryDoc(200, true, now), nil, true, now))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	large := summarizeCollection(summaryDoc(maxEntries, true, now), nil, true, now)
	elapsed := time.Since(started)
	encoded, err := json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	if large.Songs != maxEntries || len(large.Recent) != summaryRecentSongs || len(large.Tempo) > 10 ||
		len(large.Length) > len(summaryLengthBands) || len(large.Tracks) > len(summaryTrackBands) {
		t.Fatalf("5,000 songs: %+v", large)
	}
	if len(encoded) > 2*len(small) || len(encoded) > 4<<10 {
		t.Fatalf("the summary grew with the library: %d bytes at 200 songs, %d at 5,000", len(small), len(encoded))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("summarizing 5,000 songs took %v", elapsed)
	}
}
