package pluginapi

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewSettingsManager(t *testing.T) {
	// Create temp directory for testing
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		agentDir    string
		pluginName  string
		expectError bool
	}{
		{
			name:        "valid parameters",
			agentDir:    tempDir,
			pluginName:  "test-plugin",
			expectError: false,
		},
		{
			name:        "empty agent dir",
			agentDir:    "",
			pluginName:  "test-plugin",
			expectError: true,
		},
		{
			name:        "empty plugin name",
			agentDir:    tempDir,
			pluginName:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, err := NewSettingsManager(tt.agentDir, tt.pluginName)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if sm == nil {
				t.Error("expected non-nil settings manager")
			}
		})
	}
}

func TestSettingsManager_SetAndGet(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Test setting and getting a string
	err = sm.Set("key1", "value1")
	if err != nil {
		t.Errorf("failed to set string: %v", err)
	}

	val, err := sm.Get("key1")
	if err != nil {
		t.Errorf("failed to get value: %v", err)
	}
	if val != "value1" {
		t.Errorf("expected 'value1', got '%v'", val)
	}

	// Test getting non-existent key
	val, err = sm.Get("nonexistent")
	if err != nil {
		t.Errorf("unexpected error for non-existent key: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil for non-existent key, got %v", val)
	}
}

func TestSettingsManager_TypedGetters(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Test string
	_ = sm.Set("string_key", "hello")
	strVal, err := sm.GetString("string_key")
	if err != nil {
		t.Errorf("failed to get string: %v", err)
	}
	if strVal != "hello" {
		t.Errorf("expected 'hello', got '%s'", strVal)
	}

	// Test int (JSON unmarshals as float64)
	_ = sm.Set("int_key", 42.0)
	intVal, err := sm.GetInt("int_key")
	if err != nil {
		t.Errorf("failed to get int: %v", err)
	}
	if intVal != 42 {
		t.Errorf("expected 42, got %d", intVal)
	}

	// Test bool
	_ = sm.Set("bool_key", true)
	boolVal, err := sm.GetBool("bool_key")
	if err != nil {
		t.Errorf("failed to get bool: %v", err)
	}
	if !boolVal {
		t.Error("expected true, got false")
	}

	// Test float
	_ = sm.Set("float_key", 3.14)
	floatVal, err := sm.GetFloat("float_key")
	if err != nil {
		t.Errorf("failed to get float: %v", err)
	}
	if floatVal != 3.14 {
		t.Errorf("expected 3.14, got %f", floatVal)
	}
}

func TestSettingsManager_TypeErrors(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Set a string value
	_ = sm.Set("string_key", "not-a-number")

	// Try to get it as int (should error)
	_, err = sm.GetInt("string_key")
	if err == nil {
		t.Error("expected error when getting string as int")
	}

	// Try to get it as bool (should error)
	_, err = sm.GetBool("string_key")
	if err == nil {
		t.Error("expected error when getting string as bool")
	}
}

func TestSettingsManager_Delete(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Set a value
	_ = sm.Set("key1", "value1")

	// Verify it exists
	val, _ := sm.Get("key1")
	if val == nil {
		t.Error("expected key1 to exist")
	}

	// Delete the key
	err = sm.Delete("key1")
	if err != nil {
		t.Errorf("failed to delete key: %v", err)
	}

	// Verify it's gone
	val, _ = sm.Get("key1")
	if val != nil {
		t.Errorf("expected key1 to be deleted, got %v", val)
	}
}

func TestSettingsManager_GetAll(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Set multiple values
	_ = sm.Set("key1", "value1")
	_ = sm.Set("key2", 42.0)
	_ = sm.Set("key3", true)

	// Get all settings
	all, err := sm.GetAll()
	if err != nil {
		t.Errorf("failed to get all settings: %v", err)
	}

	if len(all) != 3 {
		t.Errorf("expected 3 settings, got %d", len(all))
	}

	if all["key1"] != "value1" {
		t.Errorf("expected key1='value1', got '%v'", all["key1"])
	}
	if all["key2"] != 42.0 {
		t.Errorf("expected key2=42, got %v", all["key2"])
	}
	if all["key3"] != true {
		t.Errorf("expected key3=true, got %v", all["key3"])
	}
}

