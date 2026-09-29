//go:build !unix

package workspacecontinuity

import "os"

func openReadFile(root *os.Root, name string) (*os.File, error) {
	// Windows confinement rejects device paths; openRegular verifies both the
	// path and opened handle are the same regular file. Unix additionally needs
	// NONBLOCK because a raced-in FIFO could otherwise block before fstat.
	return root.Open(name)
}
