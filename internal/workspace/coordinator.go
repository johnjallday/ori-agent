package workspace

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the shared coordinator assignment service: the single place
// that writes a task's assignee and its provenance, plus the helpers callers use
// to resolve the coordinator and the specialist roster it may assign work to.
//
// FR6/FR7: every assignment write funnels through stampAssignment so Task.To and
// the provenance fields stay in lockstep, and coordinator-driven assignment
// validates workspace membership (it never auto-adds agents).

var (
	// ErrCoordinatorMissing is returned when coordinator-driven assignment is
	// requested on a multi-agent workspace that has no explicit entry agent.
	ErrCoordinatorMissing = errors.New("workspace has no entry agent (coordinator); assign one before coordinator-driven task assignment")

	// ErrAssigneeNotInWorkspace is returned when an assignment targets an agent
	// that is not a member of the workspace. Assignment never auto-adds agents.
	ErrAssigneeNotInWorkspace = errors.New("assignee is not a member of the workspace")
)

// TaskAssignment describes a single assignment decision: which agent (and
// optional node) handles a task, who decided, in which mode, and why.
type TaskAssignment struct {
	AgentName  string             // workspace agent name; empty leaves the task unassigned
	NodeID     string             // optional specific agent instance (node) id
	Mode       TaskAssignmentMode // how the assignee was chosen
	AssignedBy string             // coordinator agent name, or TaskAssignedByManual
	Reason     string             // short human-readable rationale
}

// ApplyTaskAssignment is the single chokepoint for coordinator-driven assignment.
// It validates that the assignee is a member of the workspace (never auto-adding
// it) and then stamps the task's assignee and provenance fields. An empty
// AgentName leaves the task unassigned but still records provenance.
func (w *Workspace) ApplyTaskAssignment(task *Task, a TaskAssignment) error {
	if w == nil {
		return fmt.Errorf("workspace is nil")
	}
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	agent := strings.TrimSpace(a.AgentName)
	if agent != "" && !w.HasAgent(agent) {
		return fmt.Errorf("%w: %s", ErrAssigneeNotInWorkspace, agent)
	}
	stampAssignment(task, agent, a)
	return nil
}

// stampAssignment writes the assignee and provenance fields. It is the single
// low-level writer so Task.To never drifts from its provenance.
//
// Naming a real agent records explicit execution intent (FR11-12): it clears
// AwaitingExecutionIntent so a promoted-Ready or directly-created-Ready task
// stops being quiescent the moment it is deliberately assigned. Sweep paths
// (ApplyEntryAgentDefault, ClaimUnassignedTasksForCoordinator) never reach
// this call for a quiescent task in the first place — see
// taskEligibleForCoordinatorClaim — so this unconditional clear is safe.
func stampAssignment(task *Task, agent string, a TaskAssignment) {
	task.To = agent
	task.AssignedNodeID = strings.TrimSpace(a.NodeID)
	task.AssignedBy = strings.TrimSpace(a.AssignedBy)
	task.AssignmentMode = a.Mode
	task.AssignmentReason = strings.TrimSpace(a.Reason)
	if agent != "" {
		task.AwaitingExecutionIntent = false
	}
}

// taskAssigneeIsDefaultable reports whether a task's current assignee is empty
// or the "unassigned" sentinel — i.e. eligible for entry-agent defaulting. A
// task that already names a real assignee is left untouched.
func taskAssigneeIsDefaultable(to string) bool {
	t := strings.TrimSpace(to)
	return t == "" || strings.EqualFold(t, "unassigned")
}

// taskEligibleForCoordinatorClaim reports whether an automatic
// coordinator/entry-agent sweep may claim task: its assignee must be
// defaultable, it must not still be in Backlog, and it must not be a
// quiescent Ready item awaiting an explicit assignment/run/schedule action
// (FR7, FR11; task-list 1.8). Explicit, user-initiated dispatch is not
// gated by this check — only the two automatic sweep functions below call it.
func taskEligibleForCoordinatorClaim(task *Task) bool {
	if task == nil {
		return false
	}
	if !taskAssigneeIsDefaultable(task.To) {
		return false
	}
	if task.Status == TaskStatusBacklog {
		return false
	}
	return !task.AwaitingExecutionIntent
}

