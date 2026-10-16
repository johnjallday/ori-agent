package projectlibrary

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementActivationCorrection_ChangesSelectionNotCollectionsOrProjectApproval(t *testing.T) {
	f, authority, original := homeArrangementFixture(t)
	_, _, err := f.library.mutate(f.scope, f.doc(t).Revision, operation{key: "linked-fixture", action: "fixture", digest: "fixture"}, func(doc *Document) (string, error) {
		for i := range doc.Entries {
			if doc.Entries[i].ID == "unsupported" {
				doc.Entries[i].Link = &ExactLink{WorkspaceID: "existing-project", LinkID: "existing-link", Revision: 1}
				doc.Entries[i].Revision++
			}
		}
		return "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workspacearrangement.NewEvidence(t.Context(), func(ctx context.Context) (workspacearrangement.Snapshot, error) {
		return f.library.ArrangementEvidence(ctx, f.scope.OwnerUserID, authority)
	})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := evidence.Overview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	draft := original.Draft
	draft.SourceRevision = overview.Revision
	draft.Operations = append(draft.Operations, workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: []string{"single"}}})
	record, err := evidence.NewRecord(t.Context(), draft, workspacearrangement.Provenance{Origin: original.Origin, SourceID: f.scope.HomeID, Provider: original.Provider, Model: original.Model, ProviderFingerprint: original.ProviderFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
	}
	service := NewCollectionReviewService(f.library, resolve)
	review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	candidates, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, 1, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range candidates.Members {
		if item.ID == "unsupported" || item.ID == "single" {
			t.Fatal("linked/already-selected project offered", item)
		}
		found = found || item.ID == "alternates"
	}
	if !found {
		t.Fatal("catalog-only candidate missing")
	}
	for _, in := range []CollectionCorrectionRequest{
		{ReviewToken: review.Token, Operation: 1, AddIDs: []string{"unsupported"}},
		{ReviewToken: review.Token, Operation: 1, ExcludeIDs: []string{"single"}},
		{ReviewToken: review.Token, Operation: 1, AddIDs: []string{"single"}},
	} {
		if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, in); err == nil {
			t.Fatal("linked/empty/duplicate selection correction accepted")
		}
		if !reflect.DeepEqual(before, f.doc(t)) {
			t.Fatal("failed correction changed canonical state")
		}
	}
	changed, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: review.Token, Operation: 1, ExcludeIDs: []string{"single"}, AddIDs: []string{"alternates"}})
	if err != nil {
		t.Fatal(err)
	}
	after := f.doc(t)
	revised := after.Arrangements[0].Record
	if changed.CollectionCorrection.Added != 1 || changed.CollectionCorrection.Excluded != 1 || revised.Revision != 2 || !slices.Equal(revised.Draft.Operations[1].Members.IDs, []string{"alternates"}) || !reflect.DeepEqual(revised.Draft.Operations[0], record.Draft.Operations[0]) || revised.ItemRevisions["single"] == "" || revised.Model != record.Model || revised.ProviderFingerprint != record.ProviderFingerprint {
		t.Fatal("correction lost original collection intent, shared pins or provenance")
	}
	if after.Queue != nil || len(after.Collections) != 0 || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Sessions, after.Sessions) {
		t.Fatal("correction applied effects")
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "obsolete-selection", true); err == nil {
		t.Fatal("old approval accepted revised selection")
	}
	fresh, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, after.Revision, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, fresh.Token, "corrected-selection", true); err != nil {
		t.Fatal(err)
	}
	applied := f.doc(t)
	if applied.Queue == nil || !slices.Equal(applied.Queue.IDs, []string{"alternates"}) || len(applied.Queue.Connected) != 0 || len(applied.Collections) != 1 || !reflect.DeepEqual(applied.Entries, before.Entries) {
		t.Fatal("fresh confirmation applied wrong effects")
	}
}
