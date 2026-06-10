package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// UtilitySettings controls native utility tool providers and runtime safeguards.
type UtilitySettings struct {
	Enabled                 bool     `json:"enabled"`
	TimeoutMs               int      `json:"timeout_ms,omitempty"`
	RetryAttempts           int      `json:"retry_attempts,omitempty"`
	RetryDelayMs            int      `json:"retry_delay_ms,omitempty"`
	SearchProvider          string   `json:"search_provider,omitempty"`          // auto, duckduckgo, brave
	BrowserControlProvider  string   `json:"browser_control_provider,omitempty"` // auto, playwright, browserbase, puppeteer
	PlaywrightBrowser       string   `json:"playwright_browser,omitempty"`       // auto, chrome, firefox, webkit, msedge, brave
	PlaywrightExecutable    string   `json:"playwright_executable_path,omitempty"`
	BraveAPIKey             string   `json:"brave_api_key,omitempty"`
	WeatherProvider         string   `json:"weather_provider,omitempty"` // open-meteo
	WeatherGeocodingURL     string   `json:"weather_geocoding_url,omitempty"`
	WeatherForecastURL      string   `json:"weather_forecast_url,omitempty"`
	WebFetchMaxResponseSize int64    `json:"web_fetch_max_response_size,omitempty"`
	BrowserMaxResponseSize  int64    `json:"browser_max_response_size,omitempty"`
	BrowserAllowedDomains   []string `json:"browser_allowed_domains,omitempty"`
	BlockPrivateHosts       bool     `json:"block_private_hosts"`
	UserAgent               string   `json:"user_agent,omitempty"`
}

// MacWakeSettings controls macOS wake scheduling for workspace task schedules.
type MacWakeSettings struct {
	Enabled              bool       `json:"enabled"`
	AdminApprovalGranted bool       `json:"admin_approval_granted,omitempty"`
	DefaultLeadMinutes   int        `json:"default_lead_minutes,omitempty"`
	FallbackPolicy       string     `json:"fallback_policy,omitempty"`
	LastScheduledWakeAt  *time.Time `json:"last_scheduled_wake_at,omitempty"`
	LastScheduledTaskID  string     `json:"last_scheduled_task_id,omitempty"`
	LastScheduledOwner   string     `json:"last_scheduled_owner,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
}

// Settings holds application-wide configuration
type Settings struct {
	OpenAIAPIKey    string   `json:"openai_api_key"`
	AnthropicAPIKey string   `json:"anthropic_api_key"`
	GeminiAPIKey    string   `json:"gemini_api_key"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"` // CORS allowed origins (defaults to localhost)

	// System model settings - used for internal AI tasks (auto-config, suggestions, etc.)
	SystemProvider        string `json:"system_provider,omitempty"`         // Provider for system tasks (e.g., "openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm")
	SystemModel           string `json:"system_model,omitempty"`            // Model for system tasks (e.g., "gpt-4o-mini", "claude-3-haiku-20240307")
	SystemReasoningEffort string `json:"system_reasoning_effort,omitempty"` // Optional reasoning effort for system tasks (currently used by Codex: low, medium, high, xhigh)

	// Multi-agent orchestration defaults
	MultiAgentMode      string  `json:"multi_agent_mode,omitempty"`      // auto, force, off
	MultiAgentThreshold float64 `json:"multi_agent_threshold,omitempty"` // Complexity threshold (0-10)

	// Session cleanup settings
	SessionCleanupEnabled bool `json:"session_cleanup_enabled"` // Enable automatic cleanup of old sessions (default: true)
	SessionCleanupDays    int  `json:"session_cleanup_days"`    // Days of inactivity before session cleanup (default: 30)
	SessionMaxCount       int  `json:"session_max_count"`       // Maximum number of sessions to keep (0 = unlimited, default: 1000)

	// Web3 wallet settings
	Web3WalletAddress string `json:"web3_wallet_address,omitempty"` // Connected wallet address (0x...)
	Web3ChainID       int    `json:"web3_chain_id,omitempty"`       // Connected chain ID (1=Ethereum, 137=Polygon, etc.)
	Web3ENSName       string `json:"web3_ens_name,omitempty"`       // ENS name if available
	Web3ConnectedAt   string `json:"web3_connected_at,omitempty"`   // ISO timestamp of when wallet was connected

	// External agents settings
	ExternalAgentsClaudeEnabled bool `json:"external_agents_claude_enabled"` // Enable reading agents from Claude Code ~/.claude (default: false)
	ExternalAgentsCodexEnabled  bool `json:"external_agents_codex_enabled"`  // Enable reading agents from Codex CLI ~/.codex (default: false)

	// Speech settings
	SpeechProvider string `json:"speech_provider,omitempty"` // auto, browser, openai, off
	SpeechModel    string `json:"speech_model,omitempty"`    // Provider-specific model override
	SpeechLanguage string `json:"speech_language,omitempty"` // BCP-47 tag or "auto"

	// Workspace settings
	WorkspaceRoot string `json:"workspace_root,omitempty"` // Default directory for new workspace folders (e.g., ~/Documents/Ori Workspaces)
	VaultRoot     string `json:"vault_root,omitempty"`     // Default directory for new managed vault files
	TemplatesRoot string `json:"templates_root,omitempty"` // Directory holding project template folders (defaults to <app data>/templates)

	// Native utility settings
	Utility UtilitySettings `json:"utility,omitempty"`

	// macOS wake scheduling settings
	MacWake MacWakeSettings `json:"mac_wake,omitempty"`
}

