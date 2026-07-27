package tasklist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePlanSeparatesMilestonesFromSubtasks(t *testing.T) {
	plan := ParsePlan(`
- [x] 1.0 First group
  - [x] 1.1 Done work
  - [x] 1.2 More done work
- [ ] 2.0 Second group
  - [x] 2.1 Done work
  - [ ] 2.2 Remaining work
  - [ ] 2.3 Later work
`)
	if plan.State != PlanAvailable {
		t.Fatalf("state = %q, want available", plan.State)
	}
	if plan.MilestonesTotal != 2 || plan.MilestonesCompleted != 1 {
		t.Fatalf("milestones = %d/%d, want 1/2", plan.MilestonesCompleted, plan.MilestonesTotal)
	}
	if plan.SubtasksTotal != 5 || plan.SubtasksCompleted != 3 {
		t.Fatalf("subtasks = %d/%d, want 3/5", plan.SubtasksCompleted, plan.SubtasksTotal)
	}
	if plan.ActiveMilestone.Ordinal != "2.0" {
		t.Fatalf("active milestone = %q, want 2.0", plan.ActiveMilestone.Ordinal)
	}
	if plan.NextActionable.Ordinal != "2.2" {
		t.Fatalf("next actionable = %q, want 2.2", plan.NextActionable.Ordinal)
	}
}

func TestParsePlanSkipsMilestonesWhoseSubtasksAreAllDone(t *testing.T) {
	// A parent left unchecked after all of its subtasks are ticked is
	// bookkeeping lag, not where the work is.
	plan := ParsePlan(`
- [ ] 1.0 Bookkeeping lag
  - [x] 1.1 Done
  - [x] 1.2 Done
- [ ] 2.0 Real work
  - [ ] 2.1 Outstanding
`)
	if plan.ActiveMilestone.Ordinal != "2.0" || plan.NextActionable.Ordinal != "2.1" {
		t.Fatalf("active/next = %q/%q, want 2.0/2.1", plan.ActiveMilestone.Ordinal, plan.NextActionable.Ordinal)
	}
	// It is still counted as incomplete, because it genuinely is unchecked.
	if plan.MilestonesCompleted != 0 {
		t.Fatalf("completed milestones = %d, want 0", plan.MilestonesCompleted)
	}
}

func TestParsePlanParentOnlyPlan(t *testing.T) {
	plan := ParsePlan(`
- [x] 1.0 Shipped group
- [ ] 2.0 Next group
- [ ] 3.0 Later group
`)
	if plan.SubtasksTotal != 0 {
		t.Fatalf("subtasks = %d, want none", plan.SubtasksTotal)
	}
	if plan.ActiveMilestone.Ordinal != "2.0" {
		t.Fatalf("active milestone = %q, want 2.0", plan.ActiveMilestone.Ordinal)
	}
	if plan.NextActionable.Ordinal != "2.0" {
		t.Fatalf("next actionable = %q, want the parent itself for a parent-only plan", plan.NextActionable.Ordinal)
	}
}

func TestParsePlanIgnoresFencedExamplesAndProse(t *testing.T) {
	plan := ParsePlan(`
## Instructions

Example format:

` + "```markdown" + `
- [ ] 1.0 Example parent task
  - [ ] 1.1 Example subtask
  - [ ] 1.2 Another example
` + "```" + `

## Tasks

- [ ] 5.0 The only real group
  - [ ] 5.1 The only real subtask
- [ ] prose checkbox with no ordinal
- [ ] 1.2.3 deeply nested ordinal
- [ ] 99999.1 out-of-range ordinal
`)
	if plan.MilestonesTotal != 1 || plan.SubtasksTotal != 1 {
		t.Fatalf("counts = %d milestones / %d subtasks, want 1/1 (fenced examples and malformed ordinals must be ignored)",
			plan.MilestonesTotal, plan.SubtasksTotal)
	}
	if plan.ActiveMilestone.Ordinal != "5.0" {
		t.Fatalf("active milestone = %q, want 5.0", plan.ActiveMilestone.Ordinal)
	}
}

func TestParsePlanMalformedIsNotZeroProgress(t *testing.T) {
	plan := ParsePlan("- [ ] prose only\n- [x] still prose\n")
	if plan.State != PlanMalformed {
		t.Fatalf("state = %q, want malformed", plan.State)
	}
	if plan.ParseIssue == "" {
		t.Fatal("malformed plan carried no explanation")
	}
	// The caller must be able to tell this apart from a plan with no work done.
	if plan.MilestonesTotal != 0 || plan.SubtasksTotal != 0 {
		t.Fatalf("counts = %d/%d, want zero alongside the malformed state", plan.MilestonesTotal, plan.SubtasksTotal)
	}
}

func TestParsePlanClassifiesDeliveryCheckpoints(t *testing.T) {
	plan := ParsePlan(`
- [ ] 1.0 Group
  - [x] 1.1 Real implementation work
  - [ ] 1.2 Demo: drive the new surface in the browser
  - [ ] 1.3 Commit: "feat: add the thing"
  - [ ] 1.4 Write manual test guide: tasks/test-guide-x.md
  - [ ] 1.5 Open PR → squash-merge to dev
  - [ ] 1.6 Run ` + "`wt done`" + ` after merge
`)
	if !plan.ImplementationComplete {
		t.Fatal("implementation was not reported complete when only checkpoints remain")
	}
	if plan.DeliveryCheckpointsRemaining != 5 {
		t.Fatalf("remaining checkpoints = %d, want 5", plan.DeliveryCheckpointsRemaining)
	}
	// With no implementation work left, the next item is still concrete.
	if plan.NextActionable.Ordinal != "1.2" {
		t.Fatalf("next actionable = %q, want the first outstanding checkpoint", plan.NextActionable.Ordinal)
	}
}

