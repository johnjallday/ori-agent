package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ConfigManager handles loading and saving MCP server configurations
type ConfigManager struct {
	globalConfigPath string // mcp_registry.json
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(baseDir string) *ConfigManager {
	return &ConfigManager{
		globalConfigPath: filepath.Join(baseDir, "mcp_registry.json"),
	}
}

// GlobalConfig represents the global MCP server registry
type GlobalConfig struct {
	Servers []ServerConfig `json:"servers"`
}

type externalServerConfig struct {
	Command   string         `toml:"command"`
	Args      []string       `toml:"args"`
	Env       map[string]any `toml:"env"`
	Transport string         `toml:"transport"`
	URL       string         `toml:"url"`
}

type codexConfig struct {
	MCPServers map[string]externalServerConfig `toml:"mcp_servers"`
}

// LoadGlobalConfig loads the global MCP server registry
func (cm *ConfigManager) LoadGlobalConfig() (*GlobalConfig, error) {
	data, err := os.ReadFile(cm.globalConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &GlobalConfig{Servers: []ServerConfig{}}, nil
		}
		return nil, fmt.Errorf("failed to read global config: %w", err)
	}

	var config GlobalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse global config: %w", err)
	}

	return &config, nil
}

// SaveGlobalConfig saves the global MCP server registry
func (cm *ConfigManager) SaveGlobalConfig(config *GlobalConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(cm.globalConfigPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// AddServer adds a server to the global registry
func (cm *ConfigManager) AddServer(server ServerConfig) error {
	if err := ValidateServerConfig(server); err != nil {
		return err
	}

	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return err
	}

	// Check if server already exists
	for _, s := range config.Servers {
		if s.Name == server.Name {
			return fmt.Errorf("server %s already exists", server.Name)
		}
	}

	config.Servers = append(config.Servers, server)
	return cm.SaveGlobalConfig(config)
}

// RemoveServer removes a server from the global registry
func (cm *ConfigManager) RemoveServer(name string) error {
	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return err
	}

	found := false
	newServers := make([]ServerConfig, 0, len(config.Servers))
	for _, s := range config.Servers {
		if s.Name == name {
			found = true
			continue
		}
		newServers = append(newServers, s)
	}

	if !found {
		return fmt.Errorf("server %s not found", name)
	}

	config.Servers = newServers
	return cm.SaveGlobalConfig(config)
}

// UpdateServer updates a server in the global registry
func (cm *ConfigManager) UpdateServer(server ServerConfig) error {
	if err := ValidateServerConfig(server); err != nil {
		return err
	}

	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return err
	}

	found := false
	for i, s := range config.Servers {
		if s.Name == server.Name {
			config.Servers[i] = server
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("server %s not found", server.Name)
	}

	return cm.SaveGlobalConfig(config)
}

// GetServer retrieves a server from the global registry
func (cm *ConfigManager) GetServer(name string) (*ServerConfig, error) {
	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	for _, s := range config.Servers {
		if s.Name == name {
			return &s, nil
		}
	}

	return nil, fmt.Errorf("server %s not found", name)
}

// SetServerEnabled updates the global enabled state for a server and persists it.
func (cm *ConfigManager) SetServerEnabled(serverName string, enabled bool) (*ServerConfig, error) {
	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	for i, server := range config.Servers {
		if server.Name != serverName {
			continue
		}
		config.Servers[i].Enabled = enabled
		if err := cm.SaveGlobalConfig(config); err != nil {
			return nil, err
		}
		updated := config.Servers[i]
		return &updated, nil
	}

	return nil, fmt.Errorf("server %s not found", serverName)
}

// GetEnabledServers returns globally enabled MCP server definitions.
func (cm *ConfigManager) GetEnabledServers() ([]ServerConfig, error) {
	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	enabledServers := make([]ServerConfig, 0, len(config.Servers))
	for _, server := range config.Servers {
		if server.Enabled {
			enabledServers = append(enabledServers, server)
		}
	}

	return enabledServers, nil
}

// InitializeDefaultServers creates default MCP server configurations
func (cm *ConfigManager) InitializeDefaultServers() error {
	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return err
	}

	// Get user's home directory for default allowed directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp" // Fallback
	}

	defaultServers := defaultMCPServers(homeDir)
	existing := make(map[string]struct{}, len(config.Servers))
	for _, server := range config.Servers {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			continue
		}
		existing[name] = struct{}{}
	}

	added := false
	for _, server := range defaultServers {
		if _, ok := existing[server.Name]; ok {
			continue
		}
		config.Servers = append(config.Servers, server)
		added = true
	}
	if !added {
		return nil
	}

	return cm.SaveGlobalConfig(config)
}

func defaultMCPServers(homeDir string) []ServerConfig {
	if strings.TrimSpace(homeDir) == "" {
		homeDir = "/tmp"
	}

	return []ServerConfig{
		{
			Name:      "filesystem",
			Command:   "npx",
			Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", homeDir},
			Env:       make(map[string]string),
			Transport: "stdio",
			Enabled:   false,
		},
		{
			Name:      "fetch",
			Command:   "uvx",
			Args:      []string{"mcp-server-fetch"},
			Env:       make(map[string]string),
			Transport: "stdio",
			Enabled:   false,
		},
	}
}

