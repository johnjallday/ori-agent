package tasklist

import "testing"

// TestBoundaryClassificationIsBiasedTowardStopping is the safety property: an
// unrecognized checkpoint is manual. Getting this backwards means an agent
// opens a PR or deletes a worktree while nobody is watching.
func TestBoundaryClassificationIsBiasedTowardStopping(t *testing.T) {
	cases := []struct {
		text       string
		checkpoint bool
		want       Boundary
	}{
		// Ordinary implementation work.
		{"Add hermetic inventory fixtures", false, BoundaryImplementation},
		{"Extend the worktree inventory to union the primary checkout", false, BoundaryImplementation},
		{"Write manual test guide: tasks/test-guide-feature.md", false, BoundaryImplementation},

		// Safe delivery work the plan already asked for.
		{"Commit: \"fix(herd): unify all-agent status\"", true, BoundaryCommit},
		{"Validate the completed slice with targeted tests", true, BoundaryValidation},
		{"Verify the JSON contract is stable", true, BoundaryValidation},

		// Work only a person may do.
		{"Demo: wt demo → drive the new surface in the browser", true, BoundaryManual},
		{"Prototype demo: drive the surface against stubs", true, BoundaryManual},
		{"Open PR → squash-merge to dev", true, BoundaryManual},
		{"Open seam-PR → dev", true, BoundaryManual},
		{"Run `wt done herdr-overnight-agent-completion` after merge", true, BoundaryManual},
		{"Design sign-off with the user", false, BoundaryManual},
		{"Supply the API key for the integration", false, BoundaryManual},
		{"Deploy the release to production", false, BoundaryManual},

		// The default that matters: a checkpoint nobody recognized.
		{"Coordinate the rollout with the platform team", true, BoundaryManual},
		{"Something entirely new", true, BoundaryManual},
	}
	for _, testCase := range cases {
		t.Run(testCase.text, func(t *testing.T) {
			got := classifyBoundary(testCase.text, testCase.checkpoint)
			if got != testCase.want {
				t.Fatalf("classifyBoundary(%q, %v) = %q, want %q", testCase.text, testCase.checkpoint, got, testCase.want)
			}
			if testCase.want == BoundaryManual && got.Safe() {
				t.Fatal("a manual boundary reported itself safe")
			}
		})
	}
}

// TestSafeNextStopsAtAManualCheckpointRatherThanReachingPastIt is the ordering
// rule. The next implementation task existing somewhere later in the plan is
// not permission to skip the demo standing in front of it.
func TestSafeNextStopsAtAManualCheckpointRatherThanReachingPastIt(t *testing.T) {
	plan := ParsePlan(`
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [ ] 1.2 Demo: drive the new surface
  - [ ] 1.3 Continue the implementation
`)
	next, ok := plan.NextUnfinished()
	if !ok || next.Ordinal != "1.2" {
		t.Fatalf("next unfinished = %+v, want the demo that comes first", next)
	}
	if _, ok := plan.SafeNext(); ok {
		t.Fatal("SafeNext reached past a manual checkpoint")
	}
	manual, ok := plan.FirstManual()
	if !ok || manual.Ordinal != "1.2" {
		t.Fatalf("first manual = %+v, want 1.2", manual)
	}
	// The existing progress view still reports the later implementation task,
	// because that question — "what work remains" — has a different answer.
	if plan.NextActionable.Ordinal != "1.3" {
		t.Fatalf("next actionable = %+v, want the later implementation subtask", plan.NextActionable)
	}
}

func TestSafeNextAcceptsValidationAndCommitSteps(t *testing.T) {
	plan := ParsePlan(`
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [ ] 1.2 Validate the slice with targeted tests
  - [ ] 1.3 Commit: "feat: land the slice"
  - [ ] 1.4 Demo: drive the new surface
`)
	next, ok := plan.SafeNext()
	if !ok || next.Ordinal != "1.2" || next.Boundary != BoundaryValidation {
		t.Fatalf("safe next = %+v, %v; want the validation step", next, ok)
	}

	// With validation done, the commit is still safe.
	plan = ParsePlan(`
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [x] 1.2 Validate the slice with targeted tests
  - [ ] 1.3 Commit: "feat: land the slice"
  - [ ] 1.4 Demo: drive the new surface
`)
	next, ok = plan.SafeNext()
	if !ok || next.Ordinal != "1.3" || next.Boundary != BoundaryCommit {
		t.Fatalf("safe next = %+v, %v; want the commit", next, ok)
	}

	// With the commit done, only the demo remains and nothing is safe.
	plan = ParsePlan(`
- [ ] 1.0 Build it
  - [x] 1.1 Land the groundwork
  - [x] 1.2 Validate the slice with targeted tests
  - [x] 1.3 Commit: "feat: land the slice"
  - [ ] 1.4 Demo: drive the new surface
`)
	if _, ok := plan.SafeNext(); ok {
		t.Fatal("a plan with only a demo left offered safe work")
	}
	if !plan.ImplementationBoundaryComplete() {
		t.Fatal("the implementation boundary was not reported complete")
	}
}

