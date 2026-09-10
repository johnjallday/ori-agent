//go:build !windows

package projecttemplates

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireManifestMutationLock(library string) (func(), error) {
	if err := os.MkdirAll(library, 0o750); err != nil {
		return nil, fmt.Errorf("prepare template library lock: %w", err)
	}
	path := filepath.Join(library, ".setup-quest-manifest.lock")
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed coordinator lock under the configured template library
	if err != nil {
		return nil, fmt.Errorf("open template library lock: %w", err)
	}
	if err := unix.Flock(int(handle.Fd()), unix.LOCK_EX); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("lock template library: %w", err)
	}
	return func() {
		_ = unix.Flock(int(handle.Fd()), unix.LOCK_UN)
		_ = handle.Close()
	}, nil
}
