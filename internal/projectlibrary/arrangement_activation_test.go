package projectlibrary

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementActivation_QueuePreparationIsNotProjectApproval(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "selection-only"
		if mixed {
			name = "collections-and-selection"
		}
		t.Run(name, func(t *testing.T) {
			f, authority, record := homeArrangementFixture(t)
			selectOp := workspacearrangement.Operation{Kind: workspacearrangement.SelectActivation, Members: workspacearrangement.Selection{IDs: []string{"single"}}}
			if mixed {
				record.Draft.Operations = append(record.Draft.Operations, selectOp)
			} else {
				record.Draft.Operations = []workspacearrangement.Operation{selectOp}
				record.ItemRevisions = map[string]string{"single": record.ItemRevisions["single"]}
			}
			saved, _, err := f.library.SaveArrangement(t.Context(), f.scope.OwnerUserID, authority, f.doc(t).Revision, true, record)
			if err != nil {
				t.Fatal(err)
			}
			resolve := func(ctx context.Context, _ ManagerAuthority) (ArrangementRuntime, error) {
				return ArrangementRuntime{Provider: homePlanningProvider{}, Model: record.Model, ProviderFingerprint: record.ProviderFingerprint, Revalidate: func(ctx context.Context) error { return ctx.Err() }}, ctx.Err()
			}
			service := NewCollectionReviewService(f.library, resolve)
			before := f.doc(t)
			hash := fileDigest(t, filepath.Join(f.tree.single, "Song.rpp"))
			review, err := service.Review(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, before.Revision, 1)
			if err != nil {
				t.Fatal(err)
			}
			operation := 0
			if mixed {
				operation = 1
			}
			page, err := service.Page(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, operation, 0)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || page.Members[0].ID != "single" || !page.Members[0].After || page.Effects[operation].QueueID == "" || f.doc(t).Queue != nil {
				t.Fatal("review prepared effects", page)
			}
			if _, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "selection-apply", false); err == nil {
				t.Fatal("implicit approval accepted")
			}
			receipt, err := service.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "selection-apply", true)
			if err != nil {
				t.Fatal(err)
			}
			doc := f.doc(t)
			if doc.Queue == nil || doc.Queue.Index != 0 || len(doc.Queue.IDs) != 1 || len(doc.Queue.Connected) != 0 || !reflect.DeepEqual(before.Entries, doc.Entries) || !reflect.DeepEqual(before.Roots, doc.Roots) || !reflect.DeepEqual(before.Sessions, doc.Sessions) || fileDigest(t, filepath.Join(f.tree.single, "Song.rpp")) != hash {
				t.Fatal("queue preparation performed project effects")
			}
			if (mixed && len(doc.Collections) != 1) || (!mixed && len(doc.Collections) != 0) {
				t.Fatal("wrong metadata effects")
			}
			if receipt.CollectionReceipt.Effects[operation].SelectedCount != 1 || receipt.CollectionReceipt.Effects[operation].QueueID != doc.Queue.ID {
				t.Fatal("missing queue preparation receipt")
			}
			if _, _, err := f.library.ProgressActivationQueue(f.scope, doc.Queue.ID, "single", "connected", "forged-success", 1); err == nil {
				t.Fatal("queue preparation impersonated project success")
			}
			restarted := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
			replayService := NewCollectionReviewService(restarted, resolve)
			if _, err := replayService.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "selection-apply", true); err != nil || !reflect.DeepEqual(doc, f.doc(t)) {
				t.Fatal("preparation replay changed data", err)
			}
			activation := NewActivationService(NewActivationInspector(f.library, f.roots, f.installed, f.file), realActivationCreator(t, f.scope, f.file, f.installed))
			if _, err := activation.Commit(t.Context(), f.scope, "single", review.Token, "not-project-consent"); err == nil {
				t.Fatal("arrangement approval authorized project creation")
			}
			projectReview, err := activation.Review(t.Context(), f.scope, "single", f.doc(t).Revision, "Song.rpp", "Selected song")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := activation.Commit(t.Context(), f.scope, "single", projectReview.Token, "independent-project-consent"); err != nil {
				t.Fatal(err)
			}
			linked := f.doc(t)
			if sessionEntry(linked, "single").Link == nil || sessionEntry(linked, "alternates").Link != nil || fileDigest(t, filepath.Join(f.tree.single, "Song.rpp")) != hash {
				t.Fatal("independent activation changed unselected project or source")
			}
			if _, _, err := f.library.ProgressActivationQueue(f.scope, doc.Queue.ID, "single", "connected", "verified-link", 1); err != nil {
				t.Fatal(err)
			}
			completed := f.doc(t)
			if completed.Queue.Status != "complete" || len(completed.Queue.Connected) != 1 {
				t.Fatal("queue progress did not use canonical link")
			}
			// The existing real-creator fixture replaces the Home declaration with
			// its portfolio_manager role. That invalidates this fixture's earlier
			// manager binding; a receipt must not revive that former authority.
			if _, err := replayService.Commit(t.Context(), f.scope.OwnerUserID, f.scope.HomeID, saved.Record.ID, review.Token, "selection-apply", true); err == nil || !reflect.DeepEqual(completed, f.doc(t)) {
				t.Fatal("obsolete Manager authority revived or completed queue reset", err)
			}
			if history, err := f.library.ReadArrangementHistory(t.Context(), f.scope, saved.Record.ID); err != nil || !reflect.DeepEqual(history.CollectionReceipt, receipt.CollectionReceipt) {
				t.Fatal("completed selection receipt unreadable after authority loss", err)
			}
		})
	}
}
