package helpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestContext provides a comprehensive test environment for user tests
type TestContext struct {
	T              *testing.T
	ServerURL      string
	Client         *http.Client
	ServerCmd      *exec.Cmd
	TempDir        string
	Cleanup        func()
	CreatedAgents  []string
	EnabledPlugins map[string][]string // agent -> plugins
	TestStartTime  time.Time
	Verbose        bool
}

// NewTestContext creates a new test context with server running
func NewTestContext(t *testing.T) *TestContext {
	t.Helper()

	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping user test in short mode")
	}

	// Check for API key or Ollama
	useOllama := os.Getenv("USE_OLLAMA") == "true" || os.Getenv("OLLAMA_HOST") != ""
	hasAPIKey := os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("ANTHROPIC_API_KEY") != ""

	if !useOllama && !hasAPIKey {
		t.Skip("No LLM provider configured - set OPENAI_API_KEY, ANTHROPIC_API_KEY, or USE_OLLAMA=true")
	}

	// Create temp directory for test artifacts
	tempDir, err := os.MkdirTemp("", "ori-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Start server
	port := getFreePort(t)
	serverURL := fmt.Sprintf("http://localhost:%s", port)

	// Use a longer-lived context for the server process
	serverCtx, serverCancel := context.WithCancel(context.Background())
	_ = serverCancel // Will be called in cleanup

	agentStorePath := filepath.Join(tempDir, "agents.json")
	workspaceDir := filepath.Join(tempDir, "workspaces")
	pluginCacheDir := filepath.Join(tempDir, "plugin_cache")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}
	if err := os.MkdirAll(pluginCacheDir, 0o755); err != nil {
		t.Fatalf("Failed to create plugin cache dir: %v", err)
	}

	serverCmd := startTestServer(t, serverCtx, port, agentStorePath, workspaceDir, pluginCacheDir)

	// Wait for server to be ready with longer timeout
	if err := waitForServer(serverURL, 15*time.Second); err != nil {
		if serverCmd.Process != nil {
			_ = serverCmd.Process.Kill()
		}
		_ = os.RemoveAll(tempDir)
		t.Fatalf("Server failed to start: %v", err)
	}

	verbose := os.Getenv("TEST_VERBOSE") == "true"

	tc := &TestContext{
		T:              t,
		ServerURL:      serverURL,
		Client:         &http.Client{Timeout: 30 * time.Second},
		ServerCmd:      serverCmd,
		TempDir:        tempDir,
		CreatedAgents:  []string{},
		EnabledPlugins: make(map[string][]string),
		TestStartTime:  time.Now(),
		Verbose:        verbose,
	}

	// Setup cleanup function
	shouldCleanup := os.Getenv("TEST_CLEANUP") != "false"
	tc.Cleanup = func() {
		if shouldCleanup {
			tc.cleanupAll()
		} else {
			t.Logf("Skipping cleanup (TEST_CLEANUP=false), artifacts in: %s", tempDir)
		}
	}

	if verbose {
		t.Logf("✓ Test context initialized (server: %s, temp: %s)", serverURL, tempDir)
	}

	return tc
}

func getFreePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	defer func() { _ = ln.Close() }()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Unexpected listener address type: %T", ln.Addr())
	}
	return strconv.Itoa(addr.Port)
}

