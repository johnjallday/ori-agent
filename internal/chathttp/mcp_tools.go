package chathttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/githubscope"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

func (h *Handler) getMCPToolsForServer(serverName string) ([]toolapi.Tool, error) {
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

// filterAllowedMCPTools restricts tools to those permitted by allowlist for
// serverName. A nil allowlist, or the absence of serverName in it, means no
// restriction (legacy all-tools behavior); a present entry -- even an empty
// slice -- means only those tool names (case-insensitive) pass through. See
// workspace.ResolvedAgentRuntime.MCPToolAllowlist.
func filterAllowedMCPTools(tools []toolapi.Tool, allowlist map[string][]string, serverName string) []toolapi.Tool {
	if len(allowlist) == 0 {
		return tools
	}
	allowed, restricted := allowlist[serverName]
	if !restricted {
		return tools
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	filtered := make([]toolapi.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if _, ok := allowedSet[strings.ToLower(strings.TrimSpace(tool.Definition().Name))]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// applyMCPRepoScope confines serverName's tools to a single repository when
// its binding declares one.
//
// This is the second half of the binding's policy: filterAllowedMCPTools says
// which tools exist, this says what they may be pointed at. Both are applied
// wherever MCP tools are handed to a model, since a permitted tool aimed at
// someone else's repository is just as much a breach as a forbidden one.
func applyMCPRepoScope(tools []toolapi.Tool, scopes map[string]string, serverName string) []toolapi.Tool {
	if len(scopes) == 0 {
		return tools
	}
	repo, scoped := scopes[serverName]
	if !scoped {
		return tools
	}
	guard, ok := githubscope.New(repo)
	if !ok {
		// A malformed constraint must fail closed: expose nothing rather
		// than fall back to unrestricted access.
		logger.Warn("MCP binding declares an unusable repository scope; exposing no tools for it", logger.Fields{
			"server": serverName,
		})
		return nil
	}
	return guard.Wrap(tools)
}
