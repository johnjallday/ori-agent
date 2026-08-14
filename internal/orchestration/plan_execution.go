package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	defaultPlanTaskTimeout = 10 * time.Minute
	planPollInterval       = 500 * time.Millisecond
)

// ExecutePlannedTask runs a planner output through multi-agent execution.
func (o *Orchestrator) ExecutePlannedTask(ctx context.Context, mainAgent, request string, plan *types.PlannerOutput, decision types.PlannerDecision, maxDuration time.Duration) (*CollaborativeResult, error) {
	startTime := time.Now()
	if maxDuration <= 0 {
		maxDuration = defaultPlanTaskTimeout
	}

	workspaceName := fmt.Sprintf("plan-%s-%d", mainAgent, time.Now().Unix())
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
		Name:        workspaceName,
		Agents:      []string{mainAgent},
		InitialData: map[string]any{"request": request},
	})

	ws.SetPlannerDecision(&decision)
	if err := o.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	o.publishEvent(workspace.EventPlannerDecision, ws.ID, map[string]any{
		"complexity_score": decision.ComplexityScore,
		"threshold":        decision.Threshold,
		"mode":             decision.Mode,
		"multi_agent":      decision.MultiAgent,
	})

	assignments, dynamicAgents := o.resolvePlanAssignments(plan)
	if len(dynamicAgents) > 0 {
		logger.Info("Dynamic agents required for plan", logger.Fields{"count": len(dynamicAgents)})

		// Work that needs an agent which does not exist becomes a durable Plan,
		// not an ephemeral stash on the workspace.
		//
		// The old path saved a types.PendingPlan and let the dynamic-agent
		// approval resume it: one click created an agent AND ran a multi-agent
		// workflow, with no version, no content hash, and nothing an audit
		// could read afterwards. A durable Plan carries the proposed work as
		// reviewable content, and its own approval is the only thing that can
		// turn it into Tasks (FR-59, FR-60, FR-149).
		planID, planErr := o.openDurablePlan(ctx, ws.ID, request, plan)

		pending := &types.PendingPlan{
			ID:        uuid.New().String(),
			Request:   request,
			Plan:      *plan,
			Decision:  decision,
			CreatedAt: time.Now(),
		}
		// The stash is retained ONLY as the record of which agents this
		// proposal wanted; nothing resumes from it any more.
		ws.SetPendingPlan(pending)

		requests := make([]types.DynamicAgentRequest, 0, len(dynamicAgents))
		for _, spec := range dynamicAgents {
			req := types.DynamicAgentRequest{
				WorkspaceID:  ws.ID,
				PlanID:       pending.ID,
				Name:         spec.Name,
				Role:         spec.Role,
				Capabilities: spec.Capabilities,
				Description:  spec.Description,
				Rationale:    spec.Rationale,
			}
			requests = append(requests, ws.AddDynamicAgentRequest(req))
		}

		if err := o.workspaceStore.Save(ws); err != nil {
			return nil, fmt.Errorf("failed to save pending plan: %w", err)
		}
		o.publishEvent(workspace.EventDynamicAgentRequested, ws.ID, map[string]any{
			"requests": requests,
			"plan_id":  pending.ID,
		})

		result := &CollaborativeResult{
			WorkspaceID:          ws.ID,
			FinalOutput:          "",
			SubResults:           make(map[string]any),
			Duration:             time.Since(startTime),
			Status:               "pending_approval",
			PendingPlanID:        pending.ID,
			PlannerDecision:      &decision,
			DynamicAgentRequests: requests,
		}
		if planErr != nil {
			// A plan that could not be created is reported, not swallowed:
			// without it there is nothing for the user to approve, and silence
			// would look like work that is merely waiting.
			logger.Warn("Could not open a durable plan for proposed work", logger.Fields{
				"workspace_id": ws.ID,
				"error":        planErr,
			})
		}
		result.PlanID = planID

		return result, nil
	}

	if err := o.ensureWorkspaceAgents(ws, mainAgent, assignments); err != nil {
		return nil, err
	}

	if err := o.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	result, err := o.executePlanSequentially(ctx, ws, mainAgent, request, plan, assignments, maxDuration)
	if err != nil {
		ws.SetStatus(workspace.StatusFailed)
		_ = o.workspaceStore.Save(ws)
		return result, err
	}

	ws.SetStatus(workspace.StatusCompleted)
	_ = o.workspaceStore.Save(ws)

	result.WorkspaceID = ws.ID
	result.Duration = time.Since(startTime)
	result.Status = "completed"
	result.PlannerDecision = &decision

	return result, nil
}

