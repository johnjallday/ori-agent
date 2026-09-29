package workspacecontinuity

import (
	"bytes"
	"encoding/json"
	"io"
)

// Record is an envelope, not a generic database insert instruction. Its Data is
// interpreted only by the named domain's typed, versioned restoration adapter.
// That adapter must validate scope, cross-references and its field allowlist.
type Record struct {
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

type Chunk struct {
	Version     int      `json:"version"`
	WorkspaceID string   `json:"workspace_id"`
	Domain      string   `json:"domain"`
	Family      string   `json:"family"`
	Records     []Record `json:"records"`
}

func (c Chunk) validate() error {
	if c.Version != Version {
		return ErrVersion
	}
	if !ValidID(c.WorkspaceID) || !knownDomain(c.Domain) || !validLabel(c.Family) {
		return ErrInvalid
	}
	if len(c.Records) == 0 || len(c.Records) > MaxRecords {
		return ErrLimit
	}
	seen := map[string]bool{}
	estimatedBytes := 512
	for _, record := range c.Records {
		if !ValidID(record.ID) || seen[record.ID] {
			return ErrInvalid
		}
		seen[record.ID] = true
		if len(record.Data) > MaxRecordBytes {
			return ErrLimit
		}
		trimmed := bytes.TrimSpace(record.Data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return ErrInvalid
		}
		var data json.RawMessage
		if err := strictJSON(record.Data, &data); err != nil {
			return err
		}
		// Prevent a caller-provided 500 * 4MiB allocation at marshal time. This
		// conservative estimate includes worst-case escaped identifier bytes.
		estimatedBytes += len(record.Data) + len(record.ID)*6 + 64
		if estimatedBytes > MaxChunkBytes {
			return ErrLimit
		}
	}
	return nil
}

func EncodeChunk(chunk Chunk) ([]byte, ChunkRef, error) {
	if err := chunk.validate(); err != nil {
		return nil, ChunkRef{}, err
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		return nil, ChunkRef{}, ErrInvalid
	}
	if len(data) > MaxChunkBytes {
		return nil, ChunkRef{}, ErrLimit
	}
	return data, ChunkRef{
		Digest: Digest(data), Bytes: int64(len(data)), Family: chunk.Family, Count: int64(len(chunk.Records)),
	}, nil
}

func DecodeChunk(reader io.Reader, ref ChunkRef, workspaceID, domain string) (Chunk, error) {
	var chunk Chunk
	if !ValidID(workspaceID) || !knownDomain(domain) || !validDigest(ref.Digest) || !validLabel(ref.Family) {
		return chunk, ErrInvalid
	}
	if ref.Bytes <= 0 || ref.Bytes > MaxChunkBytes || ref.Count <= 0 || ref.Count > MaxRecords {
		return chunk, ErrLimit
	}
	data, err := readBounded(reader, ref.Bytes)
	if err != nil {
		return chunk, err
	}
	if int64(len(data)) != ref.Bytes || Digest(data) != ref.Digest {
		return chunk, ErrDigest
	}
	if err := strictJSON(data, &chunk); err != nil {
		return chunk, err
	}
	if chunk.WorkspaceID != workspaceID || chunk.Domain != domain || chunk.Family != ref.Family || int64(len(chunk.Records)) != ref.Count {
		return chunk, ErrInvalid
	}
	return chunk, chunk.validate()
}
