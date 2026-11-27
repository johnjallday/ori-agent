package projectlibrary

import (
	"github.com/johnjallday/ori-agent/internal/directoryid"
	"testing"
)

func TestFilesMarksRequireSavedFolderAndRootIdentity(t *testing.T) {
	root := Root{ID: "fed", FileIdentity: "root"}
	obs := Observation{RootID: "fed", RelativeFolder: "songs/A", Availability: "available", FileIdentity: "A"}
	entries := []Entry{{ID: "a", Observations: []Observation{obs}}}
	marks := map[string]string{}
	addFilesProjects(marks, "station", "ref", root, entries)
	stamp := directoryid.ListingStamp("station", "ref", "songs/A", "root", "A")
	if len(marks) != 1 || marks[stamp] != "a" {
		t.Fatal("expected the saved project")
	}
	for _, other := range []string{directoryid.ListingStamp("station", "ref", "songs/A", "different root", "A"), directoryid.ListingStamp("station", "ref", "songs/A", "root", "replacement"), directoryid.ListingStamp("station", "other ref", "songs/A", "root", "A"), directoryid.ListingStamp("station", "ref", "moved/A", "root", "A")} {
		if marks[other] != "" {
			t.Fatal("marked a different listing")
		}
	}
	entries[0].ID = "b"
	addFilesProjects(marks, "station", "ref", root, entries)
	if marks[stamp] != "" {
		t.Fatal("ambiguous entry was offered")
	}
	obs.Availability = "unavailable"
	marks = map[string]string{}
	addFilesProjects(marks, "station", "ref", root, []Entry{{ID: "offline", Observations: []Observation{obs}}})
	if len(marks) != 0 {
		t.Fatal("unavailable observation marked")
	}
}
