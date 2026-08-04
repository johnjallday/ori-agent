package agenthttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// TestServer wraps test infrastructure
type TestServer struct {
	store            store.Store
	handler          *Handler
	dashboardHandler *DashboardHandler
	evolutionHandler *evolutionhttp.Handler
	cleanup          func()
}

// setupTestServer creates a test server with a temporary store
func setupTestServer(t *testing.T) *TestServer {
	t.Helper()

	// Create temporary directory for test data
	tmpDir, err := os.MkdirTemp("", "agent-integration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create settings file path
	settingsPath := tmpDir + "/settings.json"

	// Create default settings
	defaultSettings := types.Settings{
		Model:       "gpt-4o",
		Temperature: 1.0,
	}

	// Create store
	st, err := store.NewFileStore(settingsPath, defaultSettings)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create handlers
	handler := New(st)
	dashboardHandler := NewDashboardHandler(st)
	onboardingMgr := onboarding.NewManager(tmpDir + "/app_state.json")
	evolutionSvc := evolution.NewService(st, onboardingMgr, nil)
	evolutionHandler := evolutionhttp.NewHandler(st, onboardingMgr, evolutionSvc)

	// Create cleanup function
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return &TestServer{
		store:            st,
		handler:          handler,
		dashboardHandler: dashboardHandler,
		evolutionHandler: evolutionHandler,
		cleanup:          cleanup,
	}
}

// Helper: Make HTTP request and return response
func (ts *TestServer) doRequest(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody []byte
	var err error
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("Failed to marshal request body: %v", err)
		}
	}

	// Agent names may legally contain spaces (validateAgentName allows them, so
	// "Workspace Manager" and any user agent called "My Agent" are valid), and
	// these test URLs are hand-assembled with the name interpolated raw. A raw
	// space is not legal in a request line, so escape both halves here rather
	// than at every call site.
	reqTarget := path
	if base, rawQuery, found := strings.Cut(reqTarget, "?"); found {
		values, err := url.ParseQuery(rawQuery)
		if err != nil {
			t.Fatalf("invalid query in %q: %v", path, err)
		}
		reqTarget = (&url.URL{Path: base}).EscapedPath() + "?" + values.Encode()
	} else {
		reqTarget = (&url.URL{Path: reqTarget}).EscapedPath()
	}

	req := httptest.NewRequest(method, reqTarget, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()

	// Route to correct handler based on path (strip query params for routing)
	pathWithoutQuery := path
	if idx := strings.Index(path, "?"); idx != -1 {
		pathWithoutQuery = path[:idx]
	}

	if pathWithoutQuery == "/api/agents/dashboard/list" || pathWithoutQuery == "/api/agents/dashboard/stats" {
		ts.dashboardHandler.ListAgentsWithStats(rr, req)
	} else if strings.HasSuffix(pathWithoutQuery, "/evolution/path") && method == http.MethodPost {
		ts.evolutionHandler.SetAgentPath(rr, req)
	} else if strings.HasSuffix(pathWithoutQuery, "/evolution") && method == http.MethodGet {
		ts.evolutionHandler.GetAgentEvolution(rr, req)
	} else if strings.HasSuffix(pathWithoutQuery, "/feed") && method == http.MethodPost {
		ts.evolutionHandler.FeedAgent(rr, req)
	} else if pathWithoutQuery == "/api/evolution/assistant" {
		ts.evolutionHandler.GetAssistantProgress(rr, req)
	} else if pathWithoutQuery == "/api/evolution/suggestions" {
		ts.evolutionHandler.GetSuggestions(rr, req)
	} else if len(pathWithoutQuery) > 12 && pathWithoutQuery[:12] == "/api/agents/" && len(pathWithoutQuery) > 7 && pathWithoutQuery[len(pathWithoutQuery)-7:] == "/detail" {
		ts.dashboardHandler.GetAgentDetail(rr, req)
	} else {
		ts.handler.ServeHTTP(rr, req)
	}

	return rr
}

// Helper: Decode JSON response
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
}

// Helper: Assert response status
func assertStatus(t *testing.T, rr *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if rr.Code != expected {
		t.Errorf("Expected status %d, got %d. Body: %s", expected, rr.Code, rr.Body.String())
	}
}

