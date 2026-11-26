//go:build darwin || linux

package filejanitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChosenFolderBindsPreviewAndUsesVerifiedReceiptUndo(t *testing.T) {
	s, b, cs, root := planFixture(t, "receipt.pdf", "other.pdf")
	dest, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.ReadReviewPicture(t.Context(), "local", "ws-1", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection := ChosenSelection{Revision: p.Revision, DraftRevision: p.DraftRevision, CandidateID: cs[0].ID}
	preview, err := s.PreviewChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, selection)
	if err != nil || preview.Digest == "" || len(preview.Items) != 1 || !exists(t, root, cs[0].Name) {
		t.Fatal(preview, err)
	}
	// A same-path replacement is not the reviewed destination.
	if err = os.Rename(dest, dest+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FileChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, selection, preview.Digest, keyOne); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatal(err)
	}
	if err = os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(dest+"-old", dest); err != nil {
		t.Fatal(err)
	}
	r, err := s.FileChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, selection, preview.Digest, keyOne)
	if err != nil || r.Filed != 1 || !exists(t, dest, cs[0].Name) || !exists(t, root, cs[1].Name) {
		t.Fatal(r, err)
	}
	replay, err := s.FileChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, selection, preview.Digest, keyOne)
	if err != nil || !replay.Replayed || replay.FilingID != r.FilingID {
		t.Fatal(replay, err)
	}
	restarted := NewService(s.store, s.workspaces)
	rc, err := restarted.ReadFiling(t.Context(), "local", "ws-1", r.FilingID, 0)
	if err != nil || !rc.UndoAvailable || rc.Filed != 1 {
		t.Fatal(rc, err)
	}
	undo, err := restarted.UndoFiling(t.Context(), "local", "ws-1", r.FilingID)
	if err != nil || undo.Back != 1 || !exists(t, root, cs[0].Name) {
		t.Fatal(undo, err)
	}
}
func TestChosenFolderGroupNeverOverwrites(t *testing.T) {
	s, b, cs, root := planFixture(t, "one.pdf", "two.pdf")
	dest, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.ReadReviewPicture(t.Context(), "local", "ws-1", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel := ChosenSelection{Revision: p.Revision, DraftRevision: p.DraftRevision, Section: PictureFiled, Key: "documents"}
	prev, err := s.PreviewChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, sel)
	if err != nil || len(prev.Items) != 2 {
		t.Fatal(prev, err)
	}
	path := filepath.Join(dest, cs[0].Name)
	if err = os.WriteFile(path, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := s.FileChosenFolder(t.Context(), "local", "ws-1", b.ID, dest, sel, prev.Digest, keyTwo)
	data, _ := os.ReadFile(path)
	if err != nil || r.Filed != 1 || r.Stale+r.Failed != 1 || string(data) != "occupied" || !exists(t, root, cs[0].Name) {
		t.Fatal(r, err)
	}
}