// Manager handles configuration loading and saving
type Manager struct {
	mu          sync.RWMutex // Protects settings from concurrent access
	filePath    string
	settings    Settings
	secretStore vault.SecretStore
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

// NewManagerWithSecretStore creates a new manager with an attached secret store.
func NewManagerWithSecretStore(filePath string, secretStore vault.SecretStore) *Manager {
	manager := NewManager(filePath)
	manager.secretStore = secretStore
	return manager
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
		OpenAIAPIKey:          "",
		GeminiAPIKey:          "",
		SessionCleanupEnabled: true,
		SessionCleanupDays:    30,
		SessionMaxCount:       1000,
		MultiAgentMode:        "off",
		MultiAgentThreshold:   6.0,
		SpeechProvider:        "auto",
		SpeechLanguage:        "auto",
		Utility:               defaultUtilitySettings(),
		MacWake:               defaultMacWakeSettings(),
	}
}

func defaultMacWakeSettings() MacWakeSettings {
	return MacWakeSettings{
		DefaultLeadMinutes: 5,
		FallbackPolicy:     "run_on_next_wake",
	}
}

func defaultUtilitySettings() UtilitySettings {
	return UtilitySettings{
		Enabled:                 true,
		TimeoutMs:               5000,
		RetryAttempts:           1,
		RetryDelayMs:            150,
		SearchProvider:          "auto",
		BrowserControlProvider:  "auto",
		PlaywrightBrowser:       "auto",
		WeatherProvider:         "open-meteo",
		WebFetchMaxResponseSize: 1 << 20,
		BrowserMaxResponseSize:  1 << 20,
		BlockPrivateHosts:       true,
		UserAgent:               "ori-agent/utility-tools",
	}
}

// DefaultWorkspaceRoot returns the fallback directory used for new workspace folders.
func DefaultWorkspaceRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		if abs, err := filepath.Abs("workspaces"); err == nil {
			return abs
		}
		return "workspaces"
	}
	return filepath.Join(home, "Ori Workspaces")
}

// DefaultVaultRoot returns the fallback directory used for new managed vault files.
func DefaultVaultRoot() string {
	if dir := strings.TrimSpace(os.Getenv("ORI_DATA_DIR")); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return filepath.Join(abs, "vaults")
		}
		return filepath.Join(dir, "vaults")
	}

	cwd, err := os.Getwd()
	if err == nil {
		if abs, absErr := filepath.Abs(cwd); absErr == nil {
			return filepath.Join(abs, "vaults")
		}
		return filepath.Join(cwd, "vaults")
	}

	return filepath.Join(".", "vaults")
}

// DefaultTemplatesRoot returns the fallback directory holding project
// template folders.
func DefaultTemplatesRoot() string {
	if dir := strings.TrimSpace(os.Getenv("ORI_DATA_DIR")); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return filepath.Join(abs, "templates")
		}
		return filepath.Join(dir, "templates")
	}

	cwd, err := os.Getwd()
	if err == nil {
		if abs, absErr := filepath.Abs(cwd); absErr == nil {
			return filepath.Join(abs, "templates")
		}
		return filepath.Join(cwd, "templates")
	}

	return filepath.Join(".", "templates")
}

