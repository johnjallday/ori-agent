package chathttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
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
		result := &resolvedChatAgent{Agent: baseAgent}
		h.attachWorkspaceTools(result, routeCtx.WorkspaceID)
		return result, nil
	}

	resolved, err := h.runtimeResolver.ResolveAgentForWorkspace(agentName, routeCtx.WorkspaceID, "")
	if err != nil {
		logger.Warn("Failed to resolve workspace runtime MCP configuration for chat; falling back to base agent", logger.Fields{
			"agent":        agentName,
			"workspace_id": routeCtx.WorkspaceID,
			"error":        err,
		})
		result := &resolvedChatAgent{Agent: baseAgent}
		h.attachWorkspaceTools(result, routeCtx.WorkspaceID)
		return result, nil
	}
	if resolved == nil || resolved.Agent == nil {
		result := &resolvedChatAgent{Agent: baseAgent}
		h.attachWorkspaceTools(result, routeCtx.WorkspaceID)
		return result, nil
	}
	result := &resolvedChatAgent{
		Agent:      resolved.Agent,
		MCPServers: append([]string{}, resolved.MCPServers...),
	}
	if len(resolved.EffectiveSkills) > 0 {
		result.EffectiveSkills = append([]workspace.ResolvedSkill{}, resolved.EffectiveSkills...)
	}
	h.attachWorkspaceTools(result, routeCtx.WorkspaceID)
	return result, nil
}

// attachWorkspaceTools adds workspace-scoped tools to a resolved agent when
// the necessary stores are available. This must be called on every code path
// that returns a resolvedChatAgent for a workspace surface.
func (h *Handler) attachWorkspaceTools(ag *resolvedChatAgent, workspaceID string) {
	if h.sessionStore == nil || h.workspaceStore == nil || strings.TrimSpace(workspaceID) == "" {
		logger.Debug("attachWorkspaceTools: skipping", logger.Fields{
			"workspace_id":    workspaceID,
			"session_store":   h.sessionStore != nil,
			"workspace_store": h.workspaceStore != nil,
		})
		return
	}
	wtp := NewWorkspaceToolProvider(h.sessionStore, h.workspaceStore, workspaceID)
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
	ag.WorkspaceTools = wtp
	logger.Info("attachWorkspaceTools: attached workspace tools", logger.Fields{
		"workspace_id": workspaceID,
		"tool_count":   len(wtp.Tools()),
	})
}

// rehydrateSessionHistory loads conversation history from the session store
// and populates the agent's Messages slice so the LLM has full context.
// This is called when a session already has stored messages but the agent's
// in-memory messages are empty (e.g. after server restart or page reload).
func (h *Handler) rehydrateSessionHistory(ctx context.Context, sessionID string, ag *resolvedChatAgent) {
	if h.sessionStore == nil || sessionID == "" || ag == nil {
		return
	}

	// Only rehydrate if the agent has no existing conversation history.
	// A non-empty Messages slice means we already have in-memory context.
	if len(ag.Messages) > 0 {
		return
	}

	msgs, err := h.sessionStore.GetMessages(ctx, sessionID)
	if err != nil {
		logger.Warn("Failed to load session messages for rehydration", logger.Fields{
			"session_id": sessionID,
			"error":      err,
		})
		return
	}

	if len(msgs) == 0 {
		return
	}

	// Convert stored messages to OpenAI message format.
	for _, m := range msgs {
		switch m.Role {
		case session.RoleUser:
			ag.Messages = append(ag.Messages, openai.UserMessage(m.Content))
		case session.RoleAssistant:
			ag.Messages = append(ag.Messages, openai.AssistantMessage(m.Content))
		case session.RoleSystem:
			ag.Messages = append(ag.Messages, openai.SystemMessage(m.Content))
		}
	}

	logger.Info("Rehydrated session history into agent context", logger.Fields{
		"session_id":    sessionID,
		"message_count": len(msgs),
	})
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
