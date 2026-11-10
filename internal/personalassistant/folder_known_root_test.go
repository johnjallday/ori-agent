package personalassistant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKnownFolderRoot_ChooserRoutesShareCanonicalIdentity(t *testing.T) {
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Downloads")
	alias := filepath.Join(f.home, "Alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	f.service.deps.Picker = fakeFolderPicker{path: alias, chosen: true}
	f.service.deps.ResolveSelection = func(token string) (string, error) {
		if token == "checked-token" {
			return alias, nil
		}
		return "", errors.New("expired")
	}
	known, err := f.service.KnownFolderRoot("downloads")
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []FolderChoice{{Chip: "downloads"}, {Picker: true}, {SelectionToken: "checked-token"}} {
		got, chosen, err := f.service.ChooseFolder(t.Context(), "local", choice)
		if err != nil || !chosen || got != known {
			t.Fatalf("%+v: %s %v %v", choice, got, chosen, err)
		}
	}
	// No scan offer was created by either known-root resolution or choice.
	if _, _, err := f.service.ChooseFolder(t.Context(), "local", FolderChoice{SelectionToken: "expired"}); err == nil {
		t.Fatal("expired token accepted")
	}
	f.service.deps.HomeDir = func() (string, error) { return "", errors.New("missing") }
	if _, err := f.service.KnownFolderRoot("downloads"); err == nil {
		t.Fatal("missing home accepted")
	}
}

func TestKnownFolderRoot_SymlinkBackedConfiguredDownloads(t *testing.T) {
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Downloads")
	target := filepath.Join(f.home, "Redirected")
	if err := os.Rename(root, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	known, err := f.service.KnownFolderRoot("downloads")
	if err != nil || known != target {
		t.Fatalf("known=%s err=%v", known, err)
	}
	if _, err := f.service.KnownFolderRoot("arbitrary"); !errors.Is(err, ErrFolderChipUnknown) {
		t.Fatal(err)
	}
}
