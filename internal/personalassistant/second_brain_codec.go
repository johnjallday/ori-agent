package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// SecondBrainDocument is the second-brain sidecar: the secondbrain package's
// record, owned by one HQ binding. Its fields are flattened into the file
// beside the schema version, version and owner.
type SecondBrainDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Version       int64          `json:"version"`
	Owner         KnowledgeOwner `json:"owner"`
	secondbrain.Record
	Present bool `json:"-"`
}

const secondBrainRetryLimit = 8

func emptySecondBrain(binding KnowledgeBinding) SecondBrainDocument {
	return SecondBrainDocument{SchemaVersion: secondbrain.RecordSchemaVersion, Owner: binding.owner()}
}

// decodeSecondBrain rejects unknown fields, trailing data, another schema, a
// foreign owner, and anything the record's validator refuses, all as
// ErrKnowledgeCorrupt.
func decodeSecondBrain(data []byte, owner KnowledgeOwner) (SecondBrainDocument, error) {
	var doc SecondBrainDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return SecondBrainDocument{}, ErrKnowledgeCorrupt
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.SchemaVersion != secondbrain.RecordSchemaVersion || doc.Version < 1 || doc.Owner != owner {
		return SecondBrainDocument{}, ErrKnowledgeCorrupt
	}
	if err := doc.Validate(); err != nil {
		return SecondBrainDocument{}, ErrKnowledgeCorrupt
	}
	doc.Present = true
	return doc, nil
}

func encodeSecondBrain(doc SecondBrainDocument) ([]byte, error) {
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > secondbrain.RecordMaxBytes {
		return nil, ErrKnowledgeLimit
	}
	return data, nil
}

// Mutate reads the current version and applies mutate, retrying when another
// writer got there first.
func (s *SecondBrainStore) Mutate(ctx context.Context, userID string, mutate func(*SecondBrainDocument) error) (SecondBrainDocument, error) {
	var last error
	for range secondBrainRetryLimit {
		doc, err := s.Read(ctx, userID)
		if err != nil {
			return SecondBrainDocument{}, err
		}
		updated, err := s.Update(ctx, userID, doc.Version, mutate)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, ErrConflict) {
			return SecondBrainDocument{}, err
		}
		last = err
	}
	return SecondBrainDocument{}, last
}
