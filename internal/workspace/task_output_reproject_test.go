package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReprojectResultToColumns_KnownColumnsNoAssistant(t *testing.T) {
	executedAt := time.Date(2026, 5, 12, 10, 40, 27, 0, time.UTC)
	task := &Task{
		ID:          "task-123",
		Description: "check pollen count in nyc",
		To:          "pollen Manager",
		ExecutionHistory: []TaskExecution{
			{RunID: "run-9", ExecutedAt: executedAt, Status: "success", Duration: 16256, Summary: "High"},
		},
	}
	raw := "Today's pollen index: 10.3 (high)"

	csv, usedAssistant, err := ReprojectResultToColumns(
		context.Background(),
		task,
		raw,
		[]string{"task_id", "description", "timestamp", "agent", "result"},
		nil,
	)
	if err != nil {
		t.Fatalf("ReprojectResultToColumns: %v", err)
	}
	if usedAssistant {
		t.Errorf("expected no assistant use for fully-known columns")
	}

	lines := strings.Split(csv, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines: %q", len(lines), csv)
	}
	if lines[0] != "task_id,description,timestamp,agent,result" {
		t.Errorf("header = %q, want exact destination header", lines[0])
	}
	if !strings.HasPrefix(lines[1], "task-123,check pollen count in nyc,") {
		t.Errorf("row did not start with task_id,description: %q", lines[1])
	}
	if !strings.Contains(lines[1], "pollen Manager") {
		t.Errorf("row missing agent: %q", lines[1])
	}
	if !strings.Contains(lines[1], "Today's pollen index: 10.3 (high)") {
		t.Errorf("row missing raw result: %q", lines[1])
	}
}

func TestReprojectResultToColumns_UnknownColumnNeedsAssistant(t *testing.T) {
	task := &Task{ID: "t1", Description: "d", To: "agent-a"}
	// "pollen_level" is not a harness-known column; with no assistant it stays
	// blank but the header still matches the destination exactly.
	csv, usedAssistant, err := ReprojectResultToColumns(
		context.Background(),
		task,
		"raw text",
		[]string{"task_id", "pollen_level"},
		nil,
	)
	if err != nil {
		t.Fatalf("ReprojectResultToColumns: %v", err)
	}
	if usedAssistant {
		t.Errorf("no assistant was provided, should not report assistant use")
	}
	lines := strings.Split(csv, "\n")
	if lines[0] != "task_id,pollen_level" {
		t.Errorf("header = %q, want task_id,pollen_level", lines[0])
	}
	if lines[1] != "t1," {
		t.Errorf("row = %q, want task_id filled and pollen_level blank", lines[1])
	}
}

func TestReprojectResultToColumns_NoColumns(t *testing.T) {
	if _, _, err := ReprojectResultToColumns(context.Background(), &Task{}, "x", []string{"  "}, nil); err == nil {
		t.Errorf("expected an error when there are no usable target columns")
	}
}

func TestReprojectResultForAppendMismatch(t *testing.T) {
	task := &Task{ID: "t1", Description: "d", To: "agent-a"}

	// Header mismatch on a fallback row (allowReconcile=true) → reproject into
	// the expected columns.
	mismatch := &CSVHeaderMismatchError{
		Expected: []string{"task_id", "result"},
		Actual:   []string{"executed_at", "status"},
	}
	csv, ok := reprojectResultForAppendMismatch(context.Background(), task, "hello", mismatch, nil, true)
	if !ok {
		t.Fatalf("expected reprojection on a fallback header mismatch")
	}
	lines := strings.Split(csv, "\n")
	if lines[0] != "task_id,result" {
		t.Errorf("header = %q, want task_id,result", lines[0])
	}
	if lines[1] != "t1,hello" {
		t.Errorf("row = %q, want t1,hello", lines[1])
	}

	// A designed contract's mismatch (allowReconcile=false) must NOT be
	// silently reconciled — it stays a hard review.
	if _, ok := reprojectResultForAppendMismatch(context.Background(), task, "hello", mismatch, nil, false); ok {
		t.Errorf("designed-contract mismatch should not auto-reconcile")
	}

	// A non-mismatch error must not trigger reprojection.
	if _, ok := reprojectResultForAppendMismatch(context.Background(), task, "hello", errors.New("disk full"), nil, true); ok {
		t.Errorf("non-mismatch error should not reproject")
	}
}
