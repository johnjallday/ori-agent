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
	"github.com/oriagent/ori-pluginapi"
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

func (ts *testStore) SetCurrentAgent(name string) error {
	ts.currentAgent = name
	return nil
}

func (ts *testStore) CreateAgent(name string, config *store.CreateAgentConfig) error {
	ts.agents[name] = &agent.Agent{
		Plugins: make(map[string]types.LoadedPlugin),
	}
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

func (ts *testStore) Save() error        { return nil }
func (ts *testStore) ClearAgents() error { return nil }

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

	defaultPlugins := []types.PluginRegistryEntry{
		{
			Name:         "test-plugin",
			Description:  "Test plugin",
			Version:      "1.0.0",
			Tags:         []string{"system", "utility"},
			Path:         "/path/to/plugin",
			Category:     "System Tools",
			Enabled:      true,
			HealthStatus: "healthy",
		},
	}
	return setupTestHandlerWithPlugins(t, defaultPlugins)
}

func setupTestHandlerWithPlugins(t *testing.T, plugins []types.PluginRegistryEntry) (*PluginsPageHandler, string, func()) {
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
	st.currentAgent = "test-agent"

	// Initialize registry manager
	regMgr := registry.NewManager()
	_ = regMgr.SaveLocal(types.PluginRegistry{
		Plugins: plugins,
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

func TestHandleListPlugins_IncludesTags(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	w := httptest.NewRecorder()

	handler.HandleListPlugins(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	plugins, ok := response["plugins"].([]interface{})
	if !ok || len(plugins) != 1 {
		t.Fatalf("Expected 1 plugin, got %#v", response["plugins"])
	}

	pluginObj, ok := plugins[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected plugin object, got %T", plugins[0])
	}

	tags, ok := pluginObj["tags"].([]interface{})
	if !ok || len(tags) != 2 {
		t.Fatalf("Expected tags array, got %#v", pluginObj["tags"])
	}
}

func TestHandleListPlugins_FilterByTag(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithPlugins(t, []types.PluginRegistryEntry{
		{
			Name:        "audio-plugin",
			Description: "Audio plugin",
			Version:     "1.0.0",
			Tags:        []string{"audio", "daw"},
			Path:        "/path/to/audio",
		},
		{
			Name:        "system-plugin",
			Description: "System plugin",
			Version:     "1.0.0",
			Tags:        []string{"system"},
			Path:        "/path/to/system",
		},
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins?tag=audio", nil)
	w := httptest.NewRecorder()

	handler.HandleListPlugins(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	plugins, ok := response["plugins"].([]interface{})
	if !ok {
		t.Fatalf("Response missing plugins array: %#v", response)
	}
	if len(plugins) != 1 {
		t.Fatalf("Expected 1 plugin, got %d", len(plugins))
	}

	pluginObj, ok := plugins[0].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected plugin object, got %T", plugins[0])
	}
	if pluginObj["name"] != "audio-plugin" {
		t.Fatalf("Expected audio-plugin, got %#v", pluginObj["name"])
	}
}

func TestHandleListPluginTags(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithPlugins(t, []types.PluginRegistryEntry{
		{Name: "p1", Tags: []string{"audio", "utility"}, Path: "/p1"},
		{Name: "p2", Tags: []string{"audio", "system"}, Path: "/p2"},
	})
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/tags", nil)
	w := httptest.NewRecorder()

	handler.HandleListPluginTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	tags, ok := response["tags"].([]interface{})
	if !ok {
		t.Fatalf("Expected tags array, got %#v", response["tags"])
	}
	if len(tags) != 3 {
		t.Fatalf("Expected 3 unique tags, got %d (%v)", len(tags), tags)
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

func TestValidateExecutableFormat(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		wantErr bool
	}{
		{
			name:    "valid ELF",
			content: []byte{0x7F, 'E', 'L', 'F', 0, 0, 0, 0},
			wantErr: false,
		},
		{
			name:    "valid Mach-O 64-bit big-endian",
			content: []byte{0xFE, 0xED, 0xFA, 0xCF, 0, 0, 0, 0},
			wantErr: false,
		},
		{
			name:    "valid Mach-O 64-bit little-endian",
			content: []byte{0xCF, 0xFA, 0xED, 0xFE, 0, 0, 0, 0},
			wantErr: false,
		},
		{
			name:    "valid Mach-O fat binary",
			content: []byte{0xCA, 0xFE, 0xBA, 0xBE, 0, 0, 0, 0},
			wantErr: false,
		},
		{
			name:    "valid PE (Windows)",
			content: []byte{'M', 'Z', 0, 0, 0, 0, 0, 0},
			wantErr: false,
		},
		{
			name:    "invalid - shell script",
			content: []byte{'#', '!', '/', 'b', 'i', 'n', '/', 's'},
			wantErr: true,
		},
		{
			name:    "invalid - text file",
			content: []byte("hello world this is a text file"),
			wantErr: true,
		},
		{
			name:    "invalid - too small",
			content: []byte{0x7F, 'E'},
			wantErr: true,
		},
		{
			name:    "invalid - random bytes",
			content: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpFile, err := os.CreateTemp("", "test-plugin-*")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			if _, err := tmpFile.Write(tt.content); err != nil {
				t.Fatalf("Failed to write test content: %v", err)
			}
			_ = tmpFile.Close()

			err = validateExecutableFormat(tmpFile.Name())

			if (err != nil) != tt.wantErr {
				t.Errorf("validateExecutableFormat() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