// ResumePendingPlan continues execution after dynamic agents are approved.
func (o *Orchestrator) ResumePendingPlan(ctx context.Context, workspaceID string) (*CollaborativeResult, error) {
	ws, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}
	if ws.PendingPlan == nil {
		return nil, fmt.Errorf("no pending plan to resume")
	}

	pending := ws.PendingPlan
	plan := &pending.Plan

	if err := o.ensureDynamicAgents(ws, pending.ID); err != nil {
		return nil, err
	}

	assignments, dynamicAgents := o.resolvePlanAssignments(plan)
	if len(dynamicAgents) > 0 {
		return nil, fmt.Errorf("dynamic agents still required before resume")
	}

	mainAgent := primaryAgent(ws)
	if mainAgent == "" {
		return nil, fmt.Errorf("no coordinator agent available")
	}

	if err := o.ensureWorkspaceAgents(ws, mainAgent, assignments); err != nil {
		return nil, err
	}

	ws.ClearPendingPlan()
	if err := o.workspaceStore.Save(ws); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	result, err := o.executePlanSequentially(ctx, ws, mainAgent, pending.Request, plan, assignments, defaultPlanTaskTimeout)
	if err != nil {
		ws.SetStatus(workspace.StatusFailed)
		_ = o.workspaceStore.Save(ws)
		return result, err
	}

	ws.SetStatus(workspace.StatusCompleted)
	_ = o.workspaceStore.Save(ws)
	result.WorkspaceID = ws.ID
	result.Status = "completed"
	result.PlannerDecision = &pending.Decision

	return result, nil
}

// PlanDrafter opens a durable Plan carrying proposed work.
//
// The orchestrator holds this rather than the plan service for the same reason
// chat does: it may propose work, never approve it. One method wide means the
// boundary is a fact about what this package can call, not a rule somebody has
// to remember (FR-59, FR-149).
type PlanDrafter interface {
	// DraftPlan creates a Plan from planner output and returns its ID.
	DraftPlan(ctx context.Context, workspaceID, request string, plan *types.PlannerOutput) (string, error)
}

// SetPlanDrafter attaches durable Plan creation.
func (o *Orchestrator) SetPlanDrafter(drafter PlanDrafter) {
	o.planDrafter = drafter
}

// HasPlanDrafter reports whether durable Plan creation is wired. Build wiring
// tests assert it, because the failure mode is silent: proposed work still
// appears, it just has no versioned record behind it.
func (o *Orchestrator) HasPlanDrafter() bool {
	return o != nil && o.planDrafter != nil
}

// openDurablePlan creates the Plan a user will review and approve.
//
// A failure here is returned rather than fatal: the proposal still exists as
// dynamic-agent requests, and reporting "no plan was created" beats failing the
// whole call for something the user can retry.
func (o *Orchestrator) openDurablePlan(ctx context.Context, workspaceID, request string, plan *types.PlannerOutput) (string, error) {
	if o == nil || o.planDrafter == nil {
		return "", fmt.Errorf("durable planning is not configured")
	}
	return o.planDrafter.DraftPlan(ctx, workspaceID, request, plan)
}

func primaryAgent(ws *workspace.Workspace) string {
	if agentNames := ws.AgentNames(); len(agentNames) > 0 {
		return agentNames[0]
	}
	return ""
}