// NormalizeWorkspaceRoot expands and normalizes a configured workspace root path.
func NormalizeWorkspaceRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	expanded, err := platform.ExpandHome(path)
	if err != nil {
		return "", fmt.Errorf("failed to expand workspace directory: %w", err)
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace directory: %w", err)
	}

	return filepath.Clean(abs), nil
}

// NormalizeVaultRoot expands and normalizes a configured vault root path.
func NormalizeVaultRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	expanded, err := platform.ExpandHome(path)
	if err != nil {
		return "", fmt.Errorf("failed to expand vault directory: %w", err)
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("failed to resolve vault directory: %w", err)
	}

	return filepath.Clean(abs), nil
}

// NormalizeTemplatesRoot expands and normalizes a configured templates root path.
func NormalizeTemplatesRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	expanded, err := platform.ExpandHome(path)
	if err != nil {
		return "", fmt.Errorf("failed to expand templates directory: %w", err)
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("failed to resolve templates directory: %w", err)
	}

	return filepath.Clean(abs), nil
}

// ResolveTemplatesRoot determines the effective templates root using settings
// first, then ORI_TEMPLATES_DIR, then the built-in default.
func ResolveTemplatesRoot(configured string) string {
	if normalized, err := NormalizeTemplatesRoot(configured); err == nil && normalized != "" {
		return normalized
	}

	if envPath := strings.TrimSpace(os.Getenv("ORI_TEMPLATES_DIR")); envPath != "" {
		if normalized, err := NormalizeTemplatesRoot(envPath); err == nil && normalized != "" {
			return normalized
		}
		return envPath
	}

	return DefaultTemplatesRoot()
}

// ResolveWorkspaceRoot determines the effective workspace root using
// settings first, then WORKSPACE_DIR, then the built-in default.
func ResolveWorkspaceRoot(configured string) string {
	if normalized, err := NormalizeWorkspaceRoot(configured); err == nil && normalized != "" {
		return normalized
	}

	if envPath := strings.TrimSpace(os.Getenv("WORKSPACE_DIR")); envPath != "" {
		if normalized, err := NormalizeWorkspaceRoot(envPath); err == nil && normalized != "" {
			return normalized
		}
		return envPath
	}

	return DefaultWorkspaceRoot()
}

// ResolveVaultRoot determines the effective vault root using settings first,
// then ORI_VAULT_DIR, then the built-in default.
func ResolveVaultRoot(configured string) string {
	if normalized, err := NormalizeVaultRoot(configured); err == nil && normalized != "" {
		return normalized
	}

	if envPath := strings.TrimSpace(os.Getenv("ORI_VAULT_DIR")); envPath != "" {
		if normalized, err := NormalizeVaultRoot(envPath); err == nil && normalized != "" {
			return normalized
		}
		return envPath
	}

	return DefaultVaultRoot()
}

// WorkspaceRootSource reports where the effective workspace root comes from.
func WorkspaceRootSource(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return "settings"
	}
	if strings.TrimSpace(os.Getenv("WORKSPACE_DIR")) != "" {
		return "environment"
	}
	return "default"
}

// VaultRootSource reports where the effective vault root comes from.
func VaultRootSource(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return "settings"
	}
	if strings.TrimSpace(os.Getenv("ORI_VAULT_DIR")) != "" {
		return "environment"
	}
	return "default"
}

// Save writes current configuration to file
func (m *Manager) Save() error {
	m.mu.RLock()
	if err := m.validate(); err != nil {
		m.mu.RUnlock()
		return fmt.Errorf("cannot save invalid configuration: %w", err)
	}

	settingsForDisk := m.settings
	m.mu.RUnlock()

	if m.hasWritableSecretStore() {
		sanitizeSecretsForDisk(&settingsForDisk)
	}

	data, err := json.MarshalIndent(settingsForDisk, "", "  ")

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
	settings := m.settings
	secretStore := m.secretStore
	m.mu.RUnlock()

	applySecretsToSettings(&settings, secretStore)
	return settings
}

// Update modifies the configuration
func (m *Manager) Update(settings Settings) error {
	if err := m.ingestSecretFields(&settings); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
	return m.validate()
}

// SetSecretStore attaches or replaces the secret store backing this manager.
func (m *Manager) SetSecretStore(secretStore vault.SecretStore) {
	m.mu.Lock()
	m.secretStore = secretStore
	m.mu.Unlock()
}

