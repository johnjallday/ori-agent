package projectlibrary

import "testing"

// A digest is a scan snapshot; the summary presents it against the links made
// since, so "to set up" counts what is left (map-library-setup FR-18).
func TestDigestWithLinks_ProjectsSetUpSinceTheScanAreNoLongerActivatable(t *testing.T) {
	digest := LibraryDigest{ScanID: "scan-1", RootID: "root-1", Projects: 6, New: 6,
		Activatable: 5, NeedsFileChoice: 1, UnsupportedFormat: 1}
	observed := func(scanID string) []Observation {
		return []Observation{{RootID: "root-1", ScanID: scanID, Availability: "available"}}
	}
	doc := Document{Entries: []Entry{
		{ID: "a", Observations: observed("scan-1"), Link: &ExactLink{WorkspaceID: "ws-a"}},
		{ID: "b", Observations: observed("scan-1"), Link: &ExactLink{WorkspaceID: "ws-b"}},
		{ID: "c", Observations: observed("scan-1")},
		// Linked, but observed by an older scan: not part of this digest.
		{ID: "old", Observations: observed("scan-0"), Link: &ExactLink{WorkspaceID: "ws-old"}},
		// Linked under another root: not this digest's either.
		{ID: "other", Observations: []Observation{{RootID: "root-2", ScanID: "scan-1"}}, Link: &ExactLink{WorkspaceID: "ws-x"}},
	}}
	got := digestWithLinks(doc, digest)
	if got.Connected != 2 || got.Activatable != 3 || got.NeedsFileChoice != 1 || got.Projects != 6 ||
		got.UnsupportedFormat != 1 || got.New != 6 {
		t.Fatalf("presented digest: %+v", got)
	}
	if digest.Activatable != 5 || digest.Connected != 0 {
		t.Fatalf("the stored digest was changed: %+v", digest)
	}
	// Everything set up: nothing left, and the file-choice count cannot exceed it.
	all := Document{Entries: []Entry{
		{ID: "a", Observations: observed("scan-1"), Link: &ExactLink{WorkspaceID: "ws-a"}},
		{ID: "b", Observations: observed("scan-1"), Link: &ExactLink{WorkspaceID: "ws-b"}},
	}}
	if got := digestWithLinks(all, LibraryDigest{ScanID: "scan-1", RootID: "root-1", Projects: 2,
		Activatable: 2, NeedsFileChoice: 1}); got.Activatable != 0 || got.NeedsFileChoice != 0 || got.Connected != 2 {
		t.Fatalf("all linked: %+v", got)
	}
	// Links the scan already counted leave the digest alone.
	counted := LibraryDigest{ScanID: "scan-1", RootID: "root-1", Projects: 2, Connected: 2}
	if got := digestWithLinks(all, counted); got != counted {
		t.Fatalf("already counted: %+v", got)
	}
}
