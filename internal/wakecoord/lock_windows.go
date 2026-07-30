//go:build windows

package wakecoord

import "sync"

// windowsLock serializes access within one process. Ori does not program macOS
// wake events on Windows, so the shared file has a single writer there and an
// in-process mutex is the honest bound rather than a partial emulation of file
// locking.
var windowsLock sync.Mutex

func (s *Store) lock() (func(), error) {
	windowsLock.Lock()
	return windowsLock.Unlock, nil
}