// SecretStoreStatus reports the configured secret store state.
func (m *Manager) SecretStoreStatus() vault.StoreStatus {
	m.mu.RLock()
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretStore == nil {
		return vault.StoreStatus{
			Backend:   vault.BackendUnavailable,
			Available: false,
			Writable:  false,
			Locked:    true,
			Message:   "no secret store configured",
		}
	}
	return secretStore.Status()
}

// SecretStore returns the attached secret store reference for subsystems that
// need to share the same secure storage namespace.
func (m *Manager) SecretStore() vault.SecretStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.secretStore
}

// GetWorkspaceRoot returns the explicitly configured workspace root, if any.
func (m *Manager) GetWorkspaceRoot() string {
	m.mu.RLock()
	raw := strings.TrimSpace(m.settings.WorkspaceRoot)
	m.mu.RUnlock()

	if raw == "" {
		return ""
	}

	normalized, err := NormalizeWorkspaceRoot(raw)
	if err != nil {
		return raw
	}
	return normalized
}

// GetVaultRoot returns the explicitly configured vault root, if any.
func (m *Manager) GetVaultRoot() string {
	m.mu.RLock()
	raw := strings.TrimSpace(m.settings.VaultRoot)
	m.mu.RUnlock()

	if raw == "" {
		return ""
	}

	normalized, err := NormalizeVaultRoot(raw)
	if err != nil {
		return raw
	}
	return normalized
}

// GetTemplatesRoot returns the explicitly configured templates root, if any.
func (m *Manager) GetTemplatesRoot() string {
	m.mu.RLock()
	raw := strings.TrimSpace(m.settings.TemplatesRoot)
	m.mu.RUnlock()

	if raw == "" {
		return ""
	}

	normalized, err := NormalizeTemplatesRoot(raw)
	if err != nil {
		return raw
	}
	return normalized
}

// SetTemplatesRoot updates the configured project templates directory.
func (m *Manager) SetTemplatesRoot(path string) error {
	normalized, err := NormalizeTemplatesRoot(path)
	if err != nil {
		return err
	}

	if normalized != "" {
		info, statErr := os.Stat(normalized)
		if statErr == nil && !info.IsDir() {
			return fmt.Errorf("templates directory must be a folder")
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect templates directory: %w", statErr)
		}
	}

	m.mu.Lock()
	m.settings.TemplatesRoot = normalized
	m.mu.Unlock()
	return nil
}

// SetWorkspaceRoot updates the configured default workspace directory.
func (m *Manager) SetWorkspaceRoot(path string) error {
	normalized, err := NormalizeWorkspaceRoot(path)
	if err != nil {
		return err
	}

	if normalized != "" {
		info, statErr := os.Stat(normalized)
		if statErr == nil && !info.IsDir() {
			return fmt.Errorf("workspace directory must be a folder")
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect workspace directory: %w", statErr)
		}
	}

	m.mu.Lock()
	m.settings.WorkspaceRoot = normalized
	m.mu.Unlock()
	return nil
}

// SetVaultRoot updates the configured default vault directory.
func (m *Manager) SetVaultRoot(path string) error {
	normalized, err := NormalizeVaultRoot(path)
	if err != nil {
		return err
	}

	if normalized != "" {
		info, statErr := os.Stat(normalized)
		if statErr == nil && !info.IsDir() {
			return fmt.Errorf("vault directory must be a folder")
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect vault directory: %w", statErr)
		}
	}

	m.mu.Lock()
	m.settings.VaultRoot = normalized
	m.mu.Unlock()
	return nil
}

// GetAPIKey returns the OpenAI API key, checking settings first, then environment variable, then discovery
func (m *Manager) GetAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.OpenAIAPIKey
	codexEnabled := m.settings.ExternalAgentsCodexEnabled
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretValue, ok := getSecret(secretStore, vault.SecretKeyOpenAIAPIKey); ok {
		return secretValue
	}

	// Check settings first
	if apiKey != "" {
		return apiKey
	}

	// Fallback to environment variable
	if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
		return envKey
	}

	// Fallback to discovery if enabled
	if codexEnabled {
		if token := authdiscovery.DiscoverOpenAIToken(); token != "" && isLikelyOpenAIAPIKey(token) {
			return token
		}
	}

	return ""
}