func TestImplementationBoundaryCompleteIgnoresManualWork(t *testing.T) {
	incomplete := ParsePlan(`
- [ ] 1.0 Build it
  - [ ] 1.1 Continue the implementation
  - [ ] 1.2 Open PR → squash-merge to dev
`)
	if incomplete.ImplementationBoundaryComplete() {
		t.Fatal("a plan with implementation work left reported its boundary complete")
	}

	complete := ParsePlan(`
- [ ] 1.0 Build it
  - [x] 1.1 Continue the implementation
  - [ ] 1.2 Open PR → squash-merge to dev
`)
	if !complete.ImplementationBoundaryComplete() {
		t.Fatal("a plan with only manual work left did not report its boundary complete")
	}
}

// TestNextUnfinishedHandlesAPlanWithNoSubtasks covers the parent-only shape a
// short plan takes.
func TestNextUnfinishedHandlesAPlanWithNoSubtasks(t *testing.T) {
	plan := ParsePlan(`
- [x] 1.0 Done group
- [ ] 2.0 Continue the work
`)
	next, ok := plan.NextUnfinished()
	if !ok || next.Ordinal != "2.0" {
		t.Fatalf("next unfinished = %+v, want the incomplete parent", next)
	}
	if _, ok := plan.SafeNext(); !ok {
		t.Fatal("a plain parent task was not safe to continue")
	}
}

func TestAFinishedPlanOffersNothing(t *testing.T) {
	plan := ParsePlan(`
- [x] 1.0 Build it
  - [x] 1.1 Land the groundwork
`)
	if _, ok := plan.NextUnfinished(); ok {
		t.Fatal("a finished plan reported unfinished work")
	}
	if _, ok := plan.SafeNext(); ok {
		t.Fatal("a finished plan offered safe work")
	}
	if !plan.ImplementationBoundaryComplete() {
		t.Fatal("a finished plan did not report its boundary complete")
	}
}

// TestThisRepositorysOwnCheckpointsClassifyCorrectly runs the classifier over
// the exact wording this project's task lists use, since those are the plans it
// will actually be pointed at.
func TestThisRepositorysOwnCheckpointsClassifyCorrectly(t *testing.T) {
	plan := ParsePlan(`
- [ ] 8.0 Validate every requirement
  - [ ] 8.4 Run formatting and the complete validation set: targeted and full Go tests
  - [ ] 8.7 Commit: "test(herd): harden overnight completion"
  - [ ] 8.8 Demo: manually review the all-agent roster and one simulated reset cycle
  - [ ] 8.10 Open PR → squash-merge to ` + "`dev`" + ` with ` + "`wt pr`" + `
  - [ ] 8.11 Run ` + "`wt done herdr-overnight-agent-completion`" + ` after merge
`)
	want := map[string]Boundary{
		"8.4":  BoundaryImplementation,
		"8.7":  BoundaryCommit,
		"8.8":  BoundaryManual,
		"8.10": BoundaryManual,
		"8.11": BoundaryManual,
	}
	for _, milestone := range plan.Milestones {
		for _, subtask := range milestone.Subtasks {
			expected, known := want[subtask.Ordinal]
			if !known {
				continue
			}
			if subtask.Boundary != expected {
				t.Fatalf("%s %q = %q, want %q", subtask.Ordinal, subtask.Text, subtask.Boundary, expected)
			}
		}
	}
	// The first thing an unattended agent may not cross is the demo.
	manual, ok := plan.FirstManual()
	if !ok || manual.Ordinal != "8.8" {
		t.Fatalf("first manual = %+v, want the demo", manual)
	}
}
