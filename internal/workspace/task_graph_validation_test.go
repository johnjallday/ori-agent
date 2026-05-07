package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTaskGraph_Empty(t *testing.T) {
	if err := validateTaskGraph(nil); err != nil {
		t.Fatalf("nil tasks: unexpected error: %v", err)
	}
	if err := validateTaskGraph([]Task{}); err != nil {
		t.Fatalf("empty tasks: unexpected error: %v", err)
	}
}

func TestValidateTaskGraph_AcyclicGraphIsValid(t *testing.T) {
	tasks := []Task{
		{ID: "root"},
		{ID: "child", ParentTaskID: "root"},
		{ID: "grandchild", ParentTaskID: "child"},
		{ID: "consumer", InputTaskIDs: []string{"root", "grandchild"}},
	}
	if err := validateTaskGraph(tasks); err != nil {
		t.Fatalf("expected valid graph, got %v", err)
	}
}

func TestValidateTaskGraph_SelfParentIsRejected(t *testing.T) {
	tasks := []Task{{ID: "a", ParentTaskID: "a"}}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected error for self-parent")
	}
	var gErr *TaskGraphError
	if !errors.As(err, &gErr) {
		t.Fatalf("expected *TaskGraphError, got %T", err)
	}
	if !containsIssue(gErr.Issues, "lists itself as parent") {
		t.Fatalf("missing self-parent issue, got %v", gErr.Issues)
	}
}

func TestValidateTaskGraph_SelfInputIsRejected(t *testing.T) {
	tasks := []Task{{ID: "a", InputTaskIDs: []string{"a"}}}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected error for self-input")
	}
	var gErr *TaskGraphError
	if !errors.As(err, &gErr) || !containsIssue(gErr.Issues, "lists itself as input") {
		t.Fatalf("missing self-input issue, got %v", err)
	}
}

func TestValidateTaskGraph_UnknownParentRef(t *testing.T) {
	tasks := []Task{{ID: "a", ParentTaskID: "ghost"}}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected error for unknown parent")
	}
	var gErr *TaskGraphError
	if !errors.As(err, &gErr) || !containsIssue(gErr.Issues, `references unknown parent "ghost"`) {
		t.Fatalf("missing unknown-parent issue, got %v", err)
	}
}

func TestValidateTaskGraph_UnknownInputRef(t *testing.T) {
	tasks := []Task{
		{ID: "a"},
		{ID: "b", InputTaskIDs: []string{"a", "ghost"}},
	}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected error for unknown input")
	}
	var gErr *TaskGraphError
	if !errors.As(err, &gErr) || !containsIssue(gErr.Issues, `references unknown input "ghost"`) {
		t.Fatalf("missing unknown-input issue, got %v", err)
	}
}

func TestValidateTaskGraph_TwoNodeParentCycle(t *testing.T) {
	tasks := []Task{
		{ID: "a", ParentTaskID: "b"},
		{ID: "b", ParentTaskID: "a"},
	}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected cycle in message, got %v", err)
	}
}

func TestValidateTaskGraph_ThreeNodeInputCycle(t *testing.T) {
	tasks := []Task{
		{ID: "a", InputTaskIDs: []string{"c"}},
		{ID: "b", InputTaskIDs: []string{"a"}},
		{ID: "c", InputTaskIDs: []string{"b"}},
	}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestValidateTaskGraph_MixedParentInputCycle(t *testing.T) {
	// a's parent is b; b takes input from a → cycle through mixed edge types.
	tasks := []Task{
		{ID: "a", ParentTaskID: "b"},
		{ID: "b", InputTaskIDs: []string{"a"}},
	}
	if err := validateTaskGraph(tasks); err == nil {
		t.Fatal("expected mixed-edge cycle error")
	}
}

func TestValidateTaskGraph_DuplicateID(t *testing.T) {
	tasks := []Task{{ID: "a"}, {ID: "a"}}
	err := validateTaskGraph(tasks)
	if err == nil {
		t.Fatal("expected duplicate-ID error")
	}
	var gErr *TaskGraphError
	if !errors.As(err, &gErr) || !containsIssue(gErr.Issues, "duplicate task ID") {
		t.Fatalf("missing duplicate-ID issue, got %v", err)
	}
}

func TestAddTask_RejectsSelfParent(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddTask(Task{ID: "a", ParentTaskID: "a"}); err == nil {
		t.Fatal("expected AddTask to reject self-parent")
	}
	if len(ws.Tasks) != 0 {
		t.Fatalf("expected rollback, but workspace contains %d tasks", len(ws.Tasks))
	}
	if _, ok := ws.taskIndex["a"]; ok {
		t.Fatal("expected task index to be rolled back")
	}
}

func TestAddTask_RejectsUnknownParent(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	err := ws.AddTask(Task{ID: "child", ParentTaskID: "missing"})
	if err == nil {
		t.Fatal("expected AddTask to reject unknown parent ref")
	}
	if len(ws.Tasks) != 0 {
		t.Fatalf("expected rollback, got %d tasks", len(ws.Tasks))
	}
}

func TestAddTask_AcceptsAddingAfterParent(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddTask(Task{ID: "parent"}); err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if err := ws.AddTask(Task{ID: "child", ParentTaskID: "parent"}); err != nil {
		t.Fatalf("add child after parent: unexpected error %v", err)
	}
	if len(ws.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(ws.Tasks))
	}
}

func TestAddTask_RejectsCycleIntroducedByNewEdge(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddTask(Task{ID: "a"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := ws.AddTask(Task{ID: "b", InputTaskIDs: []string{"a"}}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// b → a → b would be a cycle. Mutating "a" to depend on "b" requires
	// MutateTask, which doesn't validate; but adding a third task that links
	// the loop should be detected. Simulate by trying to add a task that
	// closes a cycle through itself: c with parent=c.
	err := ws.AddTask(Task{ID: "c", ParentTaskID: "c"})
	if err == nil {
		t.Fatal("expected self-parent on add to fail")
	}
	if len(ws.Tasks) != 2 {
		t.Fatalf("expected rollback to leave 2 tasks, got %d", len(ws.Tasks))
	}
}

func TestAddTask_NoEdgesSkipsValidation(t *testing.T) {
	// Tasks without parent or input refs should not pay the validation cost
	// and must always succeed even when the existing graph has issues
	// introduced through other paths (e.g. MutateTask). This test pins down
	// the hot-path optimization in AddTask.
	ws := &Workspace{ID: "ws1"}
	for i := 0; i < 5; i++ {
		if err := ws.AddTask(Task{ID: "t" + string(rune('0'+i))}); err != nil {
			t.Fatalf("AddTask %d: %v", i, err)
		}
	}
	if len(ws.Tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(ws.Tasks))
	}
}

func TestWorkspace_ValidateTaskGraph_PostBatch(t *testing.T) {
	ws := &Workspace{ID: "ws1"}
	if err := ws.AddTask(Task{ID: "a"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := ws.AddTask(Task{ID: "b", InputTaskIDs: []string{"a"}}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	// Use MutateTask (which intentionally skips validation) to introduce a
	// cycle, then confirm the batch validator catches it.
	if err := ws.MutateTask("a", func(t *Task) error {
		t.InputTaskIDs = []string{"b"}
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if err := ws.ValidateTaskGraph(); err == nil {
		t.Fatal("expected ValidateTaskGraph to report cycle introduced via MutateTask")
	}
}

func containsIssue(issues []string, needle string) bool {
	for _, s := range issues {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
