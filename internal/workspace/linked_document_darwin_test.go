package workspace

import (
	"encoding/binary"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinkedOpenRefusesFinderAlias(t *testing.T) {
	f := newLinkedOpenFixture(t, "track-2")
	token := f.list(t, false)["Song.rpp"]
	var finderInfo [32]byte
	binary.BigEndian.PutUint16(finderInfo[8:10], 0x8000)
	if err := unix.Setxattr(filepath.Join(f.root, "Song.rpp"), "com.apple.FinderInfo", finderInfo[:], 0); err != nil {
		t.Fatal(err)
	}
	if token == "" || f.list(t, false)["Song.rpp"] != "" || f.post(t, "Song.rpp", token, nil) != 409 || len(f.opened) != 0 {
		t.Fatal("Finder alias admitted for desktop open")
	}
}