// Helper: Create test agent with metadata
func createTestAgent(t *testing.T, ts *TestServer, name, agentType string) {
	t.Helper()

	reqBody := map[string]any{
		"name":         name,
		"type":         agentType,
		"role":         "general",
		"llm_provider": "openai",
		"model":        "gpt-4o",
		"temperature":  1.0,
		"description":  "Test agent for integration testing",
		"tags":         []string{"test", "integration"},
		"avatar_color": "#007bff",
	}

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
	assertStatus(t, rr, http.StatusOK)
}

func TestCreateAgent_WithAllowWebSearchSetting(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	reqBody := map[string]any{
		"name":             "restricted-web-agent",
		"type":             "tool-calling",
		"model":            "gpt-4o-mini",
		"allow_web_search": false,
	}

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
	assertStatus(t, rr, http.StatusOK)

	rr = ts.doRequest(t, http.MethodGet, "/api/agents?name=restricted-web-agent", nil)
	assertStatus(t, rr, http.StatusOK)
	var detail map[string]any
	decodeResponse(t, rr, &detail)
	if got, ok := detail["allow_web_search"].(bool); !ok || got {
		t.Fatalf("expected allow_web_search=false in agent detail response, got %#v", detail["allow_web_search"])
	}

	rr = ts.doRequest(t, http.MethodGet, "/api/agents/restricted-web-agent/detail", nil)
	assertStatus(t, rr, http.StatusOK)
	var dashboardDetail map[string]any
	decodeResponse(t, rr, &dashboardDetail)
	if got, ok := dashboardDetail["allow_web_search"].(bool); !ok || got {
		t.Fatalf("expected allow_web_search=false in dashboard detail response, got %#v", dashboardDetail["allow_web_search"])
	}
}

func TestCreateAgent_WithCodexReasoningEffort(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	reqBody := map[string]any{
		"name":             "codex-reasoning-agent",
		"type":             "research",
		"llm_provider":     "codex",
		"model":            "gpt-5.4",
		"reasoning_effort": "xhigh",
	}

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
	assertStatus(t, rr, http.StatusOK)

	ag, ok := ts.store.GetAgent("codex-reasoning-agent")
	if !ok || ag == nil {
		t.Fatal("expected agent to be stored")
	}
	if ag.Settings.ReasoningEffort != "xhigh" {
		t.Fatalf("expected store reasoning_effort xhigh, got %q", ag.Settings.ReasoningEffort)
	}

	rr = ts.doRequest(t, http.MethodGet, "/api/agents/codex-reasoning-agent/detail", nil)
	assertStatus(t, rr, http.StatusOK)

	var detail map[string]any
	decodeResponse(t, rr, &detail)
	if got := detail["reasoning_effort"]; got != "xhigh" {
		t.Fatalf("expected reasoning_effort xhigh in detail response, got %#v", got)
	}
}

func TestCreateAgent_WithInvalidReasoningEffort(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	reqBody := map[string]any{
		"name":             "invalid-reasoning-agent",
		"type":             "research",
		"llm_provider":     "codex",
		"model":            "gpt-5.4",
		"reasoning_effort": "ultra",
	}

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
	assertStatus(t, rr, http.StatusBadRequest)
}

