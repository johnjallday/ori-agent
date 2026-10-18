package projectlibrary

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func correctionService(t *testing.T, f *runFixture, record workspacearrangement.Record) *CollectionReviewService {
	t.Helper()
	return NewCollectionReviewService(f.library, func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, SkillDigest: record.SkillDigest, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
	})
}

func freshCorrectionReview(t *testing.T, f *runFixture, service *CollectionReviewService, id string, revision int64) string {
	t.Helper()
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, f.doc(t).Revision, revision)
	if err != nil {
		t.Fatal(err)
	}
	return page.Token
}

func TestCollectionCorrection_ConvertsBetweenCatalogOnlyAndActivationReview(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	service := correctionService(t, f, record)
	id := saved.Record.ID
	token := freshCorrectionReview(t, f, service, id, 1)
	before := f.doc(t)
	// A structural edit is never combined with a member edit, and a conversion
	// must fit the intention it names.
	name := "Renamed"
	question := 0
	for label, in := range map[string]CollectionCorrectionRequest{
		"conversion with a rename":        {ReviewToken: token, Operation: 0, Convert: CorrectionSelectActivation, Name: &name},
		"catalog-only for a collection":   {ReviewToken: token, Operation: 0, Convert: CorrectionCatalogOnly},
		"an unknown conversion":           {ReviewToken: token, Operation: 0, Convert: "activate_now"},
		"two structural edits":            {ReviewToken: token, Operation: 0, Convert: CorrectionSelectActivation, ReleaseUnchanged: true},
		"a resolution without a question": {ReviewToken: token, Operation: 0, Resolution: ResolutionDismiss},
		"a question that does not exist":  {ReviewToken: token, Operation: 0, ResolveQuestion: &question, Resolution: ResolutionDismiss},
		"nothing explicit to release":     {ReviewToken: token, Operation: 0, ReleaseUnchanged: true},
	} {
		if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, in); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: %v", label, err)
		}
	}
	if !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("a refused correction wrote")
	}
	converted, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, Convert: CorrectionSelectActivation})
	if err != nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	r := after.Arrangements[0].Record
	collectionMembers := slices.Sorted(slices.Values(r.Draft.Operations[0].Members.IDs))
	if converted.Arrangement.Revision != 2 || converted.CollectionCorrection == nil || converted.CollectionCorrection.Converted != CorrectionSelectActivation ||
		len(r.Draft.Operations) != 2 || r.Draft.Operations[1].Kind != workspacearrangement.SelectActivation ||
		!reflect.DeepEqual(r.Draft.Operations[1].Members.IDs, collectionMembers) || len(after.Collections) != 0 || after.Queue != nil {
		t.Fatalf("conversion: %+v / %+v", converted.CollectionCorrection, r.Draft.Operations)
	}
	// The old approval cannot apply the converted intent.
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, token, "old-approval", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("old approval applied a conversion: %v", err)
	}
	token = freshCorrectionReview(t, f, service, id, 2)
	page, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, token, 1, 0)
	if err != nil || page.Effects[1].Kind != "select_activation" || page.Effects[1].AfterMembers != len(collectionMembers) {
		t.Fatalf("fresh review of the conversion: %+v %v", page, err)
	}
	// A second activation selection is refused; converting it back leaves
	// every project catalog-only and the collection untouched.
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, Convert: CorrectionSelectActivation}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second activation selection: %v", err)
	}
	back, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 1, Convert: CorrectionCatalogOnly})
	if err != nil {
		t.Fatal(err)
	}
	r = f.doc(t).Arrangements[0].Record
	if back.Arrangement.Revision != 3 || back.CollectionCorrection.Converted != CorrectionCatalogOnly || len(r.Draft.Operations) != 1 ||
		!reflect.DeepEqual(slices.Sorted(slices.Values(r.Draft.Operations[0].Members.IDs)), collectionMembers) || f.doc(t).Queue != nil || r.Validate() != nil {
		t.Fatalf("catalog-only conversion: %+v", r.Draft.Operations)
	}
	// A proposal whose only intention is its activation selection keeps it: an
	// empty proposal would review and apply as a completed nothing.
	only := r
	only.Draft.Operations = []workspacearrangement.Operation{{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: collectionMembers[:1]}}}
	if _, _, _, err := correctStructure(&only, CollectionCorrectionRequest{Operation: 0, Convert: CorrectionCatalogOnly}, nil); !errors.Is(err, ErrConflict) || len(only.Draft.Operations) != 1 {
		t.Fatalf("removing the only intention: %v, %+v", err, only.Draft.Operations)
	}
}

