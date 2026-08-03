package overview

import (
	"strings"
	"testing"
)

func localOptions() DeriveOptions { return DeriveOptions{Baseline: "dev"} }

// feature builds a row with the evidence a test cares about and explicit
// absences everywhere else, so no assertion depends on a zero value.
func feature(slug string, mutate ...func(*Feature)) Feature {
	built := Feature{
		Slug: slug,
		Plan: Plan{
			Copy:                 PlanCopyNone,
			PRDAvailability:      AvailabilityAbsent,
			TaskListAvailability: AvailabilityAbsent,
			Progress:             PlanProgress{Availability: AvailabilityAbsent},
		},
		Git:    GitState{Availability: AvailabilityUnknown},
		Remote: Remote{Availability: AvailabilityUnknown},
	}
	for _, apply := range mutate {
		apply(&built)
	}
	return built
}

func withPlan(prd, tasks Availability) func(*Feature) {
	return func(f *Feature) {
		f.Plan.Copy = PlanCopyDev
		f.Plan.PRDAvailability = prd
		f.Plan.TaskListAvailability = tasks
	}
}

func withWorktree(path string) func(*Feature) {
	return func(f *Feature) {
		f.Git.WorktreePath = path
		f.Git.Branch = "feature/" + f.Slug
	}
}

// withMergedPR is the only evidence that can place a feature past
// implementation. It replaced the backlog states these tests used to set.
func withMergedPR(number int) func(*Feature) {
	return func(f *Feature) {
		f.Remote.Availability = AvailabilityAvailable
		f.Remote.PullRequest = &PullRequest{Number: number, State: "merged", Merged: true}
	}
}

func withOpenPR(number int) func(*Feature) {
	return func(f *Feature) {
		f.Remote.Availability = AvailabilityAvailable
		f.Remote.PullRequest = &PullRequest{Number: number, State: "open"}
	}
}

// withCompletedArchive is what a finished feature leaves behind: no worktree,
// and a fully ticked plan archived in the baseline checkout. It is the evidence
// that makes a missing pull request worth one targeted query.
func withCompletedArchive() func(*Feature) {
	return func(f *Feature) {
		f.Plan.Copy = PlanCopyDev
		f.Plan.PRDAvailability = AvailabilityAvailable
		f.Plan.TaskListAvailability = AvailabilityAvailable
		f.Plan.Progress = PlanProgress{
			Availability:      AvailabilityAvailable,
			SubtasksTotal:     10,
			SubtasksCompleted: 10,
		}
	}
}

func withGit(mutate func(*GitState)) func(*Feature) {
	return func(f *Feature) { mutate(&f.Git) }
}

func TestDerivePhaseLocalPrecedence(t *testing.T) {
	cases := []struct {
		name  string
		input Feature
		want  Phase
	}{
		{
			name:  "no evidence at all",
			input: feature("nothing"),
			want:  PhaseUnknown,
		},
		{
			name:  "PRD only",
			input: feature("early", withPlan(AvailabilityAvailable, AvailabilityAbsent)),
			want:  PhasePlanning,
		},
		{
			name:  "complete plan without a worktree",
			input: feature("planned", withPlan(AvailabilityAvailable, AvailabilityAvailable)),
			want:  PhaseReady,
		},
		{
			name:  "worktree on disk",
			input: feature("building", withPlan(AvailabilityAvailable, AvailabilityAvailable), withWorktree("/repo/worktrees/building")),
			want:  PhaseImplementing,
		},
		{
			// Delivery is a merged pull request and nothing else. Planning
			// artifacts left behind after cleanup do not make a feature look
			// unfinished, and no local file can call it shipped.
			name:  "merged pull request with cleanup done",
			input: feature("delivered", withMergedPR(200)),
			want:  PhaseShipped,
		},
		{
			name:  "merged pull request with the worktree still there",
			input: feature("tidying", withMergedPR(201), withWorktree("/repo/worktrees/tidying")),
			want:  PhaseMergedCleanup,
		},
		{
			name:  "open pull request",
			input: feature("reviewing", withOpenPR(202)),
			want:  PhaseReview,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := DerivePhase(testCase.input, localOptions())
			if got.Phase != testCase.want {
				t.Fatalf("phase = %q, want %q", got.Phase, testCase.want)
			}
			if got.Reason == "" {
				t.Fatal("phase was chosen without a stated reason")
			}
		})
	}
}

