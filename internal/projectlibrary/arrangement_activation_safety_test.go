package projectlibrary

import (
	"context"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementActivation_UnsupportedPreparationFailsWhole(t *testing.T) {
	for _, mode := range []string{"active-queue", "linked-entry", "two-selections", "empty-selection"} {
		t.Run(mode, func(t *testing.T) {
			doc, id, at := collectionCompileFixture(t)
			record := &doc.Arrangements[0].Record
			selection := workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: []string{"single"}}}
			record.Draft.Operations = append(record.Draft.Operations, selection)
			switch mode {
			case "active-queue":
				queue, err := activationQueueForSelection(doc, []string{"single"}, "existing-queue", at)
				if err != nil {
					t.Fatal(err)
				}
				doc.Queue = &queue
			case "linked-entry":
				for i := range doc.Entries {
					if doc.Entries[i].ID == "single" {
						doc.Entries[i].Link = &ExactLink{WorkspaceID: "existing", LinkID: "link", Revision: 1}
					}
				}
			case "two-selections":
				selection.Members.IDs = []string{"alternates"}
				record.Draft.Operations = append(record.Draft.Operations, selection)
			case "empty-selection":
				record.Draft.Operations[1].Members.IDs = nil
			}
			changes, err := compileCollectionChanges(doc, id, at)
			if err == nil || changes.Queue != nil || len(changes.Collections) != 0 || len(changes.Effects) != 0 {
				t.Fatal("unsupported mixed intent partly compiled", changes, err)
			}
		})
	}
}

func TestArrangementActivation_AtomicFailureAndReplayNeverRestoreDiscardedQueue(t *testing.T) {
	f, authority, record := homeArrangementFixture(t)
	record.Draft.Operations = append(record.Draft.Operations, workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: []string{"single"}}})
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
	failed := &failActivationHomeWrite{Store: f.file, homeID: f.scope.HomeID, pending: true}
	failing := NewCollectionReviewService(NewStore(failed).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true }), resolve)
	if _, err := failing.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "queue-atomic", true); err == nil || !reflect.DeepEqual(before, f.doc(t)) {
		t.Fatal("failed metadata write partially saved collections or queue", err)
	}
	receipt, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "queue-atomic", true)
	if err != nil {
		t.Fatal(err)
	}
	queue := f.doc(t).Queue
	if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "other-key", true); err == nil {
		t.Fatal("consumed approval reused")
	}
	if _, err := f.library.DiscardActivationQueue(f.scope, queue.ID, "discard-selection", queue.Revision); err != nil {
		t.Fatal(err)
	}
	discarded := f.doc(t)
	again, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "queue-atomic", true)
	if err != nil || !reflect.DeepEqual(receipt.CollectionReceipt, again.CollectionReceipt) || !reflect.DeepEqual(discarded, f.doc(t)) {
		t.Fatal("receipt replay resurrected discarded queue", err)
	}
}
