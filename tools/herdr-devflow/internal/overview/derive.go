package overview

import (
	"sort"
	"strconv"
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
// Precedence is by evidence strength, not recency. A live worktree outranks a
// backlog line, because the backlog is bookkeeping a human updates by hand
// while a worktree is a fact on disk.
func DerivePhase(feature Feature, options DeriveOptions) PhaseState {
	baseline := options.Baseline
	if baseline == "" {
		baseline = "dev"
	}
	state := PhaseState{Sources: feature.Sources, Confirmed: false}

	hasWorktree := feature.Git.WorktreePath != ""
	prdPresent := feature.Plan.PRDAvailability == AvailabilityAvailable
	tasksPresent := feature.Plan.TaskListAvailability == AvailabilityAvailable

	switch {
	case hasWorktree:
		// A checkout on disk is the strongest local lifecycle evidence there
		// is. Even a backlog entry claiming the feature shipped cannot
		// override it; that disagreement is reported as drift instead.
		state.Phase = PhaseImplementing
		state.Reason = "a feature worktree exists on disk"
	case feature.Backlog.State == BacklogDropped:
		state.Phase = PhaseDropped
		state.Reason = "BACKLOG.md records this feature as dropped"
	case feature.Backlog.State == BacklogShipped:
		state.Phase = PhaseShipped
		state.Reason = "BACKLOG.md records this feature as shipped"
	case prdPresent && tasksPresent:
		state.Phase = PhaseReady
		state.Reason = "a PRD and task list exist but no worktree does"
	case prdPresent || tasksPresent:
		state.Phase = PhasePlanning
		state.Reason = "planning artifacts are incomplete and no worktree exists"
	default:
		state.Phase = PhaseUnknown
		state.Reason = "no planning, worktree, or backlog evidence was found"
	}

	// Only a fresh remote query can settle a phase, because every local phase
	// above is falsifiable by an open or merged PR.
	if options.RemoteAvailable {
		state.Confirmed = true
	}
	return state
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
	active := hasWorktree || feature.Backlog.State == BacklogDoing

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

	// Backlog bookkeeping drift. The backlog is never authoritative; it is
	// only compared against stronger evidence.
	switch {
	case feature.Backlog.State == BacklogShipped && hasWorktree:
		raise(FindingBacklogDrift, SeverityWarning, SourceBacklog,
			"BACKLOG.md records this feature as shipped, but its worktree still exists.",
			boundedEntry(feature.Backlog))
	case feature.Backlog.State == BacklogDropped && hasWorktree:
		raise(FindingBacklogDrift, SeverityWarning, SourceBacklog,
			"BACKLOG.md records this feature as dropped, but its worktree still exists.",
			boundedEntry(feature.Backlog))
	case feature.Backlog.State == BacklogAbsent && hasWorktree:
		raise(FindingBacklogDrift, SeverityInfo, SourceBacklog,
			"A feature worktree exists with no BACKLOG.md entry.", "")
	case feature.Backlog.State == BacklogDoing && !hasWorktree && feature.Plan.Copy == PlanCopyNone:
		raise(FindingBacklogDrift, SeverityWarning, SourceBacklog,
			"BACKLOG.md lists this feature as in progress, but no worktree or plan was found.",
			boundedEntry(feature.Backlog))
	}

	// Archive bookkeeping: once the worktree is gone, `wt done` should have
	// left the ticked planning copy behind in dev.
	if !hasWorktree && feature.Backlog.State == BacklogShipped && feature.Plan.Copy == PlanCopyNone {
		raise(FindingArchiveMissing, SeverityInfo, SourcePlanning,
			"This feature shipped but no archived planning copy remains in "+baseline+".", "")
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

func boundedEntry(backlog Backlog) string {
	if backlog.Entry == "" {
		return ""
	}
	return "BACKLOG.md:" + strconv.Itoa(backlog.Line) + " " + backlog.Entry
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
