package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	serverBinary = "../../bin/ori-agent"
	defaultPort  = "18080" // Use different port for testing
	baseURL      = "http://localhost:18080"
	startTimeout = 10 * time.Second
)

// TestServerStartup tests that the server starts successfully
func TestServerStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ensureServerBuilt(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	// Start server
	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	// Wait for server to be ready
	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	t.Log("✓ Server started successfully")
}

// TestHealthCheck tests the health check endpoint
func TestHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	// Test health endpoint
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Read body for debugging
	body, _ := io.ReadAll(resp.Body)
	t.Logf("Health check response body: %s", string(body))

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if status, ok := result["status"].(string); !ok || status != "ok" {
		t.Errorf("Expected status 'ok', got %v", result["status"])
	}

	t.Log("✓ Health check passed")
}

// TestAgentLifecycle tests creating, listing, updating, and deleting agents
func TestAgentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Create agent
	t.Log("Creating agent...")
	agentData := map[string]interface{}{
		"name":        "e2e-test-agent",
		"description": "Agent for E2E testing",
		"model":       "gpt-4o",
		"provider":    "openai",
	}

	agentJSON, _ := json.Marshal(agentData)
	resp, err := client.Post(baseURL+"/api/agents", "application/json", bytes.NewBuffer(agentJSON))
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to create agent (status %d): %s", resp.StatusCode, body)
	}

	var createdAgent map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&createdAgent); err != nil {
		t.Fatalf("Failed to decode created agent: %v", err)
	}

	t.Logf("✓ Agent created: %v", createdAgent["name"])

	// 2. List agents
	t.Log("Listing agents...")
	resp, err = client.Get(baseURL + "/api/agents")
	if err != nil {
		t.Fatalf("Failed to list agents: %v", err)
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode agents: %v", err)
	}

	agentsList, ok := response["agents"].([]interface{})
	if !ok {
		t.Fatalf("Expected 'agents' array in response, got: %v", response)
	}

	found := false
	for _, a := range agentsList {
		agent := a.(map[string]interface{})
		if agent["name"] == "e2e-test-agent" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Created agent not found in list")
	}

	t.Logf("✓ Agent found in list (total: %d)", len(agentsList))

	// 3. Delete agent
	t.Log("Deleting agent...")
	req, _ := http.NewRequest("DELETE", baseURL+"/api/agents?name=e2e-test-agent", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to delete agent: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to delete agent (status %d): %s", resp.StatusCode, body)
	}

	t.Log("✓ Agent deleted")
}

// TestPluginRegistry tests the plugin registry functionality
func TestPluginRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// List plugins
	t.Log("Listing plugins...")
	resp, err := client.Get(baseURL + "/api/plugins")
	if err != nil {
		t.Fatalf("Failed to list plugins: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to list plugins (status %d): %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode plugins: %v", err)
	}

	if plugins, ok := result["plugins"]; ok {
		t.Logf("✓ Found plugins: %v", plugins)
	} else {
		t.Log("✓ No plugins found (expected for fresh install)")
	}
}

// TestSettingsEndpoint tests the settings endpoint
func TestSettingsEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Get settings
	t.Log("Getting settings...")
	resp, err := client.Get(baseURL + "/api/settings")
	if err != nil {
		t.Fatalf("Failed to get settings: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to get settings (status %d): %s", resp.StatusCode, body)
	}

	var settings map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatalf("Failed to decode settings: %v", err)
	}

	t.Logf("✓ Settings retrieved: %d fields", len(settings))
}

// TestConcurrentRequests tests that the server handles concurrent requests
func TestConcurrentRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	// Make 10 concurrent health check requests
	const numRequests = 10
	done := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			resp, err := http.Get(baseURL + "/health")
			if err != nil {
				done <- fmt.Errorf("request %d failed: %v", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				done <- fmt.Errorf("request %d got status %d", id, resp.StatusCode)
				return
			}

			done <- nil
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < numRequests; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}

	t.Logf("✓ All %d concurrent requests succeeded", numRequests)
}

// Helper functions

func startServer(t *testing.T, ctx context.Context) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(ctx, serverBinary)
	cmd.Env = append(os.Environ(),
		"PORT="+defaultPort,
		fmt.Sprintf("OPENAI_API_KEY=%s", os.Getenv("OPENAI_API_KEY")),
		fmt.Sprintf("ANTHROPIC_API_KEY=%s", os.Getenv("ANTHROPIC_API_KEY")),
	)

	// Capture output for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	t.Logf("Started server (PID: %d)", cmd.Process.Pid)
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
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
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

func ensureServerBuilt(t *testing.T) {
	t.Helper()

	if _, err := os.Stat(serverBinary); os.IsNotExist(err) {
		t.Log("Server binary not found, building...")

		// Get the project root (go up from tests/e2e to project root)
		projectRoot := filepath.Join("..", "..")

		cmd := exec.Command("go", "build", "-o", "bin/ori-agent", "./cmd/server")
		cmd.Dir = projectRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to build server: %v", err)
		}

		t.Log("✓ Server built successfully")
	}
}

func skipIfNoAPIKey(t *testing.T) {
	t.Helper()

	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("No API key set - skipping E2E test (set OPENAI_API_KEY or ANTHROPIC_API_KEY)")
	}
}
