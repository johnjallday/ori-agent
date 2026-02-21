package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig contains configuration for an MCP server
type ServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Transport string            `json:"transport"` // currently only "stdio" is supported at runtime
	Enabled   bool              `json:"enabled"`
}

// Server manages an MCP server process and client
type Server struct {
	config ServerConfig
	client *sdkmcp.Client
	cmd    *exec.Cmd
	conn   *sdkmcp.ClientSession
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

const (
	defaultMCPHealthCheckInterval = 300 * time.Second
	mcpHealthCheckIntervalEnvVar  = "ORI_MCP_HEALTHCHECK_INTERVAL"
	defaultMCPInitTimeout         = 45 * time.Second
	mcpInitTimeoutEnvVar          = "ORI_MCP_INIT_TIMEOUT"
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

	// Create fresh context if the previous one was cancelled
	if s.ctx.Err() != nil {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	}
	s.mu.Unlock()

	// Build environment variables
	env := os.Environ()
	for k, v := range s.config.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Check if command exists in PATH
	if _, err := exec.LookPath(s.config.Command); err != nil {
		s.setStatus(StatusError)
		return fmt.Errorf("command %q not found in PATH: %w. Please ensure the required runtime (e.g., Node.js/npm for npx commands) is installed and available in your PATH", s.config.Command, err)
	}

	if !strings.EqualFold(strings.TrimSpace(s.config.Transport), "stdio") {
		s.setStatus(StatusError)
		return fmt.Errorf("unsupported transport %q; only stdio is supported", s.config.Transport)
	}

	cmd := exec.CommandContext(s.ctx, s.config.Command, s.config.Args...)
	cmd.Env = env
	cmd.Stderr = os.Stderr

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "ori-agent",
		Version: "0.1.0",
	}, nil)

	initCtx, cancel := context.WithTimeout(s.ctx, resolveMCPInitTimeout())
	defer cancel()

	session, err := client.Connect(initCtx, &sdkmcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		s.setStatus(StatusError)
		return fmt.Errorf("failed to initialize: %w", err)
	}

	s.mu.Lock()
	s.client = client
	s.conn = session
	s.cmd = cmd
	s.mu.Unlock()

	// Discover tools
	if err := s.discoverTools(); err != nil {
		_ = session.Close()
		s.mu.Lock()
		s.client = nil
		s.conn = nil
		s.cmd = nil
		s.mu.Unlock()
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
	if s.status == StatusStopped || s.status == StatusError {
		conn := s.conn
		s.conn = nil
		s.client = nil
		s.cmd = nil
		cancel := s.cancel
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		cancel()
		return nil
	}

	s.status = StatusStopped
	conn := s.conn
	s.conn = nil
	s.client = nil
	s.cmd = nil
	cancel := s.cancel
	s.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			cancel()
			return fmt.Errorf("failed to close client session: %w", err)
		}
	}
	cancel()

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
	conn := s.conn
	s.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("server not running")
	}

	params := &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	return conn.CallTool(ctx, params)
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
	ctx, cancel := context.WithTimeout(s.ctx, resolveMCPInitTimeout())
	defer cancel()

	s.mu.RLock()
	conn := s.conn
	s.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("connection not established")
	}

	toolsResult, err := conn.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	tools := make([]Tool, 0, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		if tool == nil {
			continue
		}
		tools = append(tools, *tool)
	}

	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()

	return nil
}

// healthCheckLoop periodically checks if the server is still alive
func (s *Server) healthCheckLoop() {
	interval := resolveMCPHealthCheckInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			conn := s.conn
			cmd := s.cmd
			status := s.status
			s.mu.RUnlock()

			if status != StatusRunning {
				continue
			}

			if conn == nil {
				s.setStatus(StatusError)
				continue
			}
			if cmd == nil {
				// Server died, try to restart
				s.setStatus(StatusError)
				continue
			}
			if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				s.setStatus(StatusError)
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Ping(ctx, nil)
				cancel()

				if err != nil {
					s.setStatus(StatusError)
				}
			}
		}
	}
}

func resolveMCPHealthCheckInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(mcpHealthCheckIntervalEnvVar))
	if raw == "" {
		return defaultMCPHealthCheckInterval
	}

	// Prefer duration syntax: "300s", "5m", etc.
	if parsed, err := time.ParseDuration(raw); err == nil {
		if parsed > 0 {
			return parsed
		}
		return defaultMCPHealthCheckInterval
	}

	// Backward-compatible plain seconds, e.g. "300".
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return defaultMCPHealthCheckInterval
	}

	return defaultMCPHealthCheckInterval
}

func resolveMCPInitTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(mcpInitTimeoutEnvVar))
	if raw == "" {
		return defaultMCPInitTimeout
	}

	// Prefer duration syntax: "45s", "2m", etc.
	if parsed, err := time.ParseDuration(raw); err == nil {
		if parsed > 0 {
			return parsed
		}
		return defaultMCPInitTimeout
	}

	// Backward-compatible plain seconds, e.g. "45".
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return defaultMCPInitTimeout
	}

	return defaultMCPInitTimeout
}
