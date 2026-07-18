package chathttp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errAgentPaused = errors.New("agent is paused")

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
	if h.runtimeResolver == nil || strings.TrimSpace(routeCtx.WorkspaceID) == "" {
		if !ok || baseAgent == nil {
			return nil, fmt.Errorf("agent '%s' not found", agentName)
		}
		if isAgentPaused(baseAgent) {
			return nil, fmt.Errorf("%w: %s", errAgentPaused, agentName)
		}
		baseAgent = cloneAgentForChat(baseAgent)
		result := &resolvedChatAgent{Agent: baseAgent}
		h.attachWorkspaceTools(result, agentName, routeCtx)
		return result, nil
	}

	resolved, err := h.runtimeResolver.ResolveAgentForWorkspace(agentName, routeCtx.WorkspaceID, "")
	if resolved != nil && resolved.Agent != nil {
		if isAgentPaused(resolved.Agent) {
			return nil, fmt.Errorf("%w: %s", errAgentPaused, agentName)
		}
		result := &resolvedChatAgent{
			Agent:      cloneAgentForChat(resolved.Agent),
			MCPServers: append([]string{}, resolved.MCPServers...),
		}
		if len(resolved.EffectiveSkills) > 0 {
			result.EffectiveSkills = append([]workspace.ResolvedSkill{}, resolved.EffectiveSkills...)
		}
		h.attachWorkspaceTools(result, agentName, routeCtx)
		return result, nil
	}

	if err != nil {
		if errors.Is(err, workspace.ErrAgentPaused) {
			return nil, fmt.Errorf("%w: %s", errAgentPaused, agentName)
		}
		logger.Warn("Failed to resolve workspace runtime MCP configuration for chat; falling back to base agent", logger.Fields{
			"agent":        agentName,
			"workspace_id": routeCtx.WorkspaceID,
			"error":        err,
		})
	}

	if !ok || baseAgent == nil {
		return nil, fmt.Errorf("agent '%s' not found", agentName)
	}
	if isAgentPaused(baseAgent) {
		return nil, fmt.Errorf("%w: %s", errAgentPaused, agentName)
	}
	baseAgent = cloneAgentForChat(baseAgent)

	result := &resolvedChatAgent{Agent: baseAgent}
	h.attachWorkspaceTools(result, agentName, routeCtx)
	return result, nil
}

func isAgentPaused(ag *agent.Agent) bool {
	return ag != nil && ag.Status == types.AgentStatusDisabled
}

// attachWorkspaceTools adds workspace-scoped tools to a resolved agent when
// the necessary stores are available. This must be called on every code path
// that returns a resolvedChatAgent for a workspace surface.
func (h *Handler) attachWorkspaceTools(ag *resolvedChatAgent, agentName string, routeCtx normalizedChatRouteContext) {
	workspaceID := strings.TrimSpace(routeCtx.WorkspaceID)
	if h.sessionStore == nil || h.workspaceStore == nil || workspaceID == "" {
		logger.Debug("attachWorkspaceTools: skipping", logger.Fields{
			"workspace_id":    workspaceID,
			"session_store":   h.sessionStore != nil,
			"workspace_store": h.workspaceStore != nil,
		})
		return
	}
	wtp := NewWorkspaceToolProvider(h.sessionStore, h.workspaceStore, workspaceID)
	wtp.SetHQVisibilityDeps(h.hqVisibility)
	if name := strings.TrimSpace(agentName); name != "" {
		wtp.SetExecutingAgent(name)
	}
	if h.fileStore != nil {
		wtp.SetFileStore(h.fileStore)
	}
	if h.userProfileStore != nil {
		wtp.SetUserProfileDeps(h.userProfileStore, h.userProvider)
	}
	if h.templatesRootResolver != nil {
		wtp.SetProjectTemplateDeps(h.templatesRootResolver, h.workspaceEventBus)
	}
	if taskID := strings.TrimSpace(routeCtx.TaskID); taskID != "" {
		wtp.SetTaskID(taskID)
	}
	if h.mailboxAccess != nil {
		wtp.SetMailboxAccess(h.mailboxAccess)
	}
	if h.mailDrafter != nil {
		wtp.SetMailDrafter(h.mailDrafter)
	}
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
		"task_id":      routeCtx.TaskID,
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

	msgs, err := h.sessionStore.GetMessages(ctx, sessionID)
	if err != nil {
		logger.Warn("Failed to load session messages for rehydration", logger.Fields{
			"session_id": sessionID,
			"error":      err,
		})
		return
	}

	if len(msgs) == 0 {
		ag.Messages = nil
		return
	}

	ag.Messages = make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))

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

	return h.store.SetAgent(agentName, cloneAgentForChat(ag))
}

func cloneAgentForChat(src *agent.Agent) *agent.Agent {
	if src == nil {
		return nil
	}

	copyAgent := *src
	copyAgent.Messages = nil

	if len(src.Capabilities) > 0 {
		copyAgent.Capabilities = append([]string{}, src.Capabilities...)
	}
	if src.Statistics != nil {
		statsCopy := src.Statistics.GetSafeStats()
		copyAgent.Statistics = &statsCopy
	}
	if src.Metadata != nil {
		metadataCopy := *src.Metadata
		if len(src.Metadata.Tags) > 0 {
			metadataCopy.Tags = append([]string{}, src.Metadata.Tags...)
		}
		copyAgent.Metadata = &metadataCopy
	}
	if src.Evolution != nil {
		evolutionCopy := *src.Evolution
		copyAgent.Evolution = &evolutionCopy
	}

	return &copyAgent
}
