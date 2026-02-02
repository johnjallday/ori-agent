package gateway

import (
	"time"

	"github.com/google/uuid"
)

// Sender represents the entity that sent a message
type Sender struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"` // e.g., "telegram", "console", "slack"
	IsBot    bool   `json:"is_bot"`
}

// Message is the atomic unit of communication in the gateway
type Message struct {
	ID        uuid.UUID      `json:"id"`
	Content   string         `json:"content"`
	Sender    Sender         `json:"sender"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	ReplyToID string         `json:"reply_to_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}
