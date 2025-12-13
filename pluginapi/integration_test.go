package pluginapi

import (
	"embed"
	"testing"
)

//go:embed test_templates
var _ embed.FS // Embedded for documentation/reference only

// Helper function for creating float pointers
func floatPtr(f float64) *float64 {
	return &f
}

// TestPluginOptimizationAPIs_Integration tests the complete flow of using
// Settings API, YAML Tool Definitions, and Template Rendering together
func TestPluginOptimizationAPIs_Integration(t *testing.T) {
	// Scenario: A plugin that uses all three optimization APIs
	tempDir := t.TempDir()

	// Step 1: Create a plugin configuration with tool definition
	pluginConfig := &PluginConfig{
		Name:        "integration-test-plugin",
		Version:     "1.0.0",
		Description: "Integration test plugin",
		License:     "MIT",
		Repository:  "https://example.com",
		Tool: &YAMLToolDefinition{
			Name:        "test-tool",
			Description: "A test tool for integration testing",
			Parameters: []YAMLToolParameter{
				{
					Name:        "operation",
					Type:        "string",
					Description: "The operation to perform",
					Required:    true,
					Enum:        []string{"create", "list", "delete"},
				},
				{
					Name:        "name",
					Type:        "string",
					Description: "Name of the resource",
					Required:    false,
				},
				{
					Name:        "count",
					Type:        "integer",
					Description: "Number of items",
					Required:    false,
					Min:         floatPtr(1),
					Max:         floatPtr(100),
					Default:     10,
				},
			},
		},
	}

	// Step 2: Create a BasePlugin instance
	bp := newBasePlugin("test-tool", "1.0.0", "0.0.1", "", "v1")
	bp.SetPluginConfig(pluginConfig)
	bp.SetMetadata(&PluginMetadata{
		Name:        "integration-test-plugin",
		Description: "Integration test plugin",
		Maintainers: []*Maintainer{{Name: "Test", Email: "test@example.com"}},
		License:     "MIT",
		Repository:  "https://example.com",
	})

	// Step 3: Set agent context (enables Settings API)
	bp.SetAgentContext(AgentContext{
		Name:     "test-agent",
		AgentDir: tempDir,
	})

	// Step 4: Test Settings API
	t.Run("Settings API", func(t *testing.T) {
		sm := bp.Settings()
		if sm == nil {
			t.Fatal("expected non-nil settings manager")
		}

		// Store configuration
		err := sm.Set("api_key", "test-key-123")
		if err != nil {
			t.Errorf("failed to set api_key: %v", err)
		}

		err = sm.Set("max_retries", 5.0)
		if err != nil {
			t.Errorf("failed to set max_retries: %v", err)
		}

		err = sm.Set("debug_mode", true)
		if err != nil {
			t.Errorf("failed to set debug_mode: %v", err)
		}

		// Retrieve configuration
		apiKey, err := sm.GetString("api_key")
		if err != nil || apiKey != "test-key-123" {
			t.Errorf("expected api_key 'test-key-123', got '%s', err: %v", apiKey, err)
		}

		maxRetries, err := sm.GetInt("max_retries")
		if err != nil || maxRetries != 5 {
			t.Errorf("expected max_retries 5, got %d, err: %v", maxRetries, err)
		}

		debugMode, err := sm.GetBool("debug_mode")
		if err != nil || !debugMode {
			t.Errorf("expected debug_mode true, got %v, err: %v", debugMode, err)
		}

		// Verify persistence
		all, err := sm.GetAll()
		if err != nil {
			t.Errorf("failed to get all settings: %v", err)
		}
		if len(all) != 3 {
			t.Errorf("expected 3 settings, got %d", len(all))
		}
	})

	// Step 5: Test YAML Tool Definition
	t.Run("Tool Definition from YAML", func(t *testing.T) {
		tool, err := bp.GetToolDefinition()
		if err != nil {
			t.Fatalf("failed to get tool definition: %v", err)
		}

		if tool.Name != "test-tool" {
			t.Errorf("expected tool name 'test-tool', got '%s'", tool.Name)
		}

		if tool.Description != "A test tool for integration testing" {
			t.Errorf("expected description, got '%s'", tool.Description)
		}

		// Verify parameters
		params := tool.Parameters
		props, ok := params["properties"].(map[string]interface{})
		if !ok {
			t.Fatal("properties not found in tool definition")
		}

		// Check operation parameter
		operation, ok := props["operation"].(map[string]interface{})
		if !ok {
			t.Fatal("operation parameter not found")
		}
		if operation["type"] != "string" {
			t.Errorf("expected operation type 'string', got '%v'", operation["type"])
		}

		// Check enum values (might be []interface{} from JSON conversion)
		enum, ok := operation["enum"]
		if !ok {
			t.Error("enum values not found for operation")
		}
		// Convert to slice and check length
		var enumLen int
		switch v := enum.(type) {
		case []string:
			enumLen = len(v)
		case []interface{}:
			enumLen = len(v)
		default:
			t.Errorf("unexpected enum type: %T", v)
		}
		if enumLen != 3 {
			t.Errorf("expected 3 enum values, got %d", enumLen)
		}

		// Check count parameter with min/max
		count, ok := props["count"].(map[string]interface{})
		if !ok {
			t.Fatal("count parameter not found")
		}
		if count["minimum"] != 1 || count["maximum"] != 100 {
			t.Error("expected min=1, max=100 for count parameter")
		}
		if count["default"] != 10 {
			t.Errorf("expected default=10, got %v", count["default"])
		}

		// Check required fields
		required, ok := params["required"].([]string)
		if !ok {
			t.Fatal("required field not found")
		}
		if len(required) != 1 || required[0] != "operation" {
			t.Error("expected only 'operation' to be required")
		}
	})

	// Step 6: Test Template Rendering (simplified)
	t.Run("Template Rendering", func(t *testing.T) {
		// Test that template rendering works with settings data
		renderer := NewTemplateRenderer()

		// Get settings
		sm := bp.Settings()
		apiKey, _ := sm.GetString("api_key")

		// Use existing test templates
		data := map[string]interface{}{
			"Title": apiKey, // Use settings value in template
		}

		html, err := renderer.RenderTemplate(testTemplatesFS, "test_templates/simple.html", data)
		if err != nil {
			t.Fatalf("failed to render template: %v", err)
		}

		// Verify template was rendered (simple.html should exist from templates_test.go)
		if html == "" {
			t.Error("HTML should not be empty")
		}

		t.Log("Template rendering works with settings API")
	})

	// Step 7: Test end-to-end workflow
	t.Run("End-to-End Workflow", func(t *testing.T) {
		// 1. Plugin initializes and loads tool definition from YAML
		tool, err := bp.GetToolDefinition()
		if err != nil {
			t.Fatalf("workflow step 1 failed: %v", err)
		}
		if tool.Name == "" {
			t.Error("tool definition should be loaded")
		}

		// 2. Plugin receives agent context and initializes settings
		sm := bp.Settings()
		if sm == nil {
			t.Error("settings should be available after agent context is set")
		}

		// 3. Plugin stores configuration
		_ = sm.Set("resource_count", 42.0)

		// 4. Verify settings persistence
		resourceCount, _ := sm.GetInt("resource_count")
		if resourceCount != 42 {
			t.Errorf("expected resource_count=42, got %d", resourceCount)
		}

		// 5. Verify all APIs work together seamlessly
		all, _ := sm.GetAll()
		if len(all) == 0 {
			t.Error("settings should persist throughout workflow")
		}

		t.Log("End-to-end workflow: all optimization APIs work together")
	})
}

