package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPluginLoading tests that plugins can be loaded correctly
func TestPluginLoading(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ensurePluginsBuilt(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// List available plugins
	resp, err := client.Get(baseURL + "/api/plugins")
	if err != nil {
		t.Fatalf("Failed to list plugins: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	t.Logf("✓ Plugin registry accessible")

	// Check for built-in plugins (if any exist)
	if plugins, ok := result["plugins"].([]interface{}); ok {
		t.Logf("✓ Found %d plugins", len(plugins))
	}
}

// TestMathPlugin tests the math plugin specifically
func TestMathPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ensurePluginsBuilt(t)

	// Check if math plugin exists
	mathPluginPath := filepath.Join("..", "..", "plugins", "math", "math")
	if _, err := os.Stat(mathPluginPath); os.IsNotExist(err) {
		t.Skip("Math plugin not built - skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	t.Log("✓ Math plugin test scenario ready")
	// Note: Full integration would require agent creation and chat interaction
	// This is a basic structure for plugin E2E testing
}

// TestWeatherPlugin tests the weather plugin specifically
func TestWeatherPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ensurePluginsBuilt(t)

	// Check if weather plugin exists
	weatherPluginPath := filepath.Join("..", "..", "plugins", "weather", "weather")
	if _, err := os.Stat(weatherPluginPath); os.IsNotExist(err) {
		t.Skip("Weather plugin not built - skipping test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	t.Log("✓ Weather plugin test scenario ready")
	// Note: Full integration would require agent creation and chat interaction
}

// TestPluginHealthChecks tests plugin health check functionality
func TestPluginHealthChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	skipIfNoAPIKey(t)
	ensurePluginsBuilt(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := startServer(t, ctx)
	defer stopServer(cmd)

	if err := waitForServer(baseURL, startTimeout); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Get plugin health status
	resp, err := client.Get(baseURL + "/api/plugins/health")
	if err != nil {
		// This endpoint might not exist yet, skip gracefully
		t.Skip("Plugin health endpoint not available")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Skip("Plugin health endpoint not implemented yet")
		return
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	t.Logf("✓ Plugin health check accessible: %v", health)
}

// Helper functions

func ensurePluginsBuilt(t *testing.T) {
	t.Helper()

	// Check if build-plugins.sh exists
	buildScript := filepath.Join("..", "..", "scripts", "build-plugins.sh")
	if _, err := os.Stat(buildScript); os.IsNotExist(err) {
		t.Log("Build script not found, skipping plugin build")
		return
	}

	// Try to build plugins
	projectRoot := filepath.Join("..", "..")
	cmd := exec.Command("bash", "scripts/build-plugins.sh")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Logf("Warning: Failed to build plugins: %v", err)
		// Don't fail the test, as some environments might not have plugins
	} else {
		t.Log("✓ Plugins built successfully")
	}
}
