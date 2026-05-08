package workspace

import (
	"encoding/json"
	"testing"
)

func TestTopoSortTasks_PreservesOrderWhenNoDependencies(t *testing.T) {
	in := []Task{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	out, err := TopoSortTasks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 || out[0].ID != "a" || out[1].ID != "b" || out[2].ID != "c" {
		t.Fatalf("expected stable order a,b,c, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_ReordersToRespectInputDeps(t *testing.T) {
	// Input order: c depends on b, b depends on a — but presented in
	// reverse order so a naive sequential executor would run them wrong.
	in := []Task{
		{ID: "c", InputTaskIDs: []string{"b"}},
		{ID: "b", InputTaskIDs: []string{"a"}},
		{ID: "a"},
	}
	out, err := TopoSortTasks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 || out[0].ID != "a" || out[1].ID != "b" || out[2].ID != "c" {
		t.Fatalf("expected topo order a,b,c, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_RespectsParentEdges(t *testing.T) {
	in := []Task{
		{ID: "child", ParentTaskID: "parent"},
		{ID: "parent"},
	}
	out, err := TopoSortTasks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].ID != "parent" || out[1].ID != "child" {
		t.Fatalf("parent must come before child, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_StableAmongIndependentTasks(t *testing.T) {
	// All three depend on root; the three siblings have no relative order
	// constraint, so the original input order should be preserved.
	in := []Task{
		{ID: "root"},
		{ID: "first", InputTaskIDs: []string{"root"}},
		{ID: "second", InputTaskIDs: []string{"root"}},
		{ID: "third", InputTaskIDs: []string{"root"}},
	}
	out, err := TopoSortTasks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 4 || out[0].ID != "root" || out[1].ID != "first" ||
		out[2].ID != "second" || out[3].ID != "third" {
		t.Fatalf("expected stable sibling order, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_ReturnsErrorOnCycle(t *testing.T) {
	in := []Task{
		{ID: "a", InputTaskIDs: []string{"b"}},
		{ID: "b", InputTaskIDs: []string{"a"}},
	}
	out, err := TopoSortTasks(in)
	if err == nil {
		t.Fatalf("expected cycle error, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_IgnoresUnknownReferences(t *testing.T) {
	// "ghost" isn't in the input slice. It contributes no edge — the task
	// is still ready immediately. Validation of unknown refs is the job of
	// validateTaskGraph, not topo sort.
	in := []Task{
		{ID: "a", InputTaskIDs: []string{"ghost"}},
	}
	out, err := TopoSortTasks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0].ID != "a" {
		t.Fatalf("expected single task, got %v", taskIDs(out))
	}
}

func TestTopoSortTasks_SingleTaskShortCircuits(t *testing.T) {
	in := []Task{{ID: "only"}}
	out, err := TopoSortTasks(in)
	if err != nil || len(out) != 1 || out[0].ID != "only" {
		t.Fatalf("single-task topo sort failed: %v / %v", err, out)
	}
}

func TestTopoSortTasks_EmptyInput(t *testing.T) {
	out, err := TopoSortTasks(nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("nil input: err=%v out=%v", err, out)
	}
	out, err = TopoSortTasks([]Task{})
	if err != nil || len(out) != 0 {
		t.Fatalf("empty input: err=%v out=%v", err, out)
	}
}

func TestParseDependencyIndex_HandlesIntsAndStrings(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		taskCount int
		wantIdx   int
		wantOK    bool
	}{
		{"int 1-based to 0-based", `2`, 5, 1, true},
		{"int first task", `1`, 5, 0, true},
		{"int out of range high", `6`, 5, 0, false},
		{"int zero rejected", `0`, 5, 0, false},
		{"int negative rejected", `-1`, 5, 0, false},
		{"string-of-int", `"3"`, 5, 2, true},
		{"non-numeric string rejected", `"abc"`, 5, 0, false},
		{"object rejected", `{}`, 5, 0, false},
		{"null rejected", `null`, 5, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIdx, gotOK := parseDependencyIndex(json.RawMessage(tc.raw), tc.taskCount)
			if gotOK != tc.wantOK {
				t.Fatalf("ok mismatch: want %v got %v (raw=%s)", tc.wantOK, gotOK, tc.raw)
			}
			if gotOK && gotIdx != tc.wantIdx {
				t.Fatalf("idx mismatch: want %d got %d (raw=%s)", tc.wantIdx, gotIdx, tc.raw)
			}
		})
	}
}

func taskIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}
