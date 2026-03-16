package chathttp

import (
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type chatRuntimeResolver interface {
	ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error)
}

type resolvedChatAgent struct {
	*agent.Agent
	MCPServers      []string
	EffectiveSkills []workspace.ResolvedSkill
	WorkspaceTools  *WorkspaceToolProvider
}

// SetRuntimeResolver configures workspace-aware agent runtime resolution for chat requests.
func (h *Handler) SetRuntimeResolver(resolver chatRuntimeResolver) {
	if h == nil {
		return
	}
	h.runtimeResolver = resolver
}

func (h *Handler) resolveEffectiveAgent(agentName string, routeCtx normalizedChatRouteContext) (*resolvedChatAgent, error) {
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("chat handler is not configured")
	}

	baseAgent, ok := h.store.GetAgent(agentName)
	if !ok || baseAgent == nil {
		return nil, fmt.Errorf("agent '%s' not found", agentName)
	}

	if h.runtimeResolver == nil || strings.TrimSpace(routeCtx.WorkspaceID) == "" {
		return &resolvedChatAgent{Agent: baseAgent}, nil
	}

	resolved, err := h.runtimeResolver.ResolveAgentForWorkspace(agentName, routeCtx.WorkspaceID, "")
	if err != nil {
		logger.Warn("Failed to resolve workspace runtime MCP configuration for chat; falling back to base agent", logger.Fields{
			"agent":        agentName,
			"workspace_id": routeCtx.WorkspaceID,
			"error":        err,
		})
		return &resolvedChatAgent{Agent: baseAgent}, nil
	}
	if resolved == nil || resolved.Agent == nil {
		return &resolvedChatAgent{Agent: baseAgent}, nil
	}
	result := &resolvedChatAgent{
		Agent:      resolved.Agent,
		MCPServers: append([]string{}, resolved.MCPServers...),
	}
	if len(resolved.EffectiveSkills) > 0 {
		result.EffectiveSkills = append([]workspace.ResolvedSkill{}, resolved.EffectiveSkills...)
	}
	// Attach workspace-scoped tools when both stores are available
	if h.sessionStore != nil && h.workspaceStore != nil {
		wtp := NewWorkspaceToolProvider(h.sessionStore, h.workspaceStore, routeCtx.WorkspaceID)
		// Wire management deps if available
		if h.store != nil || h.mcpRegistry != nil || h.skillsManager != nil {
			var mcpLister mcpServerLister
			if reg, ok := h.mcpRegistry.(mcpServerLister); ok {
				mcpLister = reg
			}
			var skillsMgr skillLister
			if mgr, ok := h.skillsManager.(skillLister); ok {
				skillsMgr = mgr
			}
			wtp.SetManagementDeps(h.store, mcpLister, skillsMgr)
		}
		result.WorkspaceTools = wtp
	}
	return result, nil
}

func (h *Handler) persistAgent(agentName string, ag *agent.Agent) error {
	if h == nil || h.store == nil || ag == nil || strings.TrimSpace(agentName) == "" {
		return nil
	}

	copyAgent := *ag
	if len(ag.Capabilities) > 0 {
		copyAgent.Capabilities = append([]string{}, ag.Capabilities...)
	}
	if len(ag.Messages) > 0 {
		copyAgent.Messages = append([]openai.ChatCompletionMessageParamUnion{}, ag.Messages...)
	}
	if len(ag.Plugins) > 0 {
		copyAgent.Plugins = make(map[string]types.LoadedPlugin, len(ag.Plugins))
		for key, value := range ag.Plugins {
			copyAgent.Plugins[key] = value
		}
	}

	return h.store.SetAgent(agentName, &copyAgent)
}
