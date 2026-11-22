package projectlibrary

import "testing"

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

func TestOwnerSectionPageOnlyOnExplicitQuery(t *testing.T) {
	r, scope, root := deepMusicRoot(t, "Albums/One/One.rpp", "Albums/Two/Two.rpp")
	walkScopes(t, r, scope, root, []string{"Albums"}, 3)
	query := Search{PageSize: 1}
	page, err := r.library.QueryFolderSections(scope, query)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].FolderSection == nil || page.NextCursor == "" {
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
	if err != nil || len(normal.Rows) != 1 || normal.Rows[0].FolderSection != nil {
		t.Fatal(normal, err)
	}
}
