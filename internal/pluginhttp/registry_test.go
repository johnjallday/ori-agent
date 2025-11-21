package pluginhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// mockStore implements store.Store interface for testing
type mockStore struct{}

func (m *mockStore) ListAgents() (names []string, current string) {
	return []string{}, ""
}

func (m *mockStore) CreateAgent(name string, config *store.CreateAgentConfig) error {
	return nil
}

func (m *mockStore) SwitchAgent(name string) error {
	return nil
}

func (m *mockStore) DeleteAgent(name string) error {
	return nil
}

func (m *mockStore) GetAgent(name string) (*agent.Agent, bool) {
	return nil, false
}

func (m *mockStore) SetAgent(name string, ag *agent.Agent) error {
	return nil
}

func (m *mockStore) Save() error {
	return nil
}

// setupTestRegistry creates a registry.Manager with test data by writing to temporary files
func setupTestRegistry(t *testing.T, plugins []types.PluginRegistryEntry) *registry.Manager {
	// Create temporary directory
	tempDir := t.TempDir()

	// Save original working directory
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Change to temp directory for the test
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Restore working directory after test
	t.Cleanup(func() {
		_ = os.Chdir(originalWd)
	})

	// Write test registry to cache file (this is what the Manager will load)
	reg := types.PluginRegistry{Plugins: plugins}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("Failed to marshal test registry: %v", err)
	}

	if err := os.WriteFile("plugin_registry_cache.json", data, 0644); err != nil {
		t.Fatalf("Failed to write test cache file: %v", err)
	}

	// Create registry manager (it will load from the cache file we just created)
	return registry.NewManager()
}

func TestPluginDownloadHandler_PlatformCompatibility(t *testing.T) {
	currentPlatform := platform.DetectPlatform()

	tests := []struct {
		name               string
		pluginName         string
		plugin             types.PluginRegistryEntry
		expectedStatusCode int
		expectedError      string
		checkResponse      func(t *testing.T, resp map[string]any)
	}{
		{
			name:       "compatible plugin - should succeed validation",
			pluginName: "test-plugin",
			plugin: types.PluginRegistryEntry{
				Name:        "test-plugin",
				Platforms:   []string{currentPlatform, "linux-amd64"},
				DownloadURL: "http://example.com/plugin",
			},
			expectedStatusCode: http.StatusOK, // Will fail later due to network, but passes validation
			expectedError:      "",
		},
		{
			name:       "incompatible plugin - should block",
			pluginName: "incompatible-plugin",
			plugin: types.PluginRegistryEntry{
				Name:      "incompatible-plugin",
				Platforms: []string{"fake-platform-999"},
			},
			expectedStatusCode: http.StatusBadRequest,
			expectedError:      "platform_incompatible",
			checkResponse: func(t *testing.T, resp map[string]any) {
				if resp["error"] != "platform_incompatible" {
					t.Errorf("Expected error 'platform_incompatible', got %v", resp["error"])
				}
				if resp["user_platform"] != currentPlatform {
					t.Errorf("Expected user_platform %s, got %v", currentPlatform, resp["user_platform"])
				}
				if resp["message"] != "Plugin not available for your platform" {
					t.Errorf("Unexpected message: %v", resp["message"])
				}
			},
		},
		{
			name:       "plugin with unknown platform - should fallback to SupportedOS/Arch",
			pluginName: "fallback-plugin",
			plugin: types.PluginRegistryEntry{
				Name:          "fallback-plugin",
				Platforms:     []string{"unknown"},
				SupportedOS:   []string{platform.DetectOS()},
				SupportedArch: []string{platform.DetectArch()},
				DownloadURL:   "http://example.com/plugin",
			},
			expectedStatusCode: http.StatusOK, // Passes validation with fallback
			expectedError:      "",
		},
		{
			name:       "plugin not found",
			pluginName: "nonexistent-plugin",
			plugin: types.PluginRegistryEntry{
				Name: "different-plugin",
			},
			expectedStatusCode: http.StatusNotFound,
			expectedError:      "",
			checkResponse: func(t *testing.T, resp map[string]any) {
				if resp["message"] != "plugin not found in registry" {
					t.Errorf("Unexpected message: %v", resp["message"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test registry with plugin data
			testReg := setupTestRegistry(t, []types.PluginRegistryEntry{tt.plugin})

			handler := NewRegistryHandler(
				&mockStore{},
				testReg,
				plugindownloader.NewDownloader(filepath.Join(t.TempDir(), "cache")),
				filepath.Join(t.TempDir(), "agents"),
			)

			// Create request
			reqBody := map[string]string{"name": tt.pluginName}
			bodyBytes, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/plugins/download", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			w := httptest.NewRecorder()

			// Call handler
			handler.PluginDownloadHandler(w, req)

			// Check status code
			if w.Code != tt.expectedStatusCode {
				// For successful validation cases, we expect network errors later
				// So we accept either the expected status or InternalServerError
				if tt.expectedStatusCode == http.StatusOK && w.Code != http.StatusInternalServerError {
					t.Errorf("Expected status %d, got %d", tt.expectedStatusCode, w.Code)
				} else if tt.expectedStatusCode != http.StatusOK && w.Code != tt.expectedStatusCode {
					t.Errorf("Expected status %d, got %d", tt.expectedStatusCode, w.Code)
				}
			}

			// Parse response
			var resp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}

			// Check error field if expected
			if tt.expectedError != "" {
				if resp["error"] != tt.expectedError {
					t.Errorf("Expected error %s, got %v", tt.expectedError, resp["error"])
				}
			}

			// Run custom response checks
			if tt.checkResponse != nil {
				tt.checkResponse(t, resp)
			}
		})
	}
}

func TestPluginDownloadHandler_ErrorResponse(t *testing.T) {
	// Test that error response has correct structure
	currentPlatform := platform.DetectPlatform()

	testReg := setupTestRegistry(t, []types.PluginRegistryEntry{
		{
			Name:      "test-plugin",
			Platforms: []string{"windows-386"}, // Incompatible with most systems
		},
	})

	handler := NewRegistryHandler(
		&mockStore{},
		testReg,
		plugindownloader.NewDownloader(filepath.Join(t.TempDir(), "cache")),
		filepath.Join(t.TempDir(), "agents"),
	)

	reqBody := map[string]string{"name": "test-plugin"}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/download", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.PluginDownloadHandler(w, req)

	// Should return 400 Bad Request for incompatible platform
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check all required fields are present
	requiredFields := []string{"success", "error", "message", "user_platform", "supported_platforms"}
	for _, field := range requiredFields {
		if _, ok := resp[field]; !ok {
			t.Errorf("Response missing required field: %s", field)
		}
	}

	// Verify success is false
	if resp["success"] != false {
		t.Errorf("Expected success=false, got %v", resp["success"])
	}

	// Verify user_platform matches detected platform
	if resp["user_platform"] != currentPlatform {
		t.Errorf("Expected user_platform=%s, got %v", currentPlatform, resp["user_platform"])
	}
}