func isLikelyOpenAIAPIKey(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "sk-")
}

func isLikelyAnthropicAPIKey(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "sk-ant-")
}

// GetAnthropicAPIKey returns the Anthropic API key, checking settings first, then environment variable, then discovery
func (m *Manager) GetAnthropicAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.AnthropicAPIKey
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretValue, ok := getSecret(secretStore, vault.SecretKeyAnthropicAPIKey); ok {
		return secretValue
	}

	// Check settings first
	if apiKey != "" {
		return apiKey
	}

	// Fallback to environment variable
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		return envKey
	}

	// Fallback to discovery for API keys only
	if token := authdiscovery.DiscoverAnthropicToken(); token != "" && isLikelyAnthropicAPIKey(token) {
		return token
	}

	return ""
}

// GetGeminiAPIKey returns the Gemini API key, checking settings first, then environment variable
func (m *Manager) GetGeminiAPIKey() string {
	m.mu.RLock()
	apiKey := m.settings.GeminiAPIKey
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretValue, ok := getSecret(secretStore, vault.SecretKeyGeminiAPIKey); ok {
		return secretValue
	}

	// Check settings first
	if apiKey != "" {
		return apiKey
	}

	// Fallback to environment variable
	return os.Getenv("GEMINI_API_KEY")
}

// SetAPIKey updates the API key in settings
func (m *Manager) SetAPIKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if err := m.validateAPIKey(apiKey); err != nil {
		return err
	}
	if err := m.setManagedSecret(vault.SecretKeyOpenAIAPIKey, apiKey); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasWritableSecretStoreLocked() {
		m.settings.OpenAIAPIKey = ""
	} else {
		m.settings.OpenAIAPIKey = apiKey
	}
	m.mu.Unlock()
	return nil
}

// SetAnthropicAPIKey updates the Anthropic API key in settings or secret storage.
func (m *Manager) SetAnthropicAPIKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if err := m.setManagedSecret(vault.SecretKeyAnthropicAPIKey, apiKey); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasWritableSecretStoreLocked() {
		m.settings.AnthropicAPIKey = ""
	} else {
		m.settings.AnthropicAPIKey = apiKey
	}
	m.mu.Unlock()
	return nil
}

// SetGeminiAPIKey updates the Gemini API key in settings
func (m *Manager) SetGeminiAPIKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if err := m.setManagedSecret(vault.SecretKeyGeminiAPIKey, apiKey); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasWritableSecretStoreLocked() {
		m.settings.GeminiAPIKey = ""
	} else {
		m.settings.GeminiAPIKey = apiKey
	}
	m.mu.Unlock()
	return nil
}

// GetBraveAPIKey returns the Brave API key, checking secret storage first, then settings, then environment.
func (m *Manager) GetBraveAPIKey() string {
	m.mu.RLock()
	braveAPIKey := strings.TrimSpace(m.settings.Utility.BraveAPIKey)
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretValue, ok := getSecret(secretStore, vault.SecretKeyBraveAPIKey); ok {
		return secretValue
	}
	if braveAPIKey != "" {
		return braveAPIKey
	}
	return strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
}

