//go:build unix

package secondbrain

import (
	"os"

	"golang.org/x/sys/unix"
)

// openNote opens a listed note read-only inside the pinned root. NOFOLLOW
// rejects a final-component link swapped in after the listing; NONBLOCK keeps
// a FIFO raced into its place from stalling the read before the stat refuses it.
func openNote(root *os.Root, rel string) (*os.File, error) {
	return root.OpenFile(rel, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
