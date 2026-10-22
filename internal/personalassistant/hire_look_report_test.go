package personalassistant

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func hireLookWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Compare Note Apps"})
	ws.MissionRuns = []workspace.MissionRunRecord{{
		RunID: "run-1", Origin: workspace.MissionOriginHireFirstLook, Succeeded: true,
		Summary: "You want one note app. The workspace is new, so I worked from the brief.\nNext: List the three apps and the criteria that matter",
		NextWhy: "Every comparison needs the criteria first.",
	}}
	ws.Tasks = []workspace.Task{{
		ID: "t-1", Description: "List the three apps and the criteria that matter",
		SourceType: workspace.TicketSourceGoalSuggestion, SourceID: "run-1",
		Context: map[string]any{workspace.MissionSuggestionContextKey: map[string]any{"why": "Criteria come before scores."}},
	}}
	return ws
}

func TestHireLookSuggestionOf_ReadsTheNextLineAndItsWhy(t *testing.T) {
	ws := hireLookWorkspace(t)
	run := HireLookRun(ws, "run-1")
	if run == nil || HireLookRun(ws, "run-2") != nil || HireLookRun(ws, "") != nil {
		t.Fatalf("run lookup: %+v", run)
	}
	got := HireLookSuggestionOf(ws, run)
	if got == nil || got.Text != "List the three apps and the criteria that matter" || got.Evidence != "Every comparison needs the criteria first." {
		t.Fatalf("suggestion %+v", got)
	}
	if summary := HireLookSummary(run); summary != "You want one note app. The workspace is new, so I worked from the brief." {
		t.Fatalf("summary %q", summary)
	}

	// Without its own why, the look's Backlog suggestion gives one.
	run.NextWhy = ""
	if got := HireLookSuggestionOf(ws, run); got == nil || got.Evidence != "Criteria come before scores." {
		t.Fatalf("why from the ticket: %+v", got)
	}
	if ticket := HireLookSuggestionTicket(ws, run, "list the three apps and the criteria that matter"); ticket == nil || ticket.ID != "t-1" {
		t.Fatalf("ticket %+v", ticket)
	}
}

func TestHireLookSuggestionOf_NothingForAFailedRunADismissedOneOrNoNextLine(t *testing.T) {
	ws := hireLookWorkspace(t)
	failed := ws.MissionRuns[0]
	failed.Succeeded = false
	if HireLookSuggestionOf(ws, &failed) != nil {
		t.Fatal("a failed look suggested something")
	}
	quiet := ws.MissionRuns[0]
	quiet.Summary = "Nothing to suggest yet."
	if HireLookSuggestionOf(ws, &quiet) != nil {
		t.Fatal("a look with no Next line suggested something")
	}
	ws.RememberDismissedSuggestion("List the three apps and the criteria that matter", "Not now", "run-1", time.Now())
	if HireLookSuggestionOf(ws, &ws.MissionRuns[0]) != nil {
		t.Fatal("a dismissed suggestion came back")
	}
}