// Test 7.1: Complete agent lifecycle (create → list → detail → update → delete)
func TestCompleteAgentLifecycle(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Step 1: Create agent with metadata
	t.Run("CreateAgent", func(t *testing.T) {
		createTestAgent(t, ts, "lifecycle-agent", "tool-calling")

		// Verify agent was created by getting it from store
		ag, ok := ts.store.GetAgent("lifecycle-agent")
		if !ok {
			t.Fatal("Agent not found in store after creation")
		}

		// Verify basic properties
		if ag.Type != "tool-calling" {
			t.Errorf("Expected type 'tool-calling', got %v", ag.Type)
		}

		// Verify statistics are initialized
		if ag.Statistics == nil {
			t.Error("Statistics not initialized")
		} else if ag.Statistics.MessageCount != 0 {
			t.Errorf("Expected message_count 0, got %d", ag.Statistics.MessageCount)
		}
	})

	// Step 2: Agent appears in list
	t.Run("ListAgents", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list", nil)
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Agents []map[string]any `json:"agents"`
		}
		decodeResponse(t, rr, &response)

		// Should have at least one agent
		if len(response.Agents) == 0 {
			t.Fatal("Expected at least one agent in list")
		}

		// Find our agent
		found := false
		for _, ag := range response.Agents {
			if ag["name"] == "lifecycle-agent" {
				found = true
				// Verify has statistics
				if ag["statistics"] == nil {
					t.Error("Expected statistics in list response")
				}
			}
		}

		if !found {
			t.Error("Created agent not found in list")
		}
	})

	// Step 3: Get agent detail
	t.Run("GetAgentDetail", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/lifecycle-agent/detail", nil)
		assertStatus(t, rr, http.StatusOK)

		var agent map[string]any
		decodeResponse(t, rr, &agent)

		// Verify all expected fields present
		if agent["name"] != "lifecycle-agent" {
			t.Errorf("Expected name 'lifecycle-agent', got %v", agent["name"])
		}

		if agent["statistics"] == nil {
			t.Error("Expected statistics in detail response")
		}

		metadata, ok := agent["metadata"].(map[string]any)
		if !ok {
			t.Fatal("Expected metadata in detail response")
		}

		if metadata["description"] != "Test agent for integration testing" {
			t.Errorf("Expected description, got %v", metadata["description"])
		}
	})

	// Step 4: Update agent metadata
	t.Run("UpdateAgentMetadata", func(t *testing.T) {
		updateBody := map[string]any{
			"description": "Updated description",
			"tags":        []string{"test", "updated"},
			"routing_profile": map[string]any{
				"match_phrases":    []string{"open my latest reaper project"},
				"example_requests": []string{"render stems from yesterday's session"},
				"domains":          []string{"reaper", "audio"},
				"external_systems": []string{"reaper"},
				"side_effects":     "local_app",
			},
		}

		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=lifecycle-agent", updateBody)
		assertStatus(t, rr, http.StatusOK)

		// Verify update
		rr = ts.doRequest(t, http.MethodGet, "/api/agents/lifecycle-agent/detail", nil)
		var agent map[string]any
		decodeResponse(t, rr, &agent)

		metadata := agent["metadata"].(map[string]any)
		if metadata["description"] != "Updated description" {
			t.Errorf("Description not updated, got %v", metadata["description"])
		}
		routingProfile, ok := metadata["routing_profile"].(map[string]any)
		if !ok {
			t.Fatal("Expected routing_profile in metadata response")
		}
		if routingProfile["side_effects"] != "local_app" {
			t.Errorf("Expected side_effects local_app, got %v", routingProfile["side_effects"])
		}
	})

	// Step 5: Simulate statistics update
	t.Run("UpdateStatistics", func(t *testing.T) {
		// Load agent and update statistics
		ag, ok := ts.store.GetAgent("lifecycle-agent")
		if !ok {
			t.Fatal("Failed to load agent")
		}

		ag.InitializeStatistics()
		ag.Statistics.RecordMessage(1000, 0.03)

		err := ts.store.SetAgent("lifecycle-agent", ag)
		if err != nil {
			t.Fatalf("Failed to save agent: %v", err)
		}

		// Verify statistics updated
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/lifecycle-agent/detail", nil)
		var agent map[string]any
		decodeResponse(t, rr, &agent)

		stats := agent["statistics"].(map[string]any)
		if stats["message_count"].(float64) != 1 {
			t.Errorf("Expected message_count 1, got %v", stats["message_count"])
		}

		if stats["token_usage"].(float64) != 1000 {
			t.Errorf("Expected token_usage 1000, got %v", stats["token_usage"])
		}
	})

	// Step 6: Delete agent
	t.Run("DeleteAgent", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodDelete, "/api/agents?name=lifecycle-agent", nil)
		assertStatus(t, rr, http.StatusOK)

		// Verify agent no longer in list
		rr = ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list", nil)
		var response struct {
			Agents []map[string]any `json:"agents"`
		}
		decodeResponse(t, rr, &response)

		for _, ag := range response.Agents {
			if ag["name"] == "lifecycle-agent" {
				t.Error("Deleted agent still in list")
			}
		}
	})
}

