package settingsreset

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameArchiveNoReplace(source *os.File, from string, destination *os.File, to string) error {
	return unix.Renameat2(int(source.Fd()), from, int(destination.Fd()), to, unix.RENAME_NOREPLACE)
}

func archiveLocalVolume(dir *os.File) bool {
	var stat unix.Statfs_t
	if unix.Fstatfs(int(dir.Fd()), &stat) != nil {
		return false
	}
	switch stat.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}

func archiveEntryAt(fd int, path string) (archiveEntry, error) {
	var stat unix.Statx_t
	const mask = unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID
	if err := unix.Statx(fd, path, unix.AT_SYMLINK_NOFOLLOW, mask, &stat); err != nil {
		return archiveEntry{}, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return archiveEntry{}, errArchiveVolume
	}
	return archiveEntry{
		Identity: archiveIdentity{Device: uint64(stat.Dev_major)<<32 | uint64(stat.Dev_minor), Inode: stat.Ino,
			BirthSec: stat.Btime.Sec, BirthNS: stat.Btime.Nsec},
		Mount: stat.Mnt_id, Owner: stat.Uid, Directory: stat.Mode&unix.S_IFMT == unix.S_IFDIR,
		UnsafeParent: stat.Mode&0o022 != 0,
	}, nil
}