// ApplyEntryAgentDefault assigns an otherwise-unassigned task to the workspace
// coordinator (entry agent) at creation time. It returns true only when it made
// the assignment.
//
// It is a no-op when the task already names an assignee, or when no coordinator
// can be resolved (a multi-agent workspace with no explicit entry agent): such
// tasks stay unassigned until a coordinator becomes available, at which point
// ClaimUnassignedTasksForCoordinator picks them up. Assignment funnels through
// ApplyTaskAssignment, so it never auto-adds an agent — the resolver only ever
// returns a current workspace member.
//
// It is also a no-op for a Backlog task or a quiescent Ready task
// (AwaitingExecutionIntent) — this default-assignment sweep must never record
// execution intent on its own (FR7, FR11).
func (w *Workspace) ApplyEntryAgentDefault(task *Task) bool {
	if w == nil || task == nil {
		return false
	}
	if !taskEligibleForCoordinatorClaim(task) {
		return false
	}
	coordinator, source := w.ResolveCoordinator()
	if source == CoordinatorSourceMissing || strings.TrimSpace(coordinator) == "" {
		return false
	}
	if err := w.ApplyTaskAssignment(task, TaskAssignment{
		AgentName:  coordinator,
		Mode:       TaskAssignmentModeEntryAgentDefault,
		AssignedBy: coordinator,
		Reason:     "defaulted to entry agent (coordinator)",
	}); err != nil {
		return false
	}
	return true
}

// ClaimUnassignedTasksForCoordinator hands every currently-unassigned task in the
// workspace to the resolved coordinator, returning the number of tasks claimed.
// It is the sweep that fixes the timing gap where tasks (e.g. template starter
// tasks) were created before an entry agent existed.
//
// It is a no-op when no coordinator can be resolved, and it never touches a task
// that already names an assignee, preserving manual assignments and prior
// delegations. Claimed tasks are stamped TaskAssignmentModeEntryAgentDefault and
// attributed to TaskAssignedBySystem (the coordinator did not actively choose
// them), distinguishing a sweep from a create-time default in audits.
//
// Callers persist the result; the workspace store's Update closure is the
// expected caller so the sweep observes an exclusively-held workspace.
//
// It never claims a Backlog task or a quiescent Ready task
// (AwaitingExecutionIntent) — those stay unclaimed until an explicit
// assignment, run, or schedule action records execution intent (FR7, FR11).
func (w *Workspace) ClaimUnassignedTasksForCoordinator() int {
	if w == nil {
		return 0
	}
	coordinator, source := w.ResolveCoordinator()
	if source == CoordinatorSourceMissing || strings.TrimSpace(coordinator) == "" {
		return 0
	}
	claimed := 0
	for i := range w.Tasks {
		if !taskEligibleForCoordinatorClaim(&w.Tasks[i]) {
			continue
		}
		if err := w.ApplyTaskAssignment(&w.Tasks[i], TaskAssignment{
			AgentName:  coordinator,
			Mode:       TaskAssignmentModeEntryAgentDefault,
			AssignedBy: TaskAssignedBySystem,
			Reason:     "claimed by entry agent (coordinator)",
		}); err != nil {
			continue
		}
		claimed++
	}
	return claimed
}

// ResolveCoordinatorForAssignment returns the coordinator agent to drive
// coordinator-owned assignment, or ErrCoordinatorMissing when a multi-agent
// workspace has no explicit entry agent. The single-agent default is allowed.
func (w *Workspace) ResolveCoordinatorForAssignment() (string, error) {
	if w == nil {
		return "", ErrCoordinatorMissing
	}
	name, source := w.ResolveCoordinator()
	if source == CoordinatorSourceMissing {
		return "", ErrCoordinatorMissing
	}
	return name, nil
}

// CoordinatorRoster lists the agents a coordinator may assign work to, with the
// coordinator marked. It is built only from workspace members; capability
// descriptions are layered on by callers that hold the agent store.
type CoordinatorRoster struct {
	Coordinator       string
	CoordinatorSource CoordinatorSource
	Specialists       []string // member agents excluding the coordinator
}

// BuildCoordinatorRoster returns the workspace's coordinator and the member
// specialists it may delegate to. Non-member agents are never included.
func (w *Workspace) BuildCoordinatorRoster() CoordinatorRoster {
	if w == nil {
		return CoordinatorRoster{CoordinatorSource: CoordinatorSourceMissing}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	coordinator, source := w.resolveCoordinatorLocked()
	names := w.runnableAgentNamesLocked()

	coordKey := normalizeAgentNameKey(coordinator)
	specialists := make([]string, 0, len(names))
	for _, name := range names {
		if coordinator != "" && normalizeAgentNameKey(name) == coordKey {
			continue
		}
		specialists = append(specialists, name)
	}

	return CoordinatorRoster{
		Coordinator:       coordinator,
		CoordinatorSource: source,
		Specialists:       specialists,
	}
}