// Test 7.2: Dashboard list filtering and sorting
func TestDashboardListFiltering(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create multiple test agents
	createTestAgent(t, ts, "agent-1", "tool-calling")
	createTestAgent(t, ts, "agent-2", "conversational")
	createTestAgent(t, ts, "agent-3", "tool-calling")

	t.Run("ListAllAgents", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list", nil)
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Agents []map[string]any `json:"agents"`
		}
		decodeResponse(t, rr, &response)

		// Should have at least our 3 test agents (may have default agent too)
		if len(response.Agents) < 3 {
			t.Errorf("Expected at least 3 agents, got %d", len(response.Agents))
		}

		// Count our test agents
		testAgents := 0
		for _, ag := range response.Agents {
			name := ag["name"].(string)
			if name == "agent-1" || name == "agent-2" || name == "agent-3" {
				testAgents++
			}
		}

		if testAgents != 3 {
			t.Errorf("Expected 3 test agents, found %d", testAgents)
		}
	})

	t.Run("FilterByStatus", func(t *testing.T) {
		// Set one agent to active status
		ag, ok := ts.store.GetAgent("agent-1")
		if !ok {
			t.Fatal("Failed to load agent-1")
		}
		ag.Status = types.AgentStatusActive
		err := ts.store.SetAgent("agent-1", ag)
		if err != nil {
			t.Fatalf("Failed to save agent: %v", err)
		}

		// Force save to disk
		_ = ts.store.Save()

		rr := ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list?status=active", nil)
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Agents []map[string]any `json:"agents"`
		}
		decodeResponse(t, rr, &response)

		// Should only return active agents
		foundAgent1 := false
		for _, agent := range response.Agents {
			if agent["status"] != "active" {
				t.Errorf("Expected only active agents, got status: %v for agent: %v", agent["status"], agent["name"])
			}
			if agent["name"] == "agent-1" {
				foundAgent1 = true
			}
		}

		if !foundAgent1 {
			t.Error("agent-1 not found in active filter results")
		}
	})

	t.Run("SortByName", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list?sort_by=name&order=asc", nil)
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Agents []map[string]any `json:"agents"`
		}
		decodeResponse(t, rr, &response)

		// Verify sorted order
		if len(response.Agents) >= 2 {
			first := response.Agents[0]["name"].(string)
			second := response.Agents[1]["name"].(string)
			if first > second {
				t.Errorf("Agents not sorted by name: %s > %s", first, second)
			}
		}
	})
}

// Test 7.3: Error handling and edge cases
func TestErrorHandling(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("CreateAgentWithoutName", func(t *testing.T) {
		reqBody := map[string]any{
			"type":         "tool-calling",
			"llm_provider": "openai",
			"model":        "gpt-4o",
		}

		rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
		assertStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("GetNonExistentAgent", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/nonexistent/detail", nil)
		assertStatus(t, rr, http.StatusNotFound)
	})

	t.Run("UpdateNonExistentAgent", func(t *testing.T) {
		updateBody := map[string]any{
			"description": "New description",
		}

		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=nonexistent", updateBody)
		assertStatus(t, rr, http.StatusNotFound)
	})

	t.Run("DeleteNonExistentAgent", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodDelete, "/api/agents?name=nonexistent", nil)
		// Delete may succeed even if agent doesn't exist (idempotent operation)
		// This is acceptable behavior
		if rr.Code != http.StatusOK && rr.Code != http.StatusNotFound {
			t.Errorf("Expected 200 or 404, got %d", rr.Code)
		}
	})

	t.Run("InvalidStatusValue", func(t *testing.T) {
		createTestAgent(t, ts, "status-test", "tool-calling")

		updateBody := map[string]any{
			"status": "invalid-status",
		}

		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=status-test", updateBody)
		// Should either reject or accept with validation
		if rr.Code != http.StatusBadRequest && rr.Code != http.StatusOK {
			t.Errorf("Unexpected status code for invalid status: %d", rr.Code)
		}
	})
}

// Test 7.4: Concurrent access
func TestConcurrentAccess(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create test agent
	createTestAgent(t, ts, "concurrent-agent", "tool-calling")

	// Load agent and simulate concurrent statistics updates
	ag, ok := ts.store.GetAgent("concurrent-agent")
	if !ok {
		t.Fatal("Failed to load agent")
	}

	ag.InitializeStatistics()

	// Simulate multiple concurrent message recordings
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			ag.Statistics.RecordMessage(100, 0.001)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final counts
	if ag.Statistics.MessageCount != 10 {
		t.Errorf("Expected 10 messages, got %d", ag.Statistics.MessageCount)
	}

	if ag.Statistics.TokenUsage != 1000 {
		t.Errorf("Expected 1000 tokens, got %d", ag.Statistics.TokenUsage)
	}

	// Cost should be approximately 0.01 (10 * 0.001)
	expectedCost := 0.01
	if ag.Statistics.TotalCost < expectedCost-0.001 || ag.Statistics.TotalCost > expectedCost+0.001 {
		t.Errorf("Expected cost ~%f, got %f", expectedCost, ag.Statistics.TotalCost)
	}
}

