//go:build darwin || linux

package filejanitor

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestStationMethodIsOnlyAPreferenceAndInvalidatesOldPictures(t *testing.T) {
	f := newDocsFixture(t, "one.txt")
	s := f.s
	read := func() StationMethod {
		v, err := s.StationMethod(t.Context(), "local", "ws-1")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	legacy := read()
	if legacy.Current != MethodKind || legacy.Suggested != MethodKind || legacy.Send {
		t.Fatal(legacy)
	}
	_, err := s.store.UpdateSettings("ws-1", func(v *JanitorSettings) error { v.DeferredDestination = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.mkdir("Orders")
	before := f.tree()
	fresh := read()
	if fresh.Current != "" || !fresh.Shelves || fresh.Suggested != MethodFolders {
		t.Fatal(fresh)
	}
	choose := func(v StationMethod, m string) (StationMethod, error) {
		return s.ChooseStationMethod(t.Context(), "local", "ws-1", v.Revision, m)
	}
	if _, err := choose(legacy, MethodKind); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatal(err)
	}
	a, err := choose(fresh, MethodFolders)
	if err != nil || a.Current != MethodFolders || read() != a {
		t.Fatal(a, err)
	}
	p, err := s.ReadShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	k, err := choose(a, MethodKind)
	if err != nil || k.Current != MethodKind {
		t.Fatal(k, err)
	}
	if _, err = choose(k, MethodFolders); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileShelfPicture(t.Context(), "local", "ws-1", f.batch.ID, p.Revision, keyA, []ShelfPlacement{{File: p.Files[0].ID, Shelf: p.Folders[0].Key}}); !errors.Is(err, ErrReviewPlanChanged) {
		t.Fatal("filed obsolete picture", err)
	}
	if !reflect.DeepEqual(before, f.tree()) {
		t.Fatal("method choice changed files")
	}
	if _, err := s.StationMethod(t.Context(), "other", "ws-1"); err == nil {
		t.Fatal("wrong owner")
	}
	if err := os.Rename(f.root, f.root+"-old"); err != nil {
		t.Fatal(err)
	}
	mkdir(t, f.root)
	if _, err := s.StationMethod(t.Context(), "local", "ws-1"); err == nil {
		t.Fatal("accepted changed root")
	}
}
