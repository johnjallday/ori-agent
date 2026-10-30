package workspacerun

import (
	"context"
	"reflect"
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

// goal-charter FR11/FR12: every origin stores its assessment and the goal's
// parts at run time; a first look's Summary still ends in its Next: line.
func TestMissionBridge_StoresTheAssessmentForEveryOrigin(t *testing.T) {
	const charterGoal = "## Outcome\nA monthly newsletter.\n## Boundaries\nNever send anything."
	const assessed = `{"summary": "A newsletter brief. Nothing written yet.",
		"assessment": "Outcome: nothing written yet.\n\nBoundaries: understood, no sending.",
		"suggested_charter": "## Outcome\nA monthly newsletter.\n## Boundaries\nNever send anything.",
		"next": "Draft the issue template",
		"items": [{"kind": "task", "title": "Draft the issue template", "why": "w", "evidence": "the brief", "priority": "high", "effort": "small"}]}`
	for name, tc := range map[string]struct {
		origin  string
		ordinal int
		goal    string
		parts   []string
	}{
		"first look": {workspace.MissionOriginHireFirstLook, 1, "help me write a newsletter", nil},
		"baseline":   {workspace.MissionOriginFirstGoal, 1, charterGoal, []string{"Outcome", "Boundaries"}},
		"cadence":    {workspace.MissionOriginCadence, 3, charterGoal, []string{"Outcome", "Boundaries"}},
	} {
		t.Run(name, func(t *testing.T) {
			wsStore := workspace.NewInMemoryStore()
			ws := makeMissionWorkspace(t, wsStore)
			if err := wsStore.Update(ws.ID, func(w *workspace.Workspace) error {
				w.Mission = tc.goal
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := bridgeAnswering(t, wsStore, assessed).TriggerMissionRunOpts(context.Background(), ws.ID, tc.ordinal,
				workspace.MissionRunOptions{Origin: tc.origin}); err != nil {
				t.Fatal(err)
			}
			fresh, _ := wsStore.Get(ws.ID)
			record := fresh.MissionRuns[0]
			if !record.Succeeded || record.Assessment != "Outcome: nothing written yet.\n\nBoundaries: understood, no sending." {
				t.Fatalf("record %+v", record)
			}
			if !reflect.DeepEqual(record.AssessmentParts, tc.parts) {
				t.Fatalf("assessment parts = %#v, want %#v", record.AssessmentParts, tc.parts)
			}
			// FR18: the bridge copies the suggested charter for every origin.
			if record.SuggestedCharter != "## Outcome\nA monthly newsletter.\n## Boundaries\nNever send anything." {
				t.Fatalf("suggested charter = %q", record.SuggestedCharter)
			}
			// ...stamped with the goal it was written for.
			if record.GoalDigest == "" || record.GoalDigest != workspace.GoalDigest(tc.goal) {
				t.Fatalf("goal digest = %q, want %q", record.GoalDigest, workspace.GoalDigest(tc.goal))
			}
			if !strings.HasSuffix(record.Summary, "\nNext: Draft the issue template") {
				t.Fatalf("the summary keeps its Next: line: %q", record.Summary)
			}
		})
	}

	// An answer without an assessment stores none and no parts.
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	if _, err := bridgeAnswering(t, wsStore, threeTasksTwoFindings).TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	if record := fresh.MissionRuns[0]; record.Assessment != "" || record.AssessmentParts != nil || record.ParseStatus != workspace.MissionParseOK || record.TasksCreated != 3 {
		t.Fatalf("old-shape record %+v", record)
	}
}
