//go:build linux

package filejanitor

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

var errNoReplaceUnsupported error = unix.EINVAL

func TestLinuxFSAllowed(t *testing.T) {
	const (
		nfs     = 0x6969
		fuse    = 0x65735546
		overlay = 0x794c7630
		smb2    = 0xfe534d42
	)
	cases := []struct {
		name  string
		magic uint32
		flags uint64
		ok    bool
	}{
		{"ext4", unix.EXT4_SUPER_MAGIC, 0, true},
		{"xfs", unix.XFS_SUPER_MAGIC, 0, true},
		{"btrfs", unix.BTRFS_SUPER_MAGIC, 0, true},
		{"tmpfs", unix.TMPFS_MAGIC, 0, true},
		{"f2fs", unix.F2FS_SUPER_MAGIC, 0, true},
		{"nfs", nfs, 0, false},
		{"fuse", fuse, 0, false},
		{"overlay", overlay, 0, false},
		{"smb2", smb2, 0, false},
		{"read-only ext4", unix.EXT4_SUPER_MAGIC, unix.MS_RDONLY, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := linuxFSAllowed(tc.magic, tc.flags)
			if tc.ok && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrMoveUnsupported) {
				t.Fatalf("want unsupported, got %v", err)
			}
		})
	}
}

func TestLinuxUnsupportedRenameErrnos(t *testing.T) {
	for _, err := range []error{unix.EINVAL, unix.ENOSYS, unix.ENOTSUP} {
		if !unsupportedRename(err) {
			t.Fatalf("%v means the kernel or filesystem lacks the flag", err)
		}
	}
	for _, err := range []error{unix.EEXIST, unix.EXDEV, unix.ENOENT, unix.EACCES} {
		if unsupportedRename(err) {
			t.Fatalf("%v is not an unsupported-filesystem error", err)
		}
	}
}
