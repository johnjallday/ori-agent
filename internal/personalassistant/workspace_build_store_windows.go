//go:build windows

package personalassistant

import (
	"context"
	"time"
)

// WorkspaceBuildStore is the Windows counterpart of the Unix store: same
// document, same lock-then-replace discipline, over the knowledge store's
// checked .ori path.
type WorkspaceBuildStore struct {
	knowledge *KnowledgeStore
	clock     func() time.Time
}

func NewWorkspaceBuildStore(knowledge *KnowledgeStore) *WorkspaceBuildStore {
	return &WorkspaceBuildStore{knowledge: knowledge}
}

func (s *WorkspaceBuildStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

func (s *WorkspaceBuildStore) Read(ctx context.Context, userID string) (WorkspaceBuildDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	dir, err := s.knowledge.path(binding, false)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if dir == "" {
		return emptyWorkspaceBuild(binding), nil
	}
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

func (s *WorkspaceBuildStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*WorkspaceBuildDocument) error) (WorkspaceBuildDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return WorkspaceBuildDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	dir, err := s.knowledge.path(binding, true)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
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

func readWorkspaceBuild(dir string, owner KnowledgeOwner) (WorkspaceBuildDocument, error) {
	data, present, err := readSidecarBytes(dir, workspaceBuildFileName, workspaceBuildMaxBytes)
	if err != nil {
		return WorkspaceBuildDocument{}, err
	}
	if !present {
		return WorkspaceBuildDocument{SchemaVersion: WorkspaceBuildSchemaVersion, Owner: owner}, nil
	}
	return decodeWorkspaceBuild(data, owner)
}
