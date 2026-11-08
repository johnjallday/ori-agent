package foldermap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLook_OpensAnUnopenedFolderAndRecordsIt(t *testing.T) {
	var paths []string
	for i := range 30 {
		paths = append(paths, fmt.Sprintf("Box %02d/a.txt", i))
	}
	paths = append(paths, "Box 29/Songs/One/Song.rpp", "Box 29/.hidden", "Box 29/Song.rpp.icloud", "Box 29/node_modules/x.js")
	root := build(t, paths...)
	m, err := Walk(root, Options{MaxEntries: 30})
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := m.Folder("Box 29"); f.State != StateUnopened {
		t.Fatalf("Box 29 should start unopened: %+v", f)
	}
	got, err := Look(root, m, "Box 29")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateWalked || got.More {
		t.Fatalf("result = %+v", got)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Kind+":"+e.Name)
	}
	if strings.Join(names, ",") != "folder:Songs,file:a.txt" {
		t.Fatalf("entries = %v (hidden, cloud placeholders and tooling folders are excluded)", names)
	}
	if f, ok := m.Folder("Box 29/Songs"); !ok || f.State != StateUnopened {
		t.Fatalf("the look must record the folder's subfolders: %+v %v", f, ok)
	}
	// A look into the song folder names its marker and closes it.
	song, err := Look(root, m, "Box 29/Songs/One")
	if !errors.Is(err, ErrNotInMap) {
		t.Fatalf("a folder the map never recorded = %+v %v", song, err)
	}
	if _, err := Look(root, m, "Box 29/Songs"); err != nil {
		t.Fatal(err)
	}
	song, err = Look(root, m, "Box 29/Songs/One")
	if err != nil || song.State != StateClosed || song.Project != "*.rpp" {
		t.Fatalf("song look = %+v %v", song, err)
	}
	if song.Entries[0].Marker != "*.rpp" || song.Entries[0].Shape == "" {
		t.Fatalf("entry facts = %+v", song.Entries)
	}
	if facts := FactsFrom(m); len(facts.Members) != 1 || facts.Members[0].Depth != 3 {
		t.Fatalf("facts after looks = %+v", facts.Members)
	}
}

func TestContinue_AnAreaOpensWhatIsUnopenedInsideItFirst(t *testing.T) {
	var paths []string
	for _, area := range []string{"Archive", "Photos"} {
		for i := range 10 {
			paths = append(paths, fmt.Sprintf("%s/Folder %02d/a.txt", area, i))
		}
	}
	root := build(t, paths...)
	m, err := Walk(root, Options{MaxEntries: 22}) // both areas listed, none of their folders
	if err != nil {
		t.Fatal(err)
	}
	if err := Continue(root, m, []string{"Photos"}, Options{MaxEntries: 3}); err != nil {
		t.Fatal(err)
	}
	opened := 0
	for _, f := range m.Folders {
		if strings.HasPrefix(f.Path, "Photos/") && f.State == StateWalked {
			opened++
		}
		if strings.HasPrefix(f.Path, "Archive/") && f.State == StateWalked && opened == 0 {
			t.Fatalf("Archive was opened before Photos: %s", f.Path)
		}
	}
	if opened == 0 {
		t.Fatal("the named area's unopened folders were not opened first")
	}
}

func TestLook_RefusesEscapes(t *testing.T) {
	outside := build(t, "Secret/Song.rpp")
	root := build(t, "Inside/a.txt")
	m, err := Walk(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/etc", outside, "..", "../" + filepath.Base(outside), "Inside/../..", `Inside\..`} {
		if _, err := Look(root, m, bad); err == nil {
			t.Errorf("Look(%q) was allowed", bad)
		}
	}
	// A recorded folder swapped for a link since the walk is not followed.
	if err := os.RemoveAll(filepath.Join(root, "Inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "Secret"), filepath.Join(root, "Inside")); err != nil {
		t.Fatal(err)
	}
	if _, err := Look(root, m, "Inside"); err == nil {
		t.Fatal("a swapped-in link must not be listed")
	}
}

func TestLook_ListsAtMostLookMaxEntriesWithAMoreMarker(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "Exports")
	if err := os.MkdirAll(big, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= LookMaxEntries; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f%04d.wav", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Walk(root, Options{MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Look(root, m, "Exports")
	if err != nil {
		t.Fatal(err)
	}
	if !got.More || len(got.Entries) != LookMaxEntries || got.State != StateUnopened {
		t.Fatalf("more=%v entries=%d state=%q", got.More, len(got.Entries), got.State)
	}
}
