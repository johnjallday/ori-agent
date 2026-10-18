package projectlibrary

import (
	"context"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestArrangementDecline_PreservesHistoryAndInvalidatesReviewWithoutModel(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	modelFree := NewCollectionReviewService(f.library, func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		t.Fatal("decline resolved a model")
		return ArrangementRuntime{}, ErrUnavailable
	})
	result, err := modelFree.Decline(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, before.Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	if result.Arrangement.State != "cancelled" || result.CollectionReceipt != nil || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Collections, after.Collections) || !reflect.DeepEqual(before.Queue, after.Queue) || !reflect.DeepEqual(before.Arrangements[0].Record.Draft, after.Arrangements[0].Record.Draft) {
		t.Fatal("decline lost intent or changed effects")
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "declined-approval", true); err == nil {
		t.Fatal("declined review applied")
	}
	if _, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, after.Revision, 1); err == nil {
		t.Fatal("declined proposal revived")
	}
	restarted := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	history, err := restarted.ReadArrangementHistory(t.Context(), f.scope, saved.Record.ID)
	if err != nil || history.Arrangement.State != "cancelled" {
		t.Fatal("decline lost on reopen", err)
	}
	if _, err := modelFree.Decline(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, after.Revision, 1); err == nil || !reflect.DeepEqual(after, f.doc(t)) {
		t.Fatal("decline retried or changed state", err)
	}
}

func TestArrangementDecline_RejectsForeignStaleAndAlreadyApplied(t *testing.T) {
	for _, mode := range []string{"foreign-owner", "catalog", "record", "cancelled-context", "applied"} {
		t.Run(mode, func(t *testing.T) {
			f, _, saved, resolve := collectionReviewFixture(t)
			service := NewCollectionReviewService(f.library, resolve)
			if mode == "applied" {
				review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "already-applied", true); err != nil {
					t.Fatal(err)
				}
			}
			before := f.doc(t)
			owner := f.scope.OwnerUserID
			catalog, revision := before.Revision, int64(1)
			ctx := t.Context()
			switch mode {
			case "foreign-owner":
				owner = "foreign"
			case "catalog":
				catalog--
			case "record":
				revision++
			case "cancelled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := service.Decline(ctx, owner, f.scope.HomeID, saved.Record.ID, catalog, revision); err == nil || !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("invalid decline altered state", err)
			}
		})
	}
}

func TestArrangementDecline_PreservesDiscussionAndFencesLateRevision(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	question := revisionCandidate(t, f, authority, saved, true)
	revised, err := f.library.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, 1, saved.Record.ID, question, "Which projects should remain catalog-only?")
	if err != nil {
		t.Fatal(err)
	}
	base := f.doc(t)
	late := revisionCandidate(t, f, authority, revised, false)
	service := NewCollectionReviewService(f.library, resolve)
	history, err := service.Decline(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, base.Revision, 2)
	if err != nil {
		t.Fatal(err)
	}
	if history.RevisionDiscussion == nil || !reflect.DeepEqual(history.RevisionDiscussion, revised.RevisionDiscussion) || history.Arrangement.State != "cancelled" {
		t.Fatal("decline erased the saved question")
	}
	declined := f.doc(t)
	if _, err := f.library.saveArrangementRevision(t.Context(), f.scope.OwnerUserID, authority, base.Revision, 2, saved.Record.ID, late, ""); err == nil || !reflect.DeepEqual(declined, f.doc(t)) {
		t.Fatal("late model response revived declined intent", err)
	}
}

func TestArrangementDecline_CannotInvalidateAnotherLiveReview(t *testing.T) {
	f, authority, saved, resolve := collectionReviewFixture(t)
	candidate := revisionCandidate(t, f, authority, saved, false)
	newer, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, candidate)
	if err != nil {
		t.Fatal(err)
	}
	service := NewCollectionReviewService(f.library, resolve)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, newer.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	if _, err := service.Decline(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, before.Revision, 1); err == nil || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("old proposal declined across another live review", err)
	}
	if _, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, newer.Record.ID, review.Token, 0, 0); err != nil {
		t.Fatal("unrelated review invalidated", err)
	}
}

func TestArrangementDecline_RaceWithApplyHasExactlyOneWinner(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	go func() {
		<-start
		_, err := service.Decline(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.CatalogRevision, 1)
		results <- err
	}()
	go func() {
		<-start
		_, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "racing-decline", true)
		results <- err
	}()
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatal("expected one transition", first, second)
	}
	doc := f.doc(t)
	saved = doc.Arrangements[0]
	if (saved.Record.State == "cancelled" && (len(doc.Collections) != 0 || saved.CollectionReceipt != nil)) || (saved.Record.State == "completed" && len(doc.Collections) != 1) {
		t.Fatal("decline/apply race mixed outcomes")
	}
}
