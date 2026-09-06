package samplelibrary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectionsAnnotationsAndSearchAreBoundedHostOwnedState(t *testing.T) {
	ctx := context.Background()
	service, store, _, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	source := filepath.Join(rootPath, "Loop.wav")
	if err := os.WriteFile(source, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := service.Index(ctx, homeID, root.ID, "index", state.CatalogRevision, root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%#v %v", entries, err)
	}
	collectionReview, err := service.ReviewCollection(ctx, homeID, "Favorites", "User curated", indexed.State.CatalogRevision)
	if err != nil {
		t.Fatal(err)
	}
	state, collection, err := service.CommitCollection(ctx, homeID, collectionReview.Token, "Favorites", "User curated", "collection", indexed.State.CatalogRevision)
	if err != nil {
		t.Fatal(err)
	}
	memberReview, err := service.ReviewCollectionMember(ctx, homeID, collection.ID, entries[0].ID, collection.Revision)
	if err != nil {
		t.Fatal(err)
	}
	collection, err = service.CommitCollectionMember(ctx, homeID, memberReview.Token, collection.ID, entries[0].ID, "member", collection.Revision)
	if err != nil || collection.Revision != 2 {
		t.Fatalf("member=%+v %v", collection, err)
	}
	annotationExpected := state.CatalogRevision
	annotationReview, err := service.ReviewAnnotation(ctx, homeID, entries[0].ID, []string{"Drums", "drums"}, "Pack A", "User", "Licensed by user", annotationExpected, 0)
	if err != nil {
		t.Fatal(err)
	}
	state, annotation, err := service.CommitAnnotation(ctx, homeID, annotationReview.Token, entries[0].ID, "annotation", []string{"Drums", "drums"}, "Pack A", "User", "Licensed by user", annotationExpected, 0)
	if err != nil || annotation.Revision != 1 || len(annotation.UserTags) != 1 {
		t.Fatalf("annotation=%+v %v", annotation, err)
	}
	replayState, replay, err := service.CommitAnnotation(ctx, homeID, annotationReview.Token, entries[0].ID, "annotation", []string{"Drums", "drums"}, "Pack A", "User", "Licensed by user", annotationExpected, 0)
	if err != nil || replay.Revision != 1 || replayState.CatalogRevision != state.CatalogRevision {
		t.Fatalf("annotation replay=%+v %+v %v", replayState, replay, err)
	}
	search, err := service.Search(ctx, homeID, "licensed", 200)
	if err != nil || len(search.Entries) != 1 {
		t.Fatalf("annotation search=%+v %v", search, err)
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	root, err = store.Root(ctx, homeID, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.Index(ctx, homeID, root.ID, "refresh", state.CatalogRevision, root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var members int
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM sample_library_collection_member WHERE collection_id=?`, collection.ID).Scan(&members); err != nil || members != 1 {
		t.Fatalf("unavailable member lost count=%d err=%v", members, err)
	}
	if len(refreshed.Issues) > MaxIssueExamples {
		t.Fatalf("issues unbounded: %d", len(refreshed.Issues))
	}
}
