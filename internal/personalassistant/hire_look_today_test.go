package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const hireLookNext = "List the three apps and the criteria that matter"

type hireLookFixture struct {
	records *SQLiteHireRecords
	store   workspace.Store
	ws      *workspace.Workspace
	record  HireRecord
	now     time.Time
	started []string
	looks   int
	lookErr error
}

// newHireLookFixture is a hire whose first look finished an hour ago and wrote
// its next task to the Backlog.
func newHireLookFixture(t *testing.T) *hireLookFixture {
	t.Helper()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Compare Note Apps", Agents: []string{"Rosa"}})
	if err := ws.SetEntryAgentName("Rosa"); err != nil {
		t.Fatal(err)
	}
	ws.FolderSlug = "compare-note-apps"
	ws.MissionRuns = []workspace.MissionRunRecord{{
		RunID: "run-1", Origin: workspace.MissionOriginHireFirstLook, Succeeded: true,
		StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-time.Hour),
		Summary: "You want one app. I worked from the brief.\nNext: " + hireLookNext,
		NextWhy: "Criteria come first.",
	}}
	ws.Tasks = []workspace.Task{{
		ID: "ticket-1", WorkspaceID: ws.ID, Description: hireLookNext, Status: workspace.TaskStatusBacklog,
		TicketState: workspace.TicketStateBacklog, SourceType: workspace.TicketSourceGoalSuggestion, SourceID: "run-1",
		CreatedAt: now.Add(-time.Hour),
	}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	records := newHireRecordsForTest(t)
	records.now = func() time.Time { return now.Add(-2 * time.Hour) }
	record, err := records.Create(context.Background(), HireRecord{
		ID: "hire-1", UserID: "local", RequestID: "req-1", AgentName: "Rosa", WorkspaceID: ws.ID, RunID: "run-1",
		Brief: "Compare the three note apps.", Role: "researcher", Blueprint: HireBlueprintBlank, State: HireStateLooking,
	})
	if err != nil {
		t.Fatal(err)
	}
	records.now = func() time.Time { return now }
	return &hireLookFixture{records: records, store: store, ws: ws, record: record, now: now}
}

func (f *hireLookFixture) actions() *HireLookActions {
	return &HireLookActions{
		Records: f.records, Workspaces: f.store, Now: func() time.Time { return f.now },
		Start: func(workspaceID, taskID string) error {
			f.started = append(f.started, workspaceID+"/"+taskID)
			return nil
		},
		Look: func(string) (string, error) {
			f.looks++
			if f.lookErr != nil {
				return "", f.lookErr
			}
			return "run-2", nil
		},
	}
}

