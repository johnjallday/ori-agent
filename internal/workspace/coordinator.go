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
func stampAssignment(task *Task, agent string, a TaskAssignment) {
	task.To = agent
	task.AssignedNodeID = strings.TrimSpace(a.NodeID)
	task.AssignedBy = strings.TrimSpace(a.AssignedBy)
	task.AssignmentMode = a.Mode
	task.AssignmentReason = strings.TrimSpace(a.Reason)
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
