//go:build !windows

package personalassistant

import (
	"context"
	"os"
)

// FolderDigestStore reads and writes the folder-digest sidecar under the
// resolved HQ. It borrows the knowledge store's directory handle, lock, and
// replace discipline, and keeps its own file and lock so a decision never
// waits on a knowledge write.
type FolderDigestStore struct {
	knowledge *KnowledgeStore
}

// NewFolderDigestStore builds the store over an existing knowledge store,
// whose resolver and folder lookup it reuses.
func NewFolderDigestStore(knowledge *KnowledgeStore) *FolderDigestStore {
	return &FolderDigestStore{knowledge: knowledge}
}

func (s *FolderDigestStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

// Binding resolves the current relationship without touching the sidecar.
func (s *FolderDigestStore) Binding(ctx context.Context, userID string) (KnowledgeBinding, error) {
	return s.resolve(ctx, userID)
}

// Read returns the current document, or an empty one when none is written.
func (s *FolderDigestStore) Read(ctx context.Context, userID string) (FolderDigestDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, false)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if dir == nil {
		return emptyFolderDigest(binding), nil
	}
	defer func() { _ = dir.Close() }()
	doc, err := readFolderDigest(dir, binding.owner())
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return FolderDigestDocument{}, err
	}
	return doc, nil
}

// Update applies mutate under the sidecar lock and writes the result. The
// document version is checked against expectedVersion; ErrConflict asks the
// caller to re-read and retry.
func (s *FolderDigestStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*FolderDigestDocument) error) (FolderDigestDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return FolderDigestDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, true)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	defer func() { _ = dir.Close() }()
	release, err := lockSidecar(dir, folderDigestLockName, true)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	defer release()

	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return FolderDigestDocument{}, err
	}
	doc, err := readFolderDigest(dir, binding.owner())
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if doc.Version != expectedVersion {
		return FolderDigestDocument{}, ErrConflict
	}
	if err := mutate(&doc); err != nil {
		return FolderDigestDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return FolderDigestDocument{}, err
	}
	doc.Version++
	doc.SchemaVersion = FolderDigestSchemaVersion
	doc.Owner = binding.owner()
	doc.Present = true
	encoded, err := encodeFolderDigest(doc)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if err := s.knowledge.writeNamed(dir, folderDigestFileName, ".folder-digest-", encoded); err != nil {
		return FolderDigestDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return FolderDigestDocument{}, err
	}
	return doc, nil
}

func readFolderDigest(dir *os.File, owner KnowledgeOwner) (FolderDigestDocument, error) {
	data, present, err := readSidecarBytes(dir, folderDigestFileName, folderDigestMaxBytes)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if !present {
		return FolderDigestDocument{SchemaVersion: FolderDigestSchemaVersion, Owner: owner}, nil
	}
	return decodeFolderDigest(data, owner)
}
