package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Orchestrator coordinates multi-agent workflows
type Orchestrator struct {
	agentStore     store.Store
	workspaceStore workspace.Store
	history        gateway.ConversationStore
	communicator   *agentcomm.Communicator
	llmFactory     *llm.Factory
	configManager  *config.Manager
	eventBus       *workspace.EventBus
	gateway        *gateway.Service
	// planDrafter opens durable Plans for proposed work. Optional: without it
	// the orchestrator still reports what it wanted, it just cannot create the
	// record the user would approve.
	planDrafter PlanDrafter
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(agentStore store.Store, workspaceStore workspace.Store, history gateway.ConversationStore, communicator *agentcomm.Communicator, llmFactory *llm.Factory, configManager *config.Manager, eventBus *workspace.EventBus) *Orchestrator {
	return &Orchestrator{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		history:        history,
		communicator:   communicator,
		llmFactory:     llmFactory,
		configManager:  configManager,
		eventBus:       eventBus,
	}
}

// SetGateway sets the gateway service for the orchestrator
func (o *Orchestrator) SetGateway(gw *gateway.Service) {
	o.gateway = gw
}

// HandleGatewayMessage handles an incoming message from the gateway
func (o *Orchestrator) HandleGatewayMessage(ctx context.Context, msg gateway.Message) error {
	logger.Info("orchestrator received gateway message", logger.Fields{"from": msg.Sender.Name, "content": msg.Content})

	// 1. Identify which agent to use (PoC: use first available agent)
	var agentName string
	plan, err := o.PlanTask(ctx, msg.Content)
	if err == nil && len(plan.Tasks) > 0 {
		// Try to use the suggested agent from the first task
		firstTask := plan.Tasks[0]
		if firstTask.SuggestedAgent != "" {
			// Verify agent exists
			if _, ok := o.agentStore.GetAgent(firstTask.SuggestedAgent); ok {
				agentName = firstTask.SuggestedAgent
				logger.Debug("routing to suggested agent", logger.Fields{"agent": agentName, "rationale": plan.Rationale})
			}
		}

		// If no valid suggested agent, try finding by role
		if agentName == "" && firstTask.RequiredRole != "" {
			if agents, err := o.findAgentsByRoles([]types.AgentRole{firstTask.RequiredRole}); err == nil && len(agents) > 0 {
				agentName = agents[0]
				logger.Debug("routing to agent by role", logger.Fields{"agent": agentName, "role": firstTask.RequiredRole})
			}
		}
	} else {
		logger.Warn("planning failed, falling back to default", logger.Fields{"error": err})
	}

	// Fallback: Use first available agent if planning failed or returned no matches
	if agentName == "" {
		agents := o.agentStore.ListAgents()
		if len(agents) == 0 {
			return fmt.Errorf("no agents found in store")
		}
		agentName = agents[0]
		logger.Debug("routing to default agent", logger.Fields{"agent": agentName})
	}

	ag, ok := o.agentStore.GetAgent(agentName)
	if !ok {
		return fmt.Errorf("agent %s not found", agentName)
	}

	// 2. Manage conversation session
	sessionID := msg.Sender.Platform // Simple mapping: one session per platform for now
	if msg.Metadata != nil {
		if sid, ok := msg.Metadata["session_id"].(string); ok && sid != "" {
			sessionID = sid
		}
	}

	// Save user message
	if o.history != nil {
		if err := o.history.SaveMessage(ctx, sessionID, msg); err != nil {
			logger.Error("failed to save user message", logger.Fields{"session_id": sessionID, "error": err})
		}
	}

	// 3. Prepare LLM request with history
	var messages []llm.Message
	if o.history != nil {
		history, err := o.history.GetHistory(ctx, sessionID)
		if err == nil {
			for _, m := range history {
				role := llm.RoleUser
				if m.Sender.IsBot {
					role = llm.RoleAssistant
				}
				messages = append(messages, llm.Message{
					Role:    role,
					Content: m.Content,
				})
			}
		}
	}

	// If history lookup failed or history not available, use current message
	// Note: SaveMessage might have just saved it, so GetHistory might return it.
	// We need to ensure we don't duplicate it or miss it.
	// Usually GetHistory returns what's stored.
	// If messages is empty, append current.
	if len(messages) == 0 {
		messages = append(messages, llm.Message{
			Role:    llm.RoleUser,
			Content: msg.Content,
		})
	}

	provider, err := o.llmFactory.GetProvider(ag.Settings.Provider)
	if err != nil {
		return fmt.Errorf("failed to get LLM provider: %w", err)
	}

	req := llm.ChatRequest{
		Model:        ag.Settings.Model,
		SystemPrompt: ag.Settings.SystemPrompt,
		Messages:     messages,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return fmt.Errorf("LLM completion failed: %w", err)
	}

	content := resp.Content

	// 4. Store assistant response
	reply := gateway.Message{
		ID:      uuid.New(),
		Content: content,
		Sender: gateway.Sender{
			ID:       agentName,
			Name:     agentName,
			Platform: "ori",
			IsBot:    true,
		},
		ReplyToID: msg.ID.String(),
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"agent_role": ag.Role,
			"model":      ag.Settings.Model,
			"session_id": sessionID,
		},
	}

	if o.history != nil {
		if err := o.history.SaveMessage(ctx, sessionID, reply); err != nil {
			logger.Error("failed to save assistant message", logger.Fields{"session_id": sessionID, "error": err})
		}
	}

	// 5. Send response back to the gateway
	if o.gateway == nil {
		logger.Warn("gateway not set in orchestrator, cannot send response")
		return nil
	}

	// Important: We need to tell the gateway where to send this.
	// We use the original message's platform to route back.
	reply.Sender.Platform = msg.Sender.Platform

	return o.gateway.Send(ctx, reply)
}

