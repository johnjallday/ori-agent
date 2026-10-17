//go:build linux

package backuprecovery

import "golang.org/x/sys/unix"

// Linux is a fixture platform only, with no production provider fallback.
func directoryTraversalFlags() int {
	return unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
}
