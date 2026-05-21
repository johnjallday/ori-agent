package workspacerun

import (
	"time"

	"github.com/google/uuid"
)

func NewArtifact(runID string, kind ArtifactKind, opts ...ArtifactOption) Artifact {
	artifact := Artifact{
		ID:        uuid.New().String(),
		RunID:     runID,
		Kind:      kind,
		CreatedAt: time.Now(),
	}
	for _, opt := range opts {
		opt(&artifact)
	}
	return artifact
}

type ArtifactOption func(*Artifact)

func ArtifactPath(path string) ArtifactOption {
	return func(a *Artifact) {
		a.Path = path
	}
}

func ArtifactInline(data []byte) ArtifactOption {
	return func(a *Artifact) {
		if data == nil {
			a.Inline = nil
			return
		}
		a.Inline = append([]byte(nil), data...)
	}
}

func ArtifactMetadata(metadata map[string]any) ArtifactOption {
	return func(a *Artifact) {
		a.Metadata = cloneMap(metadata)
	}
}

func CloneArtifact(a Artifact) Artifact {
	a.Inline = append([]byte(nil), a.Inline...)
	a.Metadata = cloneMap(a.Metadata)
	return a
}

func CloneArtifacts(values []Artifact) []Artifact {
	if values == nil {
		return nil
	}
	out := make([]Artifact, len(values))
	for i, value := range values {
		out[i] = CloneArtifact(value)
	}
	return out
}