func TestSettingsManager_Persistence(t *testing.T) {
	tempDir := t.TempDir()

	// Create first manager and set values
	sm1, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	_ = sm1.Set("persistent_key", "persistent_value")

	// Create second manager (should load existing settings)
	sm2, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create second settings manager: %v", err)
	}

	// Check if value persisted
	val, err := sm2.GetString("persistent_key")
	if err != nil {
		t.Errorf("failed to get persistent value: %v", err)
	}
	if val != "persistent_value" {
		t.Errorf("expected 'persistent_value', got '%s'", val)
	}
}

func TestSettingsManager_AtomicWrite(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Set a value (triggers save)
	_ = sm.Set("key1", "value1")

	// Check that the file exists (uses new UI-consistent path format)
	filePath := filepath.Join(tempDir, "test-plugin_settings.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("settings file should exist after Set")
	}

	// Check that temp file doesn't exist (atomic write completed)
	tempPath := filePath + ".tmp"
	if _, err := os.Stat(tempPath); err == nil {
		t.Error("temp file should not exist after successful save")
	}
}

func TestSettingsManager_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := "key" + string(rune(id))
				_ = sm.Set(key, id*iterations+j)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := "key" + string(rune(id))
				_, _ = sm.Get(key)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state
	all, err := sm.GetAll()
	if err != nil {
		t.Errorf("failed to get all settings after concurrent access: %v", err)
	}

	// Should have at least some keys
	if len(all) == 0 {
		t.Error("expected some keys after concurrent writes")
	}
}

func TestSettingsManager_LoadError(t *testing.T) {
	tempDir := t.TempDir()
	pluginDir := filepath.Join(tempDir, "plugins", "test-plugin")
	_ = os.MkdirAll(pluginDir, 0755)

	// Write invalid JSON
	settingsPath := filepath.Join(pluginDir, "settings.json")
	_ = os.WriteFile(settingsPath, []byte("invalid json{{{"), 0644)

	// Try to create settings manager (should fail to load)
	_, err := NewSettingsManager(tempDir, "test-plugin")
	if err == nil {
		t.Error("expected error when loading invalid JSON")
	}
}

func TestBasePlugin_Settings(t *testing.T) {
	tempDir := t.TempDir()

	// Create a base plugin with metadata
	bp := newBasePlugin("test-tool", "1.0.0", "", "", "v1")
	bp.SetMetadata(&PluginMetadata{
		Name: "test-tool",
	})

	// Settings should return nil before agent context is set
	if bp.Settings() != nil {
		t.Error("expected nil settings before agent context is set")
	}

	// Set agent context
	bp.SetAgentContext(AgentContext{
		Name:     "test-agent",
		AgentDir: tempDir,
	})

	// Settings should now return a valid manager
	sm := bp.Settings()
	if sm == nil {
		t.Fatal("expected non-nil settings after agent context is set")
	}

	// Test that settings work
	err := sm.Set("test_key", "test_value")
	if err != nil {
		t.Errorf("failed to set value via BasePlugin.Settings(): %v", err)
	}

	val, err := sm.GetString("test_key")
	if err != nil {
		t.Errorf("failed to get value: %v", err)
	}
	if val != "test_value" {
		t.Errorf("expected 'test_value', got '%s'", val)
	}

	// Test that calling Settings() again returns the same instance
	sm2 := bp.Settings()
	if sm2 != sm {
		t.Error("expected Settings() to return same instance")
	}
}

func TestSettingsManager_DefaultValues(t *testing.T) {
	tempDir := t.TempDir()
	sm, err := NewSettingsManager(tempDir, "test-plugin")
	if err != nil {
		t.Fatalf("failed to create settings manager: %v", err)
	}

	// Test default values for non-existent keys
	strVal, err := sm.GetString("nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if strVal != "" {
		t.Errorf("expected empty string for non-existent key, got '%s'", strVal)
	}

	intVal, err := sm.GetInt("nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if intVal != 0 {
		t.Errorf("expected 0 for non-existent key, got %d", intVal)
	}

	boolVal, err := sm.GetBool("nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if boolVal != false {
		t.Error("expected false for non-existent key")
	}

	floatVal, err := sm.GetFloat("nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if floatVal != 0.0 {
		t.Errorf("expected 0.0 for non-existent key, got %f", floatVal)
	}
}
