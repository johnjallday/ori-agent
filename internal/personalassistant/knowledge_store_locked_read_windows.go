//go:build windows

package personalassistant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// withLockedDocument uses the same stable file lock as the Windows Update
// path. Missing or unsafe lock metadata fails closed instead of racing Forget.
func (s *KnowledgeStore) withLockedDocument(ctx context.Context, binding KnowledgeBinding, apply func(KnowledgeDocument) error) error {
	if apply == nil {
		return ErrRepairNeeded
	}
	dir, err := s.path(binding, false)
	if err != nil {
		return err
	}
	if dir == "" {
		return ErrRepairNeeded
	}
	lockPath := filepath.Join(dir, knowledgeLockName)
	if info, err := os.Lstat(lockPath); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrRepairNeeded
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0) // #nosec G304 -- fixed, checked lock path under validated HQ
	if err != nil {
		return ErrRepairNeeded
	}
	defer func() { _ = lock.Close() }()
	overlap := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlap); err != nil {
		return fmt.Errorf("personal assistant: lock knowledge read: %w", err)
	}
	defer func() { _ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, overlap) }()
	if err := s.checkBinding(ctx, binding); err != nil {
		return err
	}
	doc, err := readKnowledgeWindows(dir, binding.owner())
	if err != nil {
		return err
	}
	if !doc.Present || doc.Version == 0 {
		return ErrRepairNeeded
	}
	if err := apply(doc); err != nil {
		return err
	}
	return s.checkBinding(ctx, binding)
}