// Test 7.5: Backward compatibility
func TestBackwardCompatibility(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("LoadAgentWithoutStatistics", func(t *testing.T) {
		// Create agent using the API (which will have minimal fields)
		// This simulates a legacy agent
		reqBody := map[string]any{
			"name":  "legacy-agent",
			"type":  "tool-calling",
			"role":  "general",
			"model": "gpt-4o",
		}

		rr := ts.doRequest(t, http.MethodPost, "/api/agents", reqBody)
		assertStatus(t, rr, http.StatusOK)

		// Try to load agent
		ag, ok := ts.store.GetAgent("legacy-agent")
		if !ok {
			t.Fatal("Failed to load legacy agent")
		}

		// Should initialize statistics on first access
		ag.InitializeStatistics()

		if ag.Statistics == nil {
			t.Error("Statistics not initialized for legacy agent")
		}

		if ag.Statistics.MessageCount != 0 {
			t.Errorf("Expected 0 messages for new stats, got %d", ag.Statistics.MessageCount)
		}
	})

	t.Run("AgentWithoutMetadata", func(t *testing.T) {
		// Get agent detail should work even without metadata
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/legacy-agent/detail", nil)
		assertStatus(t, rr, http.StatusOK)

		var agent map[string]any
		decodeResponse(t, rr, &agent)

		// Metadata might be nil or empty object
		if agent["name"] != "legacy-agent" {
			t.Errorf("Expected name 'legacy-agent', got %v", agent["name"])
		}
	})
}

// Test 7.6: Statistics accuracy
func TestStatisticsAccuracy(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	createTestAgent(t, ts, "stats-agent", "tool-calling")

	ag, ok := ts.store.GetAgent("stats-agent")
	if !ok {
		t.Fatal("Failed to load agent")
	}

	ag.InitializeStatistics()

	// Record multiple messages with different costs
	testCases := []struct {
		tokens int
		cost   float64
	}{
		{1000, 0.03},
		{2000, 0.06},
		{500, 0.015},
	}

	for _, tc := range testCases {
		ag.Statistics.RecordMessage(tc.tokens, tc.cost)
	}

	// Verify totals
	expectedMessages := int64(len(testCases))
	expectedTokens := int64(3500)
	expectedCost := 0.105

	if ag.Statistics.MessageCount != expectedMessages {
		t.Errorf("Expected %d messages, got %d", expectedMessages, ag.Statistics.MessageCount)
	}

	if ag.Statistics.TokenUsage != expectedTokens {
		t.Errorf("Expected %d tokens, got %d", expectedTokens, ag.Statistics.TokenUsage)
	}

	// Float comparison with tolerance
	if ag.Statistics.TotalCost < expectedCost-0.0001 || ag.Statistics.TotalCost > expectedCost+0.0001 {
		t.Errorf("Expected cost %f, got %f", expectedCost, ag.Statistics.TotalCost)
	}

	// Verify LastActive is recent
	if time.Since(ag.Statistics.LastActive) > time.Minute {
		t.Errorf("LastActive should be recent, got %v", ag.Statistics.LastActive)
	}
}

