package mcp

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp/transport"
)

// ServerConfig contains configuration for an MCP server
type ServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Transport string            `json:"transport"` // "stdio" or "sse"
	Enabled   bool              `json:"enabled"`
}

// Server manages an MCP server process and client
type Server struct {
	config ServerConfig
	client *Client
	tools  []Tool
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	status ServerStatus
}

// ServerStatus represents the current status of a server
type ServerStatus string

const (
	StatusStopped    ServerStatus = "stopped"
	StatusStarting   ServerStatus = "starting"
	StatusRunning    ServerStatus = "running"
	StatusError      ServerStatus = "error"
	StatusRestarting ServerStatus = "restarting"
)

// NewServer creates a new MCP server instance
func NewServer(config ServerConfig) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config: config,
		ctx:    ctx,
		cancel: cancel,
		status: StatusStopped,
	}
}

// Start starts the MCP server process and initializes the client
func (s *Server) Start() error {
	s.mu.Lock()
	if s.status == StatusRunning || s.status == StatusStarting {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.status = StatusStarting
	s.mu.Unlock()

	// Build environment variables
	env := os.Environ()
	for k, v := range s.config.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create transport
	t, err := transport.NewStdioTransport(s.ctx, s.config.Command, s.config.Args, env)
	if err != nil {
		s.setStatus(StatusError)
		return fmt.Errorf("failed to create transport: %w", err)
	}

	// Create client
	s.client = NewClient(t)

	// Initialize
	initCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	clientInfo := Implementation{
		Name:    "ori-agent",
		Version: "0.1.0",
	}

	_, err = s.client.Initialize(initCtx, clientInfo)
	if err != nil {
		s.client.Close()
		s.client = nil
		s.setStatus(StatusError)
		return fmt.Errorf("failed to initialize: %w", err)
	}

	// Discover tools
	if err := s.discoverTools(); err != nil {
		s.client.Close()
		s.client = nil
		s.setStatus(StatusError)
		return fmt.Errorf("failed to discover tools: %w", err)
	}

	s.setStatus(StatusRunning)

	// Start health check goroutine
	go s.healthCheckLoop()

	return nil
}

// Stop stops the MCP server process
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status == StatusStopped {
		return nil
	}

	s.status = StatusStopped

	if s.client != nil {
		if err := s.client.Close(); err != nil {
			return fmt.Errorf("failed to close client: %w", err)
		}
		s.client = nil
	}

	s.cancel()

	return nil
}

// Restart stops and starts the server
func (s *Server) Restart() error {
	s.mu.Lock()
	s.status = StatusRestarting
	s.mu.Unlock()

	if err := s.Stop(); err != nil {
		return fmt.Errorf("failed to stop: %w", err)
	}

	// Create new context
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	s.cancel = cancel

	return s.Start()
}

// GetTools returns the list of available tools
func (s *Server) GetTools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tools
}

// CallTool calls a tool on the MCP server
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*ToolCallResult, error) {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("server not running")
	}

	return client.CallTool(ctx, name, arguments)
}

// GetStatus returns the current server status
func (s *Server) GetStatus() ServerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// GetConfig returns the server configuration
func (s *Server) GetConfig() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// setStatus sets the server status (must be called with lock held or internally)
func (s *Server) setStatus(status ServerStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// discoverTools discovers available tools from the server
func (s *Server) discoverTools() error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	tools, err := s.client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()

	return nil
}

// healthCheckLoop periodically checks if the server is still alive
func (s *Server) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			client := s.client
			status := s.status
			s.mu.RUnlock()

			if status != StatusRunning {
				continue
			}

			if client == nil || !client.IsAlive() {
				// Server died, try to restart
				s.setStatus(StatusError)
				// Could implement auto-restart here if desired
			} else {
				// Optionally ping the server
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := client.Ping(ctx)
				cancel()

				if err != nil {
					s.setStatus(StatusError)
				}
			}
		}
	}
}
