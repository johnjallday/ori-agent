package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

type agentProfile struct {
	Name         string
	Role         types.AgentRole
	Capabilities []string
	Description  string
}

// PlanTask produces a planner output for the request.
func (o *Orchestrator) PlanTask(ctx context.Context, request string) (*types.PlannerOutput, error) {
	if o.llmFactory == nil || o.configManager == nil {
		return o.planWithHeuristics(request), nil
	}

	providerName, model := o.configManager.GetSystemModel()
	reasoningEffort := o.configManager.GetSystemReasoningEffort()
	systemModel, err := o.llmFactory.GetSystemModelProvider(providerName, model)
	if err != nil {
		logger.Warn("Planner system model unavailable, using fallback", logger.Fields{"error": err})
		return o.planWithHeuristics(request), nil
	}

	agentProfiles := o.listAgentProfiles()
	systemPrompt := o.buildPlannerPrompt(agentProfiles)

	req := llm.ChatRequest{
		Model:           systemModel.Model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Request: %s", request)},
		},
	}

	resp, err := systemModel.Provider.Chat(ctx, req)
	if err != nil {
		logger.Warn("Planner LLM call failed, using fallback", logger.Fields{"error": err})
		return o.planWithHeuristics(request), nil
	}

	plan, err := parsePlannerOutput(resp.Content)
	if err != nil {
		logger.Warn("Planner output parse failed, using fallback", logger.Fields{"error": err})
		return o.planWithHeuristics(request), nil
	}

	return normalizePlannerOutput(plan, request), nil
}

// GetMultiAgentDefaults returns default routing configuration.
func (o *Orchestrator) GetMultiAgentDefaults() (types.MultiAgentMode, float64) {
	mode := types.MultiAgentModeAuto
	threshold := 6.0

	if o.configManager == nil {
		return mode, threshold
	}

	rawMode, rawThreshold := o.configManager.GetMultiAgentDefaults()
	modeValue := strings.ToLower(strings.TrimSpace(rawMode))
	if parsed, ok := types.ParseMultiAgentMode(modeValue); ok {
		mode = parsed
	}
	if rawThreshold > 0 {
		threshold = rawThreshold
	}

	return mode, threshold
}

// DecideMultiAgent determines whether to run multi-agent based on mode and threshold.
func (o *Orchestrator) DecideMultiAgent(plan *types.PlannerOutput, mode types.MultiAgentMode, threshold float64) types.PlannerDecision {
	if threshold <= 0 {
		threshold = 6.0
	}

	decision := types.PlannerDecision{
		ComplexityScore: plan.ComplexityScore,
		Threshold:       threshold,
		Mode:            string(mode),
		MultiAgent:      false,
		Rationale:       plan.Rationale,
		CreatedAt:       time.Now(),
	}

	switch mode {
	case types.MultiAgentModeForce:
		decision.MultiAgent = true
	case types.MultiAgentModeOff:
		decision.MultiAgent = false
	default:
		decision.MultiAgent = plan.ComplexityScore >= threshold
	}

	return decision
}

// listAgentProfiles loads available agent metadata for planning.
func (o *Orchestrator) listAgentProfiles() []agentProfile {
	names, _ := o.agentStore.ListAgents()
	profiles := make([]agentProfile, 0, len(names))

	for _, name := range names {
		ag, ok := o.agentStore.GetAgent(name)
		if !ok || ag == nil {
			continue
		}
		desc := ""
		if ag.Metadata != nil {
			desc = ag.Metadata.Description
		}
		profiles = append(profiles, agentProfile{
			Name:         name,
			Role:         ag.Role,
			Capabilities: ag.Capabilities,
			Description:  desc,
		})
	}

	return profiles
}