// CreateAgent creates a new agent and tracks it for cleanup
func (tc *TestContext) CreateAgent(name, model string) *Agent {
	tc.T.Helper()

	agentData := map[string]interface{}{
		"name":         name,
		"description":  fmt.Sprintf("Test agent created at %s", time.Now().Format(time.RFC3339)),
		"model":        model,
		"llm_provider": tc.detectProvider(),
	}

	jsonData, _ := json.Marshal(agentData)

	resp, err := tc.Client.Post(
		tc.ServerURL+"/api/agents",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		tc.T.Fatalf("Failed to create agent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		tc.T.Fatalf("Failed to create agent (status %d): %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		tc.T.Fatalf("Failed to decode agent response: %v", err)
	}

	tc.CreatedAgents = append(tc.CreatedAgents, name)

	if tc.Verbose {
		tc.T.Logf("✓ Created agent: %s (model: %s)", name, model)
	}

	return &Agent{
		Name:   name,
		Model:  model,
		ctx:    tc,
		config: result,
	}
}

// LoadPlugin loads a plugin and returns its metadata
func (tc *TestContext) LoadPlugin(pluginName string) *Plugin {
	tc.T.Helper()

	resp, err := tc.Client.Get(tc.ServerURL + "/api/plugins")
	if err != nil {
		tc.T.Fatalf("Failed to list plugins: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		tc.T.Fatalf("Failed to decode plugins: %v", err)
	}

	plugins, ok := result["plugins"].([]interface{})
	if !ok {
		tc.T.Fatalf("Invalid plugins response")
	}

	// Debug: list all available plugins
	if tc.Verbose {
		tc.T.Logf("Available plugins from API: %d plugins", len(plugins))
		for _, p := range plugins {
			plugin := p.(map[string]interface{})
			tc.T.Logf("  - %s", plugin["name"])
		}
	}

	// Find plugin
	for _, p := range plugins {
		plugin := p.(map[string]interface{})
		if plugin["name"] == pluginName {
			if tc.Verbose {
				tc.T.Logf("✓ Loaded plugin: %s", pluginName)
			}
			return &Plugin{
				Name:   pluginName,
				ctx:    tc,
				config: plugin,
			}
		}
	}

	tc.T.Fatalf("Plugin not found: %s (searched %d plugins)", pluginName, len(plugins))
	return nil
}

// EnablePlugin enables a plugin for an agent by loading it from the registry
func (tc *TestContext) EnablePlugin(agent *Agent, pluginName string) {
	tc.T.Helper()

	// Track enabled plugins
	if tc.EnabledPlugins[agent.Name] == nil {
		tc.EnabledPlugins[agent.Name] = []string{}
	}
	tc.EnabledPlugins[agent.Name] = append(tc.EnabledPlugins[agent.Name], pluginName)

	// First, get the plugin path from the registry
	plugin := tc.LoadPlugin(pluginName)
	if plugin == nil {
		tc.T.Fatalf("Plugin not found: %s", pluginName)
		return
	}

	pluginPath, ok := plugin.config["path"].(string)
	if !ok {
		tc.T.Fatalf("Plugin path not found for: %s", pluginName)
		return
	}

	// Switch to the agent first (since plugin API uses current agent)
	switchURL := fmt.Sprintf("%s/api/agents?name=%s", tc.ServerURL, agent.Name)
	switchReq, err := http.NewRequest("PUT", switchURL, nil)
	if err != nil {
		tc.T.Fatalf("Failed to create switch request: %v", err)
	}
	switchResp, err := tc.Client.Do(switchReq)
	if err != nil {
		tc.T.Fatalf("Failed to switch agent: %v", err)
	}
	defer func() { _ = switchResp.Body.Close() }()
	if switchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(switchResp.Body)
		tc.T.Fatalf("Failed to switch agent (status %d): %s", switchResp.StatusCode, body)
	}

	// Load plugin for agent via POST /api/plugins with form data
	formData := fmt.Sprintf("name=%s&path=%s", pluginName, pluginPath)

	req, err := http.NewRequest("POST", tc.ServerURL+"/api/plugins", strings.NewReader(formData))
	if err != nil {
		tc.T.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tc.Client.Do(req)
	if err != nil {
		tc.T.Fatalf("Failed to enable plugin: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		tc.T.Fatalf("Failed to enable plugin (status %d): %s", resp.StatusCode, body)
	}

	if tc.Verbose {
		tc.T.Logf("✓ Enabled plugin '%s' for agent '%s'", pluginName, agent.Name)
	}
}

// SendChat sends a chat message and returns the response
func (tc *TestContext) SendChat(agent *Agent, message string) *ChatResponse {
	tc.T.Helper()

	sessionID := tc.ensureSession(agent)
	chatData := map[string]interface{}{
		"question":   message, // API expects "question" not "message"
		"agent_name": agent.Name,
	}

	jsonData, _ := json.Marshal(chatData)
	req, err := http.NewRequest("POST", tc.ServerURL+"/api/chat", bytes.NewBuffer(jsonData))
	if err != nil {
		tc.T.Fatalf("Failed to create chat request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Session-ID", sessionID)
	}

	resp, err := tc.Client.Do(req)
	if err != nil {
		tc.T.Fatalf("Failed to send chat: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		tc.T.Fatalf("Chat request failed (status %d): %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		tc.T.Fatalf("Failed to decode chat response: %v", err)
	}

	if tc.Verbose {
		responseText := result["response"].(string)
		tc.T.Logf("✓ Chat sent: '%s' -> '%s...'", message, truncate(responseText, 50))
	}

	return &ChatResponse{
		Message:  message,
		Response: result,
		ctx:      tc,
	}
}

// AssertToolCalled verifies a tool was called in the chat response
func (tc *TestContext) AssertToolCalled(resp *ChatResponse, toolName string) {
	tc.T.Helper()

	// Try both "toolCalls" (new format) and "tool_calls" (legacy format)
	toolCalls, ok := resp.Response["toolCalls"].([]interface{})
	if !ok {
		toolCalls, ok = resp.Response["tool_calls"].([]interface{})
	}

	if !ok || len(toolCalls) == 0 {
		tc.T.Errorf("Expected tool call for '%s', but no tools were called", toolName)
		return
	}

	for _, call := range toolCalls {
		callMap := call.(map[string]interface{})
		// Check both "name" (OpenAI format) and "function" (Ollama format)
		name := callMap["name"]
		if name == nil {
			name = callMap["function"]
		}
		if name == toolName {
			if tc.Verbose {
				tc.T.Logf("✓ Tool called: %s", toolName)
			}
			return
		}
	}

	tc.T.Errorf("Expected tool '%s' to be called, but it wasn't", toolName)
}

// AssertToolCalledT verifies a tool was called using a specific *testing.T (for use in subtests).
func (tc *TestContext) AssertToolCalledT(t *testing.T, resp *ChatResponse, toolName string) {
	t.Helper()

	toolCalls, ok := resp.Response["toolCalls"].([]interface{})
	if !ok {
		toolCalls, ok = resp.Response["tool_calls"].([]interface{})
	}

	if !ok || len(toolCalls) == 0 {
		t.Errorf("Expected tool call for '%s', but no tools were called", toolName)
		return
	}

	for _, call := range toolCalls {
		callMap := call.(map[string]interface{})
		name := callMap["name"]
		if name == nil {
			name = callMap["function"]
		}
		if name == toolName {
			if tc.Verbose {
				t.Logf("✓ Tool called: %s", toolName)
			}
			return
		}
	}

	t.Errorf("Expected tool '%s' to be called, but it wasn't", toolName)
}

// AssertResponseContains checks if response contains expected text
func (tc *TestContext) AssertResponseContains(resp *ChatResponse, expected string) {
	tc.T.Helper()

	responseText, ok := resp.Response["response"].(string)
	if !ok {
		tc.T.Fatal("Response does not contain 'response' field")
	}

	if !strings.Contains(responseText, expected) {
		tc.T.Errorf("Expected response to contain '%s', but got: %s", expected, truncate(responseText, 100))
	} else if tc.Verbose {
		tc.T.Logf("✓ Response contains: '%s'", expected)
	}
}

// AssertResponseContainsT checks if response contains expected text using a specific *testing.T.
func (tc *TestContext) AssertResponseContainsT(t *testing.T, resp *ChatResponse, expected string) {
	t.Helper()

	responseText, ok := resp.Response["response"].(string)
	if !ok {
		t.Fatal("Response does not contain 'response' field")
	}

	if !strings.Contains(responseText, expected) {
		t.Errorf("Expected response to contain '%s', but got: %s", expected, truncate(responseText, 100))
	} else if tc.Verbose {
		t.Logf("✓ Response contains: '%s'", expected)
	}
}

// WaitForCondition waits for a condition to be true with timeout
func (tc *TestContext) WaitForCondition(condition func() bool, timeout time.Duration, message string) {
	tc.T.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			tc.T.Fatalf("Timeout waiting for: %s", message)
		case <-ticker.C:
			if condition() {
				if tc.Verbose {
					tc.T.Logf("✓ Condition met: %s", message)
				}
				return
			}
		}
	}
}

// Helper types

// Agent represents a test agent
type Agent struct {
	Name      string
	Model     string
	SessionID string
	ctx       *TestContext
	config    map[string]interface{}
}

// Plugin represents a test plugin
type Plugin struct {
	Name   string
	ctx    *TestContext
	config map[string]interface{}
}

// ChatResponse represents a chat response
type ChatResponse struct {
	Message  string
	Response map[string]interface{}
	ctx      *TestContext
}

// Private helpers

func (tc *TestContext) detectProvider() string {
	// Priority order: Ollama (local) > OpenAI > Claude
	if os.Getenv("USE_OLLAMA") == "true" || os.Getenv("OLLAMA_HOST") != "" {
		return "ollama"
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "openai"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "claude"
	}
	return "openai"
}

func (tc *TestContext) ensureSession(agent *Agent) string {
	tc.T.Helper()

	if agent == nil {
		return ""
	}
	if strings.TrimSpace(agent.SessionID) != "" {
		return agent.SessionID
	}

	payload := map[string]interface{}{
		"title":      "Test Session",
		"agent_name": agent.Name,
	}
	jsonData, _ := json.Marshal(payload)

	resp, err := tc.Client.Post(
		tc.ServerURL+"/api/sessions",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		tc.T.Fatalf("Failed to create session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		tc.T.Fatalf("Failed to create session (status %d): %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		tc.T.Fatalf("Failed to decode session response: %v", err)
	}

	sessionData, _ := result["session"].(map[string]interface{})
	if sessionData == nil {
		sessionData = result
	}
	sessionID, _ := sessionData["id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		tc.T.Fatalf("Session response did not include an id: %#v", result)
	}

	agent.SessionID = sessionID
	return agent.SessionID
}

func (tc *TestContext) cleanupAll() {
	// Delete created agents
	for _, agentName := range tc.CreatedAgents {
		url := fmt.Sprintf("%s/api/agents/%s", tc.ServerURL, agentName)
		req, _ := http.NewRequest("DELETE", url, nil)
		_, _ = tc.Client.Do(req)
	}

	// Stop server
	if tc.ServerCmd != nil && tc.ServerCmd.Process != nil {
		_ = tc.ServerCmd.Process.Kill()
		_ = tc.ServerCmd.Wait()
	}

	// Remove temp directory
	_ = os.RemoveAll(tc.TempDir)

	if tc.Verbose {
		duration := time.Since(tc.TestStartTime)
		tc.T.Logf("✓ Cleanup complete (duration: %s)", duration)
	}
}

func startTestServer(t *testing.T, ctx context.Context, port string, agentStorePath string, workspaceDir string, pluginCacheDir string) *exec.Cmd {
	t.Helper()

	// Get absolute paths for project root and server binary
	projectRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("Failed to get project root path: %v", err)
	}

	serverBinary := filepath.Join(projectRoot, "bin", "ori-agent")
	if _, err := os.Stat(serverBinary); os.IsNotExist(err) {
		t.Fatalf("Server binary not found at %s. Run: make build", serverBinary)
	}

	cmd := exec.CommandContext(ctx, serverBinary)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%s", port),
		fmt.Sprintf("AGENT_STORE_PATH=%s", agentStorePath),
		fmt.Sprintf("WORKSPACE_DIR=%s", workspaceDir),
		fmt.Sprintf("PLUGIN_CACHE_DIR=%s", pluginCacheDir),
		// Add test mode indicator
		"TEST_MODE=true",
		// Don't open browser during tests
		"NO_BROWSER=true",
	)

	// Set working directory to project root so server can find plugins
	cmd.Dir = projectRoot

	// Always capture output for debugging
	if os.Getenv("TEST_VERBOSE") == "true" {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		// Capture to file for debugging
		logFile, err := os.Create(filepath.Join(os.TempDir(), fmt.Sprintf("ori-test-server-%s.log", port)))
		if err == nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Give server a moment to start before returning
	time.Sleep(500 * time.Millisecond)

	return cmd
}

func waitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready within %v", timeout)
		case <-ticker.C:
			resp, err := http.Get(url + "/health")
			if err == nil && resp.StatusCode == http.StatusOK {
				_ = resp.Body.Close()
				return nil
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
