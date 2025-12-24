package pluginapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SettingsManager provides thread-safe access to plugin settings.
// Settings are stored as JSON files in the agent's directory and cached in memory.
type SettingsManager interface {
	// Get retrieves a setting value by key.
	// Returns nil if the key doesn't exist.
	Get(key string) (interface{}, error)

	// GetString retrieves a string setting. Returns empty string if not found.
	GetString(key string) (string, error)

	// GetInt retrieves an integer setting. Returns 0 if not found.
	GetInt(key string) (int, error)

	// GetBool retrieves a boolean setting. Returns false if not found.
	GetBool(key string) (bool, error)

	// GetFloat retrieves a float64 setting. Returns 0.0 if not found.
	GetFloat(key string) (float64, error)

	// Set stores a setting value. Value will be serialized to JSON.
	Set(key string, value interface{}) error

	// Delete removes a setting by key.
	Delete(key string) error

	// GetAll returns all settings as a map.
	GetAll() (map[string]interface{}, error)

	// Save persists settings to disk atomically.
	Save() error

	// Load reloads settings from disk.
	Load() error
}

// settingsManager is the default implementation of SettingsManager.
type settingsManager struct {
	mu       sync.RWMutex
	cache    map[string]interface{}
	filePath string
	dirty    bool // Track if cache has unsaved changes
}

// NewSettingsManager creates a new settings manager for a plugin.
// The settings file is stored at: agentDir/{plugin}_settings.json (UI-consistent path).
func NewSettingsManager(agentDir, pluginName string) (SettingsManager, error) {
	if agentDir == "" {
		return nil, fmt.Errorf("agentDir cannot be empty")
	}
	if pluginName == "" {
		return nil, fmt.Errorf("pluginName cannot be empty")
	}

	normalizedName := normalizePluginNameForSettings(pluginName)
	filePath := filepath.Join(agentDir, fmt.Sprintf("%s_settings.json", normalizedName))
	sm := &settingsManager{
		cache:    make(map[string]interface{}),
		filePath: filePath,
		dirty:    false,
	}

	// Load existing settings if file exists
	if _, err := os.Stat(filePath); err == nil {
		if err := sm.Load(); err != nil {
			return nil, fmt.Errorf("failed to load existing settings: %w", err)
		}
		return sm, nil
	}

	// Legacy fallback: agentDir/plugins/pluginName/settings.json
	legacyPath := filepath.Join(agentDir, "plugins", pluginName, "settings.json")
	if _, err := os.Stat(legacyPath); err == nil {
		sm.filePath = legacyPath
		if err := sm.Load(); err != nil {
			return nil, fmt.Errorf("failed to load legacy settings: %w", err)
		}
		// Switch to UI path for future writes.
		sm.filePath = filePath
	}

	return sm, nil
}

func normalizePluginNameForSettings(name string) string {
	normalized := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	return strings.TrimSpace(normalized)
}

// Get retrieves a setting value by key.
func (sm *settingsManager) Get(key string) (interface{}, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	value, exists := sm.cache[key]
	if !exists {
		return nil, nil
	}
	return value, nil
}

// GetString retrieves a string setting.
func (sm *settingsManager) GetString(key string) (string, error) {
	value, err := sm.Get(key)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}

	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("setting %q is not a string (type: %T)", key, value)
	}
	return str, nil
}

// GetInt retrieves an integer setting.
func (sm *settingsManager) GetInt(key string) (int, error) {
	value, err := sm.Get(key)
	if err != nil {
		return 0, err
	}
	if value == nil {
		return 0, nil
	}

	// JSON unmarshals numbers as float64
	switch v := value.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("setting %q is not an integer (type: %T)", key, value)
	}
}

// GetBool retrieves a boolean setting.
func (sm *settingsManager) GetBool(key string) (bool, error) {
	value, err := sm.Get(key)
	if err != nil {
		return false, err
	}
	if value == nil {
		return false, nil
	}

	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("setting %q is not a boolean (type: %T)", key, value)
	}
	return b, nil
}

// GetFloat retrieves a float64 setting.
func (sm *settingsManager) GetFloat(key string) (float64, error) {
	value, err := sm.Get(key)
	if err != nil {
		return 0.0, err
	}
	if value == nil {
		return 0.0, nil
	}

	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0.0, fmt.Errorf("setting %q is not a number (type: %T)", key, value)
	}
}

// Set stores a setting value.
func (sm *settingsManager) Set(key string, value interface{}) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.cache[key] = value
	sm.dirty = true

	// Auto-save on set for durability
	return sm.saveUnlocked()
}

// Delete removes a setting by key.
func (sm *settingsManager) Delete(key string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.cache, key)
	sm.dirty = true

	// Auto-save on delete for durability
	return sm.saveUnlocked()
}

// GetAll returns all settings as a map.
func (sm *settingsManager) GetAll() (map[string]interface{}, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[string]interface{}, len(sm.cache))
	for k, v := range sm.cache {
		result[k] = v
	}
	return result, nil
}

// Save persists settings to disk atomically using temp file + rename pattern.
func (sm *settingsManager) Save() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveUnlocked()
}

// saveUnlocked performs the actual save without acquiring the lock.
// Caller must hold the write lock.
func (sm *settingsManager) saveUnlocked() error {
	if !sm.dirty {
		return nil // No changes to save
	}

	// Serialize to JSON with indentation for readability
	data, err := json.MarshalIndent(sm.cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Atomic write: write to temp file, then rename
	// This ensures we never corrupt the settings file
	tempPath := sm.filePath + ".tmp"

	// Write to temp file
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp settings file: %w", err)
	}

	// Atomically rename temp file to actual file
	if err := os.Rename(tempPath, sm.filePath); err != nil {
		_ = os.Remove(tempPath) // Clean up temp file on error
		return fmt.Errorf("failed to rename settings file: %w", err)
	}

	sm.dirty = false
	return nil
}

// Load reloads settings from disk.
func (sm *settingsManager) Load() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Read settings file
	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, start with empty cache
			sm.cache = make(map[string]interface{})
			sm.dirty = false
			return nil
		}
		return fmt.Errorf("failed to read settings file: %w", err)
	}

	// Parse JSON
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("failed to parse settings file: %w", err)
	}

	sm.cache = settings
	sm.dirty = false
	return nil
}
