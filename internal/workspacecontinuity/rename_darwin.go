//go:build darwin

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
	// Single validated path components under an already-confined directory FD.
	return unix.RenameatxNp(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_EXCL)
}
