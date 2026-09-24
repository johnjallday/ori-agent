package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

func emptyFolderDigest(binding KnowledgeBinding) FolderDigestDocument {
	return FolderDigestDocument{SchemaVersion: FolderDigestSchemaVersion, Owner: binding.owner()}
}

// decodeFolderDigest rejects unknown fields, trailing data, a foreign owner,
// and anything the validator refuses, all as ErrKnowledgeCorrupt.
func decodeFolderDigest(data []byte, owner KnowledgeOwner) (FolderDigestDocument, error) {
	var doc FolderDigestDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return FolderDigestDocument{}, ErrKnowledgeCorrupt
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.SchemaVersion != FolderDigestSchemaVersion || doc.Version < 1 || doc.Owner != owner {
		return FolderDigestDocument{}, ErrKnowledgeCorrupt
	}
	if err := validateFolderDigest(doc); err != nil {
		return FolderDigestDocument{}, ErrKnowledgeCorrupt
	}
	doc.Present = true
	return doc, nil
}

func encodeFolderDigest(doc FolderDigestDocument) ([]byte, error) {
	if err := validateFolderDigest(doc); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > folderDigestMaxBytes {
		return nil, ErrKnowledgeLimit
	}
	return data, nil
}

// folderDigestRetryLimit bounds optimistic retries when two writers race.
const folderDigestRetryLimit = 8

// Mutate reads the current version and applies mutate to it, retrying on a
// version conflict.
func (s *FolderDigestStore) Mutate(ctx context.Context, userID string, mutate func(*FolderDigestDocument) error) (FolderDigestDocument, error) {
	var last error
	for range folderDigestRetryLimit {
		doc, err := s.Read(ctx, userID)
		if err != nil {
			return FolderDigestDocument{}, err
		}
		updated, err := s.Update(ctx, userID, doc.Version, mutate)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, ErrConflict) {
			return FolderDigestDocument{}, err
		}
		last = err
	}
	return FolderDigestDocument{}, last
}
