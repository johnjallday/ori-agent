package projectlibrary

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestObservedSectionUsesOneActiveSourceAndNeverMergesRoots(t *testing.T) {
	roots := map[string]Root{"one": {ID: "one", Path: "/private/music"}, "two": {ID: "two", Path: "/else/music"}}
	o := Observation{RootID: "one", RelativeFolder: "2025/Artist/Disc 1/Song", Availability: "ambiguous"}
	entry := Entry{Observations: []Observation{o}}
	a := observedSection(entry, roots, nil, "")
	if a == nil || a.RootLabel != "music" || a.Label != "2025" || a.Breadcrumb != "Artist / Disc 1" {
		t.Fatal(a)
	}
	o.RootID = "two"
	entry.Observations = append(entry.Observations, o)
	if observedSection(entry, roots, nil, "") != nil {
		t.Fatal("overlap grouped")
	}
	b := observedSection(entry, roots, nil, "two")
	if b == nil || b.ID == a.ID {
		t.Fatal("same labels merged", b)
	}
	if observedSection(entry, roots, map[string]bool{"one": true}, "one") != nil || observedSection(Entry{}, roots, nil, "") != nil {
		t.Fatal("inactive or link-only entry grouped")
	}
}

func TestFolderSectionCoveragePagesAndRejectsRevisionChange(t *testing.T) {
	files := []string{"Albums/Song/session.rpp", "A-side/Song/session.rpp"}
	for i := 0; i < 27; i++ {
		files = append(files, fmt.Sprintf("Albums/P%02d/session.rpp", i))
	}
	r, scope, root := deepMusicRoot(t, files...)
	walkScopes(t, r, scope, root, []string{"Albums", "A-side"}, 3)
	query := Search{PageSize: 25}
	page, err := r.library.QueryFolderSections(scope, query)
	if err != nil || page.Total != 29 || len(page.Rows) != 25 || page.NextCursor == "" {
		t.Fatalf("first page: total=%d rows=%d cursor=%t err=%v", page.Total, len(page.Rows), page.NextCursor != "", err)
	}
	query.Cursor = page.NextCursor
	next, err := r.library.QueryFolderSections(scope, query)
	if err != nil || len(next.Rows) != 4 || next.NextCursor != "" {
		t.Fatalf("second page: %+v %v", next, err)
	}
	songs := map[string]string{}
	for _, row := range append(page.Rows, next.Rows...) {
		if row.Name == "Song" {
			songs[row.ID] = row.FolderSection.ID
		}
	}
	if len(songs) != 2 {
		t.Fatal("same-name entries merged", songs)
	}
	scan, err := r.ReviewScan(scope, root.ID, page.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.CommitScan(context.Background(), scope, root.ID, scan.Token, "next-root-scan"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.library.QueryFolderSections(scope, query); !errors.Is(err, ErrConflict) {
		t.Fatalf("obsolete page accepted: %v", err)
	}
}

func TestOwnerSectionPageOnlyOnExplicitQuery(t *testing.T) {
	r, scope, root := deepMusicRoot(t, "Albums/One/One.rpp", "Albums/Two/Two.rpp")
	walkScopes(t, r, scope, root, []string{"Albums"}, 3)
	query := Search{PageSize: 1}
	page, err := r.library.QueryFolderSections(scope, query)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].FolderSection == nil || page.Rows[0].FolderLabel != page.Rows[0].FolderSection.RootLabel+" / Albums / One" || page.NextCursor == "" {
		t.Fatalf("page: %+v %v", page, err)
	}
	query.Cursor = page.NextCursor
	next, err := r.library.QueryFolderSections(scope, query)
	if err != nil || len(next.Rows) != 1 || next.Rows[0].ID == page.Rows[0].ID || next.Rows[0].FolderSection.ID != page.Rows[0].FolderSection.ID {
		t.Fatalf("next: %+v %v", next, err)
	}
	if _, err = r.library.Query(scope, query); err == nil {
		t.Fatal("owner cursor accepted for another projection")
	}
	query.Cursor = ""
	normal, err := r.library.Query(scope, query)
	if err != nil || len(normal.Rows) != 1 || normal.Rows[0].FolderSection != nil || normal.Rows[0].FolderLabel != "" {
		t.Fatal(normal, err)
	}
}
