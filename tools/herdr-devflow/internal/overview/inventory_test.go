package overview

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

var observed = time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)

func devSet(features map[string]planning.Feature) planning.Set {
	return planning.Set{
		Dir:        "/repo/worktrees/ori-agent-dev/tasks",
		State:      planning.StateAvailable,
		Features:   features,
		ObservedAt: observed,
	}
}

func devFeature(slug, title string) planning.Feature {
	return planning.Feature{
		Slug:     slug,
		PRD:      planning.Artifact{Path: "/dev/tasks/prd-" + slug + ".md", State: planning.StateAvailable, Title: title},
		TaskList: planning.Artifact{Path: "/dev/tasks/tasks-" + slug + ".md", State: planning.StateAvailable},
	}
}

func checkoutInventory(checkouts ...worktree.Checkout) worktree.Inventory {
	inventory := worktree.Inventory{Features: map[string][]worktree.Checkout{}, ObservedAt: observed}
	for _, checkout := range checkouts {
		inventory.Checkouts = append(inventory.Checkouts, checkout)
		if checkout.Slug != "" {
			inventory.Features[checkout.Slug] = append(inventory.Features[checkout.Slug], checkout)
		}
	}
	return inventory
}

func featureCheckout(slug, path string) worktree.Checkout {
	return worktree.Checkout{
		Path:       path,
		Branch:     worktree.FeatureBranchPrefix + slug,
		Head:       "abc123",
		Slug:       slug,
		SlugOrigin: worktree.SlugFromBranch,
		PathSlug:   slug,
	}
}

func findingFor(findings []Finding, code FindingCode) (Finding, bool) {
	for _, finding := range findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return Finding{}, false
}

func TestBuildInventoryUnionsEverySourceOnExactSlugs(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{
			"planned-only": devFeature("planned-only", "PRD: Planned Only"),
		}),
		Backlog: planning.Backlog{Entries: map[string]planning.Entry{
			"shipped-history": {Slug: "shipped-history", Lifecycle: planning.LifecycleShipped, Text: "shipped-history - PR #1 merged", Line: 44},
		}},
		Checkouts:   checkoutInventory(featureCheckout("in-progress", "/repo/worktrees/in-progress")),
		BridgeSlugs: []string{"bridge-only"},
		HerdrSlugs:  []string{"herdr-only"},
		GitHubSlugs: []string{"remote-only"},
		Now:         observed,
	}

	features, findings := BuildInventory(input)
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none for a clean union", findings)
	}
	want := []string{"bridge-only", "herdr-only", "in-progress", "planned-only", "remote-only", "shipped-history"}
	if len(features) != len(want) {
		t.Fatalf("features = %d, want %d", len(features), len(want))
	}
	for index, slug := range want {
		if features[index].Slug != slug {
			t.Fatalf("features[%d] = %q, want %q (sorted union)", index, features[index].Slug, slug)
		}
	}
}

func TestBuildInventoryRecordsContributingSources(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"shared": devFeature("shared", "PRD: Shared")}),
		Backlog: planning.Backlog{Entries: map[string]planning.Entry{
			"shared": {Slug: "shared", Lifecycle: planning.LifecycleDoing, Text: "shared -> PRD", Line: 12},
		}},
		Checkouts: checkoutInventory(featureCheckout("shared", "/repo/worktrees/shared")),
		Now:       observed,
	}

	features, _ := BuildInventory(input)
	if len(features) != 1 {
		t.Fatalf("features = %d, want one joined row", len(features))
	}
	feature := features[0]
	want := []SourceKind{SourcePlanning, SourceBacklog, SourceWorktree}
	if len(feature.Sources) != len(want) {
		t.Fatalf("sources = %v, want %v", feature.Sources, want)
	}
	for index := range want {
		if feature.Sources[index] != want[index] {
			t.Fatalf("sources = %v, want %v in fixed order", feature.Sources, want)
		}
	}
	if feature.Backlog.State != BacklogDoing || feature.Backlog.Line != 12 {
		t.Fatalf("backlog = %+v, want the Doing entry with provenance", feature.Backlog)
	}
	if feature.Title != "PRD: Shared" {
		t.Fatalf("title = %q, want the PRD title", feature.Title)
	}
}

