package chathttp

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// The planning boundary: one place that decides whether a request becomes a
// durable Plan, an ephemeral Action Preview, or neither (FR-19, FR-20,
// FR-149, FR-150).
//
// Before this, "plan" meant two different things depending on which code path
// you were in. Chat's plan-before-action produced an ephemeral preview that
// could contain multi-agent delegation and several planner tasks, approved by
// clicking once in a chat bubble and surviving nothing. The durable Plan is
// versioned, reviewable, and its approval is the only thing that may create
// work. Two mechanisms wearing one word meant a user could approve what looked
// like a plan and get work nobody could later audit.
//
// The rule here is deliberately conservative: anything that produces Tasks,
// carries dependencies, spans agents, or spans Runs is Plan work. The preview
// keeps exactly one case — a single immediate action the user already named.

// PlanningRoute is where a request goes.
type PlanningRoute string

const (
	// RoutePlan means the request must become or open a durable Plan: it
	// creates Tasks, carries dependencies, involves more than one agent, or
	// would produce more than one Run (FR-19).
	RoutePlan PlanningRoute = "plan"
	// RouteActionPreview means one immediate action may be previewed and
	// approved inline, with no Plan record (FR-20).
	RouteActionPreview PlanningRoute = "action_preview"
	// RouteDirect means neither applies: a read-only answer, a simple route,
	// or a flow with its own domain-specific approval. These stay narrow and
	// are deliberately untouched by planning (FR-150).
	RouteDirect PlanningRoute = "direct"
)

// PlanningClassification is the decision plus why, so a route can be explained
// rather than merely obeyed.
type PlanningClassification struct {
	Route PlanningRoute
	// Reason is user-facing: it says what about the request forced this route.
	Reason string
	// Triggers names the specific properties that decided it, for tests and
	// diagnostics. Empty for RouteDirect.
	Triggers []string
}

// PlanningRequest describes what the router already knows about a request.
//
// Every field is something the application determined, not something a model
// asserted. A model claiming "this is simple" must not be able to route its own
// work around the Plan requirement (FR-60).
type PlanningRequest struct {
	// RouteMode is the utility router's decision.
	RouteMode UtilityRouteMode
	// DirectTool is set when the user explicitly named one tool with `/tool`.
	DirectTool bool
	// PlannerDecision is the orchestrator's multi-agent assessment, when one
	// was computed.
	PlannerDecision *types.PlannerDecision
	// ProposedTaskCount is how many Tasks the planner proposed.
	ProposedTaskCount int
	// HasDependencies is whether any proposed work waits on other work.
	HasDependencies bool
	// ProposedAgents counts the distinct agents the work would involve.
	ProposedAgents int
	// ReadOnly marks a request the router already knows changes nothing.
	ReadOnly bool
	// DomainApproval marks a flow that carries its own confirmation — the mail
	// send broker, destructive-action confirmations — which planning must not
	// duplicate or override (FR-150).
	DomainApproval bool
}

// ClassifyPlanningRoute decides where a request goes.
//
// The order of the checks is the policy. Plan-forcing properties are tested
// FIRST, so a request that both names a tool and spans agents becomes a Plan:
// the narrow case only applies when nothing broader is true.
func ClassifyPlanningRoute(request PlanningRequest) PlanningClassification {
	var triggers []string

	if request.ProposedTaskCount > 0 {
		triggers = append(triggers, "creates_tasks")
	}
	if request.HasDependencies {
		triggers = append(triggers, "has_dependencies")
	}
	if request.ProposedAgents > 1 {
		triggers = append(triggers, "multi_agent")
	}
	if request.PlannerDecision != nil && request.PlannerDecision.MultiAgent {
		triggers = append(triggers, "planner_multi_agent")
	}
	if request.RouteMode == UtilityRouteSpecial {
		// A specialist handoff is multi-agent work by definition, and FR-117
		// requires it to go through the compiled handoff policy — which lives
		// on the Plan side.
		triggers = append(triggers, "specialist_handoff")
	}

	if len(triggers) > 0 {
		return PlanningClassification{
			Route:    RoutePlan,
			Reason:   planReasonFor(triggers),
			Triggers: triggers,
		}
	}

	// A flow with its own approval keeps it. Wrapping a mail send in a Plan
	// would ask the user to approve twice for one action, and the second
	// approval would be the one that did not understand what it was approving.
	if request.DomainApproval {
		return PlanningClassification{
			Route:  RouteDirect,
			Reason: "this action has its own confirmation step",
		}
	}
	if request.ReadOnly {
		return PlanningClassification{
			Route:  RouteDirect,
			Reason: "this request only reads",
		}
	}

	// The one preview case: the user named a single immediate action.
	if request.DirectTool || request.RouteMode == UtilityRouteDirect {
		return PlanningClassification{
			Route:  RouteActionPreview,
			Reason: "one immediate action, ready to run",
		}
	}

	return PlanningClassification{
		Route:  RouteDirect,
		Reason: "an ordinary reply, with no work to create",
	}
}