func TestDerivePhaseNeverDerivesADroppedState(t *testing.T) {
	// Nothing reports abandoned work any more. The only source that ever did
	// was a hand-maintained file, and an Issue can be closed for reasons that
	// have nothing to do with abandoning the work — so no combination of
	// evidence may put those words in somebody's mouth.
	inputs := []Feature{
		feature("nothing"),
		feature("planned", withPlan(AvailabilityAvailable, AvailabilityAvailable)),
		feature("building", withWorktree("/w/building")),
		feature("reviewing", withOpenPR(1)),
		feature("delivered", withMergedPR(2)),
		feature("tidying", withMergedPR(3), withWorktree("/w/tidying")),
	}
	for _, input := range inputs {
		for _, options := range []DeriveOptions{localOptions(), {Baseline: "dev", RemoteAvailable: true}} {
			state := DerivePhase(input, options)
			if string(state.Phase) == "dropped" {
				t.Fatalf("%s derived a dropped phase", input.Slug)
			}
			if strings.Contains(strings.ToLower(state.Reason), "backlog") {
				t.Fatalf("%s explained its phase with backlog evidence: %q", input.Slug, state.Reason)
			}
		}
	}
}

func TestDerivePhaseIsNeverConfirmedWithoutAFreshRemoteQuery(t *testing.T) {
	built := feature("building", withWorktree("/repo/worktrees/building"))
	if DerivePhase(built, localOptions()).Confirmed {
		t.Fatal("a local-only phase was marked confirmed")
	}
	confirmed := DerivePhase(built, DeriveOptions{Baseline: "dev", RemoteAvailable: true})
	if !confirmed.Confirmed {
		t.Fatal("a phase backed by a fresh remote query was left unconfirmed")
	}
}

func TestDeriveFindingsPlanningGaps(t *testing.T) {
	tasksOnly := feature("orphan-tasks", withPlan(AvailabilityAbsent, AvailabilityAvailable), withWorktree("/w/orphan-tasks"))
	if finding, ok := findingFor(DeriveFindings(tasksOnly, localOptions()), FindingPRDMissing); !ok {
		t.Fatal("a task list with no PRD raised no finding")
	} else if finding.Severity != SeverityWarning {
		t.Fatalf("severity = %q, want warning once work is underway", finding.Severity)
	}

	// The same gap during planning is expected, not alarming.
	idle := feature("orphan-tasks", withPlan(AvailabilityAbsent, AvailabilityAvailable))
	if finding, ok := findingFor(DeriveFindings(idle, localOptions()), FindingPRDMissing); !ok || finding.Severity != SeverityInfo {
		t.Fatalf("finding = %+v, ok=%v, want an informational gap before work starts", finding, ok)
	}

	prdOnly := feature("no-tasks", withPlan(AvailabilityAvailable, AvailabilityAbsent), withWorktree("/repo/worktrees/no-tasks"))
	if _, ok := findingFor(DeriveFindings(prdOnly, localOptions()), FindingTaskListMissing); !ok {
		t.Fatal("a PRD with no task list raised no finding")
	}

	malformed := feature("broken", withPlan(AvailabilityMalformed, AvailabilityAvailable))
	if _, ok := findingFor(DeriveFindings(malformed, localOptions()), FindingPlanMalformed); !ok {
		t.Fatal("a malformed planning artifact raised no finding")
	}
}

