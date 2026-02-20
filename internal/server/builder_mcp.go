// Package server provides MCP initialization methods for the ServerBuilder.
// This file contains methods for initializing the Model Context Protocol system.
package server

import (
	"os"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
)

// initializeMCPRegistry initializes the MCP server browser registry store.
func (b *ServerBuilder) initializeMCPRegistry() {
	store := mcpregistry.NewStore()
	if b.mcpHandler != nil {
		b.mcpHandler.SetRegistryStore(store)
	}
}

// initializeMCP initializes the MCP system (registry, config manager, servers).
func (b *ServerBuilder) initializeMCP() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.mcpRegistry = mcp.NewRegistry()
	b.mcpConfigManager = mcp.NewConfigManager(".")

	if err := b.mcpConfigManager.InitializeDefaultServers(); err != nil {
		if verbose {
			logger.Error("failed to initialize default MCP servers", logger.Fields{"server": err})
		}
	}

	if imported, err := b.mcpConfigManager.ImportExternalGlobalServers(); err != nil {
		if verbose {
			logger.Error("failed to import external MCP servers", logger.Fields{"err": err})
		}
	} else if verbose && imported > 0 {
		logger.Info("imported external MCP servers", logger.Fields{"count": imported})
	}

	mcpGlobalConfig, err := b.mcpConfigManager.LoadGlobalConfig()
	if err != nil {
		if verbose {
			logger.Error("failed to load MCP global config", logger.Fields{"err": err})
		}
		return nil // Non-critical
	}

	for _, serverConfig := range mcpGlobalConfig.Servers {
		if err := b.mcpRegistry.AddServer(serverConfig); err != nil {
			if verbose {
				logger.Error("failed to add MCP server to registry", logger.Fields{"server": serverConfig.Name, "err": err})
			}
		}
	}

	enabledServers := b.collectEnabledMCPServerNames()
	startedCount, failedCount := startEnabledMCPServers(b.mcpRegistry, enabledServers)
	if failedCount > 0 {
		logger.Warn("some enabled MCP servers failed to start during startup", logger.Fields{
			"enabled_server_count": len(enabledServers),
			"started_count":        startedCount,
			"failed_count":         failedCount,
		})
	} else if verbose && startedCount > 0 {
		logger.Info("started enabled MCP servers during startup", logger.Fields{
			"enabled_server_count": len(enabledServers),
			"started_count":        startedCount,
		})
	}

	if verbose {
		logger.Debug("MCP system initialized", logger.Fields{"server_count": len(mcpGlobalConfig.Servers)})
	}

	return nil
}

func (b *ServerBuilder) collectEnabledMCPServerNames() []string {
	if b == nil || b.st == nil || b.mcpConfigManager == nil {
		return nil
	}

	agentNames, _ := b.st.ListAgents()
	if len(agentNames) == 0 {
		return nil
	}

	serverNames := make([]string, 0)
	seen := make(map[string]struct{})
	for _, agentName := range agentNames {
		enabledServers, err := b.mcpConfigManager.GetEnabledServersForAgent(agentName)
		if err != nil {
			logger.Warn("failed to load enabled MCP servers for agent during startup", logger.Fields{
				"agent": agentName,
				"err":   err,
			})
			continue
		}

		for _, server := range enabledServers {
			if server.Name == "" {
				continue
			}
			if _, ok := seen[server.Name]; ok {
				continue
			}
			seen[server.Name] = struct{}{}
			serverNames = append(serverNames, server.Name)
		}
	}

	return serverNames
}

type mcpRegistryStarter interface {
	GetServerStatus(name string) (mcp.ServerStatus, error)
	StartServer(name string) error
	StopServer(name string) error
}

func startEnabledMCPServers(registry mcpRegistryStarter, serverNames []string) (startedCount int, failedCount int) {
	if registry == nil || len(serverNames) == 0 {
		return 0, 0
	}

	for _, serverName := range serverNames {
		status, err := registry.GetServerStatus(serverName)
		if err != nil {
			failedCount++
			logger.Warn("failed to read MCP server status during startup", logger.Fields{"server": serverName, "err": err})
			continue
		}

		switch status {
		case mcp.StatusRunning, mcp.StatusStarting, mcp.StatusRestarting:
			continue
		case mcp.StatusError:
			if err := registry.StopServer(serverName); err != nil {
				logger.Warn("failed to stop errored MCP server before startup retry", logger.Fields{"server": serverName, "err": err})
			}
		}

		if err := registry.StartServer(serverName); err != nil {
			failedCount++
			logger.Warn("failed to start enabled MCP server during startup", logger.Fields{
				"server": serverName,
				"err":    err,
			})
			continue
		}
		startedCount++
	}

	return startedCount, failedCount
}

var _ mcpRegistryStarter = (*mcp.Registry)(nil)
