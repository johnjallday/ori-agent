package gateway

import "context"

// ConversationStore defines the interface for persisting chat history.
type ConversationStore interface {
	// GetHistory retrieves the message history for a given session.
	GetHistory(ctx context.Context, sessionID string) ([]Message, error)

	// SaveMessage persists a message to the conversation history.
	SaveMessage(ctx context.Context, sessionID string, msg Message) error
}
