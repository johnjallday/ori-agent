package overview

import "testing"

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
		Backlog: Backlog{State: BacklogAbsent},
		Git:     GitState{Availability: AvailabilityUnknown},
		Remote:  Remote{Availability: AvailabilityUnknown},
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

func withBacklog(state BacklogState) func(*Feature) {
	return func(f *Feature) { f.Backlog = Backlog{State: state, Entry: "entry text", Line: 40} }
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
			name:  "backlog dropped",
			input: feature("abandoned", withBacklog(BacklogDropped)),
			want:  PhaseDropped,
		},
		{
			name:  "backlog shipped",
			input: feature("delivered", withBacklog(BacklogShipped)),
			want:  PhaseShipped,
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

func TestDerivePhaseWorktreeOutranksBacklogBookkeeping(t *testing.T) {
	// The backlog is hand-maintained; a checkout is a fact on disk.
	shippedButLive := feature("contested", withBacklog(BacklogShipped), withWorktree("/repo/worktrees/contested"))
	if got := DerivePhase(shippedButLive, localOptions()); got.Phase != PhaseImplementing {
		t.Fatalf("phase = %q, want implementing (a backlog line must not override a live worktree)", got.Phase)
	}

	droppedButLive := feature("revived", withBacklog(BacklogDropped), withWorktree("/repo/worktrees/revived"))
	if got := DerivePhase(droppedButLive, localOptions()); got.Phase != PhaseImplementing {
		t.Fatalf("phase = %q, want implementing", got.Phase)
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
	tasksOnly := feature("orphan-tasks", withPlan(AvailabilityAbsent, AvailabilityAvailable), withBacklog(BacklogDoing))
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

func TestDeriveFindingsBacklogDrift(t *testing.T) {
	cases := []struct {
		name     string
		input    Feature
		severity Severity
	}{
		{"shipped but still checked out", feature("a", withBacklog(BacklogShipped), withWorktree("/w/a"), withPlan(AvailabilityAvailable, AvailabilityAvailable)), SeverityWarning},
		{"dropped but still checked out", feature("b", withBacklog(BacklogDropped), withWorktree("/w/b"), withPlan(AvailabilityAvailable, AvailabilityAvailable)), SeverityWarning},
		{"checked out but never recorded", feature("c", withWorktree("/w/c"), withPlan(AvailabilityAvailable, AvailabilityAvailable)), SeverityInfo},
		{"recorded doing with nothing to show", feature("d", withBacklog(BacklogDoing)), SeverityWarning},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			finding, ok := findingFor(DeriveFindings(testCase.input, localOptions()), FindingBacklogDrift)
			if !ok {
				t.Fatal("no backlog drift finding was raised")
			}
			if finding.Severity != testCase.severity {
				t.Fatalf("severity = %q, want %q", finding.Severity, testCase.severity)
			}
		})
	}
}

func TestDeriveFindingsNoDriftForACleanInProgressFeature(t *testing.T) {
	clean := feature("tidy",
		withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withWorktree("/repo/worktrees/tidy"),
		withBacklog(BacklogDoing),
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
		withBacklog(BacklogDoing),
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
		withBacklog(BacklogDoing),
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
		withBacklog(BacklogDoing),
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
