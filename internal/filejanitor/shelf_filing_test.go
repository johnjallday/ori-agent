//go:build darwin || linux

package filejanitor

import (
	"errors"
	"os"
	"testing"
)

func TestFileShelvesUsesOneReceiptAndUndoAcrossDestinations(t *testing.T) {
	f := newDocsFixture(t, "one.pdf", "two.pdf", "keep.txt", "changed.png", "taken.jpg")
	f.mkdir("First")
	f.mkdir("First/Second")
	f.write("First/taken.jpg", "existing")
	picture := func(parent string) ShelfPicture {
		p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, parent)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := picture("")
	first := p.Folders[0].Key
	second := picture(first).Folders[0].Key
	var placements []ShelfPlacement
	for _, file := range p.Files {
		switch file.Name {
		case "keep.txt":
			continue
		case "two.pdf":
			placements = append(placements, ShelfPlacement{file.ID, second, first})
		default:
			placements = append(placements, ShelfPlacement{File: file.ID, Shelf: first})
		}
	}
	f.write("changed.png", "no longer the reviewed file")
	run := func() (DocumentsFilingResult, error) {
		return f.s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, placements)
	}
	r, err := run()
	if err != nil || r.Filed != 2 || r.Stale != 2 || r.FilingID == "" {
		t.Fatalf("file: %+v, %v", r, err)
	}
	if !f.exists("First/one.pdf") || !f.exists("First/Second/two.pdf") || !f.exists("keep.txt") || f.read("First/taken.jpg") != "existing" {
		t.Fatal("wrong resulting folders")
	}
	replay, err := run()
	if err != nil || !replay.Replayed || replay.FilingID != r.FilingID || replay.Filed != 2 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	undo, err := f.s.UndoFiling(t.Context(), "local", "ws-1", r.FilingID)
	if err != nil || undo.Back != 2 || !f.exists("one.pdf") || !f.exists("two.pdf") || !f.exists("First/Second") {
		t.Fatalf("undo: %+v %v", undo, err)
	}
}
func TestFileShelfRefusesReplacedDestinationAndForeignSelection(t *testing.T) {
	f := newDocsFixture(t, "one.pdf")
	f.mkdir("Receipts")
	p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	row := ShelfPlacement{File: p.Files[0].ID, Shelf: p.Folders[0].Key}
	file := func(row ShelfPlacement) error {
		_, err := f.s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, []ShelfPlacement{row})
		return err
	}
	foreign := row
	foreign.File = "other"
	if err := file(foreign); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatal(err)
	}
	if err := os.Rename(f.abs("Receipts"), f.abs("Old")); err != nil {
		t.Fatal(err)
	}
	f.mkdir("Receipts")
	if err := file(row); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatal(err)
	}
	if !f.exists("one.pdf") || f.exists("Receipts/one.pdf") {
		t.Fatal("changed shelf moved the file")
	}
}
