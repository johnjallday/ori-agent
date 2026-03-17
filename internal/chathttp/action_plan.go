package chathttp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ActionPlanStep describes a single planned step before execution.
type ActionPlanStep struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Details   string `json:"details,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ActionPlanPreview is returned when plan-before-action mode is enabled.
type ActionPlanPreview struct {
	ID        string           `json:"id"`
	Request   string           `json:"request"`
	RouteMode string           `json:"route_mode"`
	Summary   string           `json:"summary"`
	Steps     []ActionPlanStep `json:"steps"`
	CreatedAt time.Time        `json:"created_at"`
}

func newActionPlanID() string {
	return "plan_" + uuid.NewString()
}

func truncatePlanText(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:max]) + "..."
}

func buildDirectToolActionPlan(request string, cmd *DirectToolCommand) *ActionPlanPreview {
	toolName := ""
	args := ""
	if cmd != nil {
		toolName = strings.TrimSpace(cmd.ToolName)
		args = strings.TrimSpace(cmd.Args)
	}

	steps := []ActionPlanStep{
		{
			Kind:      "tool_call",
			Title:     fmt.Sprintf("Execute direct tool `%s`", toolName),
			Details:   "Run the explicitly requested tool command.",
			ToolName:  toolName,
			Arguments: args,
		},
	}

	return &ActionPlanPreview{
		ID:        newActionPlanID(),
		Request:   strings.TrimSpace(request),
		RouteMode: routeModeDirectTool,
		Summary:   "Direct tool execution is ready and waiting for approval.",
		Steps:     steps,
		CreatedAt: time.Now(),
	}
}

func (h *Handler) buildChatActionPlan(
	ctx context.Context,
	request string,
	routeDecision UtilityRouteDecision,
	modeOverride string,
	thresholdOverride float64,
) (*ActionPlanPreview, *types.PlannerDecision) {
	routeMode := routeModeAssistantChat
	steps := make([]ActionPlanStep, 0, 5)

	switch routeDecision.Mode {
	case UtilityRouteDirect:
		routeMode = string(UtilityRouteDirect)
		steps = append(steps, ActionPlanStep{
			Kind:      "tool_call",
			Title:     fmt.Sprintf("Run utility tool `%s`", strings.TrimSpace(routeDecision.ToolName)),
			Details:   routeDecision.Reason,
			ToolName:  strings.TrimSpace(routeDecision.ToolName),
			Arguments: strings.TrimSpace(routeDecision.ToolArgs),
		})
	case UtilityRouteWorkspace:
		routeMode = string(UtilityRouteWorkspace)
		steps = append(steps, ActionPlanStep{
			Kind:    "workspace",
			Title:   "Ensure workspace context (create if needed)",
			Details: "Use an active workspace for this Assistant session, or auto-create one.",
		}, ActionPlanStep{
			Kind:    "delegation",
			Title:   "Route request to workspace-scoped execution",
			Details: routeDecision.Reason,
		})
	case UtilityRouteScratch:
		routeMode = string(UtilityRouteScratch)
		steps = append(steps, ActionPlanStep{
			Kind:    "workspace",
			Title:   "Ensure scratch workspace context (create if needed)",
			Details: "Use an active workspace for this Assistant session, or auto-create one for scratch execution.",
		}, ActionPlanStep{
			Kind:    "delegation",
			Title:   "Route request to scratch execution",
			Details: routeDecision.Reason,
		})
	case UtilityRouteSpecial:
		routeMode = routeModeSpecialistFlow
		steps = append(steps, ActionPlanStep{
			Kind:    "workspace",
			Title:   "Ensure specialist workspace context (create if needed)",
			Details: "Use an active workspace for this Assistant session, or auto-create one for specialist routing.",
		}, ActionPlanStep{
			Kind:    "delegation",
			Title:   "Route request to specialist workflow",
			Details: routeDecision.Reason,
		})
	default:
		steps = append(steps, ActionPlanStep{
			Kind:    "assistant_chat",
			Title:   "Ask the Assistant to answer",
			Details: "The agent may call tools as needed before producing a response.",
		})
	}

	var plannerDecision *types.PlannerDecision
	if h != nil && h.orchestrator != nil {
		mode, threshold := h.orchestrator.GetMultiAgentDefaults()
		if modeOverride != "" {
			if parsed, ok := types.ParseMultiAgentMode(strings.ToLower(strings.TrimSpace(modeOverride))); ok {
				mode = parsed
			}
		}
		if thresholdOverride > 0 {
			threshold = thresholdOverride
		}

		if mode != types.MultiAgentModeOff {
			if plan, err := h.orchestrator.PlanTask(ctx, request); err == nil && plan != nil {
				decision := h.orchestrator.DecideMultiAgent(plan, mode, threshold)
				plannerDecision = &decision

				if decision.MultiAgent {
					steps = append(steps, ActionPlanStep{
						Kind:    "planner",
						Title:   "Execute multi-agent workflow",
						Details: fmt.Sprintf("Planner selected multi-agent mode with %d planned task(s).", len(plan.Tasks)),
					})
					for i, task := range plan.Tasks {
						if i >= 3 {
							steps = append(steps, ActionPlanStep{
								Kind:    "planner_task",
								Title:   fmt.Sprintf("...and %d more planned step(s)", len(plan.Tasks)-3),
								Details: "Additional planner tasks omitted for brevity.",
							})
							break
						}
						steps = append(steps, ActionPlanStep{
							Kind:    "planner_task",
							Title:   fmt.Sprintf("Planner task %d", i+1),
							Details: truncatePlanText(task.Description, 180),
						})
					}
				} else {
					steps = append(steps, ActionPlanStep{
						Kind:    "planner",
						Title:   "Stay in single-agent mode",
						Details: fmt.Sprintf("Planner complexity %.2f is below threshold %.2f.", decision.ComplexityScore, decision.Threshold),
					})
				}
			}
		} else {
			decision := types.PlannerDecision{
				ComplexityScore: 0,
				Threshold:       threshold,
				Mode:            string(mode),
				MultiAgent:      false,
				Rationale:       "Multi-agent disabled",
				CreatedAt:       time.Now(),
			}
			plannerDecision = &decision
		}
	}

	summary := "Action plan prepared. Approve to execute, edit to revise, or cancel."
	if routeDecision.Mode == UtilityRouteDirect {
		summary = fmt.Sprintf("Utility action `%s` is ready to run after approval.", strings.TrimSpace(routeDecision.ToolName))
	}

	return &ActionPlanPreview{
		ID:        newActionPlanID(),
		Request:   strings.TrimSpace(request),
		RouteMode: routeMode,
		Summary:   summary,
		Steps:     steps,
		CreatedAt: time.Now(),
	}, plannerDecision
}

func formatActionPlanMessage(plan *ActionPlanPreview) string {
	if plan == nil {
		return "Action plan is ready for approval."
	}

	var sb strings.Builder
	sb.WriteString("**Planned Next Actions**\n")
	for i, step := range plan.Steps {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			title = "Step"
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, title))
		if details := strings.TrimSpace(step.Details); details != "" {
			sb.WriteString("   ")
			sb.WriteString(details)
			sb.WriteString("\n")
		}
	}
	if summary := strings.TrimSpace(plan.Summary); summary != "" {
		sb.WriteString("\n")
		sb.WriteString(summary)
	}
	return strings.TrimSpace(sb.String())
}
