package projectlibrary

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestQueryBrowser_CollectionsFilterCanonicalMembershipBeforePaging(t *testing.T) {
	library, scope, _ := createBrowserFixture(t, t.TempDir())
	query := Search{CollectionID: "collection-wide"}
	seen := map[string]bool{}
	var first SearchPage
	for {
		page, err := library.Query(scope, query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 53 || page.Collection == nil || page.Collection.ID != query.CollectionID || page.Collection.Revision != 1 || page.Collection.Members != 53 {
			t.Fatalf("untruthful collection: %+v", page)
		}
		if len(page.Rows) > 25 {
			t.Fatal("page bound lost")
		}
		if query.Cursor == "" {
			first = page
		}
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatal("duplicate row")
			}
			seen[row.ID] = true
		}
		query.Cursor = page.NextCursor
		if query.Cursor == "" {
			break
		}
	}
	if len(seen) != 53 || !seen["browser-104"] {
		t.Fatalf("first-page membership filter: %v", seen)
	}
	page, err := library.Query(scope, Search{CollectionID: "collection-wide", Text: "Zebra"})
	if err != nil || page.Total != 1 || page.Rows[0].ID != "browser-104" {
		t.Fatalf("late member search: %+v %v", page, err)
	}
	page, err = library.Query(scope, Search{CollectionID: "collection-empty"})
	if err != nil || page.Total != 0 || len(page.Rows) != 0 || page.Collection == nil {
		t.Fatalf("empty collection broadened: %+v %v", page, err)
	}
	for _, invalid := range []Search{{CollectionID: "foreign"}, {CollectionID: "collection-wide", CollectionRevision: 2}, {CollectionRevision: 1}, {View: "guessed_progress"}, {CollectionID: "collection-wide", PageSize: 51}, {CollectionID: "collection-empty", Cursor: first.NextCursor}, {CollectionID: "collection-wide", View: "needs_attention", Cursor: first.NextCursor}} {
		if _, err := library.Query(scope, invalid); err == nil {
			t.Fatalf("accepted invalid scope: %+v", invalid)
		}
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	doc.Revision++
	doc.Collections[0].Revision++
	doc.Collections[0].EntryIDs = doc.Collections[0].EntryIDs[1:]
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := library.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Query(scope, Search{CollectionID: "collection-wide", Cursor: first.NextCursor}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale membership cursor: %v", err)
	}
	if _, err := library.Query(scope, Search{CollectionID: "collection-wide", CollectionRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale collection: %v", err)
	}
	foreign := scope
	foreign.OwnerUserID = "other"
	if _, err := library.Query(foreign, Search{CollectionID: "collection-wide"}); err == nil {
		t.Fatal("cross-owner collection read")
	}
}

func TestQueryBrowser_QuickViewsAreSavedMetadataNotInferredActivity(t *testing.T) {
	library, scope, _ := createBrowserFixture(t, t.TempDir())
	all, err := library.Query(scope, Search{View: "all"})
	if err != nil || all.Total != 105 {
		t.Fatalf("All excludes records: %+v %v", all, err)
	}
	attention, err := library.Query(scope, Search{View: "needs_attention"})
	if err != nil || attention.Total != 5 {
		t.Fatalf("attention union: %+v %v", attention, err)
	}
	for _, row := range attention.Rows {
		if row.Connection != "needs_review" && row.Availability != "unavailable" && row.Availability != "ambiguous" && row.Availability != "revoked_source" {
			t.Fatalf("inferred problem: %+v", row)
		}
	}
	// Two exceptions on the same entry still produce one canonical result.
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	doc.Entries[3].Observations[0].Availability = "unavailable"
	doc.Revision++
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := library.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	overlap, err := library.Query(scope, Search{View: "needs_attention"})
	if err != nil || overlap.Total != 5 {
		t.Fatalf("double-counted overlapping exceptions: %+v %v", overlap, err)
	}
	combined, err := library.Query(scope, Search{CollectionID: "collection-wide", View: "needs_attention"})
	if err != nil || combined.Total != 2 {
		t.Fatalf("intersection: %+v %v", combined, err)
	}
	archived, err := library.Query(scope, Search{View: "archived"})
	if err != nil || archived.Total != 10 {
		t.Fatalf("archived: %+v %v", archived, err)
	}
	for _, row := range archived.Rows {
		if row.Status != "archived" {
			t.Fatal("non-archived row")
		}
	}
	query := Search{View: "recently_saved", Sort: "name", Direction: "asc", PageSize: 50}
	var previous *SearchRow
	unknown := false
	count := 0
	for {
		page, err := library.Query(scope, query)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Rows {
			if row.LastSavedAt == nil {
				unknown = true
			} else {
				if unknown || (previous != nil && previous.LastSavedAt.Before(*row.LastSavedAt)) {
					t.Fatal("recent save order or unknowns-last broken")
				}
				copy := row
				previous = &copy
			}
			count++
		}
		query.Cursor = page.NextCursor
		if query.Cursor == "" {
			break
		}
	}
	if count != 105 || !unknown {
		t.Fatalf("Recently saved hid unknown records: %d", count)
	}
}
