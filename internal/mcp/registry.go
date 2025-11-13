package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// Registry manages multiple MCP servers and their tools
type Registry struct {
	servers map[string]*Server // server name -> server instance
	mu      sync.RWMutex
}

// NewRegistry creates a new MCP server registry
func NewRegistry() *Registry {
	return &Registry{
		servers: make(map[string]*Server),
	}
}

// AddServer adds a new MCP server to the registry
func (r *Registry) AddServer(config ServerConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[config.Name]; exists {
		return fmt.Errorf("server %s already exists", config.Name)
	}

	server := NewServer(config)
	r.servers[config.Name] = server

	return nil
}

// RemoveServer removes an MCP server from the registry
func (r *Registry) RemoveServer(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	server, exists := r.servers[name]
	if !exists {
		return fmt.Errorf("server %s not found", name)
	}

	// Stop the server if it's running
	if err := server.Stop(); err != nil {
		return fmt.Errorf("failed to stop server: %w", err)
	}

	delete(r.servers, name)
	return nil
}

// GetServer retrieves an MCP server by name
func (r *Registry) GetServer(name string) (*Server, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	server, exists := r.servers[name]
	if !exists {
		return nil, fmt.Errorf("server %s not found", name)
	}

	return server, nil
}

// ListServers returns all registered servers
func (r *Registry) ListServers() []ServerConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	configs := make([]ServerConfig, 0, len(r.servers))
	for _, server := range r.servers {
		configs = append(configs, server.GetConfig())
	}

	return configs
}

// StartServer starts an MCP server by name
func (r *Registry) StartServer(name string) error {
	server, err := r.GetServer(name)
	if err != nil {
		return err
	}

	return server.Start()
}

// StopServer stops an MCP server by name
func (r *Registry) StopServer(name string) error {
	server, err := r.GetServer(name)
	if err != nil {
		return err
	}

	return server.Stop()
}

// RestartServer restarts an MCP server by name
func (r *Registry) RestartServer(name string) error {
	server, err := r.GetServer(name)
	if err != nil {
		return err
	}

	return server.Restart()
}

// GetServerStatus returns the status of an MCP server
func (r *Registry) GetServerStatus(name string) (ServerStatus, error) {
	server, err := r.GetServer(name)
	if err != nil {
		return StatusStopped, err
	}

	return server.GetStatus(), nil
}

// GetAllTools returns all tools from all running servers as adapters
func (r *Registry) GetAllTools() []pluginapi.PluginTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []pluginapi.PluginTool

	for _, server := range r.servers {
		if server.GetStatus() != StatusRunning {
			continue
		}

		mcpTools := server.GetTools()
		for _, tool := range mcpTools {
			adapter := NewMCPAdapter(server, tool)
			tools = append(tools, adapter)
		}
	}

	return tools
}

// GetToolsForServer returns all tools from a specific server as adapters
func (r *Registry) GetToolsForServer(serverName string) ([]pluginapi.PluginTool, error) {
	server, err := r.GetServer(serverName)
	if err != nil {
		return nil, err
	}

	if server.GetStatus() != StatusRunning {
		return nil, fmt.Errorf("server %s is not running", serverName)
	}

	mcpTools := server.GetTools()
	tools := make([]pluginapi.PluginTool, 0, len(mcpTools))

	for _, tool := range mcpTools {
		adapter := NewMCPAdapter(server, tool)
		tools = append(tools, adapter)
	}

	return tools, nil
}

// CallTool calls a tool on a specific server
func (r *Registry) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]interface{}) (*ToolCallResult, error) {
	server, err := r.GetServer(serverName)
	if err != nil {
		return nil, err
	}

	if server.GetStatus() != StatusRunning {
		return nil, fmt.Errorf("server %s is not running", serverName)
	}

	return server.CallTool(ctx, toolName, arguments)
}

// StartAll starts all enabled servers
func (r *Registry) StartAll() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []error
	for name, server := range r.servers {
		if !server.GetConfig().Enabled {
			continue
		}

		if err := server.Start(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to start some servers: %v", errs)
	}

	return nil
}

// StopAll stops all running servers
func (r *Registry) StopAll() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []error
	for name, server := range r.servers {
		if server.GetStatus() == StatusStopped {
			continue
		}

		if err := server.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop some servers: %v", errs)
	}

	return nil
}

// GetServerStats returns statistics about all servers
func (r *Registry) GetServerStats() map[string]ServerStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]ServerStats)
	for name, server := range r.servers {
		stats[name] = ServerStats{
			Name:      name,
			Status:    server.GetStatus(),
			ToolCount: len(server.GetTools()),
			Enabled:   server.GetConfig().Enabled,
		}
	}

	return stats
}

// ServerStats contains statistics for a server
type ServerStats struct {
	Name      string       `json:"name"`
	Status    ServerStatus `json:"status"`
	ToolCount int          `json:"tool_count"`
	Enabled   bool         `json:"enabled"`
}
