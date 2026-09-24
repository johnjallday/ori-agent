//go:build !windows

package personalassistant

import (
	"context"
	"fmt"

	"golang.org/x/sys/unix"
)

// withLockedDocument runs a bounded operation under the same per-HQ advisory
// lock as Update. A tool's check and canonical append must form one critical
// section with Forget's sidecar exclusion; a separate read/append can race it.
// The caller must not invoke another KnowledgeStore.Update while holding it.
func (s *KnowledgeStore) withLockedDocument(ctx context.Context, binding KnowledgeBinding, apply func(KnowledgeDocument) error) error {
	if apply == nil {
		return ErrRepairNeeded
	}
	dir, err := s.openDir(binding, false)
	if err != nil {
		return err
	}
	if dir == nil {
		return ErrRepairNeeded
	}
	defer func() { _ = dir.Close() }()
	lock, err := openKnowledgeChild(dir, knowledgeLockName, unix.O_RDWR, 0)
	if err != nil {
		return ErrRepairNeeded // missing lock cannot establish exclusion with Update
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("personal assistant: lock knowledge read: %w", err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	if err := s.checkBinding(ctx, binding); err != nil {
		return err
	}
	doc, err := readKnowledge(dir, binding.owner())
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