func TestCollectionCorrection_ResolvesQuestionsAndReleasesExplicitUnchanged(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	members := record.Draft.Operations[0].Members.IDs
	if len(members) < 3 {
		t.Fatalf("fixture needs at least three entries, has %d", len(members))
	}
	asked, kept := members[len(members)-1], members[len(members)-2]
	record.Draft.Operations[0].Members.IDs = slices.Clone(members[:len(members)-2])
	record.Draft.NeedsReview = []workspacearrangement.Recommendation{{Items: workspacearrangement.Selection{IDs: []string{asked}}, Reason: "Which take is authoritative?"}}
	record.Draft.Unchanged = workspacearrangement.Selection{IDs: []string{kept}}
	record.State = "needs_more_evidence"
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	service := correctionService(t, f, record)
	id := saved.Record.ID
	token := freshCorrectionReview(t, f, service, id, 1)
	// A protected item cannot be added locally while it is an open question.
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, AddIDs: []string{asked}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("added an unresolved item: %v", err)
	}
	question := 0
	resolved, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, ResolveQuestion: &question, Resolution: ResolutionUnchanged})
	if err != nil {
		t.Fatal(err)
	}
	r := f.doc(t).Arrangements[0].Record
	if !resolved.CollectionCorrection.Resolved || len(r.Draft.NeedsReview) != 0 || !slices.Contains(r.Draft.Unchanged.IDs, asked) || !slices.Contains(r.Draft.Unchanged.IDs, kept) ||
		r.State != "draft" || r.Revision != 2 || len(f.doc(t).Collections) != 0 {
		t.Fatalf("resolution: %+v state=%s", r.Draft, r.State)
	}
	// Releasing explicit unchanged items makes them addable by a later edit.
	token = freshCorrectionReview(t, f, service, id, 2)
	released, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, ReleaseUnchanged: true})
	if err != nil {
		t.Fatal(err)
	}
	r = f.doc(t).Arrangements[0].Record
	if released.CollectionCorrection.Released != 2 || len(r.Draft.Unchanged.IDs) != 0 || r.Revision != 3 || r.Validate() != nil {
		t.Fatalf("release: %+v", released.CollectionCorrection)
	}
	if _, ok := r.ItemRevisions[asked]; ok {
		t.Fatal("a released, unused item kept its authority pin")
	}
	token = freshCorrectionReview(t, f, service, id, 3)
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, id, CollectionCorrectionRequest{ReviewToken: token, Operation: 0, AddIDs: []string{asked, kept}}); err != nil {
		t.Fatalf("released items could not be added: %v", err)
	}
	// Dismissing a question removes it without adding its items anywhere.
	g, gAuthority, gRecord := homeArrangementFixture(t)
	gMembers := gRecord.Draft.Operations[0].Members.IDs
	gAsked := gMembers[len(gMembers)-1]
	gRecord.Draft.Operations[0].Members.IDs = slices.Clone(gMembers[:len(gMembers)-1])
	gRecord.Draft.NeedsReview = []workspacearrangement.Recommendation{{Items: workspacearrangement.Selection{IDs: []string{gAsked}}, Reason: "Is this a sketch?"}}
	gRecord.State = "needs_more_evidence"
	gSaved, _, err := g.library.SaveArrangement(t.Context(), g.scope.OwnerUserID, gAuthority, g.doc(t).Revision, true, gRecord)
	if err != nil {
		t.Fatal(err)
	}
	gService := correctionService(t, g, gRecord)
	gToken := freshCorrectionReview(t, g, gService, gSaved.Record.ID, 1)
	if _, err := gService.Correct(t.Context(), g.scope.OwnerUserID, g.scope.HomeID, gSaved.Record.ID, CollectionCorrectionRequest{ReviewToken: gToken, Operation: 0, ResolveQuestion: &question, Resolution: ResolutionDismiss}); err != nil {
		t.Fatal(err)
	}
	gr := g.doc(t).Arrangements[0].Record
	if len(gr.Draft.NeedsReview) != 0 || len(gr.Draft.Unchanged.IDs) != 0 || gr.ItemRevisions[gAsked] != "" {
		t.Fatalf("dismissal: %+v", gr.Draft)
	}
}
