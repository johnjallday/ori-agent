package workspacerun

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// bridgeAnswering builds a bridge over an existing workspace store whose goal
// run answers with result, so consecutive runs can share one workspace.
func bridgeAnswering(t *testing.T, wsStore workspace.Store, result string) *MissionBridge {
	t.Helper()
	runStore := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(&stubOriAgentTaskRunner{result: result}))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	bridge := NewMissionBridge(MissionBridgeConfig{
		RunStore: runStore, Service: service, WorkspaceStore: wsStore,
		OpportunityStore: workspace.NewOpportunityStore(wsStore),
	})
	if bridge == nil {
		t.Fatal("NewMissionBridge returned nil")
	}
	return bridge
}

const threeTasksTwoFindings = `{"items": [
	{"kind": "task", "title": "Draft a brand voice guide", "why": "Three posts drift in tone", "evidence": "notes/posts.md", "priority": "high", "confidence": "medium", "effort": "small", "suggested_agent": "brand-manager", "first_step": "Collect five on-brand posts"},
	{"kind": "task", "title": "Plan next week's posts", "why": "The calendar is empty", "evidence": "calendar.md", "priority": "medium", "effort": "medium"},
	{"kind": "task", "title": "Write alt text for the hero images", "why": "Accessibility", "evidence": "assets/", "priority": "low", "effort": "small"},
	{"kind": "finding", "title": "Brand voice drift", "why": "3 posts diverge", "evidence": "posts/*.md", "priority": "high", "recommended_action": "Review the last three posts"},
	{"kind": "finding", "title": "Logo files are outdated", "why": "Old palette", "evidence": "assets/logo-2023.svg", "priority": "medium", "recommended_action": "Replace the logo files"}
]}`

// splitOpportunities separates finding rows from goal-run summary rows.
func splitOpportunities(ws *workspace.Workspace) (findings, summaries []workspace.Opportunity) {
	for _, opp := range ws.Opportunities {
		if opp.SourceType == workspace.OpportunitySourceGoalSuggestions {
			summaries = append(summaries, opp)
		} else {
			findings = append(findings, opp)
		}
	}
	return findings, summaries
}

func suggestedTickets(ws *workspace.Workspace) []workspace.Task {
	var out []workspace.Task
	for _, task := range ws.Tasks {
		if task.SourceType == workspace.TicketSourceGoalSuggestion {
			out = append(out, task)
		}
	}
	return out
}

// SM5: three tasks and two findings become three suggested Backlog tickets
// and two opportunities; an identical second run adds no ticket and reports
// three duplicates.
func TestMissionBridge_WritesSuggestionsToTheBacklog(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)

	runID, err := bridgeAnswering(t, wsStore, threeTasksTwoFindings).TriggerMissionRun(context.Background(), ws.ID, 1)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := suggestedTickets(fresh)
	findings, summaries := splitOpportunities(fresh)
	if len(tickets) != 3 || len(findings) != 2 {
		t.Fatalf("tickets=%d findings=%d", len(tickets), len(findings))
	}
	// FR22: one summary row for the run, linking to the Backlog drawer.
	if len(summaries) != 1 {
		t.Fatalf("summary rows = %d, want 1", len(summaries))
	}
	summary := summaries[0]
	if summary.Title != "3 suggested tasks in Brand" || summary.SourceID != runID ||
		summary.SourceURL != "/workspaces/"+fresh.FolderSlug+"?panel=backlog" || !strings.Contains(summary.Evidence, "- Plan next week's posts") {
		t.Fatalf("summary row = %+v", summary)
	}
	guide := tickets[0]
	for _, ticket := range tickets {
		if ticket.Description == "Draft a brand voice guide" {
			guide = ticket
		}
		if ticket.CanonicalState() != workspace.TicketStateBacklog {
			t.Errorf("%q state = %q, want backlog", ticket.Description, ticket.CanonicalState())
		}
		if ticket.To != "" && !strings.EqualFold(ticket.To, "unassigned") {
			t.Errorf("%q must not be assigned, got %q", ticket.Description, ticket.To)
		}
		if ticket.SourceID != runID {
			t.Errorf("%q source id = %q, want the run %q", ticket.Description, ticket.SourceID, runID)
		}
	}
	for _, want := range []string{"**Why:** Three posts drift in tone", "**Evidence:** notes/posts.md", "**First step:** Collect five on-brand posts",
		"**Suggested agent:** brand-manager", "**Effort:** small", runID} {
		if !strings.Contains(guide.Details, want) {
			t.Errorf("details lack %q: %q", want, guide.Details)
		}
	}
	reason, _ := guide.Context[workspace.MissionSuggestionContextKey].(map[string]any)
	if reason["why"] != "Three posts drift in tone" || reason["run_id"] != runID {
		t.Errorf("structured reason = %#v", guide.Context)
	}
	if guide.Priority != 2 {
		t.Errorf("high priority maps to 2, got %d", guide.Priority)
	}
	first := fresh.MissionRuns[0]
	if !first.Succeeded || first.Origin != workspace.MissionOriginCadence || first.ParseStatus != workspace.MissionParseOK ||
		first.TasksCreated != 3 || first.FindingsCreated != 2 || first.DuplicatesSkipped != 0 || first.DroppedByCap != 0 {
		t.Fatalf("first run record = %+v", first)
	}

	if _, err := bridgeAnswering(t, wsStore, threeTasksTwoFindings).TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatalf("second run: %v", err)
	}
	fresh, _ = wsStore.Get(ws.ID)
	if got := len(suggestedTickets(fresh)); got != 3 {
		t.Fatalf("an identical second run must add no ticket; have %d", got)
	}
	findings, summaries = splitOpportunities(fresh)
	if len(findings) != 2 {
		t.Fatalf("recurring findings must merge; have %d", len(findings))
	}
	if len(summaries) != 1 {
		t.Fatalf("a run that suggested nothing new adds no summary row; have %d", len(summaries))
	}
	second := fresh.MissionRuns[0]
	if second.TasksCreated != 0 || second.DuplicatesSkipped != 3 || second.FindingsCreated != 0 || len(fresh.MissionRuns) != 2 {
		t.Fatalf("second run record = %+v (runs %d)", second, len(fresh.MissionRuns))
	}
}