// classifyChatRequest asks the orchestrator what the request would take, then
// runs the boundary over the answer.
//
// The planner is consulted for its ASSESSMENT only. It reports how many tasks
// and which mode; it does not get to decide whether its own output needs a
// Plan. That decision is made here, from facts, so a planner that
// under-estimates cannot route multi-agent work into an inline approval
// (FR-60, FR-149).
func (h *Handler) classifyChatRequest(
	ctx context.Context,
	request string,
	routeDecision UtilityRouteDecision,
	modeOverride string,
	thresholdOverride float64,
) (PlanningClassification, *types.PlannerDecision) {
	input := PlanningRequest{RouteMode: routeDecision.Mode}

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
				input.PlannerDecision = &decision
				input.ProposedTaskCount = len(plan.Tasks)
				input.ProposedAgents = distinctAssignees(plan)
				input.HasDependencies = hasDependencies(plan)
				// A proposed dynamic agent is an agent that does not exist yet.
				// Creating one is an approval-relevant assignment, so it can
				// never happen behind an inline preview (task 9.5).
				if len(plan.DynamicAgents) > 0 {
					input.ProposedAgents += len(plan.DynamicAgents)
				}
			}
		} else {
			decision := types.PlannerDecision{
				Threshold:  threshold,
				Mode:       string(mode),
				MultiAgent: false,
				Rationale:  "Multi-agent disabled",
				CreatedAt:  time.Now(),
			}
			plannerDecision = &decision
		}
	}

	return ClassifyPlanningRoute(input), plannerDecision
}

// openPlanForChat starts a durable Plan for a request chat cannot handle
// inline, returning the plan and workspace IDs.
//
// It needs a workspace: a Plan belongs to exactly one, and inventing one here
// would put the user's work somewhere they did not choose. Without a workspace
// in the route context the chat message still explains that a plan is needed —
// it just cannot make it for them.
func (h *Handler) openPlanForChat(
	ctx context.Context,
	routeContext normalizedChatRouteContext,
	request string,
) (planID string, workspaceID string) {
	if h == nil || h.planOpener == nil {
		return "", ""
	}
	workspaceID = strings.TrimSpace(routeContext.WorkspaceID)
	if workspaceID == "" {
		return "", ""
	}

	planID, err := h.planOpener.OpenPlan(ctx, workspaceID, request, routeContext.AgentName)
	if err != nil {
		return "", ""
	}
	return planID, workspaceID
}

// distinctAssignees counts how many different agents a proposed plan involves.
//
// Unassigned tasks are not counted: an unassigned task has no agent yet, and
// counting it as one would inflate a single-agent request into multi-agent work
// on the strength of a blank field.
func distinctAssignees(plan *types.PlannerOutput) int {
	if plan == nil {
		return 0
	}
	seen := map[string]struct{}{}
	for _, task := range plan.Tasks {
		name := strings.TrimSpace(task.SuggestedAgent)
		if name == "" {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
	}
	return len(seen)
}

// hasDependencies reports whether any proposed step waits on another.
//
// Ordering is what makes work a plan rather than an action: a dependency means
// something must finish before something else starts, and that is exactly the
// condition an inline "approve and go" cannot express or enforce (FR-104).
func hasDependencies(plan *types.PlannerOutput) bool {
	if plan == nil {
		return false
	}
	for _, task := range plan.Tasks {
		if len(task.DependsOn) > 0 {
			return true
		}
	}
	return false
}

// planReasonFor turns the triggers into one sentence a user can act on.
func planReasonFor(triggers []string) string {
	reasons := map[string]string{
		"creates_tasks":       "it creates workspace tasks",
		"has_dependencies":    "its steps depend on each other",
		"multi_agent":         "it involves more than one agent",
		"planner_multi_agent": "the planner selected multi-agent execution",
		"specialist_handoff":  "it hands work to a specialist",
	}

	parts := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		if reason, known := reasons[trigger]; known {
			parts = append(parts, reason)
		}
	}
	if len(parts) == 0 {
		return "this work needs a plan"
	}
	return "This needs a plan because " + joinReasons(parts) + "."
}

// joinReasons renders a list the way a person would say it.
func joinReasons(parts []string) string {
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}
