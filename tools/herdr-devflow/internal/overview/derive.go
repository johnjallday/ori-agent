package overview

import (
	"sort"
	"strconv"
	"strings"
)

// DeriveOptions controls the parts of derivation that depend on which
// collectors actually ran.
type DeriveOptions struct {
	// RemoteAvailable is true only when a fresh GitHub query succeeded. While
	// it is false, no phase may be marked confirmed: local evidence alone
	// cannot distinguish "still implementing" from "merged an hour ago".
	RemoteAvailable bool
	// Baseline is the integration branch name used in messages.
	Baseline string
}

// DerivePhase places one feature in the lifecycle using local evidence only.
// Remote-dependent phases (review, merged cleanup, remote-confirmed shipped)
// are added by the GitHub enrichment slice; until then this function never
// invents them.
//
// Precedence is by evidence strength, not recency. A merged pull request
// outranks a checkout on disk, which outranks planning files, because each is
// harder to be wrong about than the next.
func DerivePhase(feature Feature, options DeriveOptions) PhaseState {
	baseline := options.Baseline
	if baseline == "" {
		baseline = "dev"
	}
	state := PhaseState{Sources: feature.Sources, Confirmed: false}

	hasWorktree := feature.Git.WorktreePath != ""
	prdPresent := feature.Plan.PRDAvailability == AvailabilityAvailable
	tasksPresent := feature.Plan.TaskListAvailability == AvailabilityAvailable
	pull := feature.Remote.PullRequest

	// Remote delivery evidence outranks every local signal. A merged pull
	// request is a fact about the repository's history; an unchecked task list
	// is somebody forgetting to tick a box.
	switch {
	case pull != nil && pull.Merged:
		if outstandingCleanup(feature) {
			state.Phase = PhaseMergedCleanup
			state.Reason = "the pull request merged but local cleanup is outstanding"
		} else {
			state.Phase = PhaseShipped
			state.Reason = "the pull request merged and no local cleanup is outstanding"
		}
		if options.RemoteAvailable {
			state.Confirmed = true
		}
		return state
	case pull != nil && pull.State == "open":
		state.Phase = PhaseReview
		if pull.Draft {
			state.Reason = "a draft pull request is open against " + baseline
		} else {
			state.Reason = "a pull request is open against " + baseline
		}
		if options.RemoteAvailable {
			state.Confirmed = true
		}
		return state
	}

	switch {
	case hasWorktree:
		// A checkout on disk is the strongest local lifecycle evidence there is.
		state.Phase = PhaseImplementing
		state.Reason = "a feature worktree exists on disk"
	case prdPresent && tasksPresent:
		state.Phase = PhaseReady
		state.Reason = "a PRD and task list exist but no worktree does"
	case prdPresent || tasksPresent:
		state.Phase = PhasePlanning
		state.Reason = "planning artifacts are incomplete and no worktree exists"
	default:
		state.Phase = PhaseUnknown
		state.Reason = "no planning or worktree evidence was found"
	}
	// Nothing below a worktree can claim a feature shipped. Only a merged pull
	// request does that, and it was already handled above.

	// Only a fresh remote query can settle a phase, because every local phase
	// above is falsifiable by an open or merged PR.
	if options.RemoteAvailable {
		state.Confirmed = true
	}
	return state
}

// cleanupReasons lists what still stands between a merged feature and being
// finished, in the order a person would deal with them.
func cleanupReasons(feature Feature, baseline string) []string {
	var reasons []string
	if feature.Git.WorktreePath != "" {
		reasons = append(reasons, "the feature worktree still exists")
	}
	progress := feature.Plan.Progress
	if progress.Availability.OK() && progress.SubtasksTotal > 0 && progress.SubtasksCompleted < progress.SubtasksTotal {
		reasons = append(reasons, strconv.Itoa(progress.SubtasksTotal-progress.SubtasksCompleted)+
			" subtasks are unchecked in the archived plan in "+baseline)
	}
	return reasons
}

