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

// Transport identifies how Ori talks to an MCP server process/endpoint.
const (
	TransportStdio          = "stdio"
	TransportStreamableHTTP = "streamable_http"
)

// ServerConfig contains configuration for an MCP server
type ServerConfig struct {
	Name        string            `json:"name"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	EnvRequired map[string]string `json:"env_required,omitempty"`
	Transport   string            `json:"transport"`          // "stdio" (default) or "streamable_http"
	URL         string            `json:"url,omitempty"`      // HTTPS endpoint; required for streamable_http
	AuthRef     string            `json:"auth_ref,omitempty"` // opaque vault credential reference; never a raw secret
	Enabled     bool              `json:"enabled"`
}

// NormalizedTransport returns cfg.Transport lower-cased and trimmed, defaulting
// an empty value to TransportStdio so callers never have to special-case the
// historical "omitted means stdio" configs.
func NormalizedTransport(cfg ServerConfig) string {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		return TransportStdio
	}
	return transport
}

// IsRemoteTransport reports whether cfg describes a remote Streamable HTTP
// server rather than a local stdio subprocess.
func IsRemoteTransport(cfg ServerConfig) bool {
	return NormalizedTransport(cfg) == TransportStreamableHTTP
}

// ValidateServerConfig enforces the invariants that keep stdio and remote
// server definitions from bleeding into each other: a remote definition must
// carry an HTTPS URL and no local command, while a stdio definition must
// carry a command and no remote-only fields.
func ValidateServerConfig(cfg ServerConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("server name is required")
	}

	switch NormalizedTransport(cfg) {
	case TransportStdio:
		// Command is intentionally not required here: it's validated at
		// Start() time (exec.LookPath), and some callers build partial
		// stdio configs before the command is known.
		if strings.TrimSpace(cfg.URL) != "" {
			return fmt.Errorf("stdio server %q must not specify a url", cfg.Name)
		}
	case TransportStreamableHTTP:
		if strings.TrimSpace(cfg.Command) != "" {
			return fmt.Errorf("remote server %q must not specify a command", cfg.Name)
		}
		if len(cfg.Args) > 0 {
			return fmt.Errorf("remote server %q must not specify args", cfg.Name)
		}
		if len(cfg.Env) > 0 || len(cfg.EnvRequired) > 0 {
			return fmt.Errorf("remote server %q must not specify env", cfg.Name)
		}
		if _, err := ValidateRemoteEndpoint(cfg.URL); err != nil {
			return fmt.Errorf("remote server %q: %w", cfg.Name, err)
		}
	default:
		return fmt.Errorf("server %q: unsupported transport %q", cfg.Name, cfg.Transport)
	}

	return nil
}

// NormalizedAuthRef returns cfg.AuthRef if set, otherwise a stable reference
// derived from the server name. Remote servers always have a non-empty
// reference so vault lookups never depend on a value generated after the
// fact; the field stays structurally independent from the name so a future
// credential-sharing or rename scenario doesn't require a schema change.
func NormalizedAuthRef(cfg ServerConfig) string {
	if ref := strings.TrimSpace(cfg.AuthRef); ref != "" {
		return ref
	}
	return "mcp:" + strings.TrimSpace(cfg.Name)
}

// Server manages an MCP server process and client
type Server struct {
	config       ServerConfig
	client       *sdkmcp.Client
	cmd          *exec.Cmd
	conn         *sdkmcp.ClientSession
	tools        []Tool
	instructions string                 // server-provided usage hint from the initialize handshake
	serverInfo   *sdkmcp.Implementation // server name/version reported during initialization
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.RWMutex
	status       ServerStatus
	authorizeURL string // set while status == StatusAuthRequired; browser URL to open
}

// ServerStatus represents the current status of a server
type ServerStatus string

const (
	StatusStopped      ServerStatus = "stopped"
	StatusStarting     ServerStatus = "starting"
	StatusRunning      ServerStatus = "running"
	StatusError        ServerStatus = "error"
	StatusRestarting   ServerStatus = "restarting"
	StatusAuthRequired ServerStatus = "auth_required" // remote server awaiting/needing browser authorization
)

const (
	defaultMCPHealthCheckInterval = 300 * time.Second
	mcpHealthCheckIntervalEnvVar  = "ORI_MCP_HEALTHCHECK_INTERVAL"
	defaultMCPInitTimeout         = 45 * time.Second
	mcpInitTimeoutEnvVar          = "ORI_MCP_INIT_TIMEOUT"
	defaultMCPOAuthTimeout        = 5 * time.Minute
	mcpOAuthTimeoutEnvVar         = "ORI_MCP_OAUTH_TIMEOUT"
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

// Start starts the MCP server process/connection and initializes the client.
// It dispatches to the stdio subprocess path or the remote Streamable HTTP
// path based on the configured transport; both converge on finishConnect for
// tool discovery, status, and the health-check loop.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.status == StatusRunning || s.status == StatusStarting {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.status = StatusStarting
	s.authorizeURL = ""

	// Create fresh context if the previous one was cancelled
	if s.ctx.Err() != nil {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	}
	s.mu.Unlock()

	switch NormalizedTransport(s.config) {
	case TransportStreamableHTTP:
		return s.startRemote()
	default:
		return s.startStdio()
	}
}

// startStdio launches the configured command and connects over stdio. This
// is the pre-existing behavior, unchanged, for local subprocess MCP servers.
func (s *Server) startStdio() error {
	// Validate required environment variables
	if len(s.config.EnvRequired) > 0 {
		var missing []string
		for k, desc := range s.config.EnvRequired {
			val, ok := s.config.Env[k]
			// Check if provided in config OR already in OS env
			if (!ok || strings.TrimSpace(val) == "") && os.Getenv(k) == "" {
				missing = append(missing, fmt.Sprintf("%s (%s)", k, desc))
			}
		}
		if len(missing) > 0 {
			s.setStatus(StatusError)
			return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
		}
	}

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
	s.cmd = cmd
	s.mu.Unlock()

	return s.finishConnect(client, session)
}

// startRemote connects to a remote Streamable HTTP MCP server, wiring in the
// hardened SSRF-safe transport and (when the server requires it) a
// vault-backed OAuth handler. See remote_transport.go and oauth.go.
func (s *Server) startRemote() error {
	endpoint, err := ValidateRemoteEndpoint(s.config.URL)
	if err != nil {
		s.setStatus(StatusError)
		return fmt.Errorf("invalid remote MCP endpoint: %w", err)
	}
	if strings.TrimSpace(s.config.Command) != "" {
		s.setStatus(StatusError)
		return fmt.Errorf("remote MCP servers must not specify a command")
	}

	httpClient := newRemoteHTTPClient()

	oauthHandler, err := s.buildOAuthHandler(s.ctx, httpClient)
	if err != nil {
		if isOAuthReconnectError(err) {
			s.setStatus(StatusAuthRequired)
		} else {
			s.setStatus(StatusError)
		}
		return fmt.Errorf("failed to configure oauth: %w", err)
	}

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:     endpoint.String(),
		HTTPClient:   httpClient,
		OAuthHandler: oauthHandler,
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "ori-agent",
		Version: "0.1.0",
	}, nil)

	// Remote connects may require a human to complete a browser consent
	// screen, so they get a much longer budget than the local-subprocess
	// init timeout.
	initCtx, cancel := context.WithTimeout(s.ctx, resolveMCPOAuthTimeout())
	defer cancel()

	session, err := client.Connect(initCtx, transport, nil)
	if err != nil {
		if isOAuthReconnectError(err) {
			s.setStatus(StatusAuthRequired)
		} else {
			s.setStatus(StatusError)
		}
		return fmt.Errorf("failed to initialize: %w", err)
	}

	return s.finishConnect(client, session)
}

// finishConnect completes the shared tail of Start(): capture identity,
// discover tools, flip to running, and start the health-check loop.
func (s *Server) finishConnect(client *sdkmcp.Client, session *sdkmcp.ClientSession) error {
	s.mu.Lock()
	s.client = client
	s.conn = session
	s.authorizeURL = ""
	// Capture the server's self-reported usage instructions and identity from
	// the initialize handshake so callers can surface them in the UI.
	if initRes := session.InitializeResult(); initRes != nil {
		s.instructions = initRes.Instructions
		s.serverInfo = initRes.ServerInfo
	}
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

// GetAuthorizeURL returns the pending browser authorization URL while status
// is StatusAuthRequired. Empty otherwise.
func (s *Server) GetAuthorizeURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authorizeURL
}

func (s *Server) setAuthorizeURL(url string) {
	s.mu.Lock()
	s.authorizeURL = url
	s.mu.Unlock()
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

	// Create new context; s.ctx/s.cancel are read by Start and the health
	// check loop, so the swap must happen under the lock.
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.mu.Unlock()

	return s.Start()
}

// GetTools returns the list of available tools
func (s *Server) GetTools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tools
}

// GetInstructions returns the server-provided usage instructions captured from
// the MCP initialize handshake. May be empty if the server provided none or is
// not running.
func (s *Server) GetInstructions() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.instructions
}

// GetServerInfo returns the server's reported implementation info (name/title/
// version) from initialization. Returns nil if the server is not running.
func (s *Server) GetServerInfo() *sdkmcp.Implementation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverInfo
}

// CallTool calls a tool on the MCP server
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolCallResult, error) {
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
			remote := IsRemoteTransport(s.config)
			s.mu.RUnlock()

			if status != StatusRunning {
				continue
			}

			if conn == nil {
				s.setStatus(StatusError)
				continue
			}
			// Remote servers have no local subprocess to check; fall through
			// to the ping below. Stdio servers additionally verify the
			// subprocess is still alive.
			if !remote && (cmd == nil || (cmd.ProcessState != nil && cmd.ProcessState.Exited())) {
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

// resolveMCPOAuthTimeout bounds a remote connect that may require a human to
// complete a browser consent screen, so it defaults much higher than
// resolveMCPInitTimeout (which only bounds a local subprocess handshake).
func resolveMCPOAuthTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(mcpOAuthTimeoutEnvVar))
	if raw == "" {
		return defaultMCPOAuthTimeout
	}

	if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
		return parsed
	}

	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	return defaultMCPOAuthTimeout
}
