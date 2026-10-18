//go:build darwin

package backuprecovery

import "golang.org/x/sys/unix"

// O_RDONLY directory traversal requires file-read-data and permits readdir.
// Search-only descriptors support metadata/openat without that content grant.
func directoryTraversalFlags() int {
	return unix.O_SEARCH | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
}