// delivered reports whether remote evidence shows this feature's pull request
// merged. It is the authoritative shipped signal.
func delivered(feature Feature) bool {
	pull := feature.Remote.PullRequest
	return pull != nil && pull.Merged
}

// outstandingCleanup reports whether anything local still needs tidying after
// a merge: the worktree or branch survives, or the ticked plan was never
// archived back into dev.
func outstandingCleanup(feature Feature) bool {
	if feature.Git.WorktreePath != "" {
		return true
	}
	progress := feature.Plan.Progress
	if progress.Availability.OK() && progress.SubtasksTotal > 0 && progress.SubtasksCompleted < progress.SubtasksTotal {
		return true
	}
	return false
}

// DeriveFindings reports the gaps and drift visible in one feature's local
// evidence. Findings are diagnostic: nothing here repairs, rewrites, or clears
// anything, and a later collector never removes a finding raised earlier.
func DeriveFindings(feature Feature, options DeriveOptions) []Finding {
	baseline := options.Baseline
	if baseline == "" {
		baseline = "dev"
	}
	var findings []Finding
	raise := func(code FindingCode, severity Severity, source SourceKind, message, detail string) {
		findings = append(findings, Finding{
			Code: code, Severity: severity, Feature: feature.Slug,
			Source: source, Message: message, Detail: detail,
		})
	}

	hasWorktree := feature.Git.WorktreePath != ""
	prd := feature.Plan.PRDAvailability
	tasks := feature.Plan.TaskListAvailability
	// "Active" is now exactly what it sounds like: a checkout exists, or a pull
	// request is open. It used to include a hand-written Doing entry, which
	// could call a feature active months after anyone last touched it.
	pull := feature.Remote.PullRequest
	active := hasWorktree || (pull != nil && pull.State == "open")

	// Planning gaps. A missing artifact matters more once work has started.
	switch {
	case prd == AvailabilityAbsent && tasks == AvailabilityAbsent && hasWorktree:
		raise(FindingWorktreeWithoutPlan, SeverityWarning, SourcePlanning,
			"A feature worktree exists with no PRD or task list.",
			"Expected prd-"+feature.Slug+".md and tasks-"+feature.Slug+".md.")
	case prd == AvailabilityAbsent && tasks != AvailabilityAbsent:
		raise(FindingPRDMissing, severityIf(active, SeverityWarning, SeverityInfo), SourcePlanning,
			"A task list exists with no matching PRD.",
			"Expected prd-"+feature.Slug+".md beside the task list.")
	case tasks == AvailabilityAbsent && prd != AvailabilityAbsent:
		raise(FindingTaskListMissing, severityIf(active, SeverityWarning, SeverityInfo), SourcePlanning,
			"A PRD exists with no matching task list.",
			"Expected tasks-"+feature.Slug+".md beside the PRD.")
	}
	if prd == AvailabilityMalformed || tasks == AvailabilityMalformed {
		raise(FindingPlanMalformed, SeverityWarning, SourcePlanning,
			"A planning artifact exists but could not be read as Markdown.", "")
	}
	if prd == AvailabilityUnavailable || tasks == AvailabilityUnavailable {
		raise(FindingPlanMalformed, SeverityWarning, SourcePlanning,
			"A planning artifact could not be read.", "")
	}

	// There is no bookkeeping-drift family any more. Every finding it produced
	// compared a hand-maintained file against reality, and reality is now the
	// only thing recorded: the pull request, the worktree, and the plan.

	// Archive bookkeeping: once the worktree is gone, `wt done` should have
	// left the ticked planning copy behind in dev. The trigger is the merged
	// pull request — stronger evidence than the shipped line this used to read,
	// and the same evidence the shipped phase itself is derived from.
	if !hasWorktree && delivered(feature) && feature.Plan.Copy == PlanCopyNone {
		raise(FindingArchiveMissing, SeverityInfo, SourcePlanning,
			"This feature shipped but no archived planning copy remains in "+baseline+".", "")
	}
	// A merged feature is only finished once its worktree, branch, and archived
	// plan agree. Naming what is outstanding matters: a
	// row reading "Merged (cleanup)" with nothing flagged tells a reader that
	// work remains but not what it is.
	if feature.Phase.Phase == PhaseMergedCleanup {
		if reasons := cleanupReasons(feature, baseline); len(reasons) > 0 {
			raise(FindingCleanupOutstanding, SeverityInfo, SourcePlanning,
				"This feature merged but local cleanup is outstanding.",
				strings.Join(reasons, "; "))
		}
	}

	// A delivered feature whose archived plan is still mostly unticked means
	// the ticked copy never made it back from the worktree, so the archive
	// records the plan as written rather than the work as done.
	terminalOrCleanup := feature.Phase.Phase == PhaseShipped || feature.Phase.Phase == PhaseMergedCleanup
	if !hasWorktree && terminalOrCleanup && feature.Plan.Copy == PlanCopyDev {
		progress := feature.Plan.Progress
		if progress.Availability.OK() && progress.SubtasksTotal > 0 && progress.SubtasksCompleted < progress.SubtasksTotal {
			raise(FindingArchiveStale, SeverityInfo, SourcePlanning,
				"This feature shipped but its archived task list still shows unchecked work.",
				strconv.Itoa(progress.SubtasksTotal-progress.SubtasksCompleted)+" of "+
					strconv.Itoa(progress.SubtasksTotal)+" subtasks are unchecked in the archived copy.")
		}
	}

	// Local Git evidence.
	if feature.Git.Availability == AvailabilityUnavailable {
		raise(FindingGitUnavailable, SeverityWarning, SourceGit,
			"Local Git facts for this feature could not be read.", feature.Git.Detail)
	}
	if feature.Git.DivergenceAvailability == AvailabilityAvailable && feature.Git.Behind > 0 {
		raise(FindingBranchBehindBase, SeverityWarning, SourceGit,
			"This branch is behind "+baseline+".",
			strconv.Itoa(feature.Git.Behind)+" commits behind, "+strconv.Itoa(feature.Git.Ahead)+" ahead.")
	}
	if feature.Git.DirtyAvailability == AvailabilityAvailable && feature.Git.Dirty {
		raise(FindingWorktreeDirty, SeverityInfo, SourceGit,
			"This worktree has uncommitted changes.", "")
	}

	sortFindings(findings)
	return findings
}