func (o *Orchestrator) ensureWorkspaceAgents(ws *workspace.Workspace, mainAgent string, assignments map[string]string) error {
	if !ws.HasAgent(mainAgent) {
		if err := ws.AddAgent(mainAgent); err != nil {
			return fmt.Errorf("failed to add coordinator agent: %w", err)
		}
	}

	for _, agentName := range assignments {
		if agentName == "" || agentName == mainAgent {
			continue
		}
		if !ws.HasAgent(agentName) {
			if err := ws.AddAgent(agentName); err != nil {
				return fmt.Errorf("failed to add agent %s: %w", agentName, err)
			}
		}
	}

	return nil
}

func (o *Orchestrator) executePlanSequentially(ctx context.Context, ws *workspace.Workspace, mainAgent, request string, plan *types.PlannerOutput, assignments map[string]string, timeout time.Duration) (*CollaborativeResult, error) {
	subResults := make(map[string]any)
	taskResults := make(map[string]string)

	for _, step := range plan.Tasks {
		agentName := assignments[step.ID]
		if agentName == "" {
			agentName = mainAgent
		}

		context := map[string]any{
			"request":          request,
			"plan_task_id":     step.ID,
			"plan_task_role":   step.RequiredRole,
			"plan_task_reason": plan.Rationale,
		}

		if len(step.DependsOn) > 0 {
			depResults := make(map[string]string)
			for _, dep := range step.DependsOn {
				if result, ok := taskResults[dep]; ok {
					depResults[dep] = result
				}
			}
			if len(depResults) > 0 {
				context["dependency_results"] = depResults
			}
		}

		delegateTask, err := o.communicator.DelegateTask(agentcomm.DelegationRequest{
			WorkspaceID: ws.ID,
			From:        mainAgent,
			To:          agentName,
			Description: step.Description,
			Priority:    5,
			Context:     context,
			Timeout:     timeout,
		})
		if err != nil {
			return &CollaborativeResult{
				FinalOutput: "",
				SubResults:  subResults,
				Status:      "failed",
				Error:       err.Error(),
			}, err
		}

		if err := o.updateTaskInputs(ws.ID, delegateTask.ID, step.DependsOn); err != nil {
			logger.Warn("Failed to update task dependencies", logger.Fields{"error": err, "task_id": delegateTask.ID})
		}

		task, err := o.waitForTaskCompletion(ctx, delegateTask.ID, timeout)
		if err != nil {
			return &CollaborativeResult{
				FinalOutput: "",
				SubResults:  subResults,
				Status:      "failed",
				Error:       err.Error(),
			}, err
		}

		taskResults[step.ID] = task.Result
		subResults[step.ID] = task.Result
	}

	finalOutput := ""
	if len(plan.Tasks) > 0 {
		lastID := plan.Tasks[len(plan.Tasks)-1].ID
		finalOutput = taskResults[lastID]
	}
	if strings.TrimSpace(finalOutput) == "" {
		finalOutput = fmt.Sprintf("Completed %d task(s).", len(plan.Tasks))
	}

	return &CollaborativeResult{
		FinalOutput: finalOutput,
		SubResults:  subResults,
		Status:      "completed",
	}, nil
}

func (o *Orchestrator) waitForTaskCompletion(ctx context.Context, taskID string, timeout time.Duration) (*workspace.Task, error) {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("task %s timed out", taskID)
		}

		task, err := o.communicator.GetTask(taskID)
		if err != nil {
			return nil, err
		}

		switch task.Status {
		case workspace.TaskStatusCompleted:
			return task, nil
		case workspace.TaskStatusFailed, workspace.TaskStatusCancelled, workspace.TaskStatusTimeout:
			return task, fmt.Errorf("task %s failed: %s", taskID, task.Error)
		default:
			time.Sleep(planPollInterval)
		}
	}
}

func (o *Orchestrator) updateTaskInputs(workspaceID, taskID string, dependencies []string) error {
	if len(dependencies) == 0 {
		return nil
	}
	ws, err := o.workspaceStore.Get(workspaceID)
	if err != nil {
		return err
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return err
	}
	task.InputTaskIDs = dependencies
	return o.workspaceStore.Save(ws)
}