// SetBraveAPIKey updates the Brave API key in settings or secret storage.
func (m *Manager) SetBraveAPIKey(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if err := m.setManagedSecret(vault.SecretKeyBraveAPIKey, apiKey); err != nil {
		return err
	}
	m.mu.Lock()
	if m.hasWritableSecretStoreLocked() {
		m.settings.Utility.BraveAPIKey = ""
	} else {
		m.settings.Utility.BraveAPIKey = apiKey
	}
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
	if m.settings.MultiAgentMode == "" {
		m.settings.MultiAgentMode = "off"
	}
	switch m.settings.MultiAgentMode {
	case "auto", "force", "off":
	default:
		m.settings.MultiAgentMode = "off"
	}
	if m.settings.MultiAgentThreshold <= 0 {
		m.settings.MultiAgentThreshold = 6.0
	}
	if m.settings.MultiAgentThreshold > 10 {
		m.settings.MultiAgentThreshold = 10
	}

	if m.settings.SpeechProvider == "" {
		m.settings.SpeechProvider = "auto"
	}
	switch m.settings.SpeechProvider {
	case "auto", "browser", "openai", "off":
	default:
		m.settings.SpeechProvider = "auto"
	}
	if m.settings.SpeechLanguage == "" {
		m.settings.SpeechLanguage = "auto"
	}

	if m.settings.WorkspaceRoot != "" {
		normalized, err := NormalizeWorkspaceRoot(m.settings.WorkspaceRoot)
		if err == nil {
			m.settings.WorkspaceRoot = normalized
		}
	}
	if m.settings.VaultRoot != "" {
		normalized, err := NormalizeVaultRoot(m.settings.VaultRoot)
		if err == nil {
			m.settings.VaultRoot = normalized
		}
	}
	if m.settings.TemplatesRoot != "" {
		normalized, err := NormalizeTemplatesRoot(m.settings.TemplatesRoot)
		if err == nil {
			m.settings.TemplatesRoot = normalized
		}
	}

	validateUtilitySettings(&m.settings.Utility)
	validateMacWakeSettings(&m.settings.MacWake)

	if m.settings.SystemReasoningEffort != "" {
		effort := strings.ToLower(strings.TrimSpace(m.settings.SystemReasoningEffort))
		switch effort {
		case "low", "medium", "high", "xhigh":
			m.settings.SystemReasoningEffort = effort
		default:
			m.settings.SystemReasoningEffort = ""
		}
	}

	return m.validateAPIKey(m.settings.OpenAIAPIKey)
}

func validateUtilitySettings(settings *UtilitySettings) {
	if settings == nil {
		return
	}

	defaults := defaultUtilitySettings()
	if settings.TimeoutMs <= 0 {
		settings.TimeoutMs = defaults.TimeoutMs
	}
	if settings.TimeoutMs > 60000 {
		settings.TimeoutMs = 60000
	}
	if settings.RetryAttempts < 0 {
		settings.RetryAttempts = defaults.RetryAttempts
	}
	if settings.RetryAttempts > 5 {
		settings.RetryAttempts = 5
	}
	if settings.RetryDelayMs <= 0 {
		settings.RetryDelayMs = defaults.RetryDelayMs
	}
	if settings.RetryDelayMs > 5000 {
		settings.RetryDelayMs = 5000
	}

	searchProvider := strings.ToLower(strings.TrimSpace(settings.SearchProvider))
	switch searchProvider {
	case "", "auto", "duckduckgo", "brave":
		if searchProvider == "" {
			searchProvider = defaults.SearchProvider
		}
		settings.SearchProvider = searchProvider
	default:
		settings.SearchProvider = defaults.SearchProvider
	}

	browserControlProvider := strings.ToLower(strings.TrimSpace(settings.BrowserControlProvider))
	switch browserControlProvider {
	case "", "auto", "playwright", "browserbase", "puppeteer":
		if browserControlProvider == "" {
			browserControlProvider = defaults.BrowserControlProvider
		}
		settings.BrowserControlProvider = browserControlProvider
	default:
		settings.BrowserControlProvider = defaults.BrowserControlProvider
	}

	playwrightBrowser := strings.ToLower(strings.TrimSpace(settings.PlaywrightBrowser))
	switch playwrightBrowser {
	case "", "auto", "chrome", "firefox", "webkit", "msedge", "brave":
		if playwrightBrowser == "" {
			playwrightBrowser = defaults.PlaywrightBrowser
		}
		settings.PlaywrightBrowser = playwrightBrowser
	default:
		settings.PlaywrightBrowser = defaults.PlaywrightBrowser
	}
	settings.PlaywrightExecutable = strings.TrimSpace(settings.PlaywrightExecutable)

	weatherProvider := strings.ToLower(strings.TrimSpace(settings.WeatherProvider))
	if weatherProvider == "" {
		weatherProvider = defaults.WeatherProvider
	}
	if weatherProvider != "open-meteo" {
		weatherProvider = defaults.WeatherProvider
	}
	settings.WeatherProvider = weatherProvider

	settings.BraveAPIKey = strings.TrimSpace(settings.BraveAPIKey)
	settings.WeatherGeocodingURL = strings.TrimSpace(settings.WeatherGeocodingURL)
	settings.WeatherForecastURL = strings.TrimSpace(settings.WeatherForecastURL)

	if settings.WebFetchMaxResponseSize <= 0 {
		settings.WebFetchMaxResponseSize = defaults.WebFetchMaxResponseSize
	}
	if settings.WebFetchMaxResponseSize > 8*(1<<20) {
		settings.WebFetchMaxResponseSize = 8 * (1 << 20)
	}

	if settings.BrowserMaxResponseSize <= 0 {
		settings.BrowserMaxResponseSize = defaults.BrowserMaxResponseSize
	}
	if settings.BrowserMaxResponseSize > 8*(1<<20) {
		settings.BrowserMaxResponseSize = 8 * (1 << 20)
	}

	if strings.TrimSpace(settings.UserAgent) == "" {
		settings.UserAgent = defaults.UserAgent
	}

	cleanDomains := make([]string, 0, len(settings.BrowserAllowedDomains))
	for _, raw := range settings.BrowserAllowedDomains {
		domain := strings.ToLower(strings.TrimSpace(raw))
		if domain == "" {
			continue
		}
		cleanDomains = append(cleanDomains, domain)
	}
	settings.BrowserAllowedDomains = cleanDomains
}

