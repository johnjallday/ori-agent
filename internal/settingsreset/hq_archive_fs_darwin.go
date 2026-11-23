package settingsreset

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameArchiveNoReplace(source *os.File, from string, destination *os.File, to string) error {
	return unix.RenameatxNp(int(source.Fd()), from, int(destination.Fd()), to, unix.RENAME_EXCL)
}

func archiveLocalVolume(dir *os.File) bool {
	var stat unix.Statfs_t
	return unix.Fstatfs(int(dir.Fd()), &stat) == nil && stat.Flags&unix.MNT_LOCAL != 0 &&
		unix.ByteSliceToString(stat.Fstypename[:]) == "apfs"
}

func archiveEntryAt(fd int, path string) (archiveEntry, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, path, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return archiveEntry{}, err
	}
	device := uint64(uint32(stat.Dev)) // #nosec G115 -- macOS dev_t is signed; the opaque device identity uses all 32 bits, not its sign.
	return archiveEntry{
		Identity: archiveIdentity{Device: device, Inode: stat.Ino, BirthSec: stat.Btim.Sec, BirthNS: stat.Btim.Nsec},
		Mount:    device, Owner: int64(stat.Uid), Directory: stat.Mode&unix.S_IFMT == unix.S_IFDIR,
		UnsafeParent: stat.Mode&0o022 != 0,
	}, nil
}
