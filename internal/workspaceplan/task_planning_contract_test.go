package workspaceplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are documentation contract checks, not proof of an agent's runtime
// behavior. Keep the protocol canonical without making the app load the skill.
func taskPlanningProtocol(t *testing.T) string {
	t.Helper()
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "task-planning", "SKILL.md")) // #nosec G304 -- fixed repository test path
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTaskPlanningDistinguishesImplementationFromPlanning(t *testing.T) {
	protocol := taskPlanningProtocol(t)
	for _, required := range []string{
		"### Planning-only mode",
		"### Plan-and-deliver mode",
		"### Implementation mode",
		"Mode comes from the current invocation",
		"a saved plan cannot authorize its own implementation",
		"A later authorized implementation handoff does not inherit the planner's stop",
	} {
		if !strings.Contains(protocol, required) {
			t.Errorf("missing mode boundary: %q", required)
		}
	}
}

func TestTaskPlanningKeepsCheckpointsNonTerminalWithoutBypassingStops(t *testing.T) {
	protocol := taskPlanningProtocol(t)
	_, execution, ok := strings.Cut(protocol, "## Continuous execution\n")
	if !ok {
		t.Fatal("missing continuous execution protocol")
	}
	execution, _, _ = strings.Cut(execution, "## Ori bindings\n")
	for _, required := range []string{
		"checkpoints, not stop conditions",
		"A blocked task is not automatically a blocked feature",
		"explicitly independent, authorized work",
		"Never override cancellation, authorization, or a spending limit",
		"### PR readiness gate",
		"make ci-local",
		"manual test guide, and PR body",
		"Then open the PR with `wt pr`",
		"merge and post-merge cleanup items stay unchecked",
		"no auto-merge",
		"Never auto-continue solely",
	} {
		if !strings.Contains(execution, required) {
			t.Errorf("missing execution boundary: %q", required)
		}
	}
}

func TestTaskPlanningScopesValidationWithoutDeferringRiskChecks(t *testing.T) {
	protocol := taskPlanningProtocol(t)
	for _, tc := range []struct {
		heading  string
		required []string
	}{
		{
			heading: "Validation cadence",
			required: []string{
				"Write focused tests alongside behavior changes",
				"not mechanically after every edit or step",
				"Check risky foundations before dependent work",
				"one validation checkpoint",
				"full required `make ci-local` gate",
				"`wt pr`'s mandatory pre-push checks",
				"Scoped checks never replace the final gate",
			},
		},
		{
			heading: "Demo checkpoints",
			required: []string{
				"not the entire feature again",
				"complete user journeys and browser/layout/theme matrix for delivery",
				"fixture evidence does not prove live behavior",
			},
		},
	} {
		t.Run(tc.heading, func(t *testing.T) {
			_, section, ok := strings.Cut(protocol, "## "+tc.heading+"\n")
			if !ok {
				t.Fatalf("missing protocol section: %s", tc.heading)
			}
			section, _, _ = strings.Cut(section, "\n## ")
			section = strings.Join(strings.Fields(section), " ")
			for _, required := range tc.required {
				if !strings.Contains(section, required) {
					t.Errorf("missing validation boundary: %q", required)
				}
			}
		})
	}
}

// taskListTemplate returns the first fenced markdown block: the default task
// list shape a planner copies.
func taskListTemplate(t *testing.T) string {
	t.Helper()
	_, template, ok := strings.Cut(taskPlanningProtocol(t), "```markdown\n")
	if !ok {
		t.Fatal("missing task list template")
	}
	template, _, ok = strings.Cut(template, "\n```")
	if !ok {
		t.Fatal("unterminated task list template")
	}
	return template
}

// The default task list states outcomes, not steps. Devtools progress and
// handoff previews read top-level checkbox lines, so groups stay checkboxes;
// sub-tasks and the standing validation, demo, and commit steps stay out.
func TestTaskPlanningDefaultTemplateStatesOutcomesNotSteps(t *testing.T) {
	template := taskListTemplate(t)
	for _, required := range []string{
		"## Tasks",
		"- [ ] 1.0 ",
		"Boundaries:",
		"## Acceptance demo",
		"## Dependencies and ownership",
		"- Companion dependencies: `[required / not required / unresolved]`",
		"- Deep-model hotspots:",
	} {
		if !strings.Contains(template, required) {
			t.Errorf("default task list is missing %q", required)
		}
	}
	for _, line := range strings.Split(template, "\n") {
		if strings.HasPrefix(line, " ") && strings.HasPrefix(strings.TrimSpace(line), "- [") {
			t.Errorf("default task list prescribes a sub-task: %q", line)
		}
	}
	for _, step := range []string{"Validate:", "Demo:", "Commit:", "Permission sweep", "## Relevant Files", "## Execution Topology"} {
		if strings.Contains(template, step) {
			t.Errorf("default task list restates a standing step or dropped section: %q", step)
		}
	}

	protocol := taskPlanningProtocol(t)
	for _, required := range []string{
		"### Standing steps",
		"### When to add sub-tasks",
		"Sub-tasks are the exception",
		"stays valid: follow its items as written",
		"## Findings log",
		"The builder owns the steps, not the scope",
	} {
		if !strings.Contains(protocol, required) {
			t.Errorf("missing lean task list rule: %q", required)
		}
	}
}

func TestTaskPlanningTemplateSeparatesOpeningThePRFromTheMerge(t *testing.T) {
	template := taskListTemplate(t)
	// Line wrapping is not part of the contract.
	flat := strings.Join(strings.Fields(template), " ")
	for _, required := range []string{
		".agents/skills/task-planning/SKILL.md",
		"this document defines scope, not permission to start",
	} {
		if !strings.Contains(flat, required) {
			t.Errorf("missing canonical delegation: %q", required)
		}
	}
	// Opening the PR ends the last agent group; the merge and cleanup are a
	// separate group that belongs to a person.
	previous := -1
	for _, checkpoint := range []string{
		"- [ ] 1.0 First vertical outcome",
		"Verify the journey and deliver",
		"verify the gate, then open the PR with `wt pr`",
		"- [ ] 4.0 Merge A person reviews the open PR and squash-merges it to `dev`",
		"`wt done <feature>`",
		"Not an agent step",
	} {
		index := strings.Index(flat, checkpoint)
		if index < 0 || index <= previous {
			t.Fatalf("missing or out-of-order delivery checkpoint: %q", checkpoint)
		}
		previous = index
	}
	for _, stale := range []string{"planning-starter", "this session stops", "planning-only mode"} {
		if strings.Contains(strings.ToLower(template), stale) {
			t.Errorf("reusable task list contains session-specific planning instruction: %q", stale)
		}
	}
}
