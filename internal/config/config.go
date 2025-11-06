package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Settings holds application-wide configuration
type Settings struct {
	CurrentAgent    string   `json:"current_agent"`
	OpenAIAPIKey    string   `json:"openai_api_key"`
	AnthropicAPIKey string   `json:"anthropic_api_key"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"` // CORS allowed origins (defaults to localhost)
}

// Manager handles configuration loading and saving
type Manager struct {
	mu       sync.RWMutex // Protects settings from concurrent access
	filePath string
	settings Settings
}

// NewManager creates a new configuration manager
func NewManager(filePath string) *Manager {
	if filePath == "" {
		filePath = "settings.json"
	}
	return &Manager{
		filePath: filePath,
	}
}

// Load reads configuration from file with fallback to defaults
func (m *Manager) Load() error {
	// Try to read settings file
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		// If file doesn't exist, use default settings
		if os.IsNotExist(err) {
			m.mu.Lock()
			m.settings = Settings{
				CurrentAgent: "default",
				OpenAIAPIKey: "",
			}
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("failed to read config file %s: %w", m.filePath, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := json.Unmarshal(data, &m.settings); err != nil {
		return fmt.Errorf("failed to parse config file %s: %w", m.filePath, err)
	}

	return m.validate()
}

// Save writes current configuration to file
func (m *Manager) Save() error {
	m.mu.RLock()
	if err := m.validate(); err != nil {
		m.mu.RUnlock()
		return fmt.Errorf("cannot save invalid configuration: %w", err)
	}

	data, err := json.MarshalIndent(m.settings, "", "  ")
	m.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", m.filePath, err)
	}

	return nil
}

// Get returns the current configuration
func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// Update modifies the configuration
func (m *Manager) Update(settings Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
	return m.validate()
}

// GetAPIKey returns the OpenAI API key, checking settings first, then environment variable
func (m *Manager) GetAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.OpenAIAPIKey
	m.mu.RUnlock()

	// Check settings first
	if apiKey != "" {
		return apiKey
	}

	// Fallback to environment variable
	return os.Getenv("OPENAI_API_KEY")
}

// GetAnthropicAPIKey returns the Anthropic API key, checking settings first, then environment variable
func (m *Manager) GetAnthropicAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.AnthropicAPIKey
	m.mu.RUnlock()

	// Check settings first
	if apiKey != "" {
		return apiKey
	}

	// Fallback to environment variable
	return os.Getenv("ANTHROPIC_API_KEY")
}

// SetAPIKey updates the API key in settings
func (m *Manager) SetAPIKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if err := m.validateAPIKey(apiKey); err != nil {
		return err
	}
	m.mu.Lock()
	m.settings.OpenAIAPIKey = apiKey
	m.mu.Unlock()
	return nil
}

// GetCurrentAgent returns the current agent name
func (m *Manager) GetCurrentAgent() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings.CurrentAgent == "" {
		return "default"
	}
	return m.settings.CurrentAgent
}

// SetCurrentAgent updates the current agent
func (m *Manager) SetCurrentAgent(agent string) error {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return fmt.Errorf("agent name cannot be empty")
	}
	m.mu.Lock()
	m.settings.CurrentAgent = agent
	m.mu.Unlock()
	return nil
}

// GetAllowedOrigins returns the allowed CORS origins with secure defaults
func (m *Manager) GetAllowedOrigins() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If configured, return those origins
	if len(m.settings.AllowedOrigins) > 0 {
		return m.settings.AllowedOrigins
	}

	// Default to localhost only for security
	return []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
	}
}

// validate performs basic validation on configuration
func (m *Manager) validate() error {
	if m.settings.CurrentAgent == "" {
		m.settings.CurrentAgent = "default"
	}

	return m.validateAPIKey(m.settings.OpenAIAPIKey)
}

// validateAPIKey validates API key format if provided
func (m *Manager) validateAPIKey(apiKey string) error {
	// Empty API key is allowed (will fall back to environment variable)
	if apiKey == "" {
		return nil
	}

	// Check if it starts with sk-
	if !strings.HasPrefix(apiKey, "sk-") {
		return fmt.Errorf("invalid API key format: must start with 'sk-'")
	}

	// Check minimum length (OpenAI keys are typically 48+ characters)
	if len(apiKey) < 20 {
		return fmt.Errorf("invalid API key: too short (minimum 20 characters)")
	}

	// Check that it only contains valid characters (alphanumeric, dash, underscore)
	for _, char := range apiKey {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_') {
			return fmt.Errorf("invalid API key: contains invalid characters (only alphanumeric, dash, and underscore allowed)")
		}
	}

	return nil
}

// MaskAPIKey returns a masked version of the API key for display purposes
func (m *Manager) MaskAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.OpenAIAPIKey
	m.mu.RUnlock()

	// Check if we have an API key from settings
	if apiKey != "" {
		if len(apiKey) < 8 {
			return "***"
		}
		return apiKey[:8] + "***..." + apiKey[len(apiKey)-4:]
	}

	// Check if there's an environment variable set
	envKey := os.Getenv("OPENAI_API_KEY")
	if envKey != "" {
		return "Environment variable set"
	}

	// No API key found anywhere
	return "API key required"
}
