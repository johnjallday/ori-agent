package gateway

import (
	"context"
)

// Handler is a function that processes an incoming message
type Handler func(ctx context.Context, msg Message) error

// Channel defines the interface for external communication platforms
type Channel interface {
	ID() string                                       // Unique identifier for the channel instance
	Type() string                                     // Type of channel (e.g., "chat", "console", "telegram")
	Start(ctx context.Context, handler Handler) error // Start listening for incoming messages
	Stop(ctx context.Context) error                   // Stop the channel
	Send(ctx context.Context, msg Message) error      // Send an outbound message through this channel
}
