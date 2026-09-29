//go:build windows

package workspacecontinuity

import "os"

func renameNewDirectory(root *os.Root, from, to string) error {
	if !validInitializationRename(from, to) {
		return ErrUnsafe
	}
	// Windows cannot replace an existing directory with MoveFileEx/Rename,
	// including an empty one. Root keeps both single-component paths confined.
	return root.Rename(from, to)
}
