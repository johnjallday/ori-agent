package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const lockFileName = "state.lock"

// Lock serializes bridge operations that read and then update state. Unlike a
// temporary sentinel, the platform lock is released automatically if the
// helper process exits unexpectedly; the small file itself is safe to retain.
func (s *Store) Lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, fmt.Errorf("create state directory for lock: %w", err)
	}
	return acquireFileLock(ctx, filepath.Join(s.dir, lockFileName))
}
