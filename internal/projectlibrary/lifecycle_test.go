package projectlibrary

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLifecycle_PermanentHomeRemovalDropsGrantsWithoutTouchingExternalSongs(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	if scan := reviewedRootScan(t, r, scope, root, "before-permanent-removal"); scan.Status != "complete" {
		t.Fatalf("pre-removal observation: %+v", scan)
	}
	song := filepath.Join(tree.single, "Song.rpp")
	original := fileDigest(t, song)
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	// FileStore has no Trash adapter. This exercises the production lifecycle's
	// permanent-delete fallback, never the user's actual system Trash.
	lifecycle := workspace.NewAssistantProgramStore(file)
	review, err := lifecycle.ReviewHomeRemoval(scope.HomeID, home.GetAssistantProgramState().StateRevision)
	if err != nil || review.Trashes {
		t.Fatalf("removal review did not disclose permanent deletion: %+v %v", review, err)
	}
	receipt, err := lifecycle.CommitHomeRemoval(scope.HomeID, review.Token)
	if err != nil || receipt.Trashed {
		t.Fatalf("permanent removal: %+v %v", receipt, err)
	}
	if _, err := file.Get(scope.HomeID); err == nil {
		t.Fatal("deleted Home and library remain readable")
	}
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted Home retained root consent: %v", err)
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted Home retained source access: %v", err)
	}
	if after := fileDigest(t, song); after != original {
		t.Fatal("Home deletion modified external music")
	}
}

// A disposable reversible store avoids invoking the system Trash during a
// lifecycle proof. All workspaces and source markers live under t.TempDir.
type libraryReversibleTrash struct{ workspace.Store }

func (libraryReversibleTrash) TrashSupported() bool { return true }
func (s libraryReversibleTrash) Trash(id string) error {
	return s.Update(id, func(ws *workspace.Workspace) error {
		ws.Status = workspace.StatusTrashed
		return nil
	})
}

func TestLifecycle_StaleHomeRemovalReviewCannotDiscardNewScan(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	lifecycle := workspace.NewAssistantProgramStore(libraryReversibleTrash{Store: file})
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := lifecycle.ReviewHomeRemoval(scope.HomeID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	if scan := reviewedRootScan(t, r, scope, root, "after-removal-review"); scan.Status != "complete" {
		t.Fatal(scan)
	}
	if _, err := lifecycle.CommitHomeRemoval(scope.HomeID, review.Token); !errors.Is(err, workspace.ErrAssistantTopologyConflict) {
		t.Fatalf("stale impact review discarded a newer library scan: %v", err)
	}
	if _, err := r.VerifyConnectedRoot(scope, root.ID); err != nil {
		t.Fatalf("failed stale removal revoked a valid root: %v", err)
	}
}

func TestLifecycle_RemovalWaitsForInFlightSourceAccess(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	lifecycle := workspace.NewAssistantProgramStore(libraryReversibleTrash{Store: file})
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := lifecycle.ReviewHomeRemoval(scope.HomeID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatal(err)
	}
	gate := rootAccessGate(scope)
	gate.RLock() // A descriptor reader has already entered the root-access gate.
	done := make(chan error, 1)
	go func() {
		_, removeErr := lifecycle.CommitHomeRemoval(scope.HomeID, review.Token)
		done <- removeErr
	}()
	select {
	case err := <-done:
		gate.RUnlock()
		t.Fatalf("Home removal acknowledged while source access was active: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	gate.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Home removal: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Home removal deadlocked on descriptor access")
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("source access succeeded after Home removal acknowledgement: %v", err)
	}
}

func TestLifecycle_RestoredHomeKeepsNotesButOldRootNeverRegainsAuthority(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	before := reviewedRootScan(t, r, scope, root, "before-home-removal")
	if before.Status != "complete" {
		t.Fatal(before)
	}
	doc, err := r.library.Read(scope)
	if err != nil || len(doc.Entries) != 4 {
		t.Fatalf("initial history: %+v %v", doc, err)
	}
	oldID := doc.Entries[0].ID
	store := libraryReversibleTrash{Store: file}
	lifecycle := workspace.NewAssistantProgramStore(store)
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := lifecycle.ReviewHomeRemoval(scope.HomeID, home.GetAssistantProgramState().StateRevision)
	if err != nil || !review.Trashes || !strings.Contains(strings.Join(review.Impact, " "), "Old discovery roots remain inactive") {
		t.Fatalf("review did not disclose retained metadata and lost root grant: %+v %v", review, err)
	}
	if receipt, err := lifecycle.CommitHomeRemoval(scope.HomeID, review.Token); err != nil || !receipt.Trashed {
		t.Fatalf("Home removal: %+v %v", receipt, err)
	}
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed Home still exposed its former root: %v", err)
	}
	if err := file.Update(scope.HomeID, func(current *workspace.Workspace) error {
		current.Status = workspace.StatusActive
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if restored, err := lifecycle.RestoreRemovedHome(scope.HomeID); err != nil || !restored {
		t.Fatalf("restore Home: %v %v", restored, err)
	}
	doc, err = r.library.Read(scope)
	if err != nil || len(doc.Entries) != 4 || doc.Entries[0].ID != oldID {
		t.Fatalf("restoring Home erased user history: %+v %v", doc, err)
	}
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("restore silently reactivated root: %v", err)
	}
	if _, err := r.Discover(context.Background(), scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("restore permitted old root scan: %v", err)
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 10); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("restore reopened source files: %v", err)
	}
	page, err := r.library.Query(scope, Search{PageSize: 10})
	if err != nil || page.Total != 4 || page.Rows[0].Availability != "revoked_source" {
		t.Fatalf("historical source presented as available: %+v %v", page, err)
	}
	pickerReview, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r.Review(scope, pickerReview, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	reconnected, replay, err := r.Commit(scope, fresh.Token, "new-grant-after-restore")
	if err != nil || replay || reconnected.ID == root.ID {
		t.Fatalf("new reviewed grant was conflated with old grant: %+v %v %v", reconnected, replay, err)
	}
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("new grant reactivated old root: %v", err)
	}
	if _, err := r.VerifyConnectedRoot(scope, reconnected.ID); err != nil {
		t.Fatalf("new consent could not read exact root: %v", err)
	}
}
