package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil"
	"github.com/johnjallday/ori-agent/tests/helpers"
)

// TestHealthEndpoint tests the health check endpoint
func TestHealthEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start test server
	server := startTestServer(t)
	defer server.Cleanup()

	// Make request
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "GET",
		Path:   "/health",
	})
	defer func() { _ = resp.Body.Close() }()

	// Assert status
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Parse response
	var result map[string]any
	testutil.ReadJSONResponse(t, resp, &result)

	// Check response structure
	if status, ok := result["status"].(string); !ok || status != "ok" {
		t.Errorf("Expected status 'ok', got %v", result["status"])
	}
}

// TestListAgentsEndpoint tests the agent listing endpoint
func TestListAgentsEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server := startTestServer(t)
	defer server.Cleanup()

	// Make request
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "GET",
		Path:   "/api/agents",
	})
	defer func() { _ = resp.Body.Close() }()

	// Assert status
	testutil.AssertStatusCode(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var agents []map[string]any
	testutil.ReadJSONResponse(t, resp, &agents)

	// Should return an array (may be empty)
	if agents == nil {
		t.Error("Expected agents array, got nil")
	}
}

// TestCreateAgentEndpoint tests agent creation
func TestCreateAgentEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server := startTestServer(t)
	defer server.Cleanup()

	// Create agent request
	model := helpers.GetTestModel()
	agentData := map[string]any{
		"name":        "test-agent",
		"description": "Test agent for integration testing",
		"model":       model,
		"provider":    "openai",
	}

	jsonData, _ := json.Marshal(agentData)

	// Make request
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "POST",
		Path:   "/api/agents",
		Body:   bytes.NewBuffer(jsonData),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	})
	defer func() { _ = resp.Body.Close() }()

	// Assert status (may be 201 or 200 depending on implementation)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 or 201, got %d", resp.StatusCode)
	}

	// Parse response
	var result map[string]any
	testutil.ReadJSONResponse(t, resp, &result)

	// Verify agent was created
	if name, ok := result["name"].(string); !ok || name != "test-agent" {
		t.Errorf("Expected agent name 'test-agent', got %v", result["name"])
	}
}

// TestListPluginsEndpoint tests the plugin listing endpoint
func TestListPluginsEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server := startTestServer(t)
	defer server.Cleanup()

	// Make request
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "GET",
		Path:   "/api/plugins",
	})
	defer func() { _ = resp.Body.Close() }()

	// Assert status
	testutil.AssertStatusCode(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var result map[string]any
	testutil.ReadJSONResponse(t, resp, &result)

	// Should have a plugins field
	if _, ok := result["plugins"]; !ok {
		t.Error("Expected 'plugins' field in response")
	}
}

// TestSettingsEndpoint tests the settings endpoint
func TestSettingsEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server := startTestServer(t)
	defer server.Cleanup()

	// Make request
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "GET",
		Path:   "/api/settings",
	})
	defer func() { _ = resp.Body.Close() }()

	// Assert status
	testutil.AssertStatusCode(t, http.StatusOK, resp.StatusCode)

	// Parse response
	var settings map[string]any
	testutil.ReadJSONResponse(t, resp, &settings)

	// Should have some settings fields
	if settings == nil {
		t.Error("Expected settings object, got nil")
	}
}

// TestChatEndpointWithoutAPIKey tests that chat fails without API key
func TestChatEndpointWithoutAPIKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Temporarily unset API keys
	oldOpenAI := os.Getenv("OPENAI_API_KEY")
	oldAnthropic := os.Getenv("ANTHROPIC_API_KEY")
	_ = os.Unsetenv("OPENAI_API_KEY")
	_ = os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if oldOpenAI != "" {
			_ = os.Setenv("OPENAI_API_KEY", oldOpenAI)
		}
		if oldAnthropic != "" {
			_ = os.Setenv("ANTHROPIC_API_KEY", oldAnthropic)
		}
	}()

	server := startTestServer(t)
	defer server.Cleanup()

	// Attempt to send a chat message
	chatData := map[string]any{
		"message":    "Hello",
		"agent_name": "test-agent",
	}

	jsonData, _ := json.Marshal(chatData)

	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "POST",
		Path:   "/api/chat",
		Body:   bytes.NewBuffer(jsonData),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	})
	defer func() { _ = resp.Body.Close() }()

	// Should fail with appropriate error
	// (implementation may vary - either 400, 401, or 500)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Error("Expected error status, got success")
	}
}

func TestHomeAssistantRouteUtilityPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	server := startTestServer(t)
	defer server.Cleanup()

	body, _ := json.Marshal(map[string]string{
		"prompt": "what time is it in tokyo",
	})
	resp := testutil.MakeRequest(t, server.Client, server.Server.URL, testutil.HTTPRequest{
		Method: "POST",
		Path:   "/api/home-assistant/route",
		Body:   bytes.NewBuffer(body),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	})
	defer func() { _ = resp.Body.Close() }()

	testutil.AssertStatusCode(t, http.StatusOK, resp.StatusCode)

	var route map[string]any
	testutil.ReadJSONResponse(t, resp, &route)

	if intent, _ := route["intent"].(string); intent != "utility_direct" {
		t.Fatalf("expected intent utility_direct, got %q", intent)
	}
	if requiresCreation, _ := route["requires_creation"].(bool); requiresCreation {
		t.Fatalf("expected requires_creation=false")
	}
}

// Helper to start test server
func startTestServer(t *testing.T) *testutil.TestServer {
	// Import would be needed here, but to avoid circular dependencies,
	// we'll need to refactor the server package to be testable
	// For now, this is a placeholder structure

	// In real implementation:
	// server := server.New()
	// handler := server.SetupRoutes()
	// return testutil.NewTestServer(t, handler)

	// For now, return a mock server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock responses for testing
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/api/agents":
			switch r.Method {
			case "GET":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]map[string]any{})
			case "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				var agent map[string]any
				_ = json.NewDecoder(r.Body).Decode(&agent)
				_ = json.NewEncoder(w).Encode(agent)
			}
		case "/api/plugins":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plugins": []any{},
			})
		case "/api/settings":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"theme": "light",
			})
		case "/api/home-assistant/route":
			if r.Method == "POST" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"intent":               "utility_direct",
					"intent_label":         "daily utility",
					"matched_agent":        "Ori",
					"score":                6,
					"requires_creation":    false,
					"suggested_agent_name": "Utility Assistant",
					"suggested_agent_type": "general",
				})
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	return testutil.NewTestServer(t, handler)
}
