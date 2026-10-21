//go:build !unix

package secondbrain

import "os"

// openNote opens a listed note read-only inside the pinned root. The root
// already refuses a path that leaves it; the stat after opening refuses
// anything that is no longer a regular file.
func openNote(root *os.Root, rel string) (*os.File, error) {
	return root.Open(rel)
}
