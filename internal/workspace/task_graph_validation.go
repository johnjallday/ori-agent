package workspace

import (
	"fmt"
	"strings"
)

// TaskGraphIssueKind classifies the way a single graph edge fails validation.
// HTTP layers can map the kind onto a UI affordance (highlight which subtask
// row references which missing/invalid task) without having to parse the
// human-facing message.
type TaskGraphIssueKind string

const (
	TaskGraphIssueDuplicateID    TaskGraphIssueKind = "duplicate_id"
	TaskGraphIssueSelfParent     TaskGraphIssueKind = "self_parent"
	TaskGraphIssueSelfInput      TaskGraphIssueKind = "self_input"
	TaskGraphIssueUnknownParent  TaskGraphIssueKind = "unknown_parent"
	TaskGraphIssueUnknownInput   TaskGraphIssueKind = "unknown_input"
	TaskGraphIssueDependencyLoop TaskGraphIssueKind = "dependency_cycle"
)

// TaskGraphIssue describes a single validation problem.
//
// TaskID is the task the issue is about — i.e., the row a UI should highlight.
// Reference is the *other* task ID involved (e.g., the missing parent ID, or
// the bad input ID). It is empty for kinds that do not point at a peer (the
// duplicate-ID kind, for instance, is fully described by TaskID alone).
// Message is the human-readable rendering used for non-structured surfaces.
type TaskGraphIssue struct {
	Kind      TaskGraphIssueKind `json:"kind"`
	TaskID    string             `json:"task_id"`
	Reference string             `json:"reference,omitempty"`
	Message   string             `json:"message"`
}

// TaskGraphError aggregates one or more issues discovered while validating the
// task dependency graph.
type TaskGraphError struct {
	Issues []TaskGraphIssue
}

func (e *TaskGraphError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "task graph validation: no issues"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return "task graph validation: " + strings.Join(parts, "; ")
}

// IssueMessages returns the human-readable messages of every issue, preserving
// order. Useful when callers want to log or render the flat strings without
// caring about the structured fields.
func (e *TaskGraphError) IssueMessages() []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		out = append(out, issue.Message)
	}
	return out
}

// validateTaskGraph inspects a slice of tasks for problems in their parent /
// input dependency edges:
//
//   - Self-references (task lists itself as parent or input).
//   - Unknown references (parent / input ID does not match any task).
//   - Duplicate IDs.
//   - Cycles formed by the union of parent + input edges.
//
// Both edge types express a "must exist / complete before me" relationship, so
// a cycle through either kind would deadlock execution. The detector treats the
// graph as one combined directed graph: x → ParentTaskID(x) and x → each
// InputTaskIDs(x).
//
// Complexity is O(V + E). For typical workspaces (tens to hundreds of tasks
// with one or two edges each) this is negligible.
func validateTaskGraph(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}

	index := make(map[string]int, len(tasks))
	for i := range tasks {
		if tasks[i].ID == "" {
			continue
		}
		// Don't overwrite — duplicate IDs are reported below and we want the
		// first occurrence to remain the canonical edge target.
		if _, exists := index[tasks[i].ID]; !exists {
			index[tasks[i].ID] = i
		}
	}

	var issues []TaskGraphIssue

	seen := make(map[string]struct{}, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		if t.ID == "" {
			continue
		}
		if _, dup := seen[t.ID]; dup {
			issues = append(issues, TaskGraphIssue{
				Kind:    TaskGraphIssueDuplicateID,
				TaskID:  t.ID,
				Message: fmt.Sprintf("duplicate task ID %q", t.ID),
			})
		}
		seen[t.ID] = struct{}{}

		if t.ParentTaskID != "" {
			if t.ParentTaskID == t.ID {
				issues = append(issues, TaskGraphIssue{
					Kind:      TaskGraphIssueSelfParent,
					TaskID:    t.ID,
					Reference: t.ID,
					Message:   fmt.Sprintf("task %q lists itself as parent", t.ID),
				})
			} else if _, ok := index[t.ParentTaskID]; !ok {
				issues = append(issues, TaskGraphIssue{
					Kind:      TaskGraphIssueUnknownParent,
					TaskID:    t.ID,
					Reference: t.ParentTaskID,
					Message:   fmt.Sprintf("task %q references unknown parent %q", t.ID, t.ParentTaskID),
				})
			}
		}
		for _, inputID := range t.InputTaskIDs {
			if inputID == "" {
				continue
			}
			if inputID == t.ID {
				issues = append(issues, TaskGraphIssue{
					Kind:      TaskGraphIssueSelfInput,
					TaskID:    t.ID,
					Reference: t.ID,
					Message:   fmt.Sprintf("task %q lists itself as input", t.ID),
				})
				continue
			}
			if _, ok := index[inputID]; !ok {
				issues = append(issues, TaskGraphIssue{
					Kind:      TaskGraphIssueUnknownInput,
					TaskID:    t.ID,
					Reference: inputID,
					Message:   fmt.Sprintf("task %q references unknown input %q", t.ID, inputID),
				})
			}
		}
	}

	// Three-color DFS over the union edge graph. White = unvisited,
	// Gray = on current stack, Black = fully explored. Encountering a Gray
	// node during traversal proves a back-edge, i.e. a cycle.
	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := make([]int, len(tasks))
	reportedCycle := make(map[string]struct{})

	var visit func(idx int) bool
	visit = func(idx int) bool {
		if color[idx] == colorBlack {
			return false
		}
		if color[idx] == colorGray {
			return true
		}
		color[idx] = colorGray
		t := &tasks[idx]
		if pIdx, ok := index[t.ParentTaskID]; ok && pIdx != idx {
			if visit(pIdx) {
				if _, dup := reportedCycle[t.ID]; !dup {
					issues = append(issues, TaskGraphIssue{
						Kind:      TaskGraphIssueDependencyLoop,
						TaskID:    t.ID,
						Reference: t.ParentTaskID,
						Message:   fmt.Sprintf("dependency cycle through task %q", t.ID),
					})
					reportedCycle[t.ID] = struct{}{}
				}
				color[idx] = colorBlack
				return true
			}
		}
		for _, inputID := range t.InputTaskIDs {
			iIdx, ok := index[inputID]
			if !ok || iIdx == idx {
				continue
			}
			if visit(iIdx) {
				if _, dup := reportedCycle[t.ID]; !dup {
					issues = append(issues, TaskGraphIssue{
						Kind:      TaskGraphIssueDependencyLoop,
						TaskID:    t.ID,
						Reference: inputID,
						Message:   fmt.Sprintf("dependency cycle through task %q", t.ID),
					})
					reportedCycle[t.ID] = struct{}{}
				}
				color[idx] = colorBlack
				return true
			}
		}
		color[idx] = colorBlack
		return false
	}

	for i := range tasks {
		if tasks[i].ID == "" {
			continue
		}
		if color[i] == colorWhite {
			visit(i)
		}
	}

	if len(issues) > 0 {
		return &TaskGraphError{Issues: issues}
	}
	return nil
}

// ValidateTaskGraph runs validateTaskGraph over the workspace's current task
// list under the read lock. Use this from batch mutators (markdown import,
// orchestration plan apply) to surface graph errors after a sequence of
// AddTask / MutateTask calls has settled.
func (w *Workspace) ValidateTaskGraph() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return validateTaskGraph(w.Tasks)
}