func validateMacWakeSettings(settings *MacWakeSettings) {
	if settings == nil {
		return
	}

	defaults := defaultMacWakeSettings()
	if settings.DefaultLeadMinutes <= 0 {
		settings.DefaultLeadMinutes = defaults.DefaultLeadMinutes
	}
	if settings.DefaultLeadMinutes > 120 {
		settings.DefaultLeadMinutes = 120
	}

	fallbackPolicy := strings.ToLower(strings.TrimSpace(settings.FallbackPolicy))
	switch fallbackPolicy {
	case "run_on_next_wake", "skip":
		settings.FallbackPolicy = fallbackPolicy
	default:
		settings.FallbackPolicy = defaults.FallbackPolicy
	}

	settings.LastScheduledTaskID = strings.TrimSpace(settings.LastScheduledTaskID)
	settings.LastScheduledOwner = strings.TrimSpace(settings.LastScheduledOwner)
	settings.LastError = strings.TrimSpace(settings.LastError)
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
	if secretValue, ok := getSecret(m.secretStoreRef(), vault.SecretKeyOpenAIAPIKey); ok {
		if len(secretValue) < 8 {
			return "***"
		}
		return secretValue[:8] + "***..." + secretValue[len(secretValue)-4:]
	}

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

func (m *Manager) secretStoreRef() vault.SecretStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.secretStore
}

func (m *Manager) hasWritableSecretStore() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasWritableSecretStoreLocked()
}

func (m *Manager) hasWritableSecretStoreLocked() bool {
	if m.secretStore == nil {
		return false
	}
	status := m.secretStore.Status()
	return status.Available && status.Writable && !status.Locked
}

func (m *Manager) setManagedSecret(key vault.SecretKey, value string) error {
	m.mu.RLock()
	secretStore := m.secretStore
	m.mu.RUnlock()

	if secretStore == nil {
		return nil
	}
	status := secretStore.Status()
	if !status.Available || !status.Writable || status.Locked {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return secretStore.Delete(key)
	}
	return secretStore.Set(key, value)
}

func (m *Manager) ingestSecretFields(settings *Settings) error {
	if settings == nil {
		return nil
	}

	if err := m.validateAPIKey(strings.TrimSpace(settings.OpenAIAPIKey)); err != nil {
		return err
	}
	if err := m.setManagedSecret(vault.SecretKeyOpenAIAPIKey, settings.OpenAIAPIKey); err != nil {
		return err
	}
	if err := m.setManagedSecret(vault.SecretKeyAnthropicAPIKey, settings.AnthropicAPIKey); err != nil {
		return err
	}
	if err := m.setManagedSecret(vault.SecretKeyGeminiAPIKey, settings.GeminiAPIKey); err != nil {
		return err
	}
	if err := m.setManagedSecret(vault.SecretKeyBraveAPIKey, settings.Utility.BraveAPIKey); err != nil {
		return err
	}

	if m.hasWritableSecretStore() {
		settings.OpenAIAPIKey = ""
		settings.AnthropicAPIKey = ""
		settings.GeminiAPIKey = ""
		settings.Utility.BraveAPIKey = ""
	}
	return nil
}

