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

	// System model settings - used for internal AI tasks (auto-config, suggestions, etc.)
	SystemProvider string `json:"system_provider,omitempty"` // Provider for system tasks (e.g., "openai", "claude", "ollama")
	SystemModel    string `json:"system_model,omitempty"`    // Model for system tasks (e.g., "gpt-4o-mini", "claude-3-haiku-20240307")

	// Session cleanup settings
	SessionCleanupEnabled bool `json:"session_cleanup_enabled"` // Enable automatic cleanup of old sessions (default: true)
	SessionCleanupDays    int  `json:"session_cleanup_days"`    // Days of inactivity before session cleanup (default: 30)
	SessionMaxCount       int  `json:"session_max_count"`       // Maximum number of sessions to keep (0 = unlimited, default: 1000)

	// Web3 wallet settings
	Web3WalletAddress string `json:"web3_wallet_address,omitempty"` // Connected wallet address (0x...)
	Web3ChainID       int    `json:"web3_chain_id,omitempty"`       // Connected chain ID (1=Ethereum, 137=Polygon, etc.)
	Web3ENSName       string `json:"web3_ens_name,omitempty"`       // ENS name if available
	Web3ConnectedAt   string `json:"web3_connected_at,omitempty"`   // ISO timestamp of when wallet was connected
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
			m.settings = defaultSettings()
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("failed to read config file %s: %w", m.filePath, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Start with defaults
	m.settings = defaultSettings()

	if err := json.Unmarshal(data, &m.settings); err != nil {
		return fmt.Errorf("failed to parse config file %s: %w", m.filePath, err)
	}

	return m.validate()
}

// defaultSettings returns the default configuration
func defaultSettings() Settings {
	return Settings{
		CurrentAgent:          "default",
		OpenAIAPIKey:          "",
		SessionCleanupEnabled: true,
		SessionCleanupDays:    30,
		SessionMaxCount:       1000,
	}
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

	// SECURITY: Use 0600 permissions (owner read/write only) since file contains API keys
	if err := os.WriteFile(m.filePath, data, 0600); err != nil {
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
		"http://localhost:8765",
		"http://127.0.0.1:8765",
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
		isLower := char >= 'a' && char <= 'z'
		isUpper := char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		isAllowed := char == '-' || char == '_'
		if !isLower && !isUpper && !isDigit && !isAllowed {
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

// GetSessionCleanupEnabled returns whether automatic session cleanup is enabled
func (m *Manager) GetSessionCleanupEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SessionCleanupEnabled
}

// GetSessionCleanupDays returns the number of days of inactivity before cleanup
func (m *Manager) GetSessionCleanupDays() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings.SessionCleanupDays <= 0 {
		return 30 // Default
	}
	return m.settings.SessionCleanupDays
}

// GetSessionMaxCount returns the maximum number of sessions to keep
func (m *Manager) GetSessionMaxCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings.SessionMaxCount <= 0 {
		return 1000 // Default
	}
	return m.settings.SessionMaxCount
}

// SetSessionCleanupSettings updates session cleanup settings
func (m *Manager) SetSessionCleanupSettings(enabled bool, days int, maxCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.SessionCleanupEnabled = enabled
	m.settings.SessionCleanupDays = days
	m.settings.SessionMaxCount = maxCount
}

// GetSystemModel returns the configured system model provider and model
func (m *Manager) GetSystemModel() (provider, model string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SystemProvider, m.settings.SystemModel
}

// SetSystemModel updates the system model configuration
func (m *Manager) SetSystemModel(provider, model string) error {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)

	if err := validateSystemModel(provider, model); err != nil {
		return err
	}

	m.mu.Lock()
	m.settings.SystemProvider = provider
	m.settings.SystemModel = model
	m.mu.Unlock()
	return nil
}

// IsSystemModelConfigured returns true if both system provider and model are set
func (m *Manager) IsSystemModelConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SystemProvider != "" && m.settings.SystemModel != ""
}

// ValidProviders returns the list of valid provider names for system model
func ValidProviders() []string {
	return []string{"openai", "claude", "ollama"}
}

