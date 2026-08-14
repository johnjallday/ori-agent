package chathttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

// --- Work that must become a Plan (FR-19) ----------------------------------

func TestWorkThatCreatesTasksNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{ProposedTaskCount: 3})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q, want plan", got.Route)
	}
	if !strings.Contains(got.Reason, "creates workspace tasks") {
		t.Errorf("reason does not say why: %q", got.Reason)
	}
}

func TestDependentWorkNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{HasDependencies: true})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q, want plan", got.Route)
	}
	if !strings.Contains(got.Reason, "depend on each other") {
		t.Errorf("reason does not name the dependency: %q", got.Reason)
	}
}

func TestMultiAgentWorkNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{ProposedAgents: 2})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q, want plan", got.Route)
	}
}

// The planner's own multi-agent decision forces a Plan even when it proposed no
// tasks yet: the decision is the commitment.
func TestPlannerMultiAgentDecisionNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{
		PlannerDecision: &types.PlannerDecision{MultiAgent: true},
	})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q, want plan", got.Route)
	}
}

// A specialist handoff is multi-agent work by definition and carries its own
// enforced confirmation policy, so it cannot be approved inline.
func TestSpecialistRoutingNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{RouteMode: UtilityRouteSpecial})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q, want plan", got.Route)
	}
	if !strings.Contains(got.Reason, "specialist") {
		t.Errorf("reason does not name the handoff: %q", got.Reason)
	}
}

// The narrow case must LOSE to any broader property. A request that names one
// tool but also spans agents is Plan work — otherwise naming a tool would be a
// way to route around the requirement.
func TestNamingAToolDoesNotEscapeThePlanRequirement(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{
		DirectTool:        true,
		RouteMode:         UtilityRouteDirect,
		ProposedTaskCount: 2,
		ProposedAgents:    3,
	})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q; naming a tool escaped the plan requirement", got.Route)
	}
}

// Every Plan route explains itself. A refusal with no reason is one the user
// cannot act on.
func TestEveryPlanRouteCarriesTriggersAndAReason(t *testing.T) {
	cases := []PlanningRequest{
		{ProposedTaskCount: 1},
		{HasDependencies: true},
		{ProposedAgents: 4},
		{RouteMode: UtilityRouteSpecial},
	}
	for _, input := range cases {
		got := ClassifyPlanningRoute(input)
		if len(got.Triggers) == 0 {
			t.Errorf("%+v produced no triggers", input)
		}
		if strings.TrimSpace(got.Reason) == "" {
			t.Errorf("%+v produced no reason", input)
		}
	}
}

// --- The one preview case (FR-20) ------------------------------------------

func TestOneNamedToolMayBePreviewed(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{DirectTool: true})
	if got.Route != RouteActionPreview {
		t.Fatalf("route = %q, want action_preview", got.Route)
	}
}

func TestOneRoutedUtilityToolMayBePreviewed(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{RouteMode: UtilityRouteDirect})
	if got.Route != RouteActionPreview {
		t.Fatalf("route = %q, want action_preview", got.Route)
	}
}

// --- What stays narrow (FR-150) --------------------------------------------

// A read-only request creates nothing, so it needs neither a Plan nor an
// approval. Wrapping reads in approvals is how an approval stops being read.
func TestReadOnlyWorkStaysDirect(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{ReadOnly: true})
	if got.Route != RouteDirect {
		t.Fatalf("route = %q, want direct", got.Route)
	}
}

// A flow with its own confirmation keeps it. Asking twice for one action makes
// the second click the thoughtless one.
func TestDomainApprovalsAreNotDuplicated(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{DomainApproval: true})
	if got.Route != RouteDirect {
		t.Fatalf("route = %q, want direct", got.Route)
	}
	if !strings.Contains(got.Reason, "own confirmation") {
		t.Errorf("reason does not name the existing confirmation: %q", got.Reason)
	}
}