func TestDeriveFindingsWorktreeWithoutAnyPlan(t *testing.T) {
	orphan := feature("undocumented", withWorktree("/repo/worktrees/undocumented"))
	findings := DeriveFindings(orphan, localOptions())
	if _, ok := findingFor(findings, FindingWorktreeWithoutPlan); !ok {
		t.Fatalf("findings = %v, want worktree_without_plan", findings)
	}
	// It must not also claim the individual artifacts are missing; one clear
	// finding beats three overlapping ones.
	if _, ok := findingFor(findings, FindingPRDMissing); ok {
		t.Fatal("worktree_without_plan was duplicated by prd_missing")
	}
}

func TestDeriveFindingsNoLongerReportsBookkeepingDrift(t *testing.T) {
	// Every finding in this family compared a hand-maintained file against
	// reality. With the file gone there is nothing to disagree with, and a
	// perfectly ordinary checkout must not be flagged for lacking an entry that
	// no longer exists anywhere.
	inputs := []Feature{
		feature("a", withWorktree("/w/a"), withPlan(AvailabilityAvailable, AvailabilityAvailable)),
		feature("b", withMergedPR(10), withWorktree("/w/b"), withPlan(AvailabilityAvailable, AvailabilityAvailable)),
		feature("c", withMergedPR(11), withPlan(AvailabilityAvailable, AvailabilityAvailable)),
	}
	for _, input := range inputs {
		for _, finding := range DeriveFindings(input, localOptions()) {
			if string(finding.Code) == "backlog_drift" || string(finding.Source) == "backlog" {
				t.Fatalf("%s still raised backlog bookkeeping: %+v", input.Slug, finding)
			}
			if strings.Contains(finding.Message, "BACKLOG") || strings.Contains(finding.Detail, "BACKLOG") {
				t.Fatalf("%s mentioned the removed file: %+v", input.Slug, finding)
			}
		}
	}
}

func TestDeriveFindingsCleanupNamesWhatIsOutstanding(t *testing.T) {
	// A row reading "Merged (cleanup)" with nothing flagged tells a reader that
	// work remains but not what it is. The reasons now come from the worktree
	// and the archived plan, which is everything cleanup actually touches.
	merged := feature("tidying", withMergedPR(12), withWorktree("/w/tidying"),
		withPlan(AvailabilityAvailable, AvailabilityAvailable))
	merged.Phase = DerivePhase(merged, localOptions())
	if merged.Phase.Phase != PhaseMergedCleanup {
		t.Fatalf("phase = %q, want merged cleanup", merged.Phase.Phase)
	}
	finding, ok := findingFor(DeriveFindings(merged, localOptions()), FindingCleanupOutstanding)
	if !ok {
		t.Fatal("a merged feature with a live worktree flagged no outstanding cleanup")
	}
	if !strings.Contains(finding.Detail, "worktree") {
		t.Fatalf("detail = %q, want the surviving worktree named", finding.Detail)
	}
}

func TestDeriveFindingsArchiveMissingFollowsTheMergedPullRequest(t *testing.T) {
	// This used to trigger on a shipped backlog line. The merged pull request
	// is the same conclusion drawn from stronger evidence.
	delivered := feature("archived-nowhere", withMergedPR(13))
	if _, ok := findingFor(DeriveFindings(delivered, localOptions()), FindingArchiveMissing); !ok {
		t.Fatal("a merged feature with no archived plan raised no finding")
	}
	// Work that has not merged is not missing an archive; it has not finished.
	building := feature("still-going", withWorktree("/w/still-going"),
		withPlan(AvailabilityAvailable, AvailabilityAvailable))
	if _, ok := findingFor(DeriveFindings(building, localOptions()), FindingArchiveMissing); ok {
		t.Fatal("in-progress work was told its archive was missing")
	}
}

func TestDeriveFindingsNoDriftForACleanInProgressFeature(t *testing.T) {
	clean := feature("tidy",
		withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withWorktree("/repo/worktrees/tidy"),
		withGit(func(git *GitState) {
			git.Availability = AvailabilityAvailable
			git.DirtyAvailability = AvailabilityAvailable
			git.DivergenceAvailability = AvailabilityAvailable
			git.Ahead = 3
		}),
	)
	if findings := DeriveFindings(clean, localOptions()); len(findings) != 0 {
		t.Fatalf("findings = %v, want none for a healthy in-progress feature", findings)
	}
}

