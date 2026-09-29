//go:build windows

package workspace

import (
	"path/filepath"
	"strings"
	"sync"
)

// Windows has no flock. The fence there serializes writers within one process
// and still applies the version check against the record on disk, so a stale
// write from this process is refused; two Ori processes writing the same
// folder are not mutually excluded. Cross-process fencing is documented as
// unavailable on this platform rather than partially emulated.
var windowsFolderFences struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func lockWorkspaceFolder(folder string) (func(), error) {
	key := strings.ToLower(filepath.Clean(folder))
	windowsFolderFences.mu.Lock()
	if windowsFolderFences.locks == nil {
		windowsFolderFences.locks = make(map[string]*sync.Mutex)
	}
	m, ok := windowsFolderFences.locks[key]
	if !ok {
		m = &sync.Mutex{}
		windowsFolderFences.locks[key] = m
	}
	windowsFolderFences.mu.Unlock()
	m.Lock()
	return m.Unlock, nil
}
