package chathttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/oriagent/ori-pluginapi"
)

func (h *Handler) getMCPToolsForServer(serverName string) ([]pluginapi.PluginTool, error) {
	if h.mcpRegistry == nil {
		return nil, fmt.Errorf("mcp registry is not configured")
	}

	mcpTools, err := h.mcpRegistry.GetToolsForServer(serverName)
	if err == nil {
		return mcpTools, nil
	}
	if !isMCPServerNotRunningError(err) {
		return nil, err
	}

	logger.Info("MCP server is not running; attempting lazy start", logger.Fields{"server": serverName})
	if startErr := h.mcpRegistry.StartServer(serverName); startErr != nil {
		return nil, fmt.Errorf("failed to start MCP server %q: %w", serverName, startErr)
	}

	mcpTools, retryErr := h.mcpRegistry.GetToolsForServer(serverName)
	if retryErr != nil {
		return nil, fmt.Errorf("MCP server %q started but tool discovery failed: %w", serverName, retryErr)
	}

	logger.Info("MCP server started and tools loaded", logger.Fields{"server": serverName, "tool_count": len(mcpTools)})
	return mcpTools, nil
}

func isMCPServerNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "is not running")
}
