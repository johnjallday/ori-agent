//go:build !windows

package resourcecatalog

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an advisory exclusive lock. flock is per open file
// description and released with the process, and the lock file is opened
// without following a symlink so a replaced directory cannot redirect it.
func lockFile(path string) (func(), error) {
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600) // #nosec G304 G703 -- a fixed lock name inside a composed catalog directory
	if err != nil {
		return nil, fmt.Errorf("open resource catalog lock: %w", err)
	}
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_EX); err != nil { // #nosec G115 -- a file descriptor fits an int
		_ = handle.Close()
		return nil, fmt.Errorf("lock resource catalog: %w", err)
	}
	return func() {
		_ = unix.Flock(int(handle.Fd()), unix.LOCK_UN) // #nosec G115 -- a file descriptor fits an int
		_ = handle.Close()
	}, nil
}
