//go:build darwin

package wakeservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func acquireRootLock(ctx context.Context, path string, requireRoot bool) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- fixed/test state root.
	if err != nil {
		return nil, fmt.Errorf("open wake state lock: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure wake state lock: %w", err)
	}
	if requireRoot {
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("inspect wake state lock: %w", statErr)
		}
		if owner, ok := fileOwnerUID(info); !ok || owner != 0 {
			_ = file.Close()
			return nil, fmt.Errorf("wake state lock is not root-owned")
		}
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire wake state lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
