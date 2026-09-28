//go:build unix

package workspacecontinuity

import (
	"os"

	"golang.org/x/sys/unix"
)

func openReadFile(root *os.Root, name string) (*os.File, error) {
	// Nonblocking avoids hanging on a FIFO substituted after Lstat. NOFOLLOW
	// rejects a final-component symlink swapped in during the same window.
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