func TestBuildInventoryIgnoresNonCanonicalSlugs(t *testing.T) {
	input := Input{
		BridgeSlugs: []string{"", "Not-A-Slug", "../escape", "good-slug"},
		Now:         observed,
	}
	features, _ := BuildInventory(input)
	if len(features) != 1 || features[0].Slug != "good-slug" {
		t.Fatalf("features = %v, want only the canonical slug", features)
	}
}

func TestBuildInventoryPrefersTheActiveWorktreePlanCopy(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"live": devFeature("live", "PRD: Stale Dev Copy")}),
		Checkouts:   checkoutInventory(featureCheckout("live", "/repo/worktrees/live")),
		LookupActivePlan: func(path, slug string) (planning.Feature, error) {
			if path != "/repo/worktrees/live" || slug != "live" {
				t.Fatalf("active lookup called with %q/%q", path, slug)
			}
			return planning.Feature{
				Slug:     slug,
				PRD:      planning.Artifact{Path: path + "/tasks/prd-live.md", State: planning.StateAvailable, Title: "PRD: Active Copy"},
				TaskList: planning.Artifact{Path: path + "/tasks/tasks-live.md", State: planning.StateAvailable},
			}, nil
		},
		Now: observed,
	}

	features, _ := BuildInventory(input)
	plan := features[0].Plan
	if plan.Copy != PlanCopyActive {
		t.Fatalf("copy = %q, want the active worktree copy while it exists", plan.Copy)
	}
	if plan.Title != "PRD: Active Copy" {
		t.Fatalf("title = %q, want the active copy's title", plan.Title)
	}
}

func TestBuildInventoryFallsBackToDevArchiveAfterCleanup(t *testing.T) {
	// No checkout: `wt done` has removed the worktree and archived the ticked
	// copy into dev, so the dev copy is authoritative again.
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"archived": devFeature("archived", "PRD: Archived")}),
		LookupActivePlan: func(string, string) (planning.Feature, error) {
			t.Fatal("active lookup ran for a feature with no worktree")
			return planning.Feature{}, nil
		},
		Now: observed,
	}

	features, _ := BuildInventory(input)
	if features[0].Plan.Copy != PlanCopyDev {
		t.Fatalf("copy = %q, want the dev archive", features[0].Plan.Copy)
	}
}

func TestBuildInventoryFallsBackWhenTheActiveCopyIsUnreadable(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"live": devFeature("live", "PRD: Dev Copy")}),
		Checkouts:   checkoutInventory(featureCheckout("live", "/repo/worktrees/live")),
		LookupActivePlan: func(string, string) (planning.Feature, error) {
			return planning.Feature{}, errors.New("tasks directory unreadable")
		},
		Now: observed,
	}

	features, _ := BuildInventory(input)
	if features[0].Plan.Copy != PlanCopyDev {
		t.Fatalf("copy = %q, want a fallback to dev rather than an empty plan", features[0].Plan.Copy)
	}
}

func TestBuildInventoryMarksAMissingPlanAbsentNotUnknown(t *testing.T) {
	input := Input{BridgeSlugs: []string{"no-plan"}, Now: observed}
	features, _ := BuildInventory(input)

	plan := features[0].Plan
	if plan.Copy != PlanCopyNone {
		t.Fatalf("copy = %q, want none", plan.Copy)
	}
	if plan.PRDAvailability != AvailabilityAbsent || plan.TaskListAvailability != AvailabilityAbsent {
		t.Fatalf("availability = %q/%q, want absent", plan.PRDAvailability, plan.TaskListAvailability)
	}
	if plan.Progress.Availability != AvailabilityAbsent {
		t.Fatalf("progress availability = %q, want absent rather than a real zero", plan.Progress.Availability)
	}
}

