package workspace

import (
	"strings"
	"testing"
)

func TestCombineLoopResult(t *testing.T) {
	order := []string{"a", "b", "c"}
	results := map[string]string{"a": "first", "b": "", "c": "third"}

	t.Run("coordinator direct result wins (primary synthesis)", func(t *testing.T) {
		got := combineLoopResult("the coherent answer", TaskResultCombinationConcat, order, results)
		if got != "the coherent answer" {
			t.Fatalf("got %q, want the coordinator's synthesis", got)
		}
	})

	t.Run("concat joins non-empty results", func(t *testing.T) {
		got := combineLoopResult("", TaskResultCombinationConcat, order, results)
		if got != "first\n\nthird" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("last_result returns the last non-empty", func(t *testing.T) {
		got := combineLoopResult("", TaskResultCombinationLastResult, order, results)
		if got != "third" {
			t.Fatalf("got %q, want third", got)
		}
	})

	t.Run("json_map returns a map keyed by subtask id", func(t *testing.T) {
		got := combineLoopResult("", TaskResultCombinationJSONMap, order, results)
		if !strings.Contains(got, `"a":"first"`) || !strings.Contains(got, `"c":"third"`) {
			t.Fatalf("got %q, want a json map", got)
		}
	})
}

func TestCancelTaskPropagatesToSubtasks(t *testing.T) {
	te := NewTaskExecutor(NewInMemoryStore(), nil, ExecutorConfig{})

	parentCancelled, childCancelled, otherCancelled := false, false, false
	te.runningTasks = map[string]*taskExecution{
		"parent": {Task: Task{ID: "parent"}, Cancel: func() { parentCancelled = true }},
		"child":  {Task: Task{ID: "child", ParentTaskID: "parent"}, Cancel: func() { childCancelled = true }},
		"other":  {Task: Task{ID: "other"}, Cancel: func() { otherCancelled = true }},
	}

	if err := te.CancelTask("parent"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if !parentCancelled {
		t.Fatal("parent task was not cancelled")
	}
	if !childCancelled {
		t.Fatal("in-flight subtask was not cancelled (orphaned)")
	}
	if otherCancelled {
		t.Fatal("unrelated task must not be cancelled")
	}
}