// Test 7.7: Evolution and feeding API endpoints
func TestEvolutionAndFeedEndpoints(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	createTestAgent(t, ts, "evo-agent", "tool-calling")

	t.Run("GetAgentEvolutionSuccess", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodGet, "/api/agents/evo-agent/evolution", nil)
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Agent     string         `json:"agent"`
			Evolution map[string]any `json:"evolution"`
		}
		decodeResponse(t, rr, &response)

		if response.Agent != "evo-agent" {
			t.Errorf("expected agent evo-agent, got %q", response.Agent)
		}
		if response.Evolution == nil {
			t.Fatal("expected evolution payload")
		}
		if response.Evolution["stage"] != "spark" {
			t.Errorf("expected default stage spark, got %v", response.Evolution["stage"])
		}
	})

	t.Run("FeedSuccess", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodPost, "/api/agents/evo-agent/feed", map[string]any{
			"content": "project-specific context",
			"source":  "manual",
		})
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Success   bool           `json:"success"`
			Evolution map[string]any `json:"evolution"`
		}
		decodeResponse(t, rr, &response)

		if !response.Success {
			t.Fatal("expected success true")
		}
		feedCount, ok := response.Evolution["feed_count"].(float64)
		if !ok || feedCount < 1 {
			t.Errorf("expected feed_count >= 1, got %v", response.Evolution["feed_count"])
		}
	})

	t.Run("FeedValidationFailure", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodPost, "/api/agents/evo-agent/feed", map[string]any{
			"content": "   ",
		})
		assertStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("FeedMissingAgent", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodPost, "/api/agents/missing-agent/feed", map[string]any{
			"content": "context",
		})
		assertStatus(t, rr, http.StatusNotFound)
	})

	t.Run("SetPathGatedBeforeLearner", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodPost, "/api/agents/evo-agent/evolution/path", map[string]any{
			"path": "coder",
		})
		assertStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("SetPathAtLearnerStage", func(t *testing.T) {
		ag, ok := ts.store.GetAgent("evo-agent")
		if !ok || ag == nil {
			t.Fatal("expected evo-agent to exist")
		}
		ag.InitializeEvolution()
		ag.Evolution.Level = 10
		ag.Evolution.Stage = types.AgentStageLearner
		if err := ts.store.SetAgent("evo-agent", ag); err != nil {
			t.Fatalf("failed to persist learner stage for test: %v", err)
		}

		rr := ts.doRequest(t, http.MethodPost, "/api/agents/evo-agent/evolution/path", map[string]any{
			"path": "coder",
		})
		assertStatus(t, rr, http.StatusOK)

		var response struct {
			Evolution map[string]any `json:"evolution"`
		}
		decodeResponse(t, rr, &response)
		if response.Evolution["path"] != "coder" {
			t.Errorf("expected path coder, got %v", response.Evolution["path"])
		}
	})

	t.Run("SuggestionsIncludeReasonAndApproval", func(t *testing.T) {
		ag, ok := ts.store.GetAgent("evo-agent")
		if !ok || ag == nil {
			t.Fatal("expected evo-agent to exist")
		}
		ag.InitializeEvolution()
		ag.InitializeStatistics()
		ag.Evolution.Level = 12
		ag.Evolution.Stage = types.AgentStageLearner
		ag.Evolution.Path = ""
		ag.Statistics.MessageCount = 35
		if err := ts.store.SetAgent("evo-agent", ag); err != nil {
			t.Fatalf("failed to seed suggestion test state: %v", err)
		}

		rr := ts.doRequest(t, http.MethodGet, "/api/evolution/suggestions", nil)
		assertStatus(t, rr, http.StatusOK)

		var payload struct {
			Suggestions []map[string]any `json:"suggestions"`
		}
		decodeResponse(t, rr, &payload)

		if len(payload.Suggestions) == 0 {
			t.Fatal("expected at least one suggestion")
		}

		first := payload.Suggestions[0]
		if first["reason"] == nil {
			t.Fatal("expected reason in suggestion")
		}
		if first["confidence"] == nil {
			t.Fatal("expected confidence in suggestion")
		}
		if first["requires_approval"] != true {
			t.Fatalf("expected requires_approval=true, got %v", first["requires_approval"])
		}
	})
}

func TestReservedSystemAssistantProtection(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	if err := ensureSystemAssistantAgent(ts.store); err != nil {
		t.Fatalf("failed to ensure system assistant: %v", err)
	}

	t.Run("DeleteReservedAssistantBlocked", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodDelete, "/api/agents?name="+systemAssistantAgentName, nil)
		assertStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("RenameReservedAssistantBlocked", func(t *testing.T) {
		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name="+systemAssistantAgentName, map[string]any{
			"name": "Assistant Renamed",
		})
		assertStatus(t, rr, http.StatusBadRequest)
	})

	t.Run("RenameIntoReservedAssistantBlocked", func(t *testing.T) {
		createTestAgent(t, ts, "rename-source-agent", "general")
		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=rename-source-agent", map[string]any{
			"name": systemAssistantAgentName,
		})
		assertStatus(t, rr, http.StatusBadRequest)
	})
}

func TestPutAgentSwitchIsDeprecated(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	createTestAgent(t, ts, "reviewer", "general")

	rr := ts.doRequest(t, http.MethodPut, "/api/agents?name=reviewer", nil)
	assertStatus(t, rr, http.StatusOK)

	var payload map[string]any
	decodeResponse(t, rr, &payload)
	if payload["deprecated"] != true {
		t.Fatalf("expected deprecated=true, got %#v", payload["deprecated"])
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %#v", payload["success"])
	}
	if !strings.Contains(payload["message"].(string), "deprecated") {
		t.Fatalf("expected deprecation message, got %#v", payload["message"])
	}
}
