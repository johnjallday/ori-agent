package workspaceplan

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Adapting planner output into the typed Plan schema (FR-40, FR-46–FR-48).
//
// The orchestrator's planner produces a loose shape: a flat task list with
// free-text dependency references and suggested agent names it may have
// invented. The Plan schema is typed, ID-addressed, and validated. This is the
// one place that converts between them, so a planner change cannot quietly
// widen what a Plan may contain.
//
// Two things are deliberately NOT carried across. A suggested agent that does
// not exist becomes a required capability rather than an assignment, so the
// compiled availability gate refuses it instead of a Task being created for an
// agent nobody approved (FR-86, FR-118). And a dependency naming a task the
// planner did not emit is dropped rather than preserved: a dangling edge fails
// validation, and losing an edge the planner could not justify is better than
// refusing the whole plan over it.

// PlannerConversion is a converted plan plus what could not be carried across.
type PlannerConversion struct {
	Content PlanContent
	// UnavailableAgents names suggested agents that do not exist in the
	// workspace. They are reported so the Plan can say plainly that this work
	// needs an agent that is not here yet.
	UnavailableAgents []string
	// DroppedDependencies names dependency references the planner emitted that
	// pointed at nothing.
	DroppedDependencies []string
}

// FromPlannerOutput converts planner output into typed Plan content.
//
// availableAgents is the roster that actually exists. A nil slice means the
// roster is unknown, and every suggested agent is then carried through as an
// assignment — because refusing assignments against a roster nobody could read
// would strand every plan in a build with no agent store.
func FromPlannerOutput(request string, plan *types.PlannerOutput, availableAgents []string) (PlannerConversion, error) {
	if plan == nil {
		return PlannerConversion{}, fmt.Errorf("%w: no planner output to convert", ErrValidation)
	}
	if len(plan.Tasks) == 0 {
		return PlannerConversion{}, fmt.Errorf("%w: the planner proposed no work", ErrValidation)
	}

	known := map[string]string{}
	checkAgents := availableAgents != nil
	for _, name := range availableAgents {
		known[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(name)
	}

	// Plan-local item IDs are assigned here rather than trusting the planner's,
	// which may be empty or repeated. The mapping keeps dependency references
	// resolvable.
	itemIDs := make(map[string]string, len(plan.Tasks))
	for index, task := range plan.Tasks {
		itemIDs[plannerKey(task, index)] = NewItemID()
	}

	conversion := PlannerConversion{}
	group := TaskGroup{
		ID:      NewGroupID(),
		Title:   groupTitleFor(request),
		Outcome: strings.TrimSpace(plan.Rationale),
		Author:  AuthorModel,
	}

	seenUnavailable := map[string]bool{}
	for index, task := range plan.Tasks {
		item := TaskItem{
			ID:          itemIDs[plannerKey(task, index)],
			Description: strings.TrimSpace(task.Description),
			Author:      AuthorModel,
		}
		if item.Description == "" {
			item.Description = fmt.Sprintf("Step %d", index+1)
		}

		item.RequiredCapabilities = trimmed(task.RequiredCapabilities)

		suggested := strings.TrimSpace(task.SuggestedAgent)
		switch {
		case suggested == "":
			// Nothing suggested: the item materializes unassigned, and the
			// capability gate refuses to start it until somebody assigns it.
		case !checkAgents:
			item.Assignee = suggested
		default:
			if actual, exists := known[strings.ToLower(suggested)]; exists {
				item.Assignee = actual
				break
			}
			// A proposed agent that does not exist is NOT an assignment. It
			// becomes a required capability, so the compiled gate blocks the
			// item with a resolution path rather than a Task being created for
			// an agent nobody approved (task 9.5).
			item.RequiredCapabilities = append(item.RequiredCapabilities, "agent:"+suggested)
			if !seenUnavailable[suggested] {
				seenUnavailable[suggested] = true
				conversion.UnavailableAgents = append(conversion.UnavailableAgents, suggested)
			}
		}

		for _, dependency := range task.DependsOn {
			resolved, found := resolveDependency(plan, itemIDs, dependency)
			if !found {
				conversion.DroppedDependencies = append(conversion.DroppedDependencies, dependency)
				continue
			}
			if resolved == item.ID {
				// A self-dependency is never satisfiable; dropping it beats
				// materializing work that can never start.
				conversion.DroppedDependencies = append(conversion.DroppedDependencies, dependency)
				continue
			}
			item.DependsOn = append(item.DependsOn, resolved)
		}

		group.Items = append(group.Items, item)
	}

	// Every dynamic agent the planner proposed is surfaced, even when no task
	// referenced it by name: proposing one is a request to create an agent, and
	// that is approval-relevant whether or not the task list mentions it.
	for _, spec := range plan.DynamicAgents {
		name := strings.TrimSpace(spec.Name)
		if name == "" || seenUnavailable[name] {
			continue
		}
		if checkAgents {
			if _, exists := known[strings.ToLower(name)]; exists {
				continue
			}
		}
		seenUnavailable[name] = true
		conversion.UnavailableAgents = append(conversion.UnavailableAgents, name)
	}

	conversion.Content = PlanContent{
		Groups: []TaskGroup{group},
		// Step-through is the only honest default for converted work: nothing
		// about a planner suggestion says the user wanted it to run by itself
		// (FR-101, FR-131).
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
	}
	if rationale := strings.TrimSpace(plan.Rationale); rationale != "" {
		conversion.Content.Rationale = rationale
	}
	return conversion, nil
}

// plannerKey identifies one planner task for dependency resolution. The
// planner's own ID is preferred; its position is the fallback for output that
// omitted IDs entirely.
func plannerKey(task types.PlannerTask, index int) string {
	if id := strings.TrimSpace(task.ID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("index:%d", index)
}

// resolveDependency maps a planner dependency reference onto a Plan item ID.
//
// The reference may be the planner's task ID or a 1-based position, because
// planner output uses both. Anything else resolves to nothing and is reported
// as dropped rather than guessed at.
func resolveDependency(plan *types.PlannerOutput, itemIDs map[string]string, reference string) (string, bool) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", false
	}
	if id, found := itemIDs["id:"+reference]; found {
		return id, true
	}
	for index, task := range plan.Tasks {
		if strings.EqualFold(strings.TrimSpace(task.ID), reference) {
			return itemIDs[plannerKey(task, index)], true
		}
	}
	return "", false
}

// groupTitleFor names the converted group after the request it came from.
func groupTitleFor(request string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		return "Proposed work"
	}
	const limit = 60
	if len([]rune(request)) <= limit {
		return request
	}
	return string([]rune(request)[:limit]) + "…"
}

// trimmed returns the non-empty trimmed entries of a slice.
func trimmed(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			out = append(out, trimmedValue)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
