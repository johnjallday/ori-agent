//go:build !windows

package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lockWorkspaceFolder takes the advisory exclusive lock on one workspace
// folder. flock is per open file description, so two writers in one process
// serialize exactly like two processes do, and a crash releases the lock with
// the process. The lock file is created without following symlinks so a
// replaced folder cannot redirect the lock elsewhere.
func lockWorkspaceFolder(folder string) (func(), error) {
	path := filepath.Join(folder, WorkspaceFenceLockFile)
	// #nosec G304 G703 -- a fixed lock filename inside the store-resolved workspace folder, never a request path.
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0o600) // #nosec G304 G703 -- see above.
	if err != nil {
		return nil, fmt.Errorf("open workspace fence %s: %w", filepath.Base(folder), err)
	}
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_EX); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("lock workspace fence %s: %w", filepath.Base(folder), err)
	}
	return func() {
		_ = unix.Flock(int(handle.Fd()), unix.LOCK_UN)
		_ = handle.Close()
	}, nil
}
