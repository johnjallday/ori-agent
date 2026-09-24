package personalassistanthttp

import (
	"context"
	"time"
)

// KnowledgeSourceCard carries only availability, not a raw app inventory,
// filename, folder path, action text or provider-managed content.
type KnowledgeSourceCard struct {
	Status     string     `json:"status"` // available | healthy_empty | not_configured | unavailable
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type KnowledgeSources struct {
	SavedApps   KnowledgeSourceCard `json:"saved_apps"`
	FileJanitor KnowledgeSourceCard `json:"file_janitor"`
}

type KnowledgeSourceReader interface {
	ReadSources(ctx context.Context, userID string) KnowledgeSources
}

func (h *Handler) SetKnowledgeSources(reader KnowledgeSourceReader) {
	if h != nil {
		h.knowledgeSources = reader
	}
}
