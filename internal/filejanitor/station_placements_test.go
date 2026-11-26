//go:build darwin || linux || freebsd

package filejanitor

import (
	"os"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestPlacementsComeOnlyFromCompletedFilesAndConfirmedMemory(t *testing.T) {
	f := newDocsFixture(t, "receipt-1.txt", "receipt-2.txt", "trip.txt", "receipt-private.pdf")
	f.mkdir("Projects/Orders")
	_, resolver := newTestStore(t)
	memory := workspace.NewMemoryStore(resolver)
	f.s.SetPlacementMemory(memory)
	ctx := t.Context()
	const owner, id = "local", "ws-1"
	_, err := f.s.store.UpdateSettings(id, func(v *JanitorSettings) error { v.FilingMethod = MethodFolders; v.MethodRevision = "first"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	picture := func(batch, parent string) ShelfPicture {
		t.Helper()
		p, e := f.s.ReadShelfPicture(ctx, owner, id, batch, parent)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p := picture(f.batch.ID, "")
	parent := p.Folders[0].Key
	sh := picture(f.batch.ID, parent).Folders[0]
	var place []ShelfPlacement
	for _, file := range p.Files {
		if file.Name != "receipt-private.pdf" {
			place = append(place, ShelfPlacement{File: file.ID, Shelf: sh.Key, Parent: parent})
		}
	}
	result, err := f.s.FileShelfPicture(ctx, owner, id, p.Batch, p.Revision, keyA, place)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.s.ReadFiling(ctx, owner, id, result.FilingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	offer := receipt.PlacementOffer
	if offer == nil || len(offer.Rows) != 2 || offer.Rows[0].Count != 2 || offer.Rows[1].Count != 1 {
		t.Fatalf("bad offer: %+v", offer)
	}
	list := func() PlacementList {
		v, e := f.s.ReadPlacements(owner, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if len(list().Rows) != 0 {
		t.Fatal("File saved a preference")
	}
	save := func(rev string, keys ...string) error {
		return f.s.SaveReceiptPlacements(ctx, owner, id, result.FilingID, rev, keys)
	}
	if save(offer.Revision, "invented", offer.Rows[0].ID) == nil || len(list().Rows) != 0 {
		t.Fatal("unreviewed partial save")
	}
	if err = save(offer.Revision, offer.Rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if save(offer.Revision, offer.Rows[1].ID) == nil {
		t.Fatal("stale selection admitted")
	}
	if rows := list().Rows; len(rows) != 1 || rows[0].Broken {
		t.Fatalf("memory: %+v", rows)
	}
	agedFile(t, f.root, "receipt-3.txt", 10)
	agedFile(t, f.root, "trip-2.txt", 10)
	next, _, err := f.s.ScanNow(id, ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	after := picture(next.ID, "")
	found := false
	for _, file := range after.Files {
		if file.Name == "receipt-3.txt" {
			found = true
			if !file.Remembered || file.Suggested != sh.Key {
				t.Fatalf("not preplaced: %+v", file)
			}
		} else if file.Remembered {
			t.Fatal("unselected pattern preplaced")
		}
	}
	if !found || !slices.ContainsFunc(after.Folders, func(v PictureShelf) bool { return v.Key == sh.Key && v.Located && v.Parent == parent }) {
		t.Fatal("nested placement not addressable")
	}
	if _, err = os.Stat(f.abs("receipt-3.txt")); err != nil {
		t.Fatal("scan moved a file")
	}
	if pile := f.s.RememberedPile(owner, id, next.ID); pile == nil || pile.Placed != 1 {
		t.Fatalf("pile: %+v", pile)
	}
	if err = os.Rename(f.abs("Projects/Orders"), f.abs("Projects/Old")); err != nil {
		t.Fatal(err)
	}
	if !list().Rows[0].Broken {
		t.Fatal("missing shelf not marked broken")
	}
	f.mkdir("Projects/Orders")
	if !list().Rows[0].Broken {
		t.Fatal("placement adopted a replacement")
	}
	for _, file := range picture(next.ID, "").Files {
		if file.Name == "receipt-3.txt" && file.Suggested != "" {
			t.Fatal("broken rule did not leave file loose")
		}
	}
	if _, e := f.s.ReadPlacements("foreign", id); e == nil {
		t.Fatal("foreign owner saw placements")
	}
	l := list()
	if err = f.s.RemovePlacement(owner, id, l.Revision, l.Rows[0].ID); err != nil || len(list().Rows) != 0 {
		t.Fatal("exact removal failed", err)
	}
	snap, _ := memory.SnapshotExact(id)
	if len(snap.Entries) != 0 {
		t.Fatal("removed preference still in workspace memory")
	}
}
