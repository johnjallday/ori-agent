package economy

import (
	"fmt"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// leanListStore reproduces the shape every real store in this repo has:
// ListActive hands back metadata-only records with the heavy fields stripped,
// and only Get returns the hydrated workspace.
//
// A Farm listing written against ListActive alone compiles, passes a naive test
// built on an in-memory store, and then reports that nobody has any Farms on a
// real install. This fake is the regression guard for that.
type leanListStore struct {
	workspace.Store
	hydrated []*workspace.Workspace
}

func (s *leanListStore) ListActive() ([]*workspace.Workspace, error) {
	lean := make([]*workspace.Workspace, 0, len(s.hydrated))
	for _, ws := range s.hydrated {
		lean = append(lean, &workspace.Workspace{ID: ws.ID, Name: ws.Name})
	}
	return lean, nil
}

func (s *leanListStore) Get(id string) (*workspace.Workspace, error) {
	for _, ws := range s.hydrated {
		if ws.ID == id {
			return ws, nil
		}
	}
	return nil, fmt.Errorf("workspace %s not found", id)
}

func hydratedWorkspace() *workspace.Workspace {
	return &workspace.Workspace{
		ID:   "ws1",
		Name: "Email Ops",
		Tasks: []workspace.Task{
			{
				ID:              "farm-1",
				Description:     "Triage the inbox",
				ScheduleName:    "Inbox triage",
				ScheduleEnabled: true,
				Schedule:        &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"},
				ExecutionHistory: []workspace.TaskExecution{
					{Status: "success", Summary: "12 threads triaged", ExecutedAt: time.Now()},
				},
			},
			{
				ID:              "disabled-farm",
				Description:     "Paused weekly report",
				ScheduleEnabled: false,
				Schedule:        &workspace.ScheduleConfig{Type: workspace.ScheduleWeekly, TimeOfDay: "09:00"},
			},
			{
				ID:              "one-shot",
				Description:     "Run once tomorrow",
				ScheduleEnabled: true,
				Schedule:        &workspace.ScheduleConfig{Type: workspace.ScheduleOnce},
			},
			{
				ID:          "hand-run",
				Description: "No schedule at all",
				Status:      workspace.TaskStatusCompleted,
			},
		},
	}
}

func TestFarmsHydratesEachWorkspaceRatherThanTrustingTheListing(t *testing.T) {
	source := NewWorkspaceTasks(&leanListStore{hydrated: []*workspace.Workspace{hydratedWorkspace()}})

	farms, err := source.Farms()
	if err != nil {
		t.Fatalf("farms: %v", err)
	}
	if len(farms) != 1 {
		t.Fatalf("farms = %+v, want exactly the one enabled recurring task", farms)
	}
	farm := farms[0]
	if farm.TaskID != "farm-1" {
		t.Fatalf("farm task id = %q, want farm-1", farm.TaskID)
	}
	if farm.WorkspaceID != "ws1" {
		t.Fatalf("farm workspace id = %q, want ws1", farm.WorkspaceID)
	}
	// The schedule's own name is what the user typed when they set the cadence
	// up, so it wins over the task description.
	if farm.Name != "Inbox triage" {
		t.Fatalf("farm name = %q, want the schedule name", farm.Name)
	}
	if farm.LastSummary != "12 threads triaged" {
		t.Fatalf("last summary = %q, want the most recent run's summary", farm.LastSummary)
	}
	if TierFor(farm.Schedule, time.Now()) != TierDaily {
		t.Fatalf("farm tier = %d, want Daily", TierFor(farm.Schedule, time.Now()))
	}
}

func TestCountCompletedTasksHydratesToo(t *testing.T) {
	source := NewWorkspaceTasks(&leanListStore{hydrated: []*workspace.Workspace{hydratedWorkspace()}})

	completed, err := source.CountCompletedTasks()
	if err != nil {
		t.Fatalf("count completed tasks: %v", err)
	}
	if completed != 1 {
		t.Fatalf("completed tasks = %d, want 1", completed)
	}
}

func TestTaskLooksUpOneTask(t *testing.T) {
	source := NewWorkspaceTasks(&leanListStore{hydrated: []*workspace.Workspace{hydratedWorkspace()}})

	farm, ok := source.Task("ws1", "farm-1")
	if !ok {
		t.Fatal("farm-1 was not found")
	}
	if farm.Name != "Inbox triage" {
		t.Fatalf("farm name = %q, want Inbox triage", farm.Name)
	}
	// A task that has no schedule is still returned: the price check needs to
	// know that its "before" state is not a Farm.
	handRun, ok := source.Task("ws1", "hand-run")
	if !ok {
		t.Fatal("hand-run was not found")
	}
	if IsFarm(handRun.Schedule, handRun.ScheduleEnabled) {
		t.Fatal("a task with no schedule reads as a Farm")
	}

	if _, ok := source.Task("ws1", "no-such-task"); ok {
		t.Fatal("a missing task was found")
	}
	if _, ok := source.Task("no-such-workspace", "farm-1"); ok {
		t.Fatal("a task in a missing workspace was found")
	}
}

func TestWorkspaceTasksWithoutAStoreIsInert(t *testing.T) {
	var source *WorkspaceTasks

	farms, err := source.Farms()
	if err != nil || len(farms) != 0 {
		t.Fatalf("farms = %+v, err %v; want an empty listing", farms, err)
	}
	if _, ok := source.Task("ws1", "farm-1"); ok {
		t.Fatal("a nil source found a task")
	}
	count, err := source.CountCompletedTasks()
	if err != nil || count != 0 {
		t.Fatalf("completed tasks = %d, err %v; want 0", count, err)
	}
}
