// Package server provides MCP initialization methods for the ServerBuilder.
// This file contains methods for initializing the Model Context Protocol system.
package server

import (
	"os"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// initializeMCP initializes the MCP system (registry, config manager, servers).
func (b *ServerBuilder) initializeMCP() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.server.mcpRegistry = mcp.NewRegistry()
	b.server.mcpConfigManager = mcp.NewConfigManager(".")

	if err := b.server.mcpConfigManager.InitializeDefaultServers(); err != nil {
		if verbose {
			logger.Error("failed to initialize default MCP servers", logger.Fields{"server": err})
		}
	}

	mcpGlobalConfig, err := b.server.mcpConfigManager.LoadGlobalConfig()
	if err != nil {
		if verbose {
			logger.Error("failed to load MCP global config", logger.Fields{"err": err})
		}
		return nil // Non-critical
	}

	for _, serverConfig := range mcpGlobalConfig.Servers {
		if err := b.server.mcpRegistry.AddServer(serverConfig); err != nil {
			if verbose {
				logger.Error("failed to add MCP server to registry", logger.Fields{"server": serverConfig.Name, "err": err})
			}
		}
	}

	if verbose {
		logger.Debug("🔌 MCP system initialized with configured servers", logger.Fields{"server": len(mcpGlobalConfig.Servers)})
	}

	return nil
}
