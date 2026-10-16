package projectlibrary

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestCollectionCorrection_NewRevisionInvalidatesApprovalWithoutApplying(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	name := "Owner's client selection"
	excluded := page.Members[0].ID
	corrected, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, Operation: 0, Name: &name, ExcludeIDs: []string{excluded}})
	if err != nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	if corrected.Arrangement.Revision != 2 || corrected.CollectionCorrection == nil || !corrected.CollectionCorrection.Renamed || corrected.CollectionCorrection.Excluded != 1 || corrected.CollectionReceipt != nil || len(after.Collections) != 0 || !reflect.DeepEqual(before.Entries, after.Entries) {
		t.Fatal(corrected)
	}
	r := after.Arrangements[0].Record
	if r.Draft.Operations[0].Name != name || len(r.Draft.Operations[0].Members.IDs) != len(saved.Record.Draft.Operations[0].Members.IDs)-1 || r.Validate() != nil {
		t.Fatal(r)
	}
	if _, ok := r.ItemRevisions[excluded]; ok {
		t.Fatal("unused member authority retained")
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, "obsolete-approval", true); !errors.Is(err, ErrConflict) {
		t.Fatal("old approval applied", err)
	}
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, Name: &name}); err == nil {
		t.Fatal("replayed edit accepted")
	}
	fresh, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, after.Revision, 2)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, fresh.Token, "corrected-approval", true)
	if err != nil {
		t.Fatal(err)
	}
	collection := f.doc(t).Collections[0]
	if receipt.CollectionCorrection == nil || collection.Name != name || collection.Author != f.scope.OwnerUserID || len(collection.EntryIDs) != len(r.Draft.Operations[0].Members.IDs) {
		t.Fatal(collection)
	}
}

func TestCollectionCorrection_RejectsForeignDuplicateExpiredOrUnsupportedEditsAtomically(t *testing.T) {
	for _, mode := range []string{"foreign-owner", "foreign-member", "duplicate", "bad-name", "noop", "expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, _, saved, resolve := collectionReviewFixture(t)
			service := NewCollectionReviewService(f.library, resolve)
			page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
			if err != nil {
				t.Fatal(err)
			}
			name := "Corrected"
			owner := f.scope.OwnerUserID
			in := CollectionCorrectionRequest{ReviewToken: page.Token, Name: &name}
			switch mode {
			case "foreign-owner":
				owner = "foreign"
			case "foreign-member":
				in.ExcludeIDs = []string{"unknown"}
			case "duplicate":
				in.ExcludeIDs = []string{page.Members[0].ID, page.Members[0].ID}
			case "bad-name":
				name = "../escape"
			case "noop":
				name = saved.Record.Draft.Operations[0].Name
			case "expired":
				f.library.now = func() time.Time { return page.ExpiresAt }
			}
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before := f.doc(t)
			if _, err := service.Correct(ctx, owner, f.scope.HomeID, saved.Record.ID, in); err == nil {
				t.Fatal("invalid correction accepted")
			}
			if !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("failed correction changed canonical state")
			}
		})
	}
}

func TestCollectionCorrection_WriteFailureAndExplicitRetry(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	failed := &failActivationHomeWrite{Store: f.file, homeID: f.scope.HomeID, pending: true}
	service = NewCollectionReviewService(NewStore(failed).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true }), resolve)
	name := "Retry correction"
	in := CollectionCorrectionRequest{ReviewToken: page.Token, Name: &name}
	if history, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, in); err == nil || history != nil || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("failed save returned authority", err)
	}
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, in); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionCorrection_RacesWithApplyOnlyOneTransitionWins(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	go func() {
		<-start
		name := "Owner changed"
		_, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, Name: &name})
		results <- err
	}()
	go func() {
		<-start
		_, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, "racing-apply", true)
		results <- err
	}()
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatal("expected one winning transition", first, second)
	}
	doc := f.doc(t)
	if len(doc.Collections) > 0 && (doc.Collections[0].Name != saved.Record.Draft.Operations[0].Name || doc.Arrangements[0].CollectionCorrection != nil) {
		t.Fatal("old approval applied new intent")
	}
}

func TestCollectionCorrection_OverlappingIntentRetainsMemberPins(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	op := record.Draft.Operations[0]
	op.Name = "Other overlap"
	record.Draft.Operations = append(record.Draft.Operations, op)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	// Test manual correction without a model, while keeping actual Manager checks.
	resolve := ArrangementRuntimeResolver(func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, SkillDigest: record.SkillDigest, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, nil
	})
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	id := page.Members[0].ID
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, ExcludeIDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	actual := f.doc(t).Arrangements[0].Record
	if actual.ItemRevisions[id] == "" || actual.Validate() != nil || actual.Draft.Operations[1].Kind != workspacearrangement.CreateCollection {
		t.Fatal("overlap authority lost")
	}
}