func TestMissionBridge_CapsSuggestionsPerRun(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	var items []string
	for i := 1; i <= 7; i++ {
		items = append(items, fmt.Sprintf(`{"kind": "task", "title": "Task %d"}`, i), fmt.Sprintf(`{"kind": "finding", "title": "Finding %d"}`, i))
	}
	answer := `{"items": [` + strings.Join(items, ",") + `]}`
	if _, err := bridgeAnswering(t, wsStore, answer).TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	if got := len(suggestedTickets(fresh)); got != workspace.MissionMaxTaskSuggestions {
		t.Fatalf("tickets = %d, want %d", got, workspace.MissionMaxTaskSuggestions)
	}
	if findings, _ := splitOpportunities(fresh); len(findings) != workspace.MissionMaxFindings {
		t.Fatalf("findings = %d, want %d", len(findings), workspace.MissionMaxFindings)
	}
	if record := fresh.MissionRuns[0]; record.DroppedByCap != 4 {
		t.Fatalf("dropped by cap = %d, want 4 (2 tasks + 2 findings)", record.DroppedByCap)
	}
}

// SM7: an answer that is not JSON is a failed run, never a silent success:
// the run record keeps the raw tail and one Action Center finding says so.
func TestMissionBridge_UnreadableAnswerFailsTheRun(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	answer := "I reviewed the workspace and everything looks fine. Keep going!"

	_, err := bridgeAnswering(t, wsStore, answer).TriggerMissionRun(context.Background(), ws.ID, 1)
	if err == nil || !strings.Contains(err.Error(), "read goal run answer") {
		t.Fatalf("expected a read error, got %v", err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	if fresh.MissionExecutionCount != 1 || fresh.MissionFailureCount != 1 {
		t.Fatalf("counts: executions=%d failures=%d", fresh.MissionExecutionCount, fresh.MissionFailureCount)
	}
	record := fresh.MissionRuns[0]
	if record.Succeeded || record.ParseStatus != workspace.MissionParseFailed || record.OutputTail != answer || record.Error == "" {
		t.Fatalf("run record = %+v", record)
	}
	if len(fresh.Opportunities) != 1 || fresh.Opportunities[0].Title != unreadableAnswerFindingTitle {
		t.Fatalf("expected one failure finding, got %+v", fresh.Opportunities)
	}
	if len(suggestedTickets(fresh)) != 0 {
		t.Fatal("an unreadable answer must create no ticket")
	}

	// A second unreadable answer merges into the same finding.
	_, _ = bridgeAnswering(t, wsStore, "still not json").TriggerMissionRun(context.Background(), ws.ID, 2)
	fresh, _ = wsStore.Get(ws.ID)
	if len(fresh.Opportunities) != 1 {
		t.Fatalf("repeat failures must merge into one finding, have %d", len(fresh.Opportunities))
	}
}

// SM8: a suggestion marked Not useful is never created again, the run counts
// it as a duplicate, and the next prompt names it as dismissed.
func TestMissionBridge_NeverResuggestsADismissedTask(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	if _, err := bridgeAnswering(t, wsStore, threeTasksTwoFindings).TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	var guideID string
	for _, ticket := range suggestedTickets(fresh) {
		if ticket.Description == "Draft a brand voice guide" {
			guideID = ticket.ID
		}
	}
	if err := workspace.NewBacklogService(wsStore).DismissSuggestion(ws.ID, guideID, "We already have one"); err != nil {
		t.Fatal(err)
	}

	runner := &stubOriAgentTaskRunner{result: threeTasksTwoFindings}
	runStore := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(runner))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	bridge := NewMissionBridge(MissionBridgeConfig{RunStore: runStore, Service: service, WorkspaceStore: wsStore, OpportunityStore: workspace.NewOpportunityStore(wsStore)})
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatal(err)
	}

	fresh, _ = wsStore.Get(ws.ID)
	for _, ticket := range suggestedTickets(fresh) {
		if ticket.Description == "Draft a brand voice guide" {
			t.Fatal("a dismissed suggestion was created again")
		}
	}
	if record := fresh.MissionRuns[0]; record.TasksCreated != 0 || record.DuplicatesSkipped != 3 {
		t.Fatalf("second run record = %+v (the dismissed one and the two still open are duplicates)", record)
	}
	prompt := runner.lastTask.Description
	if !strings.Contains(prompt, "Suggestions the user dismissed as not useful (never suggest them again):\n  - Draft a brand voice guide") {
		t.Fatalf("the prompt does not name the dismissed suggestion:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[backlog, suggested] Plan next week's posts") {
		t.Fatalf("the prompt does not list open suggested work:\n%s", prompt)
	}
}

// Manual runs started through the scheduler are labeled manual.
func TestMissionBridge_ManualRunsAreLabeled(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeMissionWorkspace(t, wsStore)
	bridge := bridgeAnswering(t, wsStore, `{"items": []}`)
	scheduler := workspace.NewTaskScheduler(wsStore, workspace.SchedulerConfig{})
	scheduler.SetMissionTrigger(bridge)
	if _, err := scheduler.TriggerMissionManually(context.Background(), ws.ID); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	if got := fresh.MissionRuns[0].Origin; got != workspace.MissionOriginManual {
		t.Fatalf("origin = %q, want manual", got)
	}
}
