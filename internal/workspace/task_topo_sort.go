package workspace

import "fmt"

// TopoSortTasks returns the input tasks reordered so every task appears after
// all tasks it depends on (via Task.ParentTaskID and Task.InputTaskIDs).
// Order among equally-ready tasks follows their original input position so
// the result is stable when no dependencies actually constrain the order.
//
// Edges are interpreted as "must run before": a task that names X as parent
// or input depends on X. Tasks referencing IDs that aren't in the input
// slice contribute no edges (the caller should validate references upstream
// via validateTaskGraph if missing-ref errors are wanted; this function
// silently ignores them so it can be applied to a freshly LLM-generated
// batch where the cross-batch graph hasn't been pinned down yet).
//
// Returns an error if the dependency graph contains a cycle.
//
// Complexity: O(V + E) using Kahn's algorithm with a sorted ready set.
func TopoSortTasks(tasks []Task) ([]Task, error) {
	if len(tasks) <= 1 {
		out := make([]Task, len(tasks))
		copy(out, tasks)
		return out, nil
	}

	indexByID := make(map[string]int, len(tasks))
	for i := range tasks {
		if tasks[i].ID != "" {
			indexByID[tasks[i].ID] = i
		}
	}

	inDegree := make([]int, len(tasks))
	// outEdges[i] = original indices of tasks that depend on tasks[i].
	outEdges := make([][]int, len(tasks))

	addEdge := func(from, to int) {
		if from == to || from < 0 || to < 0 {
			return
		}
		outEdges[from] = append(outEdges[from], to)
		inDegree[to]++
	}

	for i := range tasks {
		t := &tasks[i]
		if t.ParentTaskID != "" {
			if pIdx, ok := indexByID[t.ParentTaskID]; ok {
				addEdge(pIdx, i)
			}
		}
		for _, depID := range t.InputTaskIDs {
			if depIdx, ok := indexByID[depID]; ok {
				addEdge(depIdx, i)
			}
		}
	}

	// Initial ready set: nodes with no incoming edges, in original order.
	ready := make([]int, 0, len(tasks))
	for i := range tasks {
		if inDegree[i] == 0 {
			ready = append(ready, i)
		}
	}

	out := make([]Task, 0, len(tasks))
	for len(ready) > 0 {
		// Pop the smallest original index — preserves stability among siblings.
		idx := ready[0]
		ready = ready[1:]
		out = append(out, tasks[idx])

		for _, next := range outEdges[idx] {
			inDegree[next]--
			if inDegree[next] == 0 {
				// Insert keeping ready sorted by original index.
				insertAt := len(ready)
				for j, r := range ready {
					if r > next {
						insertAt = j
						break
					}
				}
				ready = append(ready, 0)
				copy(ready[insertAt+1:], ready[insertAt:])
				ready[insertAt] = next
			}
		}
	}

	if len(out) != len(tasks) {
		return nil, fmt.Errorf("topological sort failed: dependency cycle (sorted %d of %d tasks)", len(out), len(tasks))
	}
	return out, nil
}
