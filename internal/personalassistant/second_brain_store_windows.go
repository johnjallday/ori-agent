//go:build windows

package personalassistant

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// SecondBrainStore is the Windows counterpart of the Unix store: same record,
// same lock-then-replace discipline, over the knowledge store's checked .ori
// path.
type SecondBrainStore struct {
	knowledge *KnowledgeStore
}

func NewSecondBrainStore(knowledge *KnowledgeStore) *SecondBrainStore {
	return &SecondBrainStore{knowledge: knowledge}
}

func (s *SecondBrainStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

func (s *SecondBrainStore) Binding(ctx context.Context, userID string) (KnowledgeBinding, error) {
	return s.resolve(ctx, userID)
}

func (s *SecondBrainStore) Read(ctx context.Context, userID string) (SecondBrainDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	dir, err := s.knowledge.path(binding, false)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if dir == "" {
		return emptySecondBrain(binding), nil
	}
	doc, err := readSecondBrain(dir, binding.owner())
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return SecondBrainDocument{}, err
	}
	return doc, nil
}

func (s *SecondBrainStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*SecondBrainDocument) error) (SecondBrainDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return SecondBrainDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	dir, err := s.knowledge.path(binding, true)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	release, err := lockSidecar(dir, secondbrain.RecordLockName, true)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	defer release()

	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return SecondBrainDocument{}, err
	}
	doc, err := readSecondBrain(dir, binding.owner())
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if doc.Version != expectedVersion {
		return SecondBrainDocument{}, ErrConflict
	}
	if err := mutate(&doc); err != nil {
		return SecondBrainDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return SecondBrainDocument{}, err
	}
	doc.Version++
	doc.SchemaVersion = secondbrain.RecordSchemaVersion
	doc.Owner = binding.owner()
	doc.Present = true
	encoded, err := encodeSecondBrain(doc)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if err := s.knowledge.writeNamed(dir, secondbrain.RecordFileName, ".second-brain-", encoded); err != nil {
		return SecondBrainDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return SecondBrainDocument{}, err
	}
	return doc, nil
}

func readSecondBrain(dir string, owner KnowledgeOwner) (SecondBrainDocument, error) {
	data, present, err := readSidecarBytes(dir, secondbrain.RecordFileName, secondbrain.RecordMaxBytes)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if !present {
		return SecondBrainDocument{SchemaVersion: secondbrain.RecordSchemaVersion, Owner: owner}, nil
	}
	return decodeSecondBrain(data, owner)
}