func TestBuildInventoryPreservesAmbiguousCheckoutsInsteadOfGuessing(t *testing.T) {
	first := featureCheckout("contested", "/repo/worktrees/contested")
	second := featureCheckout("contested", "/repo/worktrees/contested-copy")
	input := Input{Checkouts: checkoutInventory(first, second), Now: observed}

	features, findings := BuildInventory(input)
	finding, ok := findingFor(findings, FindingIdentityAmbiguous)
	if !ok {
		t.Fatalf("findings = %v, want an ambiguity finding", findings)
	}
	if finding.Severity != SeverityError || finding.Feature != "contested" {
		t.Fatalf("finding = %+v, want a feature-scoped error", finding)
	}
	if features[0].Git.WorktreePath != "" {
		t.Fatalf("Git state = %+v, want no checkout attributed when the claim is ambiguous", features[0].Git)
	}
}

func TestBuildInventoryReportsADirectoryRenamedAwayFromItsBranch(t *testing.T) {
	renamed := featureCheckout("real-slug", "/repo/worktrees/old-name")
	renamed.PathSlug = "old-name"
	input := Input{Checkouts: checkoutInventory(renamed), Now: observed}

	features, findings := BuildInventory(input)
	finding, ok := findingFor(findings, FindingNameMismatch)
	if !ok {
		t.Fatalf("findings = %v, want a name-mismatch finding", findings)
	}
	if finding.Severity != SeverityWarning {
		t.Fatalf("severity = %q, want warning", finding.Severity)
	}
	// The branch's stronger claim still wins for attribution.
	if features[0].Git.WorktreePath != "/repo/worktrees/old-name" {
		t.Fatalf("Git state = %+v, want the checkout still attributed", features[0].Git)
	}
}

func TestBuildInventoryLeavesGitAvailabilityUnknownUntilInspected(t *testing.T) {
	input := Input{Checkouts: checkoutInventory(featureCheckout("pending", "/repo/worktrees/pending")), Now: observed}
	features, _ := BuildInventory(input)

	// The union only records which checkout exists; Git facts are gathered
	// separately, so availability must not read as available yet.
	if features[0].Git.Availability != AvailabilityUnknown {
		t.Fatalf("availability = %q, want unknown before inspection", features[0].Git.Availability)
	}
	if features[0].Git.Branch != "feature/pending" {
		t.Fatalf("branch = %q, want the listed branch", features[0].Git.Branch)
	}
}

func planWith(state tasklist.PlanState, mutate ...func(*tasklist.Plan)) tasklist.Plan {
	parsed := tasklist.Plan{State: state}
	for _, apply := range mutate {
		apply(&parsed)
	}
	return parsed
}

func TestBuildInventoryAttachesHierarchicalProgress(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"measured": devFeature("measured", "PRD: Measured")}),
		ReadPlanProgress: func(path string) tasklist.Plan {
			if path != "/dev/tasks/tasks-measured.md" {
				t.Fatalf("progress read from unexpected path %q", path)
			}
			return planWith(tasklist.PlanAvailable, func(p *tasklist.Plan) {
				p.MilestonesTotal, p.MilestonesCompleted = 7, 4
				p.SubtasksTotal, p.SubtasksCompleted = 118, 66
				p.ActiveMilestone = tasklist.Item{Ordinal: "5.0", Text: "Fifth group"}
				p.NextActionable = tasklist.Item{Ordinal: "5.1", Text: "Next step"}
				p.DeliveryCheckpointsRemaining = 3
				p.DeliveryCheckpoints = []tasklist.Item{{Ordinal: "5.9", Text: "Commit: feat", Checkpoint: true}}
			})
		},
		Now: observed,
	}

	features, _ := BuildInventory(input)
	progress := features[0].Plan.Progress
	if !progress.Availability.OK() {
		t.Fatalf("availability = %q, want available", progress.Availability)
	}
	if progress.MilestonesCompleted != 4 || progress.MilestonesTotal != 7 {
		t.Fatalf("milestones = %d/%d, want 4/7", progress.MilestonesCompleted, progress.MilestonesTotal)
	}
	if progress.SubtasksCompleted != 66 || progress.SubtasksTotal != 118 {
		t.Fatalf("subtasks = %d/%d, want 66/118", progress.SubtasksCompleted, progress.SubtasksTotal)
	}
	if progress.NextActionable.Ordinal != "5.1" {
		t.Fatalf("next = %q, want 5.1", progress.NextActionable.Ordinal)
	}
	if progress.DeliveryCheckpointsRemaining != 3 || len(progress.DeliveryCheckpoints) != 1 {
		t.Fatalf("checkpoints = %d listed / %d remaining", len(progress.DeliveryCheckpoints), progress.DeliveryCheckpointsRemaining)
	}
}

