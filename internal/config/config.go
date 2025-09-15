package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Settings holds application-wide configuration
type Settings struct {
	CurrentAgent string `json:"current_agent"`
	OpenAIAPIKey string `json:"openai_api_key"`
}

// Manager handles configuration loading and saving
type Manager struct {
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
			m.settings = Settings{
				CurrentAgent: "default",
				OpenAIAPIKey: "",
			}
			return nil
		}
		return fmt.Errorf("failed to read config file %s: %w", m.filePath, err)
	}

	if err := json.Unmarshal(data, &m.settings); err != nil {
		return fmt.Errorf("failed to parse config file %s: %w", m.filePath, err)
	}

	return m.validate()
}

// Save writes current configuration to file
func (m *Manager) Save() error {
	if err := m.validate(); err != nil {
		return fmt.Errorf("cannot save invalid configuration: %w", err)
	}

	data, err := json.MarshalIndent(m.settings, "", "  ")
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
	return m.settings
}

// Update modifies the configuration
func (m *Manager) Update(settings Settings) error {
	m.settings = settings
	return m.validate()
}

// GetAPIKey returns the API key, checking settings first, then environment variable
func (m *Manager) GetAPIKey() string {
	// Check settings first
	if m.settings.OpenAIAPIKey != "" {
		return m.settings.OpenAIAPIKey
	}
	
	// Fallback to environment variable
	return os.Getenv("OPENAI_API_KEY")
}

// SetAPIKey updates the API key in settings
func (m *Manager) SetAPIKey(apiKey string) error {
	m.settings.OpenAIAPIKey = strings.TrimSpace(apiKey)
	return m.validateAPIKey(m.settings.OpenAIAPIKey)
}

// GetCurrentAgent returns the current agent name
func (m *Manager) GetCurrentAgent() string {
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
	m.settings.CurrentAgent = agent
	return nil
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
	if apiKey != "" && !strings.HasPrefix(apiKey, "sk-") {
		return fmt.Errorf("invalid API key format: must start with 'sk-'")
	}
	return nil
}

// MaskAPIKey returns a masked version of the API key for display purposes
func (m *Manager) MaskAPIKey() string {
	apiKey := m.settings.OpenAIAPIKey
	
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