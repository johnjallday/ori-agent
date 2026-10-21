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
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.Version < 1 || doc.Owner != owner {
		return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
	}
	legacy := doc.SchemaVersion >= 1 && doc.SchemaVersion < WorkspaceBuildSchemaVersion
	if legacy {
		for _, session := range doc.Sessions {
			if session.Proposal != nil || session.EditedFields != nil || session.ContextVersion != 0 ||
				session.InferenceID != "" || session.FolderRequestID != "" || session.LinkRequestID != "" {
				return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
			}
		}
	}
	if doc.SchemaVersion == 1 || doc.SchemaVersion == 2 {
		// Keep the stable filename and decode genuine older builds as they were.
		// New authority in an old schema is malformed, not a migration hint:
		// v1 had no preparation, and neither v1 nor v2 had a folder recognition.
		for _, session := range doc.Sessions {
			if doc.SchemaVersion == 1 && (session.Preparation != nil || session.Status == WorkspaceBuildPreparing) {
				return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
			}
			if (session.Folder != nil && session.Folder.Recognition != nil) || session.Setup != nil ||
				session.HomeWorkspaceID != "" || session.SetupReceipt != "" {
				return WorkspaceBuildDocument{}, ErrKnowledgeCorrupt
			}
		}
	}
	if legacy {
		doc.SchemaVersion = WorkspaceBuildSchemaVersion
		for i := range doc.Sessions {
			doc.Sessions[i].ProtectLegacyBuildDraft()
		}
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
