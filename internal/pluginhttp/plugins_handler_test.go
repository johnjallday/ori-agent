package pluginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// testStore is a functional mock store for testing
type testStore struct {
	agents       map[string]*agent.Agent
	currentAgent string
}

func newTestStore() *testStore {
	return &testStore{
		agents: make(map[string]*agent.Agent),
	}
}

func (ts *testStore) ListAgents() (names []string, current string) {
	for name := range ts.agents {
		names = append(names, name)
	}
	return names, ts.currentAgent
}

func (ts *testStore) CreateAgent(name string, config *store.CreateAgentConfig) error {
	ts.agents[name] = &agent.Agent{
		Plugins: make(map[string]types.LoadedPlugin),
	}
	return nil
}

func (ts *testStore) SwitchAgent(name string) error {
	ts.currentAgent = name
	return nil
}

func (ts *testStore) DeleteAgent(name string) error {
	delete(ts.agents, name)
	return nil
}

func (ts *testStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := ts.agents[name]
	return ag, ok
}

func (ts *testStore) SetAgent(name string, ag *agent.Agent) error {
	ts.agents[name] = ag
	return nil
}

func (ts *testStore) Save() error {
	return nil
}

type mockLoader struct{}

func (m mockLoader) Load(path string) (pluginapi.PluginTool, error) {
	// Return a mock tool - simplified for testing
	return &mockTool{}, nil
}

type mockTool struct{}

func (m *mockTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        "test-tool",
		Description: "A test tool",
		Parameters:  map[string]interface{}{},
	}
}

func (m *mockTool) Call(ctx context.Context, args string) (string, error) {
	return "test result", nil
}

func setupTestHandler(t *testing.T) (*PluginsPageHandler, string, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize test store
	st := newTestStore()

	// Create test agent
	testAgent := &agent.Agent{
		Plugins: make(map[string]types.LoadedPlugin),
	}
	_ = st.SetAgent("test-agent", testAgent)
	_ = st.SwitchAgent("test-agent")

	// Initialize registry manager
	regMgr := registry.NewManager()
	_ = regMgr.SaveLocal(types.PluginRegistry{
		Plugins: []types.PluginRegistryEntry{
			{
				Name:         "test-plugin",
				Description:  "Test plugin",
				Version:      "1.0.0",
				Path:         "/path/to/plugin",
				Category:     "System Tools",
				Enabled:      true,
				HealthStatus: "healthy",
			},
		},
	})

	// Initialize managers
	catMgr := pluginmanager.NewCategoryManager()
	permMgr := pluginmanager.NewPermissionManager(filepath.Join(tmpDir, "permissions.json"))

	// Create handler
	handler := NewPluginsPageHandler(
		st,
		regMgr,
		catMgr,
		permMgr,
		mockLoader{},
	)

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return handler, tmpDir, cleanup
}

func TestHandleListPlugins(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	w := httptest.NewRecorder()

	handler.HandleListPlugins(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	plugins, ok := response["plugins"].([]interface{})
	if !ok {
		t.Fatal("Response missing plugins array")
	}

	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin, got %d", len(plugins))
	}
}

func TestHandleListPlugins_MethodNotAllowed(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/plugins", nil)
	w := httptest.NewRecorder()

	handler.HandleListPlugins(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleGetPluginDetails(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin", nil)
	w := httptest.NewRecorder()

	handler.HandleGetPluginDetails(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var details map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&details); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if details["name"] != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got %v", details["name"])
	}
}

func TestHandleGetPluginDetails_NotFound(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/non-existent", nil)
	w := httptest.NewRecorder()

	handler.HandleGetPluginDetails(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestHandleEnablePlugin(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/enable", nil)
	w := httptest.NewRecorder()

	handler.HandleEnablePlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if success, ok := response["success"].(bool); !ok || !success {
		t.Error("Expected success: true in response")
	}
}

func TestHandleDisablePlugin(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// First enable the plugin
	enableReq := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/enable", nil)
	enableW := httptest.NewRecorder()
	handler.HandleEnablePlugin(enableW, enableReq)

	// Then disable it
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/disable", nil)
	w := httptest.NewRecorder()

	handler.HandleDisablePlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if success, ok := response["success"].(bool); !ok || !success {
		t.Error("Expected success: true in response")
	}
}

func TestHandleUpdatePluginConfig(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	configReq := map[string]interface{}{
		"config": map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
	}

	body, _ := json.Marshal(configReq)
	req := httptest.NewRequest(http.MethodPut, "/api/plugins/test-plugin/config", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleUpdatePluginConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Note: In real test this would check actual file, but we're using in-memory store
}

func TestHandleUpdatePluginConfig_InvalidJSON(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPut, "/api/plugins/test-plugin/config", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	handler.HandleUpdatePluginConfig(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHandleTestPlugin(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// First enable the plugin
	enableReq := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/enable", nil)
	enableW := httptest.NewRecorder()
	handler.HandleEnablePlugin(enableW, enableReq)

	// Then test it
	testReq := map[string]interface{}{
		"args": `{"test": "value"}`,
	}

	body, _ := json.Marshal(testReq)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/test", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleTestPlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if success, ok := response["success"].(bool); !ok || !success {
		t.Error("Expected success: true in response")
	}
}

func TestHandleTestPlugin_NotEnabled(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	testReq := map[string]interface{}{
		"args": `{"test": "value"}`,
	}

	body, _ := json.Marshal(testReq)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/test", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.HandleTestPlugin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHandleGetPluginLogs(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin/logs", nil)
	w := httptest.NewRecorder()

	handler.HandleGetPluginLogs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if _, ok := response["logs"]; !ok {
		t.Error("Expected logs in response")
	}
}

func TestHandleDeletePlugin(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodDelete, "/api/plugins/test-plugin", nil)
	w := httptest.NewRecorder()

	handler.HandleDeletePlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if success, ok := response["success"].(bool); !ok || !success {
		t.Error("Expected success: true in response")
	}
}

func TestHandleReloadPlugin(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// First enable the plugin
	enableReq := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/enable", nil)
	enableW := httptest.NewRecorder()
	handler.HandleEnablePlugin(enableW, enableReq)

	// Then reload it
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/test-plugin/reload", nil)
	w := httptest.NewRecorder()

	handler.HandleReloadPlugin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestHandleGetPluginAgents(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/test-plugin/agents", nil)
	w := httptest.NewRecorder()

	handler.HandleGetPluginAgents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if _, ok := response["agents"]; !ok {
		t.Error("Expected agents in response")
	}
}

func TestExtractPluginName(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tests := []struct {
		path string
		want string
	}{
		{"/api/plugins/test-plugin/enable", "test-plugin"},
		{"/api/plugins/my-plugin/config", "my-plugin"},
		{"/api/plugins/another/test", "another"},
		{"/api/plugins/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := handler.extractPluginName(tt.path)
			if got != tt.want {
				t.Errorf("extractPluginName(%s) = %s, want %s", tt.path, got, tt.want)
			}
		})
	}
}
