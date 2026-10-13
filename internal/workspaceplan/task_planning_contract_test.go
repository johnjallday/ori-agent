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
		"creation/merge and post-merge cleanup items remain unchecked",
		"Never auto-continue solely",
	} {
		if !strings.Contains(execution, required) {
			t.Errorf("missing execution boundary: %q", required)
		}
	}
}

func TestTaskPlanningTemplateSeparatesPRReadinessFromApprovedActions(t *testing.T) {
	protocol := taskPlanningProtocol(t)
	_, template, ok := strings.Cut(protocol, "```markdown\n")
	if !ok {
		t.Fatal("missing checklist template")
	}
	template, _, ok = strings.Cut(template, "\n```")
	if !ok {
		t.Fatal("unterminated checklist template")
	}
	for _, required := range []string{
		".agents/skills/task-planning/SKILL.md",
		"this document defines scope, not permission to start",
	} {
		if !strings.Contains(template, required) {
			t.Errorf("missing canonical delegation: %q", required)
		}
	}
	previous := -1
	for _, checkpoint := range []string{
		"Complete required validation and demos",
		"Prepare PR body with evidence and limitations",
		"PR gate: verify readiness and wait for explicit PR approval",
		"After PR approval: Open PR",
		"Run `wt done <feature>` after merge",
	} {
		index := strings.Index(template, checkpoint)
		if index < 0 || index <= previous {
			t.Fatalf("missing or out-of-order delivery checkpoint: %q", checkpoint)
		}
		previous = index
	}
	for _, stale := range []string{"planning-starter", "this session stops", "planning-only mode"} {
		if strings.Contains(strings.ToLower(template), stale) {
			t.Errorf("reusable checklist contains session-specific planning instruction: %q", stale)
		}
	}
}
