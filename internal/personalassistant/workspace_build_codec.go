package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

func emptyWorkspaceBuild(binding KnowledgeBinding) WorkspaceBuildDocument {
	return WorkspaceBuildDocument{SchemaVersion: WorkspaceBuildSchemaVersion, Owner: binding.owner()}
}

// decodeWorkspaceBuild rejects unknown fields, trailing data, a foreign owner,
// and anything the validator refuses, all as ErrKnowledgeCorrupt.
func decodeWorkspaceBuild(data []byte, owner KnowledgeOwner) (WorkspaceBuildDocument, error) {
	var doc WorkspaceBuildDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.SchemaVersion != WorkspaceBuildSchemaVersion || doc.Version < 1 || doc.Owner != owner {
		return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
	}
	if err := validateWorkspaceBuild(doc); err != nil {
		return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
	}
	doc.Present = true
	return doc, nil
}

func encodeWorkspaceBuild(doc WorkspaceBuildDocument) ([]byte, error) {
	if err := validateWorkspaceBuild(doc); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > workspaceBuildMaxBytes {
		return nil, ErrKnowledgeLimit
	}
	return data, nil
}

// SetClock replaces the store's clock; tests use it to cross the expiry.
func (s *WorkspaceBuildStore) SetClock(clock func() time.Time) {
	if s != nil {
		s.clock = clock
	}
}

func (s *WorkspaceBuildStore) now() time.Time {
	if s != nil && s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

// Now is the store's clock, so callers stamp sessions with the same time the
// store expires them by.
func (s *WorkspaceBuildStore) Now() time.Time { return s.now() }

// workspaceBuildRetryLimit bounds optimistic retries when two writers race.
const workspaceBuildRetryLimit = 8

// Mutate reads the current version and applies mutate to it, retrying on a
// version conflict. mutate must be safe to run more than once.
func (s *WorkspaceBuildStore) Mutate(ctx context.Context, userID string, mutate func(*WorkspaceBuildDocument) error) (WorkspaceBuildDocument, error) {
	var last error
	for range workspaceBuildRetryLimit {
		doc, err := s.Read(ctx, userID)
		if err != nil {
			return WorkspaceBuildDocument{}, err
		}
		updated, err := s.Update(ctx, userID, doc.Version, mutate)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, ErrConflict) {
			return WorkspaceBuildDocument{}, err
		}
		last = err
	}
	return WorkspaceBuildDocument{}, last
}
