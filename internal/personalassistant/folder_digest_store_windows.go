//go:build windows

package personalassistant

import (
	"context"
	"sync"
)

// FolderDigestStore is the Windows counterpart of the Unix store: same
// document, same lock-then-replace discipline, over the knowledge store's
// checked .ori path.
type FolderDigestStore struct {
	knowledge    *KnowledgeStore
	pathWarnings sync.Map // one capacity warning per HQ owner, never a path
}

func NewFolderDigestStore(knowledge *KnowledgeStore) *FolderDigestStore {
	return &FolderDigestStore{knowledge: knowledge}
}

func (s *FolderDigestStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

func (s *FolderDigestStore) Binding(ctx context.Context, userID string) (KnowledgeBinding, error) {
	return s.resolve(ctx, userID)
}

func (s *FolderDigestStore) Read(ctx context.Context, userID string) (FolderDigestDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	dir, err := s.knowledge.path(binding, false)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if dir == "" {
		return emptyFolderDigest(binding), nil
	}
	doc, err := readFolderDigest(dir, binding.owner())
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return FolderDigestDocument{}, err
	}
	return doc, nil
}

func (s *FolderDigestStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*FolderDigestDocument) error) (FolderDigestDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return FolderDigestDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	dir, err := s.knowledge.path(binding, true)
	if err != nil {
		return FolderDigestDocument{}, err
	}
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
	if err := s.pruneFolderPaths(dir, doc); err != nil {
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

// withFolderPaths mirrors the Unix store using the checked Windows directory.
func (s *FolderDigestStore) withFolderPaths(ctx context.Context, userID string, change func(FolderDigestDocument, *folderPathDocument) (bool, error)) error {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return err
	}
	dir, err := s.knowledge.path(binding, true)
	if err != nil {
		return err
	}
	release, err := lockSidecar(dir, folderDigestLockName, true)
	if err != nil {
		return err
	}
	defer release()
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return err
	}
	offers, err := readFolderDigest(dir, binding.owner())
	if err != nil {
		return err
	}
	paths, err := readFolderPaths(dir, binding.owner())
	if err != nil {
		return err
	}
	changed, err := change(offers, &paths)
	if err != nil {
		return err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return err
	}
	if changed {
		data, err := encodeFolderPaths(paths)
		if err != nil {
			return err
		}
		if err := s.knowledge.writeNamed(dir, folderPathsFileName, ".folder-paths-", data); err != nil {
			return err
		}
	}
	return s.knowledge.checkBinding(ctx, binding)
}

func readFolderPaths(dir string, owner KnowledgeOwner) (folderPathDocument, error) {
	data, present, err := readSidecarBytes(dir, folderPathsFileName, folderPathsMaxBytes)
	if present && len(data) == 0 {
		return folderPathDocument{}, ErrKnowledgeCorrupt
	}
	if err != nil {
		return folderPathDocument{}, err
	}
	return decodeFolderPaths(data, owner)
}

func (s *FolderDigestStore) pruneFolderPaths(dir string, offers FolderDigestDocument) error {
	paths, err := readFolderPaths(dir, offers.Owner)
	if err != nil {
		return err
	}
	if !paths.prune(offers) {
		return nil
	}
	data, err := encodeFolderPaths(paths)
	if err != nil {
		return err
	}
	return s.knowledge.writeNamed(dir, folderPathsFileName, ".folder-paths-", data)
}

func readFolderDigest(dir string, owner KnowledgeOwner) (FolderDigestDocument, error) {
	data, present, err := readSidecarBytes(dir, folderDigestFileName, folderDigestMaxBytes)
	if err != nil {
		return FolderDigestDocument{}, err
	}
	if !present {
		return FolderDigestDocument{SchemaVersion: FolderDigestSchemaVersion, Owner: owner}, nil
	}
	return decodeFolderDigest(data, owner)
}
