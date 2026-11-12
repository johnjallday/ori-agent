package projectlibrary

import (
	"reflect"
	"strings"
	"testing"
)

// realSavedProject holds the marker and track lines of a project REAPER 7.78 saved
// (the reaper-project-tidy spike's messy-project fixture), unchanged: a name with
// no space is written bare, one with a space or no characters is quoted, a region
// is two MARKER lines of the same index whose second is the short five-field form,
// and tracks are blocks named by a NAME line after the block's GUID.
const realSavedProject = `<REAPER_PROJECT 0.1 "7.78/macOS-arm64" 1787925124 0
  TEMPO 120 4 4 0
  RULERLANE 1 4 "" 0 -1 0
  MARKER 1 10 chorus 0 17572408 1 B {F2F136B2-B71D-FB41-B256-EAFC29EA256A} 0 2
  MARKER 3 10 "" 0 0 1 B {159DE412-0C08-AF40-96BD-0C6BD183F755} 0 2
  MARKER 101 20 Verse 0 17572408 1 B {A705B1A5-F5D7-2445-81C1-E010C5885D2B} 0 2
  MARKER 202 30 "chorus 2" 1 17572408 1 B {B037B8EB-B429-2648-93B8-B34844C83888} 0 1
  MARKER 202 40 "" 1
  <PROJBAY
  >
  <TRACK {DB7F23BB-38B6-3844-8A87-22FD017EE05B}
    NAME "Vox 2"
    PEAKCOL 20455948
    TRACKID {DB7F23BB-38B6-3844-8A87-22FD017EE05B}
  >
  <TRACK {771C2680-B9FA-2B48-AD94-ADFFA0A2A90C}
    NAME Kick
    PEAKCOL 16576
  >
>
`

// The reader was written against the grammar REAPER's file format is documented to
// have; this holds it to a file REAPER itself wrote. Bare and quoted names both
// come out as the person typed them, a marker with no name keeps its place, a
// region's end comes from the short second line, and a bare track name is read
// like a quoted one.
func TestReadSongDetails_ReadsTheShapesARealSavedProjectUses(t *testing.T) {
	facts, names, err := ReadSongDetails(strings.NewReader(realSavedProject), factsGrammar("reaper"), DefaultSongFactsLimits)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if facts.TempoBPM != 120 || facts.TrackCount != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	if names == nil || !reflect.DeepEqual(names.Tracks, []string{"Vox 2", "Kick"}) {
		t.Fatalf("tracks = %+v", names)
	}
	want := []SongMarker{
		{Name: "chorus", At: 10},
		{Name: "", At: 10},
		{Name: "Verse", At: 20},
		{Name: "chorus 2", At: 30, End: 40},
	}
	if !reflect.DeepEqual(names.Markers, want) {
		t.Fatalf("markers = %+v, want %+v", names.Markers, want)
	}
	if names.TracksLeftOut != 0 || names.MarkersLeftOut != 0 {
		t.Fatalf("left out = %d tracks, %d markers", names.TracksLeftOut, names.MarkersLeftOut)
	}
}
