//go:build linux || darwin || freebsd

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/directoryid"
)

func TestVersionedFileRejectsSameSizeAndDateReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	listing, err := ListContainedEntries(root, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	e := listing.Entries[0]
	ri, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	rootID := directoryid.Witness(ri)
	if _, err := ReadVersionedFile(root, "a.txt", 256, "stale-root", e.Version); !errors.Is(err, ErrSourceChanged) {
		t.Fatal(err)
	}
	if _, err := ReadVersionedFile(root, "a.txt", 256, rootID, e.Version); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, e.ModTime, e.ModTime); err != nil {
		t.Fatal(err)
	}
	if f, err := ReadVersionedFile(root, "a.txt", 256, rootID, e.Version); !errors.Is(err, ErrSourceChanged) || len(f.Data) > 0 {
		t.Fatal("substitute source returned")
	}
}