func TestDeriveFindingsLocalGitEvidence(t *testing.T) {
	behind := feature("lagging", withWorktree("/w/lagging"), withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withGit(func(git *GitState) {
			git.Availability = AvailabilityAvailable
			git.DivergenceAvailability = AvailabilityAvailable
			git.Behind = 9
			git.Ahead = 2
			git.DirtyAvailability = AvailabilityAvailable
			git.Dirty = true
		}))

	findings := DeriveFindings(behind, localOptions())
	branch, ok := findingFor(findings, FindingBranchBehindBase)
	if !ok || branch.Severity != SeverityWarning {
		t.Fatalf("finding = %+v, ok=%v, want a behind-dev warning", branch, ok)
	}
	if dirty, ok := findingFor(findings, FindingWorktreeDirty); !ok || dirty.Severity != SeverityInfo {
		t.Fatalf("finding = %+v, ok=%v, want an informational dirty-worktree note", dirty, ok)
	}
	// Most severe first.
	if findings[0].Severity != SeverityWarning {
		t.Fatalf("findings were not sorted by severity: %v", findings)
	}
}

func TestDeriveFindingsUnavailableGitIsNotSilence(t *testing.T) {
	broken := feature("opaque", withWorktree("/w/opaque"), withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withGit(func(git *GitState) {
			git.Availability = AvailabilityUnavailable
			git.Detail = "divergence versus dev could not be computed"
		}))

	finding, ok := findingFor(DeriveFindings(broken, localOptions()), FindingGitUnavailable)
	if !ok {
		t.Fatal("unreadable Git facts were reported as no news")
	}
	if finding.Detail == "" {
		t.Fatal("unavailable Git finding carried no explanation")
	}
}

func TestDeriveFindingsDoesNotInventDivergenceFromUnknownCounts(t *testing.T) {
	unknown := feature("unmeasured", withWorktree("/w/unmeasured"), withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withGit(func(git *GitState) {
			git.Availability = AvailabilityAvailable
			git.DivergenceAvailability = AvailabilityUnavailable
			git.DirtyAvailability = AvailabilityUnavailable
		}))

	findings := DeriveFindings(unknown, localOptions())
	if _, ok := findingFor(findings, FindingBranchBehindBase); ok {
		t.Fatal("a behind-dev finding was raised from an unavailable count")
	}
	if _, ok := findingFor(findings, FindingWorktreeDirty); ok {
		t.Fatal("a dirty finding was raised from an unavailable status")
	}
}

func TestSortFeaturesGroupsAttentionFirstAndHistoryLast(t *testing.T) {
	attention := feature("needs-help", withWorktree("/w/needs-help"))
	attention.Phase = PhaseState{Phase: PhaseImplementing}
	attention.Findings = []Finding{{Code: FindingBranchBehindBase, Severity: SeverityWarning}}

	quiet := feature("all-good", withWorktree("/w/all-good"))
	quiet.Phase = PhaseState{Phase: PhaseImplementing}

	broken := feature("broken", withWorktree("/w/broken"))
	broken.Phase = PhaseState{Phase: PhaseImplementing}
	broken.Findings = []Finding{{Code: FindingIdentityAmbiguous, Severity: SeverityError}}

	shipped := feature("history")
	shipped.Phase = PhaseState{Phase: PhaseShipped}
	shipped.Findings = []Finding{{Code: FindingArchiveMissing, Severity: SeverityError}}

	features := []Feature{shipped, quiet, attention, broken}
	SortFeatures(features)

	want := []string{"broken", "needs-help", "all-good", "history"}
	for index, slug := range want {
		if features[index].Slug != slug {
			t.Fatalf("order = %v, want %v (errors first, history last even when it has findings)",
				[]string{features[0].Slug, features[1].Slug, features[2].Slug, features[3].Slug}, want)
		}
	}
}

