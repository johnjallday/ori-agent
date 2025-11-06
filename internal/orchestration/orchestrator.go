package orchestration

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Orchestrator coordinates multi-agent workflows
type Orchestrator struct {
	agentStore     store.Store
	workspaceStore agentstudio.Store
	communicator   *agentcomm.Communicator
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(agentStore store.Store, workspaceStore agentstudio.Store, communicator *agentcomm.Communicator) *Orchestrator {
	return &Orchestrator{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		communicator:   communicator,
	}
}

// CollaborativeTask represents a task requiring multiple agents
type CollaborativeTask struct {
	Goal          string                 `json:"goal"`
	RequiredRoles []types.AgentRole      `json:"required_roles"`
	Context       map[string]interface{} `json:"context"`
	MaxDuration   time.Duration          `json:"max_duration"`
}

// CollaborativeResult represents the result of a collaborative task
type CollaborativeResult struct {
	WorkspaceID string                 `json:"studio_id"`
	FinalOutput string                 `json:"final_output"`
	SubResults  map[string]interface{} `json:"sub_results"`
	Duration    time.Duration          `json:"duration"`
	Status      string                 `json:"status"`
	Error       string                 `json:"error,omitempty"`
}

// ExecuteCollaborativeTask coordinates multiple agents to complete a task
func (o *Orchestrator) ExecuteCollaborativeTask(ctx context.Context, mainAgent string, task CollaborativeTask) (*CollaborativeResult, error) {
	startTime := time.Now()

	log.Printf("🚀 Starting collaborative task: %s", task.Goal)

	// 1. Create workspace
	workspaceName := fmt.Sprintf("collab-%s-%d", mainAgent, time.Now().Unix())
	ws := agentstudio.NewWorkspace(agentstudio.CreateWorkspaceParams{
		Name:        workspaceName,
		ParentAgent: mainAgent,
		Agents:      []string{}, // Will be populated as we find agents
		InitialData: task.Context,
	})

	if err := o.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	log.Printf("📦 Created workspace: %s (ID: %s)", workspaceName, ws.ID)

	// 2. Identify required agents based on roles
	agents, err := o.findAgentsByRoles(task.RequiredRoles)
	if err != nil {
		return nil, fmt.Errorf("failed to find agents: %w", err)
	}

	// Add agents to workspace
	for _, agentName := range agents {
		if err := ws.AddAgent(agentName); err != nil {
			log.Printf("⚠️  Failed to add agent %s: %v", agentName, err)
		}
	}
	if err := o.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	log.Printf("👥 Selected agents: %v", agents)

	// 3. Execute workflow based on required roles
	result, err := o.executeWorkflow(ctx, ws, task, agents)
	if err != nil {
		ws.SetStatus(agentstudio.StatusFailed)
		o.workspaceStore.Save(ws)
		return &CollaborativeResult{
			WorkspaceID: ws.ID,
			FinalOutput: "",
			SubResults:  make(map[string]interface{}),
			Duration:    time.Since(startTime),
			Status:      "failed",
			Error:       err.Error(),
		}, err
	}

	// 4. Mark workspace as completed
	ws.SetStatus(agentstudio.StatusCompleted)
	o.workspaceStore.Save(ws)

	result.WorkspaceID = ws.ID
	result.Duration = time.Since(startTime)
	result.Status = "completed"

	log.Printf("✅ Collaborative task completed in %v", result.Duration)

	return result, nil
}

// findAgentsByRoles finds agents that match the required roles
func (o *Orchestrator) findAgentsByRoles(requiredRoles []types.AgentRole) ([]string, error) {
	allAgents, _ := o.agentStore.ListAgents()

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
			log.Printf("⚠️  No agent found for role: %s, using general agent", role)
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

// executeWorkflow executes the appropriate workflow based on required roles
func (o *Orchestrator) executeWorkflow(ctx context.Context, ws *agentstudio.Workspace, task CollaborativeTask, agents []string) (*CollaborativeResult, error) {
	// Determine workflow type based on roles
	hasResearcher := o.hasRole(task.RequiredRoles, types.RoleResearcher)
	hasAnalyzer := o.hasRole(task.RequiredRoles, types.RoleAnalyzer)
	hasSynthesizer := o.hasRole(task.RequiredRoles, types.RoleSynthesizer)

	if hasResearcher && hasAnalyzer && hasSynthesizer {
		// Full research pipeline
		return o.executeResearchPipeline(ctx, ws, task, agents)
	} else if hasResearcher {
		// Simple research workflow
		return o.executeResearchWorkflow(ctx, ws, task, agents)
	} else {
		// Generic parallel workflow
		return o.executeParallelWorkflow(ctx, ws, task, agents)
	}
}

// hasRole checks if a role is in the list
func (o *Orchestrator) hasRole(roles []types.AgentRole, target types.AgentRole) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

// executeResearchWorkflow executes a simple research workflow
func (o *Orchestrator) executeResearchWorkflow(ctx context.Context, ws *agentstudio.Workspace, task CollaborativeTask, agents []string) (*CollaborativeResult, error) {
	log.Printf("📚 Executing research workflow")

	subResults := make(map[string]interface{})

	// Find researcher agent
	var researcherAgent string
	for _, agentName := range agents {
		agent, _ := o.agentStore.GetAgent(agentName)
		if agent != nil && agent.Role == types.RoleResearcher {
			researcherAgent = agentName
			break
		}
	}

	if researcherAgent == "" {
		return nil, fmt.Errorf("no researcher agent found")
	}

	// Delegate research task
	delegateTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
		WorkspaceID: ws.ID,
		From:        ws.ParentAgent,
		To:          researcherAgent,
		Description: task.Goal,
		Priority:    5,
		Context:     task.Context,
		Timeout:     task.MaxDuration,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to delegate research task: %w", err)
	}

	log.Printf("📋 Delegated research to %s (task: %s)", researcherAgent, delegateTask.ID)

	// Wait for completion (simplified - in production, this would be event-driven)
	// For now, we'll return a status indicating the task is in progress
	subResults["research_task_id"] = delegateTask.ID
	subResults["researcher"] = researcherAgent

	return &CollaborativeResult{
		FinalOutput: fmt.Sprintf("Research task delegated to %s. Task ID: %s", researcherAgent, delegateTask.ID),
		SubResults:  subResults,
	}, nil
}

// executeParallelWorkflow executes tasks in parallel across agents
func (o *Orchestrator) executeParallelWorkflow(ctx context.Context, ws *agentstudio.Workspace, task CollaborativeTask, agents []string) (*CollaborativeResult, error) {
	log.Printf("⚡ Executing parallel workflow with %d agents", len(agents))

	subResults := make(map[string]interface{})
	taskIDs := make([]string, 0)

	// Delegate subtasks to each agent
	for i, agentName := range agents {
		subtaskDesc := fmt.Sprintf("%s (part %d of %d)", task.Goal, i+1, len(agents))

		delegateTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
			WorkspaceID: ws.ID,
			From:        ws.ParentAgent,
			To:          agentName,
			Description: subtaskDesc,
			Priority:    3,
			Context:     task.Context,
			Timeout:     task.MaxDuration,
		})

		if err != nil {
			log.Printf("⚠️  Failed to delegate to %s: %v", agentName, err)
			continue
		}

		taskIDs = append(taskIDs, delegateTask.ID)
		subResults[agentName] = delegateTask.ID
		log.Printf("📋 Delegated to %s (task: %s)", agentName, delegateTask.ID)
	}

	return &CollaborativeResult{
		FinalOutput: fmt.Sprintf("Tasks delegated to %d agents. Task IDs: %v", len(taskIDs), taskIDs),
		SubResults:  subResults,
	}, nil
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
