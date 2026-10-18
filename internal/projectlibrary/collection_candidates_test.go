package projectlibrary

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestCollectionCandidates_LocalExclusionCanBeReaddedOnlyWithFreshReview(t *testing.T) {
	f, _, saved, resolve := collectionReviewFixture(t)
	service := NewCollectionReviewService(f.library, resolve)
	page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	original := slices.Clone(saved.Record.Draft.Operations[0].Members.IDs)
	slices.Sort(original)
	// Removing all membership also exercises adding to an empty, persisted pin map.
	if len(original) > 50 {
		t.Fatal("fixture too large")
	}
	if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, ExcludeIDs: original}); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := f.doc(t)
	candidates, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, fresh.Token, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidates.Total != len(original) || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("picker changed state or lost local exclusions", candidates)
	}
	for _, mode := range []string{"unknown", "duplicate", "foreign-owner", "too-many", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			in := CollectionCorrectionRequest{ReviewToken: fresh.Token, AddIDs: []string{original[0]}}
			owner := f.scope.OwnerUserID
			ctx := t.Context()
			switch mode {
			case "unknown":
				in.AddIDs = append(in.AddIDs, "foreign-entry")
			case "duplicate":
				in.AddIDs = append(in.AddIDs, original[0])
			case "foreign-owner":
				owner = "foreign"
			case "too-many":
				in.AddIDs = make([]string, 51)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := service.Correct(ctx, owner, f.scope.HomeID, saved.Record.ID, in); err == nil {
				t.Fatal("invalid addition accepted")
			}
			if !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("rejected addition changed state")
			}
		})
	}
	changed, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: fresh.Token, AddIDs: original})
	if err != nil {
		t.Fatal(err)
	}
	if changed.CollectionCorrection.Added != len(original) || changed.Arrangement.Revision != 3 || len(f.doc(t).Collections) != 0 {
		t.Fatal(changed)
	}
	record := f.doc(t).Arrangements[0].Record
	if record.Validate() != nil || !reflect.DeepEqual(record.ItemRevisions, saved.Record.ItemRevisions) || !slices.Equal(record.Draft.Operations[0].Members.IDs, original) || record.Draft.Operations[0].Name != saved.Record.Draft.Operations[0].Name {
		t.Fatalf("addition did not restore exact current pins/intent: validation=%v pins=%v want=%v ops=%+v want=%+v", record.Validate(), record.ItemRevisions, saved.Record.ItemRevisions, record.Draft.Operations, saved.Record.Draft.Operations)
	}
	if _, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, fresh.Token, 0, 0, ""); err == nil {
		t.Fatal("old picker still usable")
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, fresh.Token, "old-addition-review", true); err == nil {
		t.Fatal("old approval applied widened membership")
	}
	latest, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, latest.Token, "owner-added-members", true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.doc(t).Collections[0].EntryIDs, original) {
		t.Fatal("fresh explicit approval applied wrong members")
	}
}

func TestCollectionCandidates_CannotOverrideGlobalExclusionsOrUnresolvedChoices(t *testing.T) {
	for _, mode := range []string{"unchanged", "needs-review"} {
		t.Run(mode, func(t *testing.T) {
			f, authority, record := homeArrangementFixture(t)
			id := record.Draft.Operations[0].Members.IDs[0]
			record.Draft.Operations[0].Members.IDs = record.Draft.Operations[0].Members.IDs[1:]
			if mode == "unchanged" {
				record.Draft.Unchanged = workspacearrangement.Selection{IDs: []string{id}}
			} else {
				record.Draft.NeedsReview = []workspacearrangement.Recommendation{{Reason: "Owner must resolve this choice", Items: workspacearrangement.Selection{IDs: []string{id}}}}
				record.State = "needs_more_evidence"
			}
			saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
			if err != nil {
				t.Fatal(err)
			}
			resolve := func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
				return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
			}
			service := NewCollectionReviewService(f.library, resolve)
			page, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, f.doc(t).Revision, 1)
			if err != nil {
				t.Fatal(err)
			}
			before := f.doc(t)
			candidates, err := service.Candidates(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, page.Token, 0, 0, "")
			if err != nil || candidates.Total != 0 {
				t.Fatal("excluded entry offered", candidates, err)
			}
			if _, err := service.Correct(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, CollectionCorrectionRequest{ReviewToken: page.Token, AddIDs: []string{id}}); err == nil {
				t.Fatal("global decision overridden")
			}
			if !reflect.DeepEqual(before, f.doc(t)) {
				t.Fatal("blocked correction mutated state")
			}
		})
	}
}