func TestSortFeaturesIsDeterministicOnTies(t *testing.T) {
	first := feature("bbb")
	first.Phase = PhaseState{Phase: PhaseReady}
	second := feature("aaa")
	second.Phase = PhaseState{Phase: PhaseReady}

	features := []Feature{first, second}
	SortFeatures(features)
	if features[0].Slug != "aaa" || features[1].Slug != "bbb" {
		t.Fatalf("order = %q,%q, want slug order on ties", features[0].Slug, features[1].Slug)
	}
}

func withProgress(mutate func(*PlanProgress)) func(*Feature) {
	return func(f *Feature) {
		f.Plan.Copy = PlanCopyDev
		f.Plan.PRDAvailability = AvailabilityAvailable
		f.Plan.TaskListAvailability = AvailabilityAvailable
		f.Plan.Progress = PlanProgress{Availability: AvailabilityAvailable}
		mutate(&f.Plan.Progress)
	}
}

func TestDeriveFindingsStaleArchivedPlan(t *testing.T) {
	// `wt done` archives the ticked copy back into dev. A shipped feature whose
	// archived plan is still untouched means that never happened.
	shipped := feature("workspace-backlog", withMergedPR(254),
		withProgress(func(progress *PlanProgress) {
			progress.MilestonesTotal, progress.MilestonesCompleted = 7, 0
			progress.SubtasksTotal, progress.SubtasksCompleted = 136, 0
		}))
	shipped.Phase = DerivePhase(shipped, localOptions())

	finding, ok := findingFor(DeriveFindings(shipped, localOptions()), FindingArchiveStale)
	if !ok {
		t.Fatal("a shipped feature with an unticked archive raised no finding")
	}
	if finding.Severity != SeverityInfo {
		t.Fatalf("severity = %q, want info", finding.Severity)
	}
	if !strings.Contains(finding.Detail, "136") {
		t.Fatalf("detail = %q, want the outstanding count stated", finding.Detail)
	}
}

func TestDeriveFindingsNoStaleArchiveForACompletedPlan(t *testing.T) {
	shipped := feature("herdr-devflow-bridge", withMergedPR(258),
		withProgress(func(progress *PlanProgress) {
			progress.MilestonesTotal, progress.MilestonesCompleted = 7, 7
			progress.SubtasksTotal, progress.SubtasksCompleted = 103, 103
		}))
	shipped.Phase = DerivePhase(shipped, localOptions())

	if _, ok := findingFor(DeriveFindings(shipped, localOptions()), FindingArchiveStale); ok {
		t.Fatal("a fully ticked archive was reported as stale")
	}
}

func TestDeriveFindingsNoStaleArchiveWhileStillImplementing(t *testing.T) {
	// An in-progress feature's plan is supposed to have unchecked work.
	building := feature("in-flight", withWorktree("/w/in-flight"),
		withProgress(func(progress *PlanProgress) {
			progress.SubtasksTotal, progress.SubtasksCompleted = 100, 12
		}))
	building.Phase = DerivePhase(building, localOptions())

	if _, ok := findingFor(DeriveFindings(building, localOptions()), FindingArchiveStale); ok {
		t.Fatal("work in progress was reported as a stale archive")
	}
}

func TestDeriveFindingsNoStaleArchiveFromAnUnparsedPlan(t *testing.T) {
	shipped := feature("opaque-archive", withMergedPR(260),
		withProgress(func(progress *PlanProgress) {
			progress.Availability = AvailabilityMalformed
			progress.SubtasksTotal = 0
		}))
	shipped.Phase = DerivePhase(shipped, localOptions())

	if _, ok := findingFor(DeriveFindings(shipped, localOptions()), FindingArchiveStale); ok {
		t.Fatal("an unparsed archive was reported as stale")
	}
}
