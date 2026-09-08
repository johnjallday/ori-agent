package console

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Channel implements gateway.Channel for terminal interaction
type Channel struct {
	id            string
	logger        *logger.Logger
	mu            sync.Mutex
	cancel        context.CancelFunc
	stopRequested bool
}

// NewConsoleChannel creates a new console channel
func NewConsoleChannel(id string, l *logger.Logger) *Channel {
	return &Channel{
		id:     id,
		logger: l,
	}
}

// ID returns the channel ID
func (c *Channel) ID() string { return c.id }

// Type returns the channel type
func (c *Channel) Type() string { return "console" }

// Start begins listening for input from os.Stdin
func (c *Channel) Start(ctx context.Context, handler gateway.Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.stopRequested {
		c.mu.Unlock()
		cancel()
		return nil
	}
	c.cancel = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
		cancel()
	}()

	scanner := bufio.NewScanner(os.Stdin)

	// RegisterChannel already runs Start on its owned goroutine. Stay in this
	// method for the channel lifetime so reset admission is retained until all
	// input handling has ended instead of releasing while an inner goroutine
	// can still dispatch messages.
	time.Sleep(1 * time.Second)
	fmt.Println("\n>>> Console Channel Active. Type your message and press Enter.")
	fmt.Print("> ")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if !scanner.Scan() {
				return scanner.Err()
			}
			text := scanner.Text()
			if text == "" {
				fmt.Print("> ")
				continue
			}

			msg := gateway.Message{
				ID:      uuid.New(),
				Content: text,
				Sender: gateway.Sender{
					ID:       "local-user",
					Name:     "Console User",
					Platform: "console",
					IsBot:    false,
				},
				Timestamp: time.Now(),
			}

			if err := handler(ctx, msg); err != nil {
				c.logger.Error("failed to handle console message", logger.Fields{"error": err})
			}
			fmt.Print("> ")
		}
	}
}

// Stop stops the console channel
func (c *Channel) Stop(ctx context.Context) error {
	c.mu.Lock()
	c.stopRequested = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Send outputs a message to os.Stdout
func (c *Channel) Send(ctx context.Context, msg gateway.Message) error {
	fmt.Printf("\n[ORI]: %s\n> ", msg.Content)
	return nil
}
