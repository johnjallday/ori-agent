package workspace

import (
	"strings"
	"testing"
)

func TestTaskResultToCSV_UsesStructuredOutput(t *testing.T) {
	task := &Task{
		ID:          "task-1",
		Description: "Check pollen",
		Context: map[string]interface{}{
			"structured_output": map[string]interface{}{
				"location": "NYC",
				"value":    8,
				"unit":     "index",
			},
		},
	}

	got := TaskResultToCSV(task, "plain result", "20260519-120000", "")
	if !strings.Contains(got, "location,value,unit") {
		t.Fatalf("expected structured columns, got %q", got)
	}
	if !strings.Contains(got, "NYC,8,index") {
		t.Fatalf("expected structured row, got %q", got)
	}
}

func TestTaskResultToCSV_ParsesJSONArray(t *testing.T) {
	got := TaskResultToCSV(nil, `[{"date":"2026-05-18","level":"Moderate"},{"date":"2026-05-19","level":"High"}]`, "20260519-120000", "")
	if !strings.Contains(got, "date,level") {
		t.Fatalf("expected date and level header, got %q", got)
	}
	if !strings.Contains(got, "2026-05-19,High") {
		t.Fatalf("expected high pollen row, got %q", got)
	}
}

func TestTaskResultToCSV_FallsBackToSingleResultRow(t *testing.T) {
	task := &Task{ID: "task-1", Description: "Check pollen"}
	got := TaskResultToCSV(task, "Pollen is high.", "20260519-120000", "agent-1")
	if !strings.HasPrefix(got, "task_id,description,timestamp,agent,result") {
		t.Fatalf("expected fallback header, got %q", got)
	}
	if !strings.Contains(got, "task-1,Check pollen,20260519-120000,agent-1,Pollen is high.") {
		t.Fatalf("expected fallback row, got %q", got)
	}
}
