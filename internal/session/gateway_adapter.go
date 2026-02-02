package session

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// GatewaySessionStore adapts the HybridStore to the gateway.ConversationStore interface.
type GatewaySessionStore struct {
	store HybridStore
}

// NewGatewaySessionStore creates a new adapter.
func NewGatewaySessionStore(store HybridStore) *GatewaySessionStore {
	return &GatewaySessionStore{store: store}
}

// GetHistory retrieves message history for a session.
func (s *GatewaySessionStore) GetHistory(ctx context.Context, sessionID string) ([]gateway.Message, error) {
	// Ensure session exists
	_, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		if err == ErrSessionNotFound {
			// Return empty history for new sessions
			return []gateway.Message{}, nil
		}
		return nil, err
	}

	messages, err := s.store.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	var result []gateway.Message
	for _, m := range messages {
		id, _ := uuid.Parse(m.ID)

		senderID := "user"
		senderName := "User"
		isBot := false
		if m.Role == RoleAssistant {
			senderID = "agent" // or specific agent name if available
			senderName = "Assistant"
			isBot = true
		}

		result = append(result, gateway.Message{
			ID:      id,
			Content: m.Content,
			Sender: gateway.Sender{
				ID:    senderID,
				Name:  senderName,
				IsBot: isBot,
			},
			Timestamp: m.CreatedAt,
		})
	}

	return result, nil
}

// SaveMessage persists a message.
func (s *GatewaySessionStore) SaveMessage(ctx context.Context, sessionID string, msg gateway.Message) error {
	// Ensure session exists
	sess, err := s.store.GetSession(ctx, sessionID)
	if err == ErrSessionNotFound {
		// Create new session
		sess = &Session{
			ID:        sessionID,
			Title:     fmt.Sprintf("Gateway Chat (%s)", sessionID),
			AgentName: "default", // We might want to pass this down
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.store.CreateSession(ctx, sess); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
	} else if err != nil {
		return err
	}

	// Update agent name if needed (e.g. if we switched agents)
	if msg.Sender.IsBot && msg.Sender.Name != "" && sess.AgentName != msg.Sender.Name {
		// We could update the session agent name here, but let's keep it simple for now
	}

	role := RoleUser
	if msg.Sender.IsBot {
		role = RoleAssistant
	}

	m := &Message{
		ID:        msg.ID.String(),
		SessionID: sessionID,
		Role:      role,
		Content:   msg.Content,
		CreatedAt: msg.Timestamp,
	}

	if err := s.store.AddMessage(ctx, sessionID, m); err != nil {
		return err
	}

	logger.Debug("saved gateway message to session", logger.Fields{"id": sessionID, "role": role})
	return nil
}
