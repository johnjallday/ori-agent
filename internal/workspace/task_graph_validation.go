package workspace

import (
	"fmt"
	"strings"
)

// TaskGraphError aggregates one or more issues discovered while validating the
// task dependency graph. The Issues slice is exposed so callers can render
// per-issue feedback (e.g. surface them as markdown-import warnings) instead
// of just dumping the joined Error string.
type TaskGraphError struct {
	Issues []string
}

func (e *TaskGraphError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "task graph validation: no issues"
	}
	return "task graph validation: " + strings.Join(e.Issues, "; ")
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

	var issues []string

	seen := make(map[string]struct{}, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		if t.ID == "" {
			continue
		}
		if _, dup := seen[t.ID]; dup {
			issues = append(issues, fmt.Sprintf("duplicate task ID %q", t.ID))
		}
		seen[t.ID] = struct{}{}

		if t.ParentTaskID != "" {
			if t.ParentTaskID == t.ID {
				issues = append(issues, fmt.Sprintf("task %q lists itself as parent", t.ID))
			} else if _, ok := index[t.ParentTaskID]; !ok {
				issues = append(issues, fmt.Sprintf("task %q references unknown parent %q", t.ID, t.ParentTaskID))
			}
		}
		for _, inputID := range t.InputTaskIDs {
			if inputID == "" {
				continue
			}
			if inputID == t.ID {
				issues = append(issues, fmt.Sprintf("task %q lists itself as input", t.ID))
				continue
			}
			if _, ok := index[inputID]; !ok {
				issues = append(issues, fmt.Sprintf("task %q references unknown input %q", t.ID, inputID))
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
					issues = append(issues, fmt.Sprintf("dependency cycle through task %q", t.ID))
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
					issues = append(issues, fmt.Sprintf("dependency cycle through task %q", t.ID))
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
