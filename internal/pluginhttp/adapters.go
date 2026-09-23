// Package pluginhttp wires the plugin installer (internal/plugin) to Ori's live
// MCP manager and exposes plugin operations over HTTP. A plugin's skills are
// read in place from its install folder, so nothing here copies them.
package pluginhttp

import (
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// mcpRegistrar adapts Ori's MCP config manager (persistence) + runtime registry
// to plugin.MCPRegistrar, registering to both — the same pair the MCP import
// path uses (configManager.AddServer + registry.AddServer).
type mcpRegistrar struct {
	config   *mcp.ConfigManager
	registry *mcp.Registry
}

func newMCPRegistrar(config *mcp.ConfigManager, registry *mcp.Registry) *mcpRegistrar {
	return &mcpRegistrar{config: config, registry: registry}
}

func (a *mcpRegistrar) AddServer(cfg mcp.ServerConfig) error {
	if err := a.config.AddServer(cfg); err != nil {
		return err
	}
	if err := a.registry.AddServer(cfg); err != nil {
		_ = a.config.RemoveServer(cfg.Name) // keep persisted + runtime in sync
		return err
	}
	return nil
}

func (a *mcpRegistrar) RemoveServer(name string) error {
	rerr := a.registry.RemoveServer(name)
	if cerr := a.config.RemoveServer(name); cerr != nil {
		return cerr
	}
	return rerr
}

var _ plugin.MCPRegistrar = (*mcpRegistrar)(nil)
