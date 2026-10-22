package workspacerun

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const firstLookAnswer = `{
	"summary": "You want one note app. The workspace is new, so I worked from the brief.",
	"next": "List the three apps and the criteria that matter",
	"next_why": "Every comparison needs the criteria first.",
	"items": [
		{"kind": "task", "title": "List the three apps and the criteria that matter", "why": "Criteria first", "evidence": "the brief", "priority": "high", "effort": "small", "first_step": "Ask which apps"},
		{"kind": "task", "title": "Try each app for a day", "why": "Use beats reading", "evidence": "the brief", "priority": "medium", "effort": "medium"},
		{"kind": "task", "title": "Score the apps against the criteria", "why": "Makes the call", "evidence": "the brief", "priority": "medium", "effort": "small"},
		{"kind": "task", "title": "Write the recommendation", "why": "The ask", "evidence": "the brief", "priority": "medium", "effort": "small"},
		{"kind": "finding", "title": "No criteria yet", "why": "None given", "evidence": "the brief", "priority": "low", "recommended_action": "Say what matters"}
	]}`

func TestMissionBridge_HireFirstLookReadsOnlyAndReportsItsNext(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore) // its autonomy is Propose

	runner := &stubOriAgentTaskRunner{result: firstLookAnswer}
	runStore := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(runner))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	bridge := NewMissionBridge(MissionBridgeConfig{
		RunStore: runStore, Service: service, WorkspaceStore: wsStore,
		OpportunityStore: workspace.NewOpportunityStore(wsStore),
	})

	runID, err := bridge.TriggerMissionRunOpts(context.Background(), ws.ID, 1,
		workspace.MissionRunOptions{Origin: workspace.MissionOriginHireFirstLook})
	if err != nil {
		t.Fatal(err)
	}

	// The look ran under Watch with its own framing, whatever the workspace allows.
	task := runner.lastTask
	if task.Context[workspace.MissionTaskContextPolicyKey] != string(workspace.AutonomyWatch) ||
		workspace.MissionRunOriginFromContext(task.Context) != workspace.MissionOriginHireFirstLook {
		t.Fatalf("look context %#v", task.Context)
	}
	if !strings.Contains(task.Description, "FIRST LOOK") || !strings.Contains(task.Description, "do not change anything") {
		t.Fatalf("look prompt:\n%s", task.Description)
	}

	fresh, _ := wsStore.Get(ws.ID)
	tickets := suggestedTickets(fresh)
	if len(tickets) != 3 {
		t.Fatalf("%d suggested tickets, want at most 3", len(tickets))
	}
	findings, summaries := splitOpportunities(fresh)
	if len(findings) != 0 || len(summaries) != 0 {
		t.Fatalf("a first look changed the Action Center: findings=%d summary rows=%d", len(findings), len(summaries))
	}
	record := fresh.MissionRuns[0]
	if record.RunID != runID || !record.Succeeded || record.Origin != workspace.MissionOriginHireFirstLook ||
		record.TasksCreated != 3 || record.DroppedByCap != 2 {
		t.Fatalf("run record %+v", record)
	}
	if record.Summary != "You want one note app. The workspace is new, so I worked from the brief.\nNext: List the three apps and the criteria that matter" ||
		record.NextWhy != "Every comparison needs the criteria first." {
		t.Fatalf("summary %q why %q", record.Summary, record.NextWhy)
	}
}