func TestParsePlanImplementationCompleteRequiresRealWork(t *testing.T) {
	// A group of nothing but checkpoints must not report implementation done.
	plan := ParsePlan("- [ ] 1.0 Group\n  - [ ] 1.1 Commit: \"chore: nothing\"\n")
	if plan.ImplementationComplete {
		t.Fatal("a checkpoint-only plan claimed its implementation was complete")
	}
}

func TestParsePlanPrefersImplementationOverCheckpoints(t *testing.T) {
	plan := ParsePlan(`
- [ ] 3.0 Group
  - [ ] 3.1 Commit: "wip"
  - [ ] 3.2 Actual outstanding work
`)
	if plan.NextActionable.Ordinal != "3.2" {
		t.Fatalf("next actionable = %q, want real work over a checkpoint", plan.NextActionable.Ordinal)
	}
	if plan.ImplementationComplete {
		t.Fatal("implementation reported complete with outstanding work")
	}
}

func TestParsePlanAdoptsSubtasksListedBeforeTheirParent(t *testing.T) {
	plan := ParsePlan(`
  - [ ] 4.1 Subtask before its parent
- [ ] 4.0 Parent declared later
  - [ ] 4.2 Second subtask
`)
	if plan.MilestonesTotal != 1 {
		t.Fatalf("milestones = %d, want the parent adopted rather than split", plan.MilestonesTotal)
	}
	if plan.SubtasksTotal != 2 {
		t.Fatalf("subtasks = %d, want 2", plan.SubtasksTotal)
	}
	if plan.ParseIssue != "" {
		t.Fatalf("parse issue = %q, want none once the parent is declared", plan.ParseIssue)
	}
}

func TestParsePlanReportsOrphanSubtasks(t *testing.T) {
	plan := ParsePlan("  - [ ] 8.1 Subtask with no parent anywhere\n")
	if plan.SubtasksTotal != 1 {
		t.Fatalf("subtasks = %d, want the orphan still counted", plan.SubtasksTotal)
	}
	if plan.MilestonesTotal != 0 {
		t.Fatalf("milestones = %d, want no undeclared parent counted", plan.MilestonesTotal)
	}
	if !strings.Contains(plan.ParseIssue, "no declared parent") {
		t.Fatalf("parse issue = %q, want the orphan explained", plan.ParseIssue)
	}
}

func TestParsePlanSanitizesItemText(t *testing.T) {
	plan := ParsePlan("- [ ] 1.0 Group\n  - [ ] 1.1 text with \x1b[31mescape\x00 codes\n")
	next := plan.NextActionable
	if strings.ContainsAny(next.Text, "\x00\x1b") {
		t.Fatalf("item text kept control characters: %q", next.Text)
	}
}

func TestParsePlanBoundsItemText(t *testing.T) {
	long := strings.Repeat("x", MaxItemRunes*2)
	plan := ParsePlan("- [ ] 1.0 Group\n  - [ ] 1.1 " + long + "\n")
	if runes := []rune(plan.NextActionable.Text); len(runes) > MaxItemRunes+1 {
		t.Fatalf("item text length = %d, want bounded to %d", len(runes), MaxItemRunes)
	}
}

func TestReadPlanDistinguishesAbsentFromUnreadable(t *testing.T) {
	absent := ReadPlan(filepath.Join(t.TempDir(), "tasks-missing.md"))
	if absent.State != PlanAbsent {
		t.Fatalf("state = %q, want absent", absent.State)
	}
	if absent.Path != "" {
		t.Fatalf("absent plan retained path %q", absent.Path)
	}

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tasks-dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	directory := ReadPlan(filepath.Join(dir, "tasks-dir.md"))
	if directory.State != PlanMalformed {
		t.Fatalf("state = %q, want malformed for a directory", directory.State)
	}
}

func TestReadPlanParsesFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks-feature.md")
	contents := "- [x] 1.0 Done group\n  - [x] 1.1 Done\n- [ ] 2.0 Live group\n  - [ ] 2.1 Next\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := ReadPlan(path)
	if plan.State != PlanAvailable || plan.NextActionable.Ordinal != "2.1" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.NextActionable.Line != 4 {
		t.Fatalf("line = %d, want the 1-based source line 4", plan.NextActionable.Line)
	}
}

func TestParsePlanAllWorkCompleteLeavesNoNextItem(t *testing.T) {
	plan := ParsePlan("- [x] 1.0 Group\n  - [x] 1.1 Done\n")
	if !plan.ActiveMilestone.Empty() {
		t.Fatalf("active milestone = %+v, want none when everything is complete", plan.ActiveMilestone)
	}
	if !plan.NextActionable.Empty() {
		t.Fatalf("next actionable = %+v, want none", plan.NextActionable)
	}
	if plan.MilestonesCompleted != plan.MilestonesTotal {
		t.Fatal("a fully checked plan did not report all milestones complete")
	}
}