func (o *Orchestrator) buildPlannerPrompt(profiles []agentProfile) string {
	var sb strings.Builder
	sb.WriteString("You are a task planner for multi-agent execution.\n")
	sb.WriteString("Return JSON only, no markdown.\n\n")
	sb.WriteString("Schema:\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"complexity_score\": 0-10,\n")
	sb.WriteString("  \"rationale\": \"short rationale\",\n")
	sb.WriteString("  \"tasks\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"id\": \"step1\",\n")
	sb.WriteString("      \"description\": \"task description\",\n")
	sb.WriteString("      \"required_role\": \"orchestrator|researcher|analyzer|synthesizer|validator|specialist|general\",\n")
	sb.WriteString("      \"required_capabilities\": [\"web_search\",\"code_analysis\",\"data_processing\",\"file_operations\",\"api_integration\",\"research\",\"synthesis\",\"validation\"],\n")
	sb.WriteString("      \"depends_on\": [\"step0\"],\n")
	sb.WriteString("      \"suggested_agent\": \"agent_name_or_dynamic_agent\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ],\n")
	sb.WriteString("  \"dynamic_agents\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"name\": \"agent_name\",\n")
	sb.WriteString("      \"role\": \"specialist\",\n")
	sb.WriteString("      \"capabilities\": [\"api_integration\"],\n")
	sb.WriteString("      \"description\": \"purpose\",\n")
	sb.WriteString("      \"rationale\": \"why needed\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Keep tasks minimal and ordered.\n")
	sb.WriteString("- Use depends_on when one task needs another's output.\n")
	sb.WriteString("- Use suggested_agent only from available agents or from dynamic_agents.\n")
	sb.WriteString("- Only include dynamic_agents if no available agent fits the task.\n")
	sb.WriteString("- If the request is simple, return one task with low complexity_score.\n\n")
	sb.WriteString("Available agents:\n")
	if len(profiles) == 0 {
		sb.WriteString("- (none)\n")
	} else {
		for _, profile := range profiles {
			sb.WriteString(fmt.Sprintf("- %s (role: %s; capabilities: %s", profile.Name, profile.Role, strings.Join(profile.Capabilities, ", ")))
			if profile.Description != "" {
				sb.WriteString(fmt.Sprintf("; description: %s", profile.Description))
			}
			sb.WriteString(")\n")
		}
	}

	return sb.String()
}

func parsePlannerOutput(content string) (*types.PlannerOutput, error) {
	jsonPayload := extractJSON(content)
	var output types.PlannerOutput
	if err := json.Unmarshal([]byte(jsonPayload), &output); err != nil {
		return nil, err
	}
	return &output, nil
}

func normalizePlannerOutput(plan *types.PlannerOutput, fallback string) *types.PlannerOutput {
	if plan == nil {
		return &types.PlannerOutput{
			ComplexityScore: 0,
			Rationale:       "Planner unavailable",
			Tasks: []types.PlannerTask{
				{ID: "step1", Description: fallback, RequiredRole: types.RoleGeneral},
			},
		}
	}

	if plan.ComplexityScore < 0 {
		plan.ComplexityScore = 0
	}
	if plan.ComplexityScore > 10 {
		plan.ComplexityScore = 10
	}
	if strings.TrimSpace(plan.Rationale) == "" {
		plan.Rationale = "Planner generated tasks"
	}
	if len(plan.Tasks) == 0 {
		plan.Tasks = []types.PlannerTask{
			{ID: "step1", Description: fallback, RequiredRole: types.RoleGeneral},
		}
	}

	seen := make(map[string]bool)
	for i := range plan.Tasks {
		task := &plan.Tasks[i]
		if strings.TrimSpace(task.Description) == "" {
			task.Description = fallback
		}
		if task.ID == "" || seen[task.ID] {
			task.ID = fmt.Sprintf("step-%d", i+1)
		}
		seen[task.ID] = true

		task.RequiredCapabilities = trimEmpty(task.RequiredCapabilities)
		task.RequiredRole = normalizeRole(task.RequiredRole)
		task.DependsOn = trimEmpty(task.DependsOn)
	}

	for i := range plan.DynamicAgents {
		if strings.TrimSpace(plan.DynamicAgents[i].Name) == "" {
			plan.DynamicAgents[i].Name = "dynamic-" + uuid.New().String()
		}
		plan.DynamicAgents[i].Capabilities = trimEmpty(plan.DynamicAgents[i].Capabilities)
		plan.DynamicAgents[i].Role = normalizeRole(plan.DynamicAgents[i].Role)
	}

	return plan
}

func normalizeRole(role types.AgentRole) types.AgentRole {
	switch role {
	case types.RoleOrchestrator,
		types.RoleResearcher,
		types.RoleAnalyzer,
		types.RoleSynthesizer,
		types.RoleValidator,
		types.RoleSpecialist,
		types.RoleGeneral:
		return role
	default:
		return types.RoleGeneral
	}
}

func trimEmpty(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

func extractJSON(content string) string {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")

	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end <= start {
		return trimmed
	}
	return trimmed[start : end+1]
}

func (o *Orchestrator) planWithHeuristics(request string) *types.PlannerOutput {
	roles := o.IdentifyRequiredRoles(request)
	tasks := make([]types.PlannerTask, 0, len(roles))
	for i, role := range roles {
		tasks = append(tasks, types.PlannerTask{
			ID:           fmt.Sprintf("step-%d", i+1),
			Description:  request,
			RequiredRole: role,
		})
	}

	score := 0.0
	if o.DetectOrchestrationNeed(request) {
		score = 8.0
	}

	return &types.PlannerOutput{
		ComplexityScore: score,
		Rationale:       "Heuristic planner fallback",
		Tasks:           tasks,
	}
}
