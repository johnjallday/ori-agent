//go:build !windows

package personalassistant

import (
	"context"
	"os"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// SecondBrainStore reads and writes the second-brain record under the resolved
// HQ (FR15). Like the folder-digest store it borrows the knowledge store's
// directory handle and replace discipline, and keeps its own file and lock.
type SecondBrainStore struct {
	knowledge *KnowledgeStore
}

// NewSecondBrainStore builds the store over an existing knowledge store.
func NewSecondBrainStore(knowledge *KnowledgeStore) *SecondBrainStore {
	return &SecondBrainStore{knowledge: knowledge}
}

func (s *SecondBrainStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.knowledge == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.knowledge.resolve(ctx, userID)
}

// Binding resolves the current relationship without touching the sidecar.
func (s *SecondBrainStore) Binding(ctx context.Context, userID string) (KnowledgeBinding, error) {
	return s.resolve(ctx, userID)
}

// Read returns the current document, or an empty one when none is written.
func (s *SecondBrainStore) Read(ctx context.Context, userID string) (SecondBrainDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, false)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if dir == nil {
		return emptySecondBrain(binding), nil
	}
	defer func() { _ = dir.Close() }()
	doc, err := readSecondBrain(dir, binding.owner())
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if err := s.knowledge.checkBinding(ctx, binding); err != nil {
		return SecondBrainDocument{}, err
	}
	return doc, nil
}

// Update applies mutate under the sidecar lock and writes the result. The
// document version is checked against expectedVersion; ErrConflict asks the
// caller to re-read and retry.
func (s *SecondBrainStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*SecondBrainDocument) error) (SecondBrainDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return SecondBrainDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	dir, err := s.knowledge.openDir(binding, true)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	defer func() { _ = dir.Close() }()
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

func readSecondBrain(dir *os.File, owner KnowledgeOwner) (SecondBrainDocument, error) {
	data, present, err := readSidecarBytes(dir, secondbrain.RecordFileName, secondbrain.RecordMaxBytes)
	if err != nil {
		return SecondBrainDocument{}, err
	}
	if !present {
		return SecondBrainDocument{SchemaVersion: secondbrain.RecordSchemaVersion, Owner: owner}, nil
	}
	return decodeSecondBrain(data, owner)
}
