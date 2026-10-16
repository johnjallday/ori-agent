package projectlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCollectionCatalog_BoundedExactReadOnlyPagingAndProviderLoss(t *testing.T) {
	f := newRunFixture(t)
	before := f.doc(t)
	_, _, err := f.library.mutate(f.scope, before.Revision, operation{key: "browse-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		doc.SchemaVersion = ArrangementSchemaVersion
		seed := doc.Entries[0]
		doc.Entries = nil
		ids := []string{}
		for i := 0; i < 1001; i++ {
			entry := seed
			entry.ID = fmt.Sprintf("entry-%04d", i)
			entry.Fields.DisplayName = "<img onerror=alert(1)>"
			doc.Entries = append(doc.Entries, entry)
			ids = append(ids, entry.ID)
		}
		for i := 0; i < 21; i++ {
			members := ids[:2]
			if i == 0 {
				members = ids
			}
			doc.Collections = append(doc.Collections, LogicalCollection{ID: fmt.Sprintf("collection-%02d", i), Name: "Client references", Revision: 1, EntryIDs: members, Author: f.scope.OwnerUserID, Origin: "manual", CreatedAt: f.library.now(), UpdatedAt: f.library.now()})
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before = f.doc(t)
	list, err := f.library.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil || len(list.Collections) != 20 || list.Total != 21 || list.NextOffset == nil || *list.NextOffset != 20 {
		t.Fatal(list, err)
	}
	last, err := f.library.CollectionCatalog(t.Context(), f.scope, list.ContextPin, 20)
	if err != nil || len(last.Collections) != 1 || last.NextOffset != nil {
		t.Fatal(last, err)
	}
	seen := map[string]bool{}
	for offset := 0; offset < 1001; offset += 50 {
		page, err := f.library.CollectionMembers(t.Context(), f.scope, "collection-00", list.ContextPin, offset)
		if err != nil || page.Total != 1001 || len(page.Members) > 50 {
			t.Fatal(page, err)
		}
		raw, _ := json.Marshal(page)
		if len(raw) > 64<<10 || strings.Contains(string(raw), before.Roots[0].Path) || strings.Contains(string(raw), "facts") || strings.Contains(string(raw), "file_identity") {
			t.Fatal("oversized or private projection")
		}
		for _, member := range page.Members {
			if seen[member.ID] {
				t.Fatal("duplicate", member.ID)
			}
			seen[member.ID] = true
		}
	}
	if len(seen) != 1001 || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("paging lost entries or changed canonical storage")
	}
	foreign := f.scope
	foreign.OwnerUserID = "foreign"
	if _, err := f.library.CollectionCatalog(t.Context(), foreign, "", 0); err == nil {
		t.Fatal("foreign owner read")
	}
	for _, offset := range []int{-1, 1, 1050} {
		if _, err := f.library.CollectionMembers(t.Context(), f.scope, "collection-00", list.ContextPin, offset); err == nil {
			t.Fatal("invalid offset", offset)
		}
	}
	if _, err := f.library.CollectionMembers(t.Context(), f.scope, "unknown", list.ContextPin, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := f.file.Update(f.scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.PluginAvailable = false
		state.ProjectLibraryInactiveRoots = []string{before.Roots[0].ID}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.library.CollectionMembers(t.Context(), f.scope, "collection-00", list.ContextPin, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("old pin accepted after root/provider change", err)
	}
	fresh, err := f.library.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil {
		t.Fatal("provider loss blocked owner metadata", err)
	}
	page, err := f.library.CollectionMembers(t.Context(), f.scope, "collection-00", fresh.ContextPin, 0)
	if err != nil || page.Members[0].Availability != "revoked_source" {
		t.Fatal(page, err)
	}
	if !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("browsing changed the library")
	}
}

func TestCollectionCatalog_RestartAndForgetCannotRestoreMembership(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	view, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, saved.Record.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, view.Token, "browse-commit", true); err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(f.file)
	list, err := restarted.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil || len(list.Collections) != 1 {
		t.Fatal(list, err)
	}
	id := list.Collections[0].ID
	page, err := restarted.CollectionMembers(t.Context(), f.scope, id, list.ContextPin, 0)
	if err != nil || len(page.Members) == 0 {
		t.Fatal(page, err)
	}
	// Forget requires sources to be revoked first; exercise that real review
	// boundary rather than weakening it to make a browsing fixture pass.
	for _, root := range f.doc(t).Roots {
		revoke, err := f.roots.ReviewRevoke(f.scope, root.ID, f.doc(t).Revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.roots.CommitRevoke(f.scope, root.ID, revoke.Token, "browse-revoke-"+root.ID); err != nil {
			t.Fatal(err)
		}
	}
	revoked, err := restarted.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil || revoked.Collections[0].Members != page.Total {
		t.Fatal("revoke lost membership", err)
	}
	review, err := f.library.ReviewForget(f.scope, page.Members[0].ID, f.doc(t).Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.library.CommitForget(f.scope, page.Members[0].ID, review.Token, "browse-forget"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CollectionMembers(t.Context(), f.scope, id, list.ContextPin, 0); !errors.Is(err, ErrConflict) {
		t.Fatal("old page survived Forget", err)
	}
	fresh, err := restarted.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil || fresh.Collections[0].ID != id || fresh.Collections[0].Members != page.Total-1 {
		t.Fatal(fresh, err)
	}
	remaining, err := restarted.CollectionMembers(t.Context(), f.scope, id, fresh.ContextPin, 0)
	if err != nil || remaining.Total != page.Total-1 {
		t.Fatal(remaining, err)
	}
	for _, member := range remaining.Members {
		if member.ID == page.Members[0].ID {
			t.Fatal("Forgotten member restored")
		}
	}
}

func TestCollectionCatalog_LegacyReadsNeverUpgrade(t *testing.T) {
	f := newRunFixture(t)
	before := f.doc(t)
	if before.SchemaVersion != SchemaVersion {
		t.Fatal("expected legacy fixture")
	}
	page, err := f.library.CollectionCatalog(t.Context(), f.scope, "", 0)
	if err != nil || page.Total != 0 || page.Collections == nil || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal(page, err)
	}
}
