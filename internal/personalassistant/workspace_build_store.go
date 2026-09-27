//go:build !windows

package personalassistant

import (
	"context"
	"os"
	"time"
)

// WorkspaceBuildStore reads and writes the workspace-build sidecar under the
// resolved HQ. Like the folder-digest store it borrows the knowledge store's
// directory handle and replace discipline, and keeps its own file and lock.
type WorkspaceBuildStore struct {
	knowledge *KnowledgeStore
	clock     func() time.Time
}

// NewWorkspaceBuildStore builds the store over an existing knowledge store.
func NewWorkspaceBuildStore(knowledge *KnowledgeStore) *WorkspaceBuildStore {
	return &WorkspaceBuildStore{knowledge: knowledge}
}

func (s *WorkspaceBuildStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

// Read returns the current document, or an empty one when none is written.
// A build untouched past its expiry reads as abandoned; the next write
// records that.
func (s *WorkspaceBuildStore) Read(ctx context.Context, userID string) (WorkspaceBuildDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, false)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if dir == nil {
		return emptyWorkspaceBuild(binding), nil
	}
	defer func() { _ = dir.Close() }()
	doc, err := readWorkspaceBuild(dir, binding.owner())
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	doc.ExpireStale(s.now())
	return doc, nil
}

// Update applies mutate under the sidecar lock and writes the result. The
// document version is checked against expectedVersion; ErrConflict asks the
// caller to re-read and retry.
func (s *WorkspaceBuildStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*WorkspaceBuildDocument) error) (WorkspaceBuildDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return WorkspaceBuildDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, true)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	defer func() { _ = dir.Close() }()
	release, err := lockSidecar(dir, workspaceBuildLockName, true)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	defer release()

	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	doc, err := readWorkspaceBuild(dir, binding.owner())
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if doc.Version != expectedVersion {
		return WorkspaceBuildDocument{}, ErrConflict
	}
	doc.ExpireStale(s.now())
	if err := mutate(&doc); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	doc.Version++
	doc.SchemaVersion = WorkspaceBuildSchemaVersion
	doc.Owner = binding.owner()
	doc.Present = true
	encoded, err := encodeWorkspaceBuild(doc)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if err := s.knowledge.writeNamed(dir, workspaceBuildFileName, ".workspace-build-", encoded); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	return doc, nil
}

func readWorkspaceBuild(dir *os.File, owner KnowledgeOwner) (WorkspaceBuildDocument, error) {
	data, present, err := readSidecarBytes(dir, workspaceBuildFileName, workspaceBuildMaxBytes)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if !present {
		return WorkspaceBuildDocument{SchemaVersion: WorkspaceBuildSchemaVersion, Owner: owner}, nil
	}
	return decodeWorkspaceBuild(data, owner)
}