func (o *Orchestrator) resolvePlanAssignments(plan *types.PlannerOutput) (map[string]string, []types.PlannerAgent) {
	assignments := make(map[string]string)
	dynamicAgents := make(map[string]types.PlannerAgent)
	available := o.listAgentProfiles()
	availableByName := make(map[string]agentProfile, len(available))

	for _, profile := range available {
		availableByName[profile.Name] = profile
	}

	for _, spec := range plan.DynamicAgents {
		if _, exists := availableByName[spec.Name]; exists {
			continue
		}
		dynamicAgents[spec.Name] = spec
	}

	for _, task := range plan.Tasks {
		agentName := ""

		if task.SuggestedAgent != "" {
			if _, exists := availableByName[task.SuggestedAgent]; exists {
				agentName = task.SuggestedAgent
			} else if _, exists := dynamicAgents[task.SuggestedAgent]; exists {
				agentName = task.SuggestedAgent
			} else {
				dynamicAgents[task.SuggestedAgent] = types.PlannerAgent{
					Name:         task.SuggestedAgent,
					Role:         task.RequiredRole,
					Capabilities: task.RequiredCapabilities,
					Description:  fmt.Sprintf("Auto-created for task %s", task.ID),
					Rationale:    "Suggested agent not found",
				}
				agentName = task.SuggestedAgent
			}
		}

		if agentName == "" && task.RequiredRole != "" {
			if match := findAgentByRole(available, task.RequiredRole); match != "" {
				agentName = match
			}
		}

		if agentName == "" && len(task.RequiredCapabilities) > 0 {
			if match := findAgentByCapabilities(available, task.RequiredCapabilities); match != "" {
				agentName = match
			}
		}

		if agentName == "" {
			agentName = findAgentByRole(available, types.RoleGeneral)
		}

		if agentName == "" && len(available) > 0 {
			agentName = available[0].Name
		}

		assignments[task.ID] = agentName
	}

	resultDynamic := make([]types.PlannerAgent, 0, len(dynamicAgents))
	for _, spec := range dynamicAgents {
		resultDynamic = append(resultDynamic, spec)
	}

	return assignments, resultDynamic
}

func findAgentByRole(available []agentProfile, role types.AgentRole) string {
	for _, profile := range available {
		if profile.Role == role {
			return profile.Name
		}
	}
	return ""
}

func findAgentByCapabilities(available []agentProfile, required []string) string {
	for _, profile := range available {
		if hasAllCapabilities(profile.Capabilities, required) {
			return profile.Name
		}
	}
	return ""
}

func hasAllCapabilities(agentCaps []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	capSet := make(map[string]bool, len(agentCaps))
	for _, cap := range agentCaps {
		capSet[cap] = true
	}
	for _, req := range required {
		if !capSet[req] {
			return false
		}
	}
	return true
}

func (o *Orchestrator) ensureDynamicAgents(ws *workspace.Workspace, planID string) error {
	for _, req := range ws.DynamicAgentRequests {
		if planID != "" && req.PlanID != planID {
			continue
		}
		if req.Status != types.DynamicAgentStatusApproved {
			return fmt.Errorf("dynamic agent %s not approved", req.Name)
		}

		if _, exists := o.agentStore.GetAgent(req.Name); exists {
			continue
		}

		if err := o.agentStore.CreateAgent(req.Name, &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
			return fmt.Errorf("failed to create dynamic agent %s: %w", req.Name, err)
		}

		ag, ok := o.agentStore.GetAgent(req.Name)
		if ok && ag != nil {
			ag.Role = req.Role
			ag.Capabilities = req.Capabilities
			if ag.Metadata == nil {
				ag.Metadata = &types.AgentMetadata{}
			}
			ag.Metadata.Description = req.Description
			if err := o.agentStore.SetAgent(req.Name, ag); err != nil {
				logger.Warn("Failed to update dynamic agent metadata", logger.Fields{"agent": req.Name, "error": err})
			}
		}
	}

	return nil
}
