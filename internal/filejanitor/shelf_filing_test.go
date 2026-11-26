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
			placements = append(placements, ShelfPlacement{File: file.ID, Shelf: second, Parent: first})
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
func TestUndoShelfFilingRetainsOnlyConfirmedNewFolderNames(t *testing.T) {
	f := newDocsFixture(t, "order.pdf", "note.txt")
	f.mkdir("Invoices")
	p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	sh := p.Folders[0]
	check := func(parent, name string) error {
		return f.s.CheckShelfName(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, parent, name)
	}
	for _, bad := range []string{"", "..", "a/b", ".hidden", "Invoices", "Filed", "order.pdf"} {
		if check("", bad) == nil {
			t.Fatalf("accepted name %q", bad)
		}
	}
	if err := check(sh.Key, "Winter"); err != nil || f.exists("Invoices/Winter") {
		t.Fatal("name check wrote or failed", err)
	}
	rows := []ShelfPlacement{{File: p.Files[0].ID, NewName: "Winter", Parent: sh.Key}, {File: p.Files[1].ID, Shelf: sh.Key}}
	file := func() (DocumentsFilingResult, error) {
		return f.s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, rows)
	}
	r, err := file()
	if err != nil || r.Filed != 2 || len(r.CreatedFolders) != 1 || r.CreatedFolders[0] != "Invoices/Winter" {
		t.Fatalf("file %+v %v", r, err)
	}
	replay, err := file()
	if err != nil || !replay.Replayed || len(replay.CreatedFolders) != 1 {
		t.Fatalf("replay %+v %v", replay, err)
	}
	for _, wantReplay := range []bool{false, true} {
		u, err := f.s.UndoFiling(t.Context(), "local", "ws-1", r.FilingID)
		if err != nil || u.Back != 2 || u.Replayed != wantReplay || len(u.RetainedFolders) != 1 || u.RetainedFolders[0] != "Invoices/Winter" {
			t.Fatalf("undo %+v %v", u, err)
		}
	}
	entries, err := os.ReadDir(f.abs("Invoices/Winter"))
	if err != nil || len(entries) != 0 || !f.exists("order.pdf") || !f.exists("note.txt") {
		t.Fatal("files not restored or empty folder not retained", err)
	}
}

func TestNewShelfUsesNameCheckAsAdviceOnly(t *testing.T) {
	for _, scenario := range []string{"stale source", "name taken", "replaced after filing", "another file added"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDocsFixture(t, "one.pdf")
			p, err := f.s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.CheckShelfName(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, "", "Orders"); err != nil {
				t.Fatal(err)
			}
			if scenario == "stale source" {
				f.write("one.pdf", "changed since scan")
			} else if scenario == "name taken" {
				f.mkdir("Orders")
				f.write("Orders/other.txt", "not ours")
			}
			r, err := f.s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, []ShelfPlacement{{File: p.Files[0].ID, NewName: "Orders"}})
			switch scenario {
			case "stale source":
				if err != nil || r.Filed != 0 || len(r.CreatedFolders) != 0 || f.exists("Orders") || f.read("one.pdf") != "changed since scan" {
					t.Fatal(r, err)
				}
				return
			case "name taken":
				if err == nil || f.exists("Orders/one.pdf") || f.read("Orders/other.txt") != "not ours" || !f.exists("one.pdf") {
					t.Fatal(r, err)
				}
				return
			}
			if err != nil || r.Filed != 1 {
				t.Fatal(r, err)
			}
			if scenario == "replaced after filing" {
				if err := os.Rename(f.abs("Orders"), f.abs("Old")); err != nil {
					t.Fatal(err)
				}
				f.mkdir("Orders")
			} else {
				f.write("Orders/other.txt", "not ours")
			}
			u, err := f.s.UndoFiling(t.Context(), "local", "ws-1", r.FilingID)
			if err != nil || len(u.RetainedFolders) != 1 || u.RetainedFolders[0] != "Orders" || !f.exists("Orders") {
				t.Fatal(u, err)
			}
			if scenario == "replaced after filing" {
				if u.NotBack != 1 || !f.exists("Old/one.pdf") || f.exists("one.pdf") {
					t.Fatal(u)
				}
			} else if u.Back != 1 || !f.exists("one.pdf") || f.read("Orders/other.txt") != "not ours" {
				t.Fatal(u)
			}
		})
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
