package workspaceplan

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func plannerOutput(tasks ...types.PlannerTask) *types.PlannerOutput {
	return &types.PlannerOutput{Rationale: "Ship it safely", Tasks: tasks}
}

// --- Conversion basics (FR-40) ---------------------------------------------

func TestConversionAssignsPlanLocalIDs(t *testing.T) {
	conversion, err := FromPlannerOutput("migrate reporting", plannerOutput(
		types.PlannerTask{ID: "t1", Description: "Snapshot"},
		types.PlannerTask{ID: "t2", Description: "Verify"},
	), []string{"builder"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if len(conversion.Content.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(conversion.Content.Groups))
	}
	items := conversion.Content.Groups[0].Items
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	// The planner's own IDs are not reused: they may be empty or repeated, and
	// Plan item IDs must be stable and unique.
	for _, item := range items {
		if !strings.HasPrefix(item.ID, "itm_") {
			t.Errorf("item id %q is not a Plan-local item ID", item.ID)
		}
		if item.ID == "t1" || item.ID == "t2" {
			t.Errorf("the planner's own id leaked through: %q", item.ID)
		}
	}
	if items[0].ID == items[1].ID {
		t.Error("two items share an ID")
	}
}

// A converted plan steps through. Nothing about a planner suggestion says the
// user wanted it to run by itself.
func TestConvertedWorkStepsThrough(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{Description: "Something"},
	), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if conversion.Content.Execution.Mode != ExecutionStepThrough {
		t.Errorf("mode = %q, want step_through", conversion.Content.Execution.Mode)
	}
}

func TestEmptyPlannerOutputIsRefused(t *testing.T) {
	if _, err := FromPlannerOutput("do it", nil, nil); err == nil {
		t.Error("nil planner output was accepted")
	}
	if _, err := FromPlannerOutput("do it", plannerOutput(), nil); err == nil {
		t.Error("an empty task list was accepted")
	}
}

// --- Assignments vs capabilities (FR-86, FR-118, task 9.5) -----------------

func TestAnExistingAgentBecomesAnAssignment(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{Description: "Something", SuggestedAgent: "Builder"},
	), []string{"builder"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	item := conversion.Content.Groups[0].Items[0]
	// The roster's spelling wins, so the Task names the agent as the workspace
	// knows it rather than as the planner typed it.
	if item.Assignee != "builder" {
		t.Errorf("assignee = %q, want the roster spelling", item.Assignee)
	}
	if len(conversion.UnavailableAgents) != 0 {
		t.Errorf("an existing agent was reported unavailable: %v", conversion.UnavailableAgents)
	}
}

// A proposed agent that does not exist is NOT an assignment. It becomes a
// required capability, so the compiled gate blocks the item with a resolution
// path instead of a Task being created for an agent nobody approved.
func TestAProposedAgentBecomesACapabilityNotAnAssignment(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{Description: "Something", SuggestedAgent: "Researcher"},
	), []string{"builder"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	item := conversion.Content.Groups[0].Items[0]
	if item.Assignee != "" {
		t.Errorf("a nonexistent agent was assigned: %q", item.Assignee)
	}
	if !containsString(item.RequiredCapabilities, "agent:Researcher") {
		t.Errorf("the proposed agent is not a required capability: %v", item.RequiredCapabilities)
	}
	if !containsString(conversion.UnavailableAgents, "Researcher") {
		t.Errorf("the proposed agent was not reported: %v", conversion.UnavailableAgents)
	}
}

// A dynamic agent the planner proposed is surfaced even when no task named it:
// proposing one is a request to create an agent, and that is approval-relevant
// whether or not the task list mentions it.
func TestUnreferencedDynamicAgentsAreStillReported(t *testing.T) {
	output := plannerOutput(types.PlannerTask{Description: "Something", SuggestedAgent: "builder"})
	output.DynamicAgents = []types.PlannerAgent{{Name: "Auditor"}}

	conversion, err := FromPlannerOutput("do it", output, []string{"builder"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !containsString(conversion.UnavailableAgents, "Auditor") {
		t.Errorf("an unreferenced dynamic agent was dropped: %v", conversion.UnavailableAgents)
	}
}

// A nil roster means "unknown", not "empty". Refusing assignments against a
// roster nobody could read would strand every plan in a build with no agent
// store.
func TestAnUnknownRosterCarriesAssignmentsThrough(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{Description: "Something", SuggestedAgent: "Whoever"},
	), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if conversion.Content.Groups[0].Items[0].Assignee != "Whoever" {
		t.Error("an unknown roster refused an assignment instead of carrying it")
	}
}

// An empty (non-nil) roster is the honest "this workspace has no agents", and
// every suggestion becomes a capability.
func TestAnEmptyRosterMakesEverySuggestionACapability(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{Description: "Something", SuggestedAgent: "Whoever"},
	), []string{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if conversion.Content.Groups[0].Items[0].Assignee != "" {
		t.Error("an empty roster still produced an assignment")
	}
}

// --- Dependencies (FR-104) -------------------------------------------------

func TestDependenciesResolveToPlanItemIDs(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{ID: "t1", Description: "First"},
		types.PlannerTask{ID: "t2", Description: "Second", DependsOn: []string{"t1"}},
	), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	items := conversion.Content.Groups[0].Items
	if len(items[1].DependsOn) != 1 {
		t.Fatalf("second item has %d dependencies, want 1", len(items[1].DependsOn))
	}
	if items[1].DependsOn[0] != items[0].ID {
		t.Errorf("dependency = %q, want the first item's ID %q", items[1].DependsOn[0], items[0].ID)
	}
}

// A dependency pointing at nothing is dropped and reported. Keeping it would
// fail validation and refuse the whole plan over one edge the planner could not
// justify.
func TestDanglingDependenciesAreDroppedAndReported(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{ID: "t1", Description: "First", DependsOn: []string{"ghost"}},
	), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(conversion.Content.Groups[0].Items[0].DependsOn) != 0 {
		t.Error("a dangling dependency survived conversion")
	}
	if !containsString(conversion.DroppedDependencies, "ghost") {
		t.Errorf("the dropped dependency was not reported: %v", conversion.DroppedDependencies)
	}
}

// A self-dependency can never be satisfied, so it is dropped rather than
// materializing work that can never start.
func TestSelfDependenciesAreDropped(t *testing.T) {
	conversion, err := FromPlannerOutput("do it", plannerOutput(
		types.PlannerTask{ID: "t1", Description: "First", DependsOn: []string{"t1"}},
	), nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(conversion.Content.Groups[0].Items[0].DependsOn) != 0 {
		t.Error("a self-dependency survived conversion")
	}
}

// The converted content must pass the same validation every other plan does,
// or the conversion has produced something that can never be reviewed.
func TestConvertedContentPassesValidation(t *testing.T) {
	conversion, err := FromPlannerOutput("migrate reporting", plannerOutput(
		types.PlannerTask{ID: "t1", Description: "Snapshot", SuggestedAgent: "builder"},
		types.PlannerTask{ID: "t2", Description: "Verify", DependsOn: []string{"t1"}},
	), []string{"builder"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	result := ValidatePlanContent("Migrate reporting safely", conversion.Content,
		ValidationContext{AvailableAgents: []string{"builder"}})
	if !result.OK() {
		t.Errorf("converted content failed validation: %v", result.Issues)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