// validateSystemModel validates the system model configuration
func validateSystemModel(provider, model string) error {
	// Both must be set or both must be empty
	if (provider == "") != (model == "") {
		return fmt.Errorf("system provider and model must both be set or both be empty")
	}

	// If both are empty, that's valid (unconfigured)
	if provider == "" && model == "" {
		return nil
	}

	// Validate provider is one of the known providers
	validProviders := ValidProviders()
	isValidProvider := false
	for _, vp := range validProviders {
		if strings.EqualFold(provider, vp) {
			isValidProvider = true
			break
		}
	}
	if !isValidProvider {
		return fmt.Errorf("invalid system provider %q: must be one of %v", provider, validProviders)
	}

	// Validate model is not empty (we already checked this above, but be explicit)
	if model == "" {
		return fmt.Errorf("system model cannot be empty when provider is set")
	}

	// Basic model format validation - must contain only alphanumeric, dash, underscore, dot, colon
	for _, char := range model {
		isLower := char >= 'a' && char <= 'z'
		isUpper := char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		isAllowed := char == '-' || char == '_' || char == '.' || char == ':'
		if !isLower && !isUpper && !isDigit && !isAllowed {
			return fmt.Errorf("invalid system model %q: contains invalid character %q", model, string(char))
		}
	}

	return nil
}

// Web3Wallet represents the connected wallet information
type Web3Wallet struct {
	Address     string `json:"address"`
	ChainID     int    `json:"chain_id"`
	ENSName     string `json:"ens_name,omitempty"`
	ConnectedAt string `json:"connected_at,omitempty"`
}

// GetWeb3Wallet returns the connected Web3 wallet info
func (m *Manager) GetWeb3Wallet() *Web3Wallet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.settings.Web3WalletAddress == "" {
		return nil
	}

	return &Web3Wallet{
		Address:     m.settings.Web3WalletAddress,
		ChainID:     m.settings.Web3ChainID,
		ENSName:     m.settings.Web3ENSName,
		ConnectedAt: m.settings.Web3ConnectedAt,
	}
}

// SetWeb3Wallet updates the Web3 wallet connection
func (m *Manager) SetWeb3Wallet(wallet *Web3Wallet) error {
	if wallet == nil {
		// Clear wallet
		m.mu.Lock()
		m.settings.Web3WalletAddress = ""
		m.settings.Web3ChainID = 0
		m.settings.Web3ENSName = ""
		m.settings.Web3ConnectedAt = ""
		m.mu.Unlock()
		return nil
	}

	// Validate address format
	if err := validateWeb3Address(wallet.Address); err != nil {
		return err
	}

	// Validate chain ID
	if err := validateChainID(wallet.ChainID); err != nil {
		return err
	}

	m.mu.Lock()
	m.settings.Web3WalletAddress = wallet.Address
	m.settings.Web3ChainID = wallet.ChainID
	m.settings.Web3ENSName = wallet.ENSName
	m.settings.Web3ConnectedAt = wallet.ConnectedAt
	m.mu.Unlock()

	return nil
}

// ClearWeb3Wallet removes the Web3 wallet connection
func (m *Manager) ClearWeb3Wallet() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.Web3WalletAddress = ""
	m.settings.Web3ChainID = 0
	m.settings.Web3ENSName = ""
	m.settings.Web3ConnectedAt = ""
}

// IsWeb3WalletConnected returns true if a wallet is connected
func (m *Manager) IsWeb3WalletConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.Web3WalletAddress != ""
}

// MaskWeb3Address returns a masked version of the wallet address for display
func MaskWeb3Address(address string) string {
	if len(address) < 10 {
		return address
	}
	return address[:6] + "..." + address[len(address)-4:]
}

// validateWeb3Address validates an Ethereum address format
func validateWeb3Address(address string) error {
	if address == "" {
		return fmt.Errorf("wallet address cannot be empty")
	}

	// Must start with 0x
	if !strings.HasPrefix(address, "0x") {
		return fmt.Errorf("invalid wallet address: must start with '0x'")
	}

	// Must be exactly 42 characters (0x + 40 hex chars)
	if len(address) != 42 {
		return fmt.Errorf("invalid wallet address: must be 42 characters")
	}

	// Must contain only valid hex characters after 0x
	for _, char := range address[2:] {
		isDigit := char >= '0' && char <= '9'
		isLowerHex := char >= 'a' && char <= 'f'
		isUpperHex := char >= 'A' && char <= 'F'
		if !isDigit && !isLowerHex && !isUpperHex {
			return fmt.Errorf("invalid wallet address: contains invalid character %q", string(char))
		}
	}

	return nil
}

// SupportedChains returns the list of supported chain IDs and names
func SupportedChains() map[int]string {
	return map[int]string{
		1:     "Ethereum",
		137:   "Polygon",
		42161: "Arbitrum",
		10:    "Optimism",
		8453:  "Base",
	}
}

// validateChainID validates that the chain ID is supported
func validateChainID(chainID int) error {
	if chainID == 0 {
		return fmt.Errorf("chain ID cannot be zero")
	}

	supported := SupportedChains()
	if _, ok := supported[chainID]; !ok {
		return fmt.Errorf("unsupported chain ID %d: must be one of %v", chainID, supported)
	}

	return nil
}
