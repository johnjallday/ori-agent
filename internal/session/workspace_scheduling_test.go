package session

import (
	"testing"
	"time"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// TestListActiveForScheduling_KeepsTasksDropsMessages guards item 2.4: the
// scheduler's per-tick listing keeps scheduling state (tasks, schedule fields,
// status) but omits chat history, which the scheduler never reads.
func TestListActiveForScheduling_KeepsTasksDropsMessages(t *testing.T) {
	store, cleanup := setupHybridStore(t, 10)
	defer cleanup()

	adapter := NewWorkspaceStoreAdapter(store)

	next := time.Now().Add(time.Hour)
	ws := &agentworkspace.Workspace{
		ID:     "ws-sched-1",
		Name:   "Scheduler WS",
		Status: agentworkspace.StatusActive,
		Messages: []agentworkspace.AgentMessage{
			{ID: "m1", Content: "hello"},
			{ID: "m2", Content: "world"},
		},
		Tasks: []agentworkspace.Task{{
			ID:              "t1",
			ScheduleEnabled: true,
			NextRun:         &next,
			Schedule:        &agentworkspace.ScheduleConfig{},
		}},
	}
	if err := adapter.Save(ws); err != nil {
		t.Fatalf("Save: %v", err)
	}

	active, err := adapter.ListActiveForScheduling()
	if err != nil {
		t.Fatalf("ListActiveForScheduling: %v", err)
	}

	var got *agentworkspace.Workspace
	for _, w := range active {
		if w.ID == "ws-sched-1" {
			got = w
		}
	}
	if got == nil {
		t.Fatal("workspace not returned by ListActiveForScheduling")
	}
	if len(got.Messages) != 0 {
		t.Errorf("scheduling view should omit messages, got %d", len(got.Messages))
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "t1" {
		t.Fatalf("scheduling view should keep tasks, got %d", len(got.Tasks))
	}
	if !got.Tasks[0].ScheduleEnabled || got.Tasks[0].NextRun == nil {
		t.Errorf("schedule fields not preserved: enabled=%v nextRun=%v",
			got.Tasks[0].ScheduleEnabled, got.Tasks[0].NextRun)
	}

	// Sanity: a full Get still returns the chat history.
	full, err := adapter.Get("ws-sched-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(full.Messages) != 2 {
		t.Errorf("full Get should keep messages, got %d", len(full.Messages))
	}
}
