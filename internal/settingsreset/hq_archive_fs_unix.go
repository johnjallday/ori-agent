//go:build darwin || linux

package settingsreset

import (
	"os"

	"golang.org/x/sys/unix"
)

func openArchiveParent(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G703 -- canonical installation or workspace root, never an HTTP path.
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func archiveFileAt(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}

func archiveEntryPath(path string) (archiveEntry, error) {
	return archiveEntryAt(unix.AT_FDCWD, path)
}

func archiveInodeAt(dir *os.File, name string) (archiveEntry, error) {
	return archiveEntryAt(int(dir.Fd()), name)
}

func archiveOwner() int64 { return int64(os.Geteuid()) }