// A domain approval that ALSO creates tasks is still Plan work: its own
// confirmation covers its own action, not a task tree.
func TestADomainApprovalThatCreatesTasksStillNeedsAPlan(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{DomainApproval: true, ProposedTaskCount: 2})
	if got.Route != RoutePlan {
		t.Fatalf("route = %q; a domain approval escaped the plan requirement", got.Route)
	}
}

func TestAnOrdinaryReplyStaysDirect(t *testing.T) {
	got := ClassifyPlanningRoute(PlanningRequest{})
	if got.Route != RouteDirect {
		t.Fatalf("route = %q, want direct", got.Route)
	}
}

// --- The preview refuses what it cannot represent (task 9.7) ---------------

// The preview holds one step. A delegation or planner step reaching it means
// routing failed, and it must fail loudly rather than render Plan work as an
// inline approval.
func TestThePreviewRefusesNonImmediateSteps(t *testing.T) {
	for _, kind := range []string{"delegation", "planner", "planner_task", "workspace"} {
		_, err := NewActionPreview("do it", "chat", "summary", ActionPreviewStep{Kind: kind})
		if err == nil {
			t.Errorf("the preview accepted a %q step", kind)
		}
	}
}

func TestThePreviewAcceptsOneToolCall(t *testing.T) {
	preview, err := NewActionPreview("do it", "chat", "ready", ActionPreviewStep{
		Kind: "tool_call", Title: "Run `get_time`", ToolName: "get_time",
	})
	if err != nil {
		t.Fatalf("a single tool call was refused: %v", err)
	}
	if len(preview.Steps) != 1 {
		t.Errorf("preview holds %d steps, want 1", len(preview.Steps))
	}
}

// The message says "action", never "plan". Reusing the word is what made the
// ephemeral preview and the durable record indistinguishable.
func TestThePreviewMessageDoesNotSayPlan(t *testing.T) {
	preview, err := directToolPreview("run it", &DirectToolCommand{ToolName: "get_time"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	message := strings.ToLower(formatActionPreviewMessage(preview))
	if strings.Contains(message, "plan") {
		t.Errorf("the preview message calls itself a plan: %q", message)
	}
}

// A Plan-routed request points at the canonical surface rather than offering to
// approve anything inline (FR-149).
func TestThePlanRequiredMessagePointsAtTheCanonicalSurface(t *testing.T) {
	message := planRequiredMessage(ClassifyPlanningRoute(PlanningRequest{ProposedTaskCount: 2}))
	if !strings.Contains(message, "Open the plan") {
		t.Errorf("message does not point at the plan: %q", message)
	}
	if !strings.Contains(strings.ToLower(message), "approve") {
		t.Errorf("message does not say approval happens there: %q", message)
	}
}

// --- Planner assessment mapping --------------------------------------------

func TestDependenciesAreDetectedFromPlannerOutput(t *testing.T) {
	plan := &types.PlannerOutput{Tasks: []types.PlannerTask{
		{ID: "a", Description: "first"},
		{ID: "b", Description: "second", DependsOn: []string{"a"}},
	}}
	if !hasDependencies(plan) {
		t.Error("a depends_on edge was not detected")
	}
	if hasDependencies(&types.PlannerOutput{Tasks: []types.PlannerTask{{ID: "a"}}}) {
		t.Error("independent work was reported as dependent")
	}
}

// Unassigned tasks are not agents. Counting a blank field would turn one
// request into "multi-agent" on the strength of missing data.
func TestUnassignedTasksAreNotCountedAsAgents(t *testing.T) {
	plan := &types.PlannerOutput{Tasks: []types.PlannerTask{
		{Description: "one", SuggestedAgent: "builder"},
		{Description: "two"},
		{Description: "three", SuggestedAgent: "Builder"},
	}}
	if got := distinctAssignees(plan); got != 1 {
		t.Errorf("distinct agents = %d, want 1 (case-insensitive, blanks ignored)", got)
	}
}