// TestRealWorldScenario tests a realistic plugin implementation
func TestRealWorldScenario_MusicPlugin(t *testing.T) {
	tempDir := t.TempDir()

	// Plugin configuration similar to ori-music-project-manager
	config := &PluginConfig{
		Name:        "music-project-manager",
		Version:     "0.0.8",
		Description: "Manage music projects",
		License:     "MIT",
		Repository:  "https://github.com/example/music-plugin",
		Tool: &YAMLToolDefinition{
			Name:        "music-manager",
			Description: "Manage music projects",
			Parameters: []YAMLToolParameter{
				{
					Name:        "operation",
					Type:        "string",
					Description: "Operation to perform",
					Required:    true,
					Enum:        []string{"create", "list", "open", "delete"},
				},
				{
					Name:        "name",
					Type:        "string",
					Description: "Project name",
					Required:    false,
				},
				{
					Name:        "bpm",
					Type:        "integer",
					Description: "Beats per minute",
					Required:    false,
					Min:         floatPtr(30),
					Max:         floatPtr(300),
				},
			},
		},
	}

	// Initialize plugin
	bp := newBasePlugin("music-manager", "0.0.8", "0.0.1", "", "v1")
	bp.SetPluginConfig(config)
	bp.SetAgentContext(AgentContext{
		Name:     "music-agent",
		AgentDir: tempDir,
	})

	// Store plugin-specific settings
	sm := bp.Settings()
	_ = sm.Set("project_dir", "/Users/test/Music/Projects")
	_ = sm.Set("template_dir", "/Users/test/Music/Templates")
	_ = sm.Set("default_bpm", 120.0)

	// Get tool definition
	tool, err := bp.GetToolDefinition()
	if err != nil {
		t.Fatalf("failed to get tool definition: %v", err)
	}

	// Verify tool definition matches YAML
	if tool.Name != "music-manager" {
		t.Errorf("expected tool name 'music-manager', got '%s'", tool.Name)
	}

	// Verify settings persistence
	projectDir, err := sm.GetString("project_dir")
	if err != nil || projectDir != "/Users/test/Music/Projects" {
		t.Error("settings should persist correctly")
	}

	defaultBPM, err := sm.GetInt("default_bpm")
	if err != nil || defaultBPM != 120 {
		t.Error("numeric settings should work correctly")
	}

	t.Log("Real-world scenario test passed: all APIs work together")
}