// CollaborativeResult represents the result of a collaborative task
type CollaborativeResult struct {
	WorkspaceID          string                      `json:"workspace_id"`
	FinalOutput          string                      `json:"final_output"`
	SubResults           map[string]any              `json:"sub_results"`
	Duration             time.Duration               `json:"duration"`
	Status               string                      `json:"status"`
	Error                string                      `json:"error,omitempty"`
	PendingPlanID        string                      `json:"pending_plan_id,omitempty"`
	PlannerDecision      *types.PlannerDecision      `json:"planner_decision,omitempty"`
	DynamicAgentRequests []types.DynamicAgentRequest `json:"dynamic_agent_requests,omitempty"`
	// PlanID is the durable Plan opened for proposed work that cannot run yet.
	// It is where the user reviews and approves; PendingPlanID above is only
	// the record of which agents the proposal wanted.
	PlanID string `json:"plan_id,omitempty"`
}

// findAgentsByRoles finds agents that match the required roles. It is the
// role-resolution step of gateway routing (HandleGatewayMessage), which asks
// for a single role at a time.
func (o *Orchestrator) findAgentsByRoles(requiredRoles []types.AgentRole) ([]string, error) {
	allAgents := o.agentStore.ListAgents()

	selectedAgents := make(map[types.AgentRole]string)

	// Find one agent for each required role
	for _, role := range requiredRoles {
		for _, agentName := range allAgents {
			agent, ok := o.agentStore.GetAgent(agentName)
			if !ok || agent == nil {
				continue
			}

			// Check if agent has the required role
			if agent.Role == role {
				if _, exists := selectedAgents[role]; !exists {
					selectedAgents[role] = agentName
					break
				}
			}
		}

		// If no agent found for role, log warning
		if _, exists := selectedAgents[role]; !exists {
			logger.Warn("No agent found for role: , using general agent", logger.Fields{"agent": role})
			// Find a general agent as fallback
			for _, agentName := range allAgents {
				agent, ok := o.agentStore.GetAgent(agentName)
				if ok && agent != nil && agent.Role == types.RoleGeneral {
					selectedAgents[role] = agentName
					break
				}
			}
		}
	}

	// Convert to slice
	agents := make([]string, 0, len(selectedAgents))
	for _, agentName := range selectedAgents {
		agents = append(agents, agentName)
	}

	if len(agents) == 0 {
		return nil, fmt.Errorf("no suitable agents found for required roles")
	}

	return agents, nil
}

// DetectOrchestrationNeed analyzes a message to determine if it requires orchestration
func (o *Orchestrator) DetectOrchestrationNeed(message string) bool {
	messageLower := strings.ToLower(message)

	// Keywords suggesting complexity requiring multiple agents
	orchestrationKeywords := []string{
		"research and analyze",
		"comprehensive analysis",
		"investigate and",
		"compare multiple",
		"analyze data from",
		"research and synthesize",
		"gather information and analyze",
		"comprehensive report",
		"multi-step",
		"coordinate",
	}

	for _, keyword := range orchestrationKeywords {
		if strings.Contains(messageLower, keyword) {
			return true
		}
	}

	return false
}

// IdentifyRequiredRoles determines which agent roles are needed for a task
func (o *Orchestrator) IdentifyRequiredRoles(message string) []types.AgentRole {
	roles := make([]types.AgentRole, 0)
	messageLower := strings.ToLower(message)

	if strings.Contains(messageLower, "research") ||
		strings.Contains(messageLower, "find information") ||
		strings.Contains(messageLower, "gather") {
		roles = append(roles, types.RoleResearcher)
	}

	if strings.Contains(messageLower, "analyze") ||
		strings.Contains(messageLower, "process") ||
		strings.Contains(messageLower, "examine") {
		roles = append(roles, types.RoleAnalyzer)
	}

	if strings.Contains(messageLower, "comprehensive") ||
		strings.Contains(messageLower, "report") ||
		strings.Contains(messageLower, "synthesize") ||
		strings.Contains(messageLower, "combine") {
		roles = append(roles, types.RoleSynthesizer)
	}

	if strings.Contains(messageLower, "verify") ||
		strings.Contains(messageLower, "validate") ||
		strings.Contains(messageLower, "check") {
		roles = append(roles, types.RoleValidator)
	}

	// If no specific roles identified, use general
	if len(roles) == 0 {
		roles = append(roles, types.RoleGeneral)
	}

	return roles
}
