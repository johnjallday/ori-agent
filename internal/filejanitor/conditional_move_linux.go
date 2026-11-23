//go:build linux

package filejanitor

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from -> to and fails with EEXIST if anything has the
// destination name. The kernel does the existence check and the rename as one
// step.
func renameNoReplace(fromfd int, from string, tofd int, to string) error {
	return unix.Renameat2(fromfd, from, tofd, to, unix.RENAME_NOREPLACE)
}

// unsupportedRename reports the errors a filesystem returns when it does not
// implement the flag. Nothing moved.
func unsupportedRename(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.ENOTSUP)
}

// isPlaceholder is false here: Linux has no portable cloud-placeholder flag.
func isPlaceholder(*unix.Stat_t) bool { return false }

// localExclusiveFS reports nil only for local filesystems where the no-replace
// rename is atomic and inode numbers are stable.
func localExclusiveFS(fd int) error {
	var sf unix.Statfs_t
	if err := unix.Fstatfs(fd, &sf); err != nil {
		return fmt.Errorf("%w: the folder's filesystem could not be identified", ErrMoveUnsupported)
	}
	return linuxFSAllowed(uint32(sf.Type), uint64(sf.Flags)) // #nosec G115 -- the kernel's filesystem magic and flag bits
}

// linuxFSAllowed is the pure rule. Network, FUSE and overlay filesystems are
// refused: a no-replace rename there is not atomic on the server, or inodes are
// not stable.
func linuxFSAllowed(magic uint32, flags uint64) error {
	switch magic {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.F2FS_SUPER_MAGIC:
	default:
		return fmt.Errorf("%w: this kind of disk is not supported", ErrMoveUnsupported)
	}
	if flags&unix.MS_RDONLY != 0 {
		return fmt.Errorf("%w: the folder is on a read-only disk", ErrMoveUnsupported)
	}
	return nil
}