// TestConcurrentAPIUsage tests concurrent access to all optimization APIs
func TestConcurrentAPIUsage(t *testing.T) {
	tempDir := t.TempDir()

	config := &PluginConfig{
		Name:        "concurrent-test",
		Version:     "1.0.0",
		Description: "Concurrent test",
		License:     "MIT",
		Repository:  "https://example.com",
		Tool: &YAMLToolDefinition{
			Name:        "concurrent-tool",
			Description: "Test tool",
			Parameters: []YAMLToolParameter{
				{Name: "param1", Type: "string", Description: "Test", Required: true},
			},
		},
	}

	bp := newBasePlugin("concurrent-tool", "1.0.0", "", "", "v1")
	bp.SetPluginConfig(config)
	bp.SetAgentContext(AgentContext{
		Name:     "test-agent",
		AgentDir: tempDir,
	})

	renderer := NewTemplateRenderer()

	// Concurrent operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			// Settings API
			sm := bp.Settings()
			_ = sm.Set("key"+string(rune(id)), id)

			// Tool Definition API
			tool, err := bp.GetToolDefinition()
			if err != nil {
				t.Errorf("concurrent tool definition failed: %v", err)
			}
			if tool.Name != "concurrent-tool" {
				t.Error("tool definition name incorrect in concurrent access")
			}

			// Template Rendering (use existing test template)
			_, err = renderer.RenderTemplate(testTemplatesFS, "test_templates/simple.html", map[string]interface{}{"Title": id})
			if err != nil {
				t.Errorf("concurrent template rendering failed: %v", err)
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	t.Log("Concurrent API usage test passed")
}
