package agenthttp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// spyOpportunityStore records MarkSeen calls so the test can prove the home tool
// is read-only (unlike the Action Center Get handler, which marks seen).
type spyOpportunityStore struct {
	opps          map[string][]workspace.Opportunity
	markSeenCalls int
}

func (s *spyOpportunityStore) List(workspaceID string) ([]workspace.Opportunity, error) {
	return s.opps[workspaceID], nil
}
func (s *spyOpportunityStore) Get(workspaceID, opportunityID string) (workspace.Opportunity, error) {
	return workspace.Opportunity{}, nil
}
func (s *spyOpportunityStore) Upsert(opp workspace.Opportunity) (workspace.Opportunity, bool, error) {
	return opp, false, nil
}
func (s *spyOpportunityStore) Delete(workspaceID, opportunityID string) error { return nil }
func (s *spyOpportunityStore) MarkSeen(workspaceID, opportunityID string) error {
	s.markSeenCalls++
	return nil
}
func (s *spyOpportunityStore) Dismiss(workspaceID, opportunityID string, reason workspace.DismissalReason) error {
	return nil
}
func (s *spyOpportunityStore) Snooze(workspaceID, opportunityID string, until time.Time) error {
	return nil
}
func (s *spyOpportunityStore) MarkResolved(workspaceID, opportunityID string) error { return nil }

func TestHomeOpportunitiesToolIsReadOnly(t *testing.T) {
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Alpha", nil)
	spy := &spyOpportunityStore{opps: map[string][]workspace.Opportunity{
		"ws-1": {{ID: "o1", WorkspaceID: "ws-1", Title: "Fix brand voice", Status: workspace.OpportunityNew}},
	}}
	reg := newHomeToolRegistry(HomeSnapshotSources{Workspaces: store, Opportunities: spy})

	out, err := reg.Execute(context.Background(), "home_opportunities", "{}")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if spy.markSeenCalls != 0 {
		t.Errorf("home_opportunities marked %d opportunities seen; must be read-only (0)", spy.markSeenCalls)
	}
	var parsed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Total != 1 {
		t.Errorf("total = %d, want 1", parsed.Total)
	}
}

func TestHomeTasksToolStatusFilter(t *testing.T) {
	store := workspace.NewInMemoryStore()
	now := time.Now()
	makeTestWorkspace(t, store, "ws-1", "Alpha", []workspace.Task{
		{Description: "a", Status: workspace.TaskStatusInProgress, CreatedAt: now},
		{Description: "b", Status: workspace.TaskStatusCompleted, CreatedAt: now, CompletedAt: timePtr(now)},
	})
	reg := newHomeToolRegistry(HomeSnapshotSources{Workspaces: store})

	out, err := reg.Execute(context.Background(), "home_tasks", `{"status":"completed"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var parsed struct {
		Total int `json:"total"`
		Tasks []struct {
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Total != 1 || len(parsed.Tasks) != 1 || parsed.Tasks[0].Status != "completed" {
		t.Errorf("status filter failed: %+v", parsed)
	}
}

func TestHomeToolRegistryHasReadOnlyToolsOnly(t *testing.T) {
	reg := newHomeToolRegistry(HomeSnapshotSources{})
	for _, def := range reg.Definitions() {
		if !reg.Has(def.Name) {
			t.Errorf("definition %q not recognized by Has()", def.Name)
		}
	}
	if reg.Has("home_delete_workspace") {
		t.Error("registry must not expose any write tool")
	}
}
