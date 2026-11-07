package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// This is a reviewed data reference, not a learning receipt or execution grant.
// The recipe reads only this same-workspace canonical inline artifact. A fresh
// execution review is still required after import or any input change.
const TaskContextLearningOriginal = "learning_draft_original"

type LearningDraftOriginal struct {
	TicketID   string `json:"ticket_id"`
	RunID      string `json:"run_id"`
	ArtifactID string `json:"artifact_id"`
	Digest     string `json:"digest"`
}

func (o LearningDraftOriginal) valid() bool {
	for _, id := range []string{o.TicketID, o.RunID, o.ArtifactID} {
		if len(id) == 0 || len(id) > 128 || id == "." || id == ".." {
			return false
		}
		for _, r := range id {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
				return false
			}
		}
	}
	digest, err := hex.DecodeString(o.Digest)
	return err == nil && len(digest) == sha256.Size && o.Digest == strings.ToLower(o.Digest)
}

func LearningOriginal(task Task) (*LearningDraftOriginal, error) {
	raw, exists := task.Context[TaskContextLearningOriginal]
	if !exists {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 1024 || bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("invalid original result reference")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var original LearningDraftOriginal
	if decoder.Decode(&original) != nil || decoder.Decode(new(any)) != io.EOF || !original.valid() {
		return nil, fmt.Errorf("invalid original result reference")
	}
	return &original, nil
}

func LearningInputDigest(input string, original *LearningDraftOriginal) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > 4096 || !utf8.ValidString(input) || strings.ContainsRune(input, 0) {
		return "", fmt.Errorf("invalid reviewed draft input")
	}
	data := []byte(input) // preserve existing plain-draft identities
	if original != nil {
		if !original.valid() {
			return "", fmt.Errorf("invalid original result reference")
		}
		var err error
		data, err = json.Marshal(struct {
			Version    int                   `json:"version"`
			Correction string                `json:"correction"`
			Original   LearningDraftOriginal `json:"original"`
		}{1, input, *original})
		if err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func LearningTaskInputDigest(task Task) (string, error) {
	original, err := LearningOriginal(task)
	if err != nil {
		return "", err
	}
	return LearningInputDigest(task.Details, original)
}