// ImportExternalGlobalServers imports MCP server definitions from external/global
// tool configs (e.g. Codex, Claude Desktop) into Ori's global MCP registry.
func (cm *ConfigManager) ImportExternalGlobalServers() (int, error) {
	externalServers, loadErr := loadExternalGlobalServers()
	if len(externalServers) == 0 {
		return 0, loadErr
	}

	config, err := cm.LoadGlobalConfig()
	if err != nil {
		return 0, err
	}

	existing := make(map[string]struct{}, len(config.Servers))
	for _, server := range config.Servers {
		existing[server.Name] = struct{}{}
	}

	added := 0
	for _, server := range externalServers {
		if _, ok := existing[server.Name]; ok {
			continue
		}
		config.Servers = append(config.Servers, server)
		existing[server.Name] = struct{}{}
		added++
	}

	if added == 0 {
		return 0, loadErr
	}

	if err := cm.SaveGlobalConfig(config); err != nil {
		return 0, err
	}

	return added, loadErr
}

func loadExternalGlobalServers() ([]ServerConfig, error) {
	sources := []func() ([]ServerConfig, error){
		loadCodexServers,
		loadClaudeDesktopServers,
	}

	servers := make([]ServerConfig, 0)
	seen := make(map[string]struct{})
	var warnings []string

	for _, source := range sources {
		discovered, err := source()
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}

		for _, server := range discovered {
			if _, ok := seen[server.Name]; ok {
				continue
			}
			seen[server.Name] = struct{}{}
			servers = append(servers, server)
		}
	}

	if len(warnings) > 0 {
		return servers, fmt.Errorf("some external MCP sources failed: %s", strings.Join(warnings, "; "))
	}

	return servers, nil
}

func loadCodexServers() ([]ServerConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir for codex MCP import: %w", err)
	}

	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ServerConfig{}, nil
		}
		return nil, fmt.Errorf("read codex config: %w", err)
	}

	var cfg codexConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("parse codex config: %w", err)
	}

	servers := make([]ServerConfig, 0, len(cfg.MCPServers))
	for name, raw := range cfg.MCPServers {
		server, ok := convertExternalServer(name, raw)
		if !ok {
			continue
		}
		servers = append(servers, server)
	}

	return servers, nil
}

func loadClaudeDesktopServers() ([]ServerConfig, error) {
	configPath, err := claudeDesktopConfigPath()
	if err != nil {
		return nil, fmt.Errorf("resolve claude desktop config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ServerConfig{}, nil
		}
		return nil, fmt.Errorf("read claude desktop config: %w", err)
	}

	var raw struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse claude desktop config: %w", err)
	}

	servers := make([]ServerConfig, 0, len(raw.MCPServers))
	for name, value := range raw.MCPServers {
		server, ok := convertExternalServer(name, externalServerConfig{
			Command:   mapString(value, "command"),
			Args:      mapStringSlice(value, "args"),
			Env:       mapObject(value, "env"),
			Transport: mapString(value, "transport"),
			URL:       mapString(value, "url"),
		})
		if !ok {
			continue
		}
		servers = append(servers, server)
	}

	return servers, nil
}

func claudeDesktopConfigPath() (string, error) {
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		return filepath.Join(appData, "Claude", "claude_desktop_config.json"), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", "Claude", "claude_desktop_config.json"), nil
	default:
		return filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json"), nil
	}
}

func convertExternalServer(name string, raw externalServerConfig) (ServerConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServerConfig{}, false
	}

	command := strings.TrimSpace(raw.Command)
	transport := normalizeTransport(raw.Transport, raw.URL)
	if command == "" || transport != "stdio" {
		return ServerConfig{}, false
	}

	args := make([]string, 0, len(raw.Args))
	for _, arg := range raw.Args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		args = append(args, trimmed)
	}

	env := make(map[string]string)
	for key, value := range raw.Env {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" || value == nil {
			continue
		}
		env[trimmedKey] = fmt.Sprint(value)
	}

	return ServerConfig{
		Name:      name,
		Command:   command,
		Args:      args,
		Env:       env,
		Transport: "stdio",
		Enabled:   false,
	}, true
}

func normalizeTransport(rawTransport, rawURL string) string {
	transport := strings.ToLower(strings.TrimSpace(rawTransport))
	if transport == "" {
		transport = "stdio"
	}
	if transport == "stdio" && strings.TrimSpace(rawURL) != "" {
		// URL-backed servers are typically SSE/HTTP; stdio server startup cannot run them.
		transport = "sse"
	}
	return transport
}

func mapString(data map[string]any, key string) string {
	raw, ok := data[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		return fmt.Sprint(value)
	}
}

func mapStringSlice(data map[string]any, key string) []string {
	raw, ok := data[key]
	if !ok || raw == nil {
		return nil
	}

	values, ok := raw.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		out = append(out, fmt.Sprint(value))
	}

	return out
}

func mapObject(data map[string]any, key string) map[string]any {
	raw, ok := data[key]
	if !ok || raw == nil {
		return nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return value
}
