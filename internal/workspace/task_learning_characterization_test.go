package workspace

import (
	"strings"
	"testing"
	"time"
)

func TestTaskLearningCharacterization_HistoryIsLossyAndLatestRunMoves(t *testing.T) {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	task := &Task{ID: "ticket", CurrentRunID: "original"}
	result := strings.Repeat("x", maxRecordedTaskExecutionResult+1)
	RecordTaskExecution(task, "success", result, at, time.Second)
	if task.ExecutionHistory[0].Result == result || !strings.HasSuffix(task.ExecutionHistory[0].Result, "…[truncated]") {
		t.Fatal("history should disclose truncation; it cannot replace the full Run artifact")
	}
	for i := range maxRecordedTaskExecutions {
		task.CurrentRunID = "later"
		RecordTaskExecution(task, "success", "another execution", at.Add(time.Duration(i+1)*time.Minute), time.Second)
	}
	if len(task.ExecutionHistory) != maxRecordedTaskExecutions || task.ExecutionHistory[0].RunID == "original" {
		t.Fatal("the capped history must not be treated as durable evidence of every attempt")
	}
	if task.CurrentRunID != "later" {
		t.Fatal("the current Run pointer is latest-attempt state, not original-attempt authority")
	}
}
