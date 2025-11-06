package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigManager handles loading and saving MCP server configurations
type ConfigManager struct {
	globalConfigPath string // mcp_registry.json
	agentConfigDir   string // agents/{name}/
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(baseDir string) *ConfigManager {
	return &ConfigManager{
		globalConfigPath: filepath.Join(baseDir, "mcp_registry.json"),
		agentConfigDir:   filepath.Join(baseDir, "agents"),
	}
}

// GlobalConfig represents the global MCP server registry
type GlobalConfig struct {
	Servers []ServerConfig `json:"servers"`
}

// AgentMCPConfig represents per-agent MCP server enablement
type AgentMCPConfig struct {
	EnabledServers []string `json:"enabled_servers"`
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

// LoadAgentConfig loads per-agent MCP configuration
func (cm *ConfigManager) LoadAgentConfig(agentName string) (*AgentMCPConfig, error) {
	configPath := filepath.Join(cm.agentConfigDir, agentName, "mcp_servers.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &AgentMCPConfig{EnabledServers: []string{}}, nil
		}
		return nil, fmt.Errorf("failed to read agent config: %w", err)
	}

	var config AgentMCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse agent config: %w", err)
	}

	return &config, nil
}

// SaveAgentConfig saves per-agent MCP configuration
func (cm *ConfigManager) SaveAgentConfig(agentName string, config *AgentMCPConfig) error {
	agentDir := filepath.Join(cm.agentConfigDir, agentName)
	configPath := filepath.Join(agentDir, "mcp_servers.json")

	// Ensure agent directory exists
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// AddServer adds a server to the global registry
func (cm *ConfigManager) AddServer(server ServerConfig) error {
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

// EnableServerForAgent enables an MCP server for a specific agent
func (cm *ConfigManager) EnableServerForAgent(agentName, serverName string) error {
	config, err := cm.LoadAgentConfig(agentName)
	if err != nil {
		return err
	}

	// Check if already enabled
	for _, s := range config.EnabledServers {
		if s == serverName {
			return nil // Already enabled
		}
	}

	config.EnabledServers = append(config.EnabledServers, serverName)
	return cm.SaveAgentConfig(agentName, config)
}

// DisableServerForAgent disables an MCP server for a specific agent
func (cm *ConfigManager) DisableServerForAgent(agentName, serverName string) error {
	config, err := cm.LoadAgentConfig(agentName)
	if err != nil {
		return err
	}

	newEnabled := make([]string, 0, len(config.EnabledServers))
	for _, s := range config.EnabledServers {
		if s != serverName {
			newEnabled = append(newEnabled, s)
		}
	}

	config.EnabledServers = newEnabled
	return cm.SaveAgentConfig(agentName, config)
}

// IsServerEnabledForAgent checks if a server is enabled for an agent
func (cm *ConfigManager) IsServerEnabledForAgent(agentName, serverName string) (bool, error) {
	config, err := cm.LoadAgentConfig(agentName)
	if err != nil {
		return false, err
	}

	for _, s := range config.EnabledServers {
		if s == serverName {
			return true, nil
		}
	}

	return false, nil
}

// GetEnabledServersForAgent returns all enabled servers for an agent
func (cm *ConfigManager) GetEnabledServersForAgent(agentName string) ([]ServerConfig, error) {
	agentConfig, err := cm.LoadAgentConfig(agentName)
	if err != nil {
		return nil, err
	}

	globalConfig, err := cm.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	enabledMap := make(map[string]bool)
	for _, name := range agentConfig.EnabledServers {
		enabledMap[name] = true
	}

	var enabledServers []ServerConfig
	for _, server := range globalConfig.Servers {
		if enabledMap[server.Name] {
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

	// If already has servers, don't overwrite
	if len(config.Servers) > 0 {
		return nil
	}

	// Add default filesystem server (disabled by default for security)
	defaultServers := []ServerConfig{
		{
			Name:      "filesystem",
			Command:   "npx",
			Args:      []string{"-y", "@modelcontextprotocol/server-filesystem"},
			Env:       make(map[string]string),
			Transport: "stdio",
			Enabled:   false,
		},
	}

	config.Servers = defaultServers
	return cm.SaveGlobalConfig(config)
}