func applySecretsToSettings(settings *Settings, secretStore vault.SecretStore) {
	if settings == nil || secretStore == nil {
		return
	}
	if secretValue, ok := getSecret(secretStore, vault.SecretKeyOpenAIAPIKey); ok {
		settings.OpenAIAPIKey = secretValue
	}
	if secretValue, ok := getSecret(secretStore, vault.SecretKeyAnthropicAPIKey); ok {
		settings.AnthropicAPIKey = secretValue
	}
	if secretValue, ok := getSecret(secretStore, vault.SecretKeyGeminiAPIKey); ok {
		settings.GeminiAPIKey = secretValue
	}
	if secretValue, ok := getSecret(secretStore, vault.SecretKeyBraveAPIKey); ok {
		settings.Utility.BraveAPIKey = secretValue
	}
}

func sanitizeSecretsForDisk(settings *Settings) {
	if settings == nil {
		return
	}
	settings.OpenAIAPIKey = ""
	settings.AnthropicAPIKey = ""
	settings.GeminiAPIKey = ""
	settings.Utility.BraveAPIKey = ""
}

func getSecret(secretStore vault.SecretStore, key vault.SecretKey) (string, bool) {
	if secretStore == nil {
		return "", false
	}
	value, err := secretStore.Get(key)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
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

// GetMultiAgentDefaults returns the default multi-agent mode and threshold.
func (m *Manager) GetMultiAgentDefaults() (mode string, threshold float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.MultiAgentMode, m.settings.MultiAgentThreshold
}

// SetMultiAgentDefaults updates the default multi-agent mode and threshold.
func (m *Manager) SetMultiAgentDefaults(mode string, threshold float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode != "" {
		m.settings.MultiAgentMode = mode
	}
	if threshold > 0 {
		m.settings.MultiAgentThreshold = threshold
	}
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
	if provider == "" || model == "" || !strings.EqualFold(provider, "codex") {
		m.settings.SystemReasoningEffort = ""
	}
	m.mu.Unlock()
	return nil
}

// GetSystemReasoningEffort returns the configured reasoning effort for system tasks.
// Defaults to "medium" when unset or invalid.
func (m *Manager) GetSystemReasoningEffort() string {
	m.mu.RLock()
	effort := strings.TrimSpace(strings.ToLower(m.settings.SystemReasoningEffort))
	m.mu.RUnlock()

	switch effort {
	case "low", "medium", "high", "xhigh":
		return effort
	default:
		return "medium"
	}
}

// SetSystemReasoningEffort updates the system reasoning effort.
// Empty string clears the override and falls back to defaults.
func (m *Manager) SetSystemReasoningEffort(effort string) error {
	effort = strings.TrimSpace(strings.ToLower(effort))
	if effort == "" {
		m.mu.Lock()
		m.settings.SystemReasoningEffort = ""
		m.mu.Unlock()
		return nil
	}

	switch effort {
	case "low", "medium", "high", "xhigh":
		m.mu.Lock()
		m.settings.SystemReasoningEffort = effort
		m.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("invalid system reasoning effort %q: must be one of [low medium high xhigh]", effort)
	}
}

// IsSystemModelConfigured returns true if both system provider and model are set
func (m *Manager) IsSystemModelConfigured() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.SystemProvider != "" && m.settings.SystemModel != ""
}

// ValidProviders returns the list of valid provider names for system model
func ValidProviders() []string {
	return []string{"openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm"}
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

	// Basic model format validation - must contain only alphanumeric, dash,
	// underscore, dot, colon, or slash. Slash is needed for local model IDs
	// such as "mlx-community/Llama-3.2-3B-Instruct-4bit".
	for _, char := range model {
		isLower := char >= 'a' && char <= 'z'
		isUpper := char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		isAllowed := char == '-' || char == '_' || char == '.' || char == ':' || char == '/'
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

// GetExternalAgentsClaudeEnabled returns whether Claude Code agents reading is enabled
func (m *Manager) GetExternalAgentsClaudeEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.ExternalAgentsClaudeEnabled
}

// SetExternalAgentsClaudeEnabled updates the Claude Code agents enabled setting
func (m *Manager) SetExternalAgentsClaudeEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.ExternalAgentsClaudeEnabled = enabled
}

// GetExternalAgentsCodexEnabled returns whether Codex CLI agents reading is enabled
func (m *Manager) GetExternalAgentsCodexEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings.ExternalAgentsCodexEnabled
}

// SetExternalAgentsCodexEnabled updates the Codex CLI agents enabled setting
func (m *Manager) SetExternalAgentsCodexEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings.ExternalAgentsCodexEnabled = enabled
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