func TestBuildInventoryDropsCountsFromAnUnparsedPlan(t *testing.T) {
	for _, state := range []tasklist.PlanState{tasklist.PlanMalformed, tasklist.PlanUnavailable} {
		input := Input{
			DevPlanning: devSet(map[string]planning.Feature{"broken": devFeature("broken", "PRD: Broken")}),
			ReadPlanProgress: func(string) tasklist.Plan {
				return planWith(state, func(p *tasklist.Plan) {
					// A parser that bailed out may still carry partial counts.
					p.MilestonesTotal, p.SubtasksTotal = 3, 9
					p.ParseIssue = "no numbered checklist items were found"
				})
			},
			Now: observed,
		}
		features, _ := BuildInventory(input)
		progress := features[0].Plan.Progress
		if progress.Availability.OK() {
			t.Fatalf("state %q was reported as available progress", state)
		}
		if progress.MilestonesTotal != 0 || progress.SubtasksTotal != 0 {
			t.Fatalf("state %q kept counts %d/%d; an unparsed plan must not present numbers",
				state, progress.MilestonesTotal, progress.SubtasksTotal)
		}
		if progress.ParseIssue == "" {
			t.Fatalf("state %q carried no explanation", state)
		}
	}
}

func TestBuildInventoryReadsProgressFromTheAuthoritativeCopy(t *testing.T) {
	var readPaths []string
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"live": devFeature("live", "PRD: Dev")}),
		Checkouts:   checkoutInventory(featureCheckout("live", "/repo/worktrees/live")),
		LookupActivePlan: func(path, slug string) (planning.Feature, error) {
			return planning.Feature{
				Slug:     slug,
				PRD:      planning.Artifact{Path: path + "/tasks/prd-live.md", State: planning.StateAvailable, Title: "PRD: Active"},
				TaskList: planning.Artifact{Path: path + "/tasks/tasks-live.md", State: planning.StateAvailable},
			}, nil
		},
		ReadPlanProgress: func(path string) tasklist.Plan {
			readPaths = append(readPaths, path)
			return planWith(tasklist.PlanAvailable, func(p *tasklist.Plan) { p.MilestonesTotal = 1 })
		},
		Now: observed,
	}

	BuildInventory(input)
	if len(readPaths) != 1 || readPaths[0] != "/repo/worktrees/live/tasks/tasks-live.md" {
		t.Fatalf("progress read from %v, want only the active worktree copy", readPaths)
	}
}

func TestBuildInventoryLeavesProgressAbsentWithNoTaskList(t *testing.T) {
	input := Input{
		DevPlanning: devSet(map[string]planning.Feature{"prd-only": {
			Slug:     "prd-only",
			PRD:      planning.Artifact{Path: "/dev/tasks/prd-prd-only.md", State: planning.StateAvailable},
			TaskList: planning.Artifact{State: planning.StateAbsent},
		}}),
		ReadPlanProgress: func(string) tasklist.Plan {
			t.Fatal("the parser ran for a feature with no task list")
			return tasklist.Plan{}
		},
		Now: observed,
	}

	features, _ := BuildInventory(input)
	if got := features[0].Plan.Progress.Availability; got != AvailabilityAbsent {
		t.Fatalf("availability = %q, want absent", got)
	}
}
