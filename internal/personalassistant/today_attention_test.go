package personalassistant

import (
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"testing"
)

func TestHQInputRequestsUseCanonicalTaskOwner(t *testing.T) {
	hq := &workspace.Workspace{ID: "hq", Tasks: []workspace.Task{
		{ID: "pause", Status: workspace.TaskStatusInProgress, Context: map[string]any{"execution_step_waiting": true}},
		{ID: "closed", Status: workspace.TaskStatusCompleted, Context: map[string]any{"execution_step_waiting": true}},
		{ID: "backlog", Status: workspace.TaskStatusBacklog, Context: map[string]any{"execution_step_waiting": true}},
	}}
	items := hqInputItems(hq, "/workspaces/my-hq")
	if len(items) != 1 {
		t.Fatalf("want one input request, got %+v", items)
	}
	item := items[0]
	if item.Ref.WorkspaceID != "hq" || item.Ref.EntityID != "pause" || item.Route != "/workspaces/my-hq?ticket=pause" || item.State != "waiting_for_choice" {
		t.Fatalf("lost owner: %+v", item)
	}
}

func TestTodayDeduplicatesCanonicalOwnerNotTitleOrBareID(t *testing.T) {
	a := TodayItem{ID: "same", Kind: "ticket", Title: "Respond", Ref: dailybrief.SourceRef{WorkspaceID: "a", EntityType: "task", EntityID: "same"}}
	brief := a
	brief.Kind = "brief"
	b := a
	b.Ref.WorkspaceID = "b"
	items := uniqueTodayItems([]TodayItem{a, brief, b})
	if len(items) != 2 || items[0].Ref.WorkspaceID != "a" || items[1].Ref.WorkspaceID != "b" {
		t.Fatalf("bad dedup: %+v", items)
	}
}