func (f *hireLookFixture) items(t *testing.T, running func(string) bool) (needs, working, done []TodayItem) {
	t.Helper()
	record, err := f.records.Get(context.Background(), f.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := f.store.Get(f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	return hireTodayItems(record, ws, running, f.now)
}

func TestHireTodayItems_TheFinishedLookIsAReportUnderNeedsYou(t *testing.T) {
	f := newHireLookFixture(t)
	needs, working, done := f.items(t, nil)
	if len(needs) != 1 || len(working) != 0 || len(done) != 0 {
		t.Fatalf("needs %d working %d done %d", len(needs), len(working), len(done))
	}
	item := needs[0]
	if item.Kind != TodayKindHireLookReport || item.State != "finished" || item.Attribution != "Rosa" ||
		item.Workspace != "Compare Note Apps" || item.Detail != "You want one app. I worked from the brief." ||
		item.Ref.EntityType != "hire" || item.Ref.EntityID != "hire-1" || item.Route != "/workspaces/compare-note-apps" {
		t.Fatalf("report %+v", item)
	}
	if item.Suggestion == nil || item.Suggestion.Text != hireLookNext || item.Suggestion.Evidence != "Criteria come first." ||
		len(item.Actions) != 2 || item.Actions[0] != FirstLookActionStartThis || item.Actions[1] != FirstLookActionNotNow {
		t.Fatalf("suggestion %+v actions %v", item.Suggestion, item.Actions)
	}

	// A week later the report is no longer news.
	f.now = f.now.Add(8 * 24 * time.Hour)
	if needs, _, _ := f.items(t, nil); len(needs) != 0 {
		t.Fatalf("a report from last week is still under Needs you: %+v", needs)
	}
}

func TestHireTodayItems_TheLookUnderWorkingOnUntilItFinishesOrStops(t *testing.T) {
	f := newHireLookFixture(t)
	if _, err := f.records.Update(context.Background(), f.record.ID, func(r *HireRecord) error { r.RunID = "run-9"; return nil }); err != nil {
		t.Fatal(err)
	}
	// Started a moment ago, no record yet: it is running.
	_, working, _ := f.items(t, nil)
	if len(working) != 1 || working[0].Kind != TodayKindHireFirstLook || working[0].Title != "First look: Rosa on Compare Note Apps" {
		t.Fatalf("working %+v", working)
	}
	// Long after, with no run in flight and no record, it stopped.
	f.now = f.now.Add(time.Hour)
	needs, working, _ := f.items(t, func(string) bool { return false })
	if len(working) != 0 || len(needs) != 1 || needs[0].State != "failed" || needs[0].Detail != hireLookInterrupted ||
		len(needs[0].Actions) != 1 || needs[0].Actions[0] != HireLookActionRetry {
		t.Fatalf("interrupted look: needs %+v working %+v", needs, working)
	}
	// While the scheduler says it is in flight, it is still running.
	if _, working, _ := f.items(t, func(string) bool { return true }); len(working) != 1 {
		t.Fatalf("a look in flight is not under Working on: %+v", working)
	}
	// No run yet: it waits for the workspace to open.
	if _, err := f.records.Update(context.Background(), f.record.ID, func(r *HireRecord) error { r.RunID = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, working, _ := f.items(t, nil); len(working) != 1 || working[0].Detail != "Starts when you open the workspace" {
		t.Fatalf("waiting look %+v", working)
	}
}

func TestHireTodayItems_ALookThatDidNotStartOrFailedOffersRetry(t *testing.T) {
	f := newHireLookFixture(t)
	ws, _ := f.store.Get(f.ws.ID)
	ws.MissionRuns[0].Succeeded, ws.MissionRuns[0].Error = false, "LLM call failed"
	if err := f.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	needs, _, _ := f.items(t, nil)
	if len(needs) != 1 || needs[0].Title != "First look did not finish" || needs[0].Detail != "LLM call failed" || needs[0].Suggestion != nil {
		t.Fatalf("failed run %+v", needs)
	}
	if _, err := f.records.Update(context.Background(), f.record.ID, func(r *HireRecord) error {
		r.State, r.Failure, r.FinishedAt = HireStateFailed, "Choose a model first.", f.now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if needs, _, _ := f.items(t, nil); len(needs) != 1 || needs[0].Detail != "Choose a model first." {
		t.Fatalf("look that did not start %+v", needs)
	}
}

func TestHireLookActions_StartThisPromotesTheLooksSuggestionOnce(t *testing.T) {
	f := newHireLookFixture(t)
	actions := f.actions()
	started, err := actions.StartSuggestion(context.Background(), "local", "hire-1")
	if err != nil || started.TaskID != "ticket-1" || started.Route != "/workspaces/compare-note-apps/task/ticket-1" {
		t.Fatalf("started %+v %v", started, err)
	}
	ws, _ := f.store.Get(f.ws.ID)
	ticket := findTask(ws, "ticket-1")
	if ticket.CanonicalState() != workspace.TicketStateReady || ticket.To != "Rosa" || ticket.Context[HireTaskKey] != "hire-1" ||
		ticket.Context[workspace.TaskContextAutonomyGate] != workspace.TaskAutonomyGateWorkspace || len(ws.Tasks) != 1 {
		t.Fatalf("ticket %+v (tasks %d)", ticket, len(ws.Tasks))
	}
	if len(f.started) != 1 || f.started[0] != ws.ID+"/ticket-1" {
		t.Fatalf("started %v", f.started)
	}
	record, _ := f.records.Get(context.Background(), "hire-1")
	if record.State != HireStateStarted || record.TaskID != "ticket-1" {
		t.Fatalf("record %+v", record)
	}
	// Today follows the task now, and the report is gone.
	needs, working, _ := f.items(t, nil)
	if len(needs) != 0 || len(working) != 1 || working[0].Kind != TodayKindFirstLookSuggestion || working[0].Detail != "Started from its first look" {
		t.Fatalf("after Start this: needs %+v working %+v", needs, working)
	}

	again, err := actions.StartSuggestion(context.Background(), "local", "hire-1")
	if !errors.Is(err, ErrFirstLookActedOn) || again.TaskID != "ticket-1" || len(f.started) != 1 {
		t.Fatalf("second click %+v %v (starts %d)", again, err, len(f.started))
	}
}

func TestHireLookActions_StartThisWithoutABacklogSuggestionCreatesTheTask(t *testing.T) {
	f := newHireLookFixture(t)
	ws, _ := f.store.Get(f.ws.ID)
	ws.Tasks = nil
	if err := f.store.Save(ws); err != nil {
		t.Fatal(err)
	}
	started, err := f.actions().StartSuggestion(context.Background(), "local", "hire-1")
	if err != nil {
		t.Fatal(err)
	}
	ws, _ = f.store.Get(f.ws.ID)
	task := findTask(ws, started.TaskID)
	if task == nil || task.Description != hireLookNext || task.To != "Rosa" || task.CanonicalState() != workspace.TicketStateReady ||
		task.SourceType != workspace.TicketSourceGoalSuggestion || task.SourceID != "run-1" {
		t.Fatalf("task %+v", task)
	}
}

func TestHireLookActions_RefuseALookThatIsNotOursOrNotFinished(t *testing.T) {
	f := newHireLookFixture(t)
	if _, err := f.actions().StartSuggestion(context.Background(), "someone-else", "hire-1"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatalf("another user's hire: %v", err)
	}
	if _, err := f.actions().StartSuggestion(context.Background(), "local", "missing"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatalf("a missing hire: %v", err)
	}
	if _, err := f.records.Update(context.Background(), "hire-1", func(r *HireRecord) error { r.RunID = "run-9"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.actions().StartSuggestion(context.Background(), "local", "hire-1"); !errors.Is(err, ErrFirstLookNotFinished) || len(f.started) != 0 {
		t.Fatalf("a running look: %v", err)
	}
}

func TestHireLookActions_NotNowPutsTheSuggestionAside(t *testing.T) {
	f := newHireLookFixture(t)
	actions := f.actions()
	if err := actions.Dismiss(context.Background(), "local", "hire-1"); err != nil {
		t.Fatal(err)
	}
	record, _ := f.records.Get(context.Background(), "hire-1")
	ws, _ := f.store.Get(f.ws.ID)
	if _, remembered := ws.DismissedSuggestionKeys()[workspace.NormalizeSuggestionTitle(hireLookNext)]; record.State != HireStateDismissed || !remembered {
		t.Fatalf("record %+v remembered %v", record, remembered)
	}
	if needs, working, done := f.items(t, nil); len(needs)+len(working)+len(done) != 0 {
		t.Fatalf("a dismissed report is still on Today: %+v %+v %+v", needs, working, done)
	}
	if err := actions.Dismiss(context.Background(), "local", "hire-1"); err != nil {
		t.Fatalf("a second Not now: %v", err)
	}
	if _, err := actions.StartSuggestion(context.Background(), "local", "hire-1"); !errors.Is(err, ErrFirstLookActedOn) {
		t.Fatalf("Start this after Not now: %v", err)
	}
}

func TestHireLookActions_RetryStartsOneMoreLookOnlyForAFailedOne(t *testing.T) {
	f := newHireLookFixture(t)
	actions := f.actions()
	if _, err := actions.Retry(context.Background(), "local", "hire-1"); !errors.Is(err, ErrHireLookNotFailed) || f.looks != 0 {
		t.Fatalf("Retry on a finished look: %v (looks %d)", err, f.looks)
	}
	if _, err := f.records.Update(context.Background(), "hire-1", func(r *HireRecord) error {
		r.State, r.Failure, r.FinishedAt = HireStateFailed, "Choose a model first.", f.now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	record, err := actions.Retry(context.Background(), "local", "hire-1")
	if err != nil || f.looks != 1 || record.State != HireStateLooking || record.RunID != "run-2" || record.Failure != "" || !record.FinishedAt.IsZero() {
		t.Fatalf("retry %+v %v (looks %d)", record, err, f.looks)
	}

	// A retry that cannot start says why and stays failed.
	if _, err := f.records.Update(context.Background(), "hire-1", func(r *HireRecord) error {
		r.State, r.Failure = HireStateFailed, "x"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.lookErr = errors.New("a goal run is already in flight")
	record, err = actions.Retry(context.Background(), "local", "hire-1")
	if err != nil || record.State != HireStateFailed || record.Failure != "a goal run is already in flight" {
		t.Fatalf("blocked retry %+v %v", record, err)
	}
}

type stubHireRecords struct{ records []HireRecord }

func (s stubHireRecords) List(context.Context, string) ([]HireRecord, error) { return s.records, nil }

func TestToday_AHireReportsBeforePersonalHQExists(t *testing.T) {
	f := newHireLookFixture(t)
	service := NewTodayService(stubTodayRelationship{projection: &Projection{State: APIStateNeedsHQ}}, stubTodayBrief{}, f.store, stubTodayFollowUps{})
	service.now = func() time.Time { return f.now }
	service.SetHireRecordReader(stubHireRecords{records: []HireRecord{f.record}}, nil)
	today, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if today.State != "needs_hq" || len(today.NeedsYou.Items) != 2 ||
		today.NeedsYou.Items[0].Kind != TodayKindHireLookReport || today.NeedsYou.Items[1].Kind != "hq_setup" {
		t.Fatalf("needs you %+v", today.NeedsYou.Items)
	}
}
