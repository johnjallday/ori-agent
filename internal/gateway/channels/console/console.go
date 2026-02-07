package console

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// ConsoleChannel implements gateway.Channel for terminal interaction
type ConsoleChannel struct {
	id     string
	logger *logger.Logger
	cancel context.CancelFunc
}

// NewConsoleChannel creates a new console channel
func NewConsoleChannel(id string, l *logger.Logger) *ConsoleChannel {
	return &ConsoleChannel{
		id:     id,
		logger: l,
	}
}

// ID returns the channel ID
func (c *ConsoleChannel) ID() string { return c.id }

// Type returns the channel type
func (c *ConsoleChannel) Type() string { return "console" }

// Start begins listening for input from os.Stdin
func (c *ConsoleChannel) Start(ctx context.Context, handler gateway.Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	scanner := bufio.NewScanner(os.Stdin)

	// We use a goroutine to not block the main server startup
	go func() {
		// Small delay to allow other startup logs to finish
		time.Sleep(1 * time.Second)
		fmt.Println("\n>>> Console Channel Active. Type your message and press Enter.")
		fmt.Print("> ")

		for {
			select {
			case <-ctx.Done():
				return
			default:
				if !scanner.Scan() {
					return
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
	}()

	return nil
}

// Stop stops the console channel
func (c *ConsoleChannel) Stop(ctx context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

// Send outputs a message to os.Stdout
func (c *ConsoleChannel) Send(ctx context.Context, msg gateway.Message) error {
	fmt.Printf("\n[ORI]: %s\n> ", msg.Content)
	return nil
}
