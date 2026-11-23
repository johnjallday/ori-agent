//go:build darwin

package filejanitor

import (
	"bytes"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from -> to and fails with EEXIST if anything has the
// destination name, as the filesystem sees names (APFS is case-insensitive and
// normalization-insensitive by default, so "Report.pdf" and "report.pdf" collide).
func renameNoReplace(fromfd int, from string, tofd int, to string) error {
	return unix.RenameatxNp(fromfd, from, tofd, to, unix.RENAME_EXCL)
}

// unsupportedRename reports the errors a filesystem returns when it does not
// implement the exclusive flag. Nothing moved.
func unsupportedRename(err error) bool {
	return errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL)
}

// isPlaceholder reports a cloud placeholder whose content is not on this disk.
func isPlaceholder(st *unix.Stat_t) bool { return st.Flags&unix.SF_DATALESS != 0 }

// localExclusiveFS reports nil only for a writable, local APFS volume.
func localExclusiveFS(fd int) error {
	var sf unix.Statfs_t
	if err := unix.Fstatfs(fd, &sf); err != nil {
		return fmt.Errorf("%w: the folder's filesystem could not be identified", ErrMoveUnsupported)
	}
	return darwinFSAllowed(string(bytes.TrimRight(sf.Fstypename[:], "\x00")), sf.Flags)
}

// darwinFSAllowed is the pure rule, so it can be tested with made-up volumes.
func darwinFSAllowed(fstype string, flags uint32) error {
	switch {
	case fstype != "apfs":
		return fmt.Errorf("%w: only APFS folders are supported on this Mac", ErrMoveUnsupported)
	case flags&unix.MNT_LOCAL == 0:
		return fmt.Errorf("%w: the folder is not on a local disk", ErrMoveUnsupported)
	case flags&unix.MNT_RDONLY != 0:
		return fmt.Errorf("%w: the folder is on a read-only disk", ErrMoveUnsupported)
	}
	return nil
}
