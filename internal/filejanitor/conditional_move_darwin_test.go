//go:build darwin

package filejanitor

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// What macOS returns for a filesystem that lacks RENAME_EXCL is one of these two;
// which one was not observed on a real unsupported volume (see the findings log).
var errNoReplaceUnsupported error = unix.ENOTSUP

func TestDarwinFSAllowed(t *testing.T) {
	cases := []struct {
		name   string
		fstype string
		flags  uint32
		ok     bool
	}{
		{"local APFS", "apfs", unix.MNT_LOCAL, true},
		{"HFS+", "hfs", unix.MNT_LOCAL, false},
		{"exFAT", "exfat", unix.MNT_LOCAL, false},
		{"network share", "smbfs", 0, false},
		{"APFS that is not local", "apfs", 0, false},
		{"read-only APFS", "apfs", unix.MNT_LOCAL | unix.MNT_RDONLY, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := darwinFSAllowed(tc.fstype, tc.flags)
			if tc.ok && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrMoveUnsupported) {
				t.Fatalf("want unsupported, got %v", err)
			}
		})
	}
}

func TestDarwinUnsupportedRenameErrnos(t *testing.T) {
	for _, err := range []error{unix.ENOTSUP, unix.EINVAL} {
		if !unsupportedRename(err) {
			t.Fatalf("%v means the filesystem lacks the flag", err)
		}
	}
	for _, err := range []error{unix.EEXIST, unix.EXDEV, unix.ENOENT, unix.EACCES} {
		if unsupportedRename(err) {
			t.Fatalf("%v is not an unsupported-filesystem error", err)
		}
	}
}
