//go:build windows

package state

import (
	"context"
	"sync"
)

// The bridge scheduler is intentionally unsupported on Windows, but the
// helper still builds there. A process-local lock keeps command semantics safe
// for the supported one-process use case without introducing another backend.
var windowsLock sync.Mutex

func acquireFileLock(ctx context.Context, _ string) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	windowsLock.Lock()
	return windowsLock.Unlock, nil
}
