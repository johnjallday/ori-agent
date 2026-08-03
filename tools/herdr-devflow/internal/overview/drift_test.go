package overview

import (
	"strings"
	"testing"
)

// The six drift cases this feature was written to explain. Each is asserted
// against the derivation rules rather than against a live repository, so they
// stay meaningful after the real branches move on.
func TestKnownDriftCases(t *testing.T) {
	remote := DeriveOptions{Baseline: "dev", RemoteAvailable: true}

	t.Run("merged but never cleaned up", func(t *testing.T) {
		// calendar-ops-mcp: merged, worktree gone, archived plan still unticked.
		row := feature("calendar-ops-mcp",
			withProgress(func(progress *PlanProgress) {
				progress.MilestonesTotal, progress.MilestonesCompleted = 8, 7
				progress.SubtasksTotal, progress.SubtasksCompleted = 84, 81
			}))
		merged := PullRequest{Number: 248, State: "merged", Merged: true, Checks: ChecksPassing}
		row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &merged}
		row.Phase = DerivePhase(row, remote)

		if row.Phase.Phase != PhaseMergedCleanup {
			t.Fatalf("phase = %q, want merged_cleanup", row.Phase.Phase)
		}
		findings := DeriveFindings(row, remote)
		finding, ok := findingFor(findings, FindingCleanupOutstanding)
		if !ok {
			t.Fatalf("findings = %v, want the outstanding cleanup named", findings)
		}
		if !strings.Contains(finding.Detail, "subtasks") {
			t.Fatalf("detail = %q, want the unchecked archive named", finding.Detail)
		}
	})

	t.Run("stale archived plan", func(t *testing.T) {
		// workspace-backlog: shipped, but the ticked copy never returned to dev.
		row := feature("workspace-backlog", withMergedPR(254),
			withProgress(func(progress *PlanProgress) {
				progress.MilestonesTotal, progress.MilestonesCompleted = 7, 0
				progress.SubtasksTotal, progress.SubtasksCompleted = 136, 0
			}))
		row.Phase = DerivePhase(row, remote)

		// The unticked archive is itself outstanding cleanup, so a merged
		// feature carrying one is not finished yet.
		if row.Phase.Phase != PhaseMergedCleanup {
			t.Fatalf("phase = %q, want merged_cleanup", row.Phase.Phase)
		}
		finding, ok := findingFor(DeriveFindings(row, remote), FindingArchiveStale)
		if !ok {
			t.Fatal("a shipped feature with an untouched archive raised nothing")
		}
		if finding.Detail == "" {
			t.Fatal("the stale archive was not quantified")
		}
	})

	t.Run("planning with a missing PRD", func(t *testing.T) {
		// google-connection-ship-readiness: a task list with no PRD beside it.
		row := feature("google-connection-ship-readiness",
			withPlan(AvailabilityAbsent, AvailabilityAvailable))
		row.Phase = DerivePhase(row, remote)

		if row.Phase.Phase != PhasePlanning {
			t.Fatalf("phase = %q, want planning", row.Phase.Phase)
		}
		if _, ok := findingFor(DeriveFindings(row, remote), FindingPRDMissing); !ok {
			t.Fatal("a task list with no PRD raised nothing")
		}
	})

	t.Run("branch behind the baseline", func(t *testing.T) {
		row := feature("downloads-janitor", withWorktree("/w/downloads-janitor"),
			withPlan(AvailabilityAvailable, AvailabilityAvailable),
			withGit(func(git *GitState) {
				git.Availability = AvailabilityAvailable
				git.DivergenceAvailability = AvailabilityAvailable
				git.DirtyAvailability = AvailabilityAvailable
				git.Ahead, git.Behind = 15, 6
			}))
		row.Phase = DerivePhase(row, remote)

		if row.Phase.Phase != PhaseImplementing {
			t.Fatalf("phase = %q, want implementing", row.Phase.Phase)
		}
		finding, ok := findingFor(DeriveFindings(row, remote), FindingBranchBehindBase)
		if !ok || finding.Severity != SeverityWarning {
			t.Fatalf("finding = %+v, ok=%v, want a behind-baseline warning", finding, ok)
		}
	})

	t.Run("worktree with no plan at all", func(t *testing.T) {
		row := feature("undocumented", withWorktree("/w/undocumented"))
		row.Phase = DerivePhase(row, remote)

		if _, ok := findingFor(DeriveFindings(row, remote), FindingWorktreeWithoutPlan); !ok {
			t.Fatal("an undocumented worktree raised nothing")
		}
	})

	t.Run("delivered work overrides stale local bookkeeping", func(t *testing.T) {
		// The case only a remote query can settle: every local signal says
		// "in progress", the pull request says otherwise.
		row := feature("email-ops-workspace",
			withProgress(func(progress *PlanProgress) {
				progress.SubtasksTotal, progress.SubtasksCompleted = 90, 4
			}))
		merged := PullRequest{Number: 244, State: "merged", Merged: true, Checks: ChecksFailing}
		row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &merged}
		row.Phase = DerivePhase(row, remote)

		if row.Phase.Phase != PhaseMergedCleanup {
			t.Fatalf("phase = %q, want merged_cleanup despite the unticked plan", row.Phase.Phase)
		}
		if !row.Phase.Confirmed {
			t.Fatal("a phase backed by a merged pull request was left unconfirmed")
		}
	})
}

// TestKnownDriftCasesAreUnconfirmedWithoutGitHub proves the same inputs cannot
// reach a delivered phase on local evidence alone.
func TestKnownDriftCasesAreUnconfirmedWithoutGitHub(t *testing.T) {
	local := DeriveOptions{Baseline: "dev"}

	row := feature("email-ops-workspace",
		withProgress(func(progress *PlanProgress) {
			progress.SubtasksTotal, progress.SubtasksCompleted = 90, 4
		}))
	row.Remote = Remote{Availability: AvailabilityUnavailable}
	phase := DerivePhase(row, local)

	if phase.Confirmed {
		t.Fatal("a local-only phase was marked confirmed")
	}
	if phase.Phase == PhaseMergedCleanup || phase.Phase == PhaseShipped {
		t.Fatalf("phase = %q, want a local phase: delivery cannot be inferred without a remote query", phase.Phase)
	}
}
