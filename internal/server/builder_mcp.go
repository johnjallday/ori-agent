// Package server provides MCP initialization methods for the ServerBuilder.
// This file contains methods for initializing the Model Context Protocol system.
package server

import (
	"os"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
)

const disableExternalMCPImportEnv = "ORI_DISABLE_EXTERNAL_MCP_IMPORT"

// initializeMCPRegistry initializes the MCP server browser registry store.
func (b *ServerBuilder) initializeMCPRegistry() {
	store := mcpregistry.NewStore()
	if b.mcpHandler != nil {
		b.mcpHandler.SetRegistryStore(store)
	}
}

// initializeMCP initializes the MCP system (registry, config manager, servers).
func (b *ServerBuilder) initializeMCP() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.mcpRegistry = mcp.NewRegistry()
	b.mcpConfigManager = mcp.NewConfigManager(".")

	if err := b.mcpConfigManager.InitializeDefaultServers(); err != nil {
		if verbose {
			logger.Error("failed to initialize default MCP servers", logger.Fields{"server": err})
		}
	}

	if externalMCPImportEnabled() {
		if imported, err := b.mcpConfigManager.ImportExternalGlobalServers(); err != nil {
			if verbose {
				logger.Error("failed to import external MCP servers", logger.Fields{"err": err})
			}
		} else if verbose && imported > 0 {
			logger.Info("imported external MCP servers", logger.Fields{"count": imported})
		}
	} else if verbose {
		logger.Info("skipping external MCP server import", logger.Fields{"env": disableExternalMCPImportEnv})
	}

	mcpGlobalConfig, err := b.mcpConfigManager.LoadGlobalConfig()
	if err != nil {
		if verbose {
			logger.Error("failed to load MCP global config", logger.Fields{"err": err})
		}
		return // Non-critical
	}

	for _, serverConfig := range mcpGlobalConfig.Servers {
		if err := b.mcpRegistry.AddServer(serverConfig); err != nil {
			if verbose {
				logger.Error("failed to add MCP server to registry", logger.Fields{"server": serverConfig.Name, "err": err})
			}
		}
	}

	if verbose {
		logger.Debug("MCP system initialized", logger.Fields{"server_count": len(mcpGlobalConfig.Servers)})
	}
}

func externalMCPImportEnabled() bool {
	raw, ok := os.LookupEnv(disableExternalMCPImportEnv)
	if !ok {
		return true
	}

	disabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return true
	}

	return !disabled
}