// severityIf downgrades a finding for features that have not started yet: an
// incomplete plan is expected during planning and only becomes a warning once
// work is underway.
func severityIf(active bool, whenActive, otherwise Severity) Severity {
	if active {
		return whenActive
	}
	return otherwise
}

// sortFindings orders findings most severe first, then by stable code, so both
// human and JSON output are deterministic.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity.order() != findings[j].Severity.order() {
			return findings[i].Severity.order() < findings[j].Severity.order()
		}
		if findings[i].Feature != findings[j].Feature {
			return findings[i].Feature < findings[j].Feature
		}
		return findings[i].Code < findings[j].Code
	})
}

// SortFeatures applies the shared deterministic order used by every surface:
// features needing attention first, then active work by phase, with shipped
// and dropped history last. Ties break on slug so output never flickers.
func SortFeatures(features []Feature) {
	sort.SliceStable(features, func(i, j int) bool {
		left, right := features[i], features[j]
		if left.Phase.Phase.Terminal() != right.Phase.Phase.Terminal() {
			return !left.Phase.Phase.Terminal()
		}
		leftSeverity, leftHas := left.Attention()
		rightSeverity, rightHas := right.Attention()
		if leftHas != rightHas {
			return leftHas
		}
		if leftHas && leftSeverity.order() != rightSeverity.order() {
			return leftSeverity.order() < rightSeverity.order()
		}
		if left.Phase.Phase.order() != right.Phase.Phase.order() {
			return left.Phase.Phase.order() < right.Phase.Phase.order()
		}
		return left.Slug < right.Slug
	})
}
