package gateway

import (
	"context"
	"fmt"
	"sync"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Service is the central gateway manager
type Service struct {
	channels map[string]Channel
	mu       sync.RWMutex
	router   Handler
	logger   *logger.Logger
}

// NewService creates a new gateway service
func NewService(l *logger.Logger) *Service {
	return &Service{
		channels: make(map[string]Channel),
		logger:   l,
	}
}

// SetRouter sets the function that will handle incoming messages from channels
func (s *Service) SetRouter(router Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.router = router
}

// RegisterChannel adds a new channel to the gateway
func (s *Service) RegisterChannel(ctx context.Context, c Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := c.ID()
	if _, exists := s.channels[id]; exists {
		return fmt.Errorf("channel with ID %s already registered", id)
	}

	s.channels[id] = c

	// Start the channel with the gateway's internal message handler in a separate goroutine
	go func() {
		s.logger.Info("starting channel", logger.Fields{"id": id, "type": c.Type()})
		if err := c.Start(ctx, s.handleMessage); err != nil {
			s.logger.Error("channel failed", logger.Fields{"channel": id, "error": err})
		}
	}()

	s.logger.Info("channel registered", logger.Fields{"id": id, "type": c.Type()})
	return nil
}

// UnregisterChannel removes a channel from the gateway
func (s *Service) UnregisterChannel(ctx context.Context, id string) error {
	s.mu.Lock()
	channel, exists := s.channels[id]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("channel with ID %s not found", id)
	}
	delete(s.channels, id)
	s.mu.Unlock()

	s.logger.Info("unregistering channel", logger.Fields{"id": id})
	return channel.Stop(ctx)
}

// Send sends a message to a specific channel
func (s *Service) Send(ctx context.Context, msg Message) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Use Platform as the channel ID for now as per MVP assumption
	id := msg.Sender.Platform
	channel, ok := s.channels[id]
	if !ok {
		// Try finding by searching if no direct match
		for _, c := range s.channels {
			if c.ID() == id || c.Type() == id {
				channel = c
				ok = true
				break
			}
		}
	}

	if !ok {
		return fmt.Errorf("no channel found for platform/id: %s", id)
	}

	return channel.Send(ctx, msg)
}

// handleMessage is the internal router that passes messages to the registered router
func (s *Service) handleMessage(ctx context.Context, msg Message) error {
	s.mu.RLock()
	router := s.router
	s.mu.RUnlock()

	if router == nil {
		s.logger.Warn("no router configured for gateway, message dropped", logger.Fields{"message_id": msg.ID})
		return nil
	}

	return router(ctx, msg)
}

// Shutdown stops all channels
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("shutting down gateway service")
	var lastErr error
	for id, c := range s.channels {
		if err := c.Stop(ctx); err != nil {
			s.logger.Error("failed to stop channel", logger.Fields{"id": id, "error": err})
			lastErr = err
		}
	}
	s.channels = make(map[string]Channel)
	return lastErr
}
