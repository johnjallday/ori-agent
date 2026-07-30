package tasklist

import "regexp"

// This file answers one question for every checklist item: may an unattended
// agent do this?
//
// The existing parser already separates implementation work from delivery
// checkpoints, which is the right split for reporting progress. It is not the
// right split for an Overnight Run, because "Commit: …" and "Demo: …" are both
// checkpoints and only one of them is safe to do while nobody is watching. So
// checkpoints are classified further, and anything not recognized is manual.
//
// The bias is deliberate and one-directional. Misclassifying manual work as
// safe means an agent opens a PR, merges, or deletes a worktree overnight;
// misclassifying safe work as manual means the run stops early and says why.
// Only one of those is recoverable in the morning.

// Boundary says who may do a checklist item.
type Boundary string

const (
	// BoundaryImplementation is ordinary implementation work: code, tests,
	// documentation the plan calls for. An unattended agent may do it.
	BoundaryImplementation Boundary = "implementation"
	// BoundaryValidation is running the checks the plan requires. An
	// unattended agent may do it: it reads and reports, and a failure stops
	// the participant rather than changing anything.
	BoundaryValidation Boundary = "validation"
	// BoundaryCommit is a milestone commit the plan already requires. An
	// unattended agent may do it — the work is local, reversible, and the plan
	// asked for it — but nothing beyond it.
	BoundaryCommit Boundary = "commit"
	// BoundaryManual is everything a person must decide or authorize: looking
	// at a demo, approving a design, supplying credentials, opening a PR,
	// merging, releasing, deploying, or cleaning up a worktree. An unattended
	// agent stops before it, every time.
	BoundaryManual Boundary = "manual"
)

// Safe reports whether an unattended agent may do work at this boundary.
func (b Boundary) Safe() bool {
	return b == BoundaryImplementation || b == BoundaryValidation || b == BoundaryCommit
}

// Label is the operator-facing name for a boundary.
func (b Boundary) Label() string {
	switch b {
	case BoundaryImplementation:
		return "implementation"
	case BoundaryValidation:
		return "validation"
	case BoundaryCommit:
		return "milestone commit"
	default:
		return "manual checkpoint"
	}
}

// commitPattern matches a milestone commit step. It requires the conventional
// leading keyword: an item that merely mentions committing is not one.
var commitPattern = regexp.MustCompile(`(?i)^commit\b`)

// validationPattern matches the checks a plan asks to be run. "Write manual
// test guide" is deliberately absent — writing the guide is implementation
// work, and it is caught as such because it is not a checkpoint keyword an
// agent must stop at.
var validationPattern = regexp.MustCompile(`(?i)^(validate|verify|run the (complete |full )?validation)\b`)

// manualPattern names the checkpoints that require a person, beyond the ones
// the checkpoint classifier already recognizes. Each of these has been seen in
// a real task list in this repository.
var manualPattern = regexp.MustCompile(`(?i)\b(demo|prototype demo|design sign-?off|sign-?off|approval|approve|credential|api key|secret|authori[sz]e|open (a )?pr|open seam-pr|squash-?merge|merge to|deploy|release|publish|wt done|worktree cleanup|delete the worktree)\b`)

// classifyBoundary decides who may do one item.
//
// Items the parser did not flag as checkpoints are implementation work: they
// are the ordinary subtasks a plan is mostly made of. A checkpoint is safe
// only when it is recognizably a validation or a commit step, because an
// unrecognized checkpoint is exactly the case where guessing is expensive.
func classifyBoundary(text string, checkpoint bool) Boundary {
	if manualPattern.MatchString(text) {
		return BoundaryManual
	}
	if !checkpoint {
		return BoundaryImplementation
	}
	switch {
	case commitPattern.MatchString(text):
		return BoundaryCommit
	case validationPattern.MatchString(text):
		return BoundaryValidation
	default:
		return BoundaryManual
	}
}

// NextUnfinished returns the first incomplete item in plan order, whatever kind
// it is. It is what the supervisor looks at, because order is what decides
// whether a manual checkpoint stands between the agent and the next task.
func (p Plan) NextUnfinished() (Item, bool) {
	for _, milestone := range p.Milestones {
		for _, subtask := range milestone.Subtasks {
			if !subtask.Completed {
				return subtask, true
			}
		}
		// A parent left unchecked after all its subtasks are done is
		// bookkeeping the agent still owns, but it is not work to prompt for.
		if !milestone.Completed && !milestone.Implicit && len(milestone.Subtasks) == 0 {
			return milestone.Item, true
		}
	}
	return Item{}, false
}

// SafeNext returns the next item an unattended agent may work on.
//
// It is not "the next implementation task somewhere in the plan". If a manual
// checkpoint comes first, there is no safe next item at all, and reaching past
// it would be exactly the boundary crossing this feature must never make.
func (p Plan) SafeNext() (Item, bool) {
	next, ok := p.NextUnfinished()
	if !ok || !next.Boundary.Safe() {
		return Item{}, false
	}
	return next, true
}

// FirstManual returns the first incomplete manual checkpoint in plan order.
func (p Plan) FirstManual() (Item, bool) {
	for _, milestone := range p.Milestones {
		for _, subtask := range milestone.Subtasks {
			if !subtask.Completed && subtask.Boundary == BoundaryManual {
				return subtask, true
			}
		}
	}
	return Item{}, false
}

// ImplementationBoundaryComplete reports whether every item an unattended agent
// may do is finished, so only manual work remains.
func (p Plan) ImplementationBoundaryComplete() bool {
	for _, milestone := range p.Milestones {
		for _, subtask := range milestone.Subtasks {
			if !subtask.Completed && subtask.Boundary.Safe() {
				return false
			}
		}
	}
	return true
}
