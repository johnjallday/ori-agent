//go:build linux

package workspacecontinuity

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameNewDirectory(root *os.Root, from, to string) error {
	if !validInitializationRename(from, to) {
		return ErrUnsafe
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	// No unsafe fallback when the filesystem lacks exclusive rename support.
	return unix.Renameat2(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_NOREPLACE)
}
