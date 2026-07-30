//go:build !windows

package wakecoord

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lock serializes access to the shared document across processes.
//
// The lock is advisory and process-wide, which is exactly the guarantee needed
// here: the writers are separate Ori processes on one machine, and the thing
// being protected is a single small file they all rewrite.
func (s *Store) lock() (func(), error) {
	path := filepath.Join(s.Dir, ".wake.lock")
	// #nosec G304 -- a fixed lock filename under this store's own directory.
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the wake coordinator lock: %w", err)
	}
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_EX); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("lock the wake coordinator: %w", err)
	}
	return func() {
		_ = unix.Flock(int(handle.Fd()), unix.LOCK_UN)
		_ = handle.Close()
	}, nil
}
