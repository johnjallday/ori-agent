package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const assessmentReport = `## What it is
A Flask service that serves a school's timetable. It has three apps and a pytest suite. No CI runs it.

## Suggested first task
Add a CI workflow that runs the existing pytest suite on every push.
It is small, and the tests already exist under tests/.

Next task: Add a CI workflow that runs the existing pytest suite on every push.`

// reportFixture is a hire that resolved with its look started, and the
// workspace it made with that look finished.
type reportFixture struct {
	*oneCardFixture
	workspaces *workspace.InMemoryStore
	started    []string
	actions    *FirstLookActions
}

func newReportFixture(t *testing.T, result string) *reportFixture {
	t.Helper()
	f, _ := hireAndStartFixture(t, &fakeFirstLook{result: FolderFirstLookStart{Started: true, TaskID: "look-1"}})
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the look record", func() bool {
		o := f.stored(t)
		return o.Outcome != nil && o.Outcome.FirstLook != nil
	})
	finished := f.now.Add(-time.Hour)
	ws := &workspace.Workspace{ID: "quest-project", Name: "My Song", FolderSlug: "my-song", Status: workspace.StatusActive,
		AgentInstances: []workspace.AgentInstance{{Name: "Song Assistant"}},
		Tasks: []workspace.Task{{
			ID: "look-1", WorkspaceID: "quest-project", Description: "Summarize the session", To: "Song Assistant",
			Status: workspace.TaskStatusCompleted, TicketState: workspace.TicketStateReview, Result: result, CompletedAt: &finished,
			Context: map[string]any{"template_id": FolderFirstTaskTemplateID, "template_starter_task": true},
		}},
	}
	store := workspace.NewInMemoryStore()
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	r := &reportFixture{oneCardFixture: f, workspaces: store}
	r.actions = &FirstLookActions{Digest: f.service, Workspaces: store, Now: func() time.Time { return f.now },
		Start: func(workspaceID, taskID string) error {
			r.started = append(r.started, workspaceID+"/"+taskID)
			return nil
		}}
	return r
}

func (r *reportFixture) items(t *testing.T) (needs, working, done []TodayItem) {
	t.Helper()
	ws, _ := r.workspaces.Get("quest-project")
	needs, working, done, handled := firstLookReportItems(r.stored(t), ws, "/workspaces/my-song", r.now)
	if !handled {
		t.Fatal("a finished hired look is handled by its report")
	}
	return needs, working, done
}

func (r *reportFixture) look(t *testing.T) workspace.Task {
	t.Helper()
	ws, _ := r.workspaces.Get("quest-project")
	return *FindFolderFirstTask(ws)
}

// A finished hired look reports back under Needs you: who did it and where, a
// short summary, what it suggests first with its reason, and Start this beside
// Not now and the full report.
func TestFirstLookReport_AFinishedHiredLookNeedsYou(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	needs, working, done := r.items(t)
	if len(needs) != 1 || len(working) != 0 || len(done) != 0 {
		t.Fatalf("needs %d working %d done %d", len(needs), len(working), len(done))
	}
	item := needs[0]
	if item.Kind != TodayKindFirstLookReport || item.Title != "Finished its first look" || item.Attribution != "Song Assistant" ||
		item.Workspace != "My Song" || item.ReportRoute != "/workspaces/my-song/task/look-1" || !item.SourceAt.Equal(r.now.Add(-time.Hour)) {
		t.Fatalf("item = %+v", item)
	}
	if item.Detail != "A Flask service that serves a school's timetable. It has three apps and a pytest suite." {
		t.Fatalf("summary = %q", item.Detail)
	}
	if item.Suggestion == nil || item.Suggestion.Text != "Add a CI workflow that runs the existing pytest suite on every push." ||
		item.Suggestion.Evidence != "It is small, and the tests already exist under tests/." {
		t.Fatalf("suggestion = %+v", item.Suggestion)
	}
	if strings.Join(item.Actions, ",") != "start_this,not_now" {
		t.Fatalf("actions = %v", item.Actions)
	}
}

// The evidence is the agent's reason only: not the task named again (with or
// without code marks), not the closing line, and not Ori's own note on what
// the report was based on.
func TestFirstLookEvidence_IsTheReasonAlone(t *testing.T) {
	for name, tc := range map[string]struct{ result, suggestion, want string }{
		"plain": {assessmentReport, "Add a CI workflow that runs the existing pytest suite on every push.",
			"It is small, and the tests already exist under tests/."},
		"code marks and Ori's note": {"## Suggested first task\nInspect the contents of `index.js`. This shows the app's logic.\n\nNext task: Inspect the contents of index.js.\n\n---\n*What this is based on: Ori read 2 files.*",
			"Inspect the contents of index.js.", "This shows the app's logic."},
		"only the task": {"## Suggested first task\nInspect `index.js`.\n\nNext task: Inspect index.js.", "Inspect index.js.", ""},
		"no section":    {"The folder holds drafts.\n\nNext: Outline chapter four.", "Outline chapter four.", ""},
	} {
		if got := firstLookEvidence(tc.result, tc.suggestion); got != tc.want {
			t.Errorf("%s: evidence = %q, want %q", name, got, tc.want)
		}
	}
}

// Without a suggestion there is nothing to start: the report keeps Not now
// and its full report, and no suggestion box.
func TestFirstLookReport_WithoutASuggestionThereIsNoStartThis(t *testing.T) {
	r := newReportFixture(t, "The folder holds two drafts and an outline.")
	needs, _, _ := r.items(t)
	if len(needs) != 1 || needs[0].Suggestion != nil || strings.Join(needs[0].Actions, ",") != "not_now" {
		t.Fatalf("item = %+v", needs)
	}
	if _, err := r.actions.StartSuggestion(context.Background(), "local", "look-1"); !errors.Is(err, ErrFirstLookNoSuggestion) {
		t.Fatalf("start without a suggestion: %v", err)
	}
}

// Start this makes the suggested task in the look's workspace, assigned to the
// agent that made the look, under the workspace's own autonomy, and starts it.
// The report then follows that task: Working on, and Done once it finished.
func TestFirstLookReport_StartThisStartsTheSuggestionOnce(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	ctx := context.Background()
	started, err := r.actions.StartSuggestion(ctx, "local", "look-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.started) != 1 || r.started[0] != "quest-project/"+started.TaskID || started.Route != "/workspaces/my-song/task/"+started.TaskID {
		t.Fatalf("started = %+v, calls %v", started, r.started)
	}
	ws, _ := r.workspaces.Get("quest-project")
	task := findTask(ws, started.TaskID)
	if task == nil || task.Description != "Add a CI workflow that runs the existing pytest suite on every push." ||
		task.To != "Song Assistant" || task.AssignmentMode != workspace.TaskAssignmentModeManual ||
		task.SourceType != workspace.TicketSourceFirstLookSuggestion || task.SourceID != "look-1" ||
		task.CanonicalState() != workspace.TicketStateReady || task.Context[FirstLookOriginTaskKey] != "look-1" ||
		task.Context[workspace.TaskContextAutonomyGate] != workspace.TaskAutonomyGateWorkspace {
		t.Fatalf("task = %+v", task)
	}
	// A second click starts nothing more.
	again, err := r.actions.StartSuggestion(ctx, "local", "look-1")
	if !errors.Is(err, ErrFirstLookActedOn) || again.TaskID != started.TaskID || len(r.started) != 1 {
		t.Fatalf("second click: %+v %v (calls %v)", again, err, r.started)
	}
	needs, working, done := r.items(t)
	if len(needs) != 0 || len(working) != 1 || len(done) != 0 || working[0].Kind != TodayKindFirstLookSuggestion ||
		working[0].Title != task.Description || working[0].Attribution != "Song Assistant" {
		t.Fatalf("after Start this: needs %v working %+v done %v", needs, working, done)
	}
	// Once it has finished it is under Done, with its own page to open.
	finished := r.now.Add(-time.Minute)
	if err := r.workspaces.Update("quest-project", func(ws *workspace.Workspace) error {
		t := findTask(ws, started.TaskID)
		t.Status, t.TicketState, t.Result, t.CompletedAt = workspace.TaskStatusCompleted, workspace.TicketStateReview, "Added the workflow plan.", &finished
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, working, done := r.items(t); len(working) != 0 || len(done) != 1 || done[0].Route != "/workspaces/my-song/task/"+started.TaskID {
		t.Fatalf("after it finished: working %v done %+v", working, done)
	}
}

// Not now takes the report out of Today, remembers the suggestion so goal runs
// do not suggest it again, and leaves nothing to start.
func TestFirstLookReport_NotNowPutsItAside(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	ctx := context.Background()
	if err := r.actions.Dismiss(ctx, "local", "look-1"); err != nil {
		t.Fatal(err)
	}
	if needs, working, done := r.items(t); len(needs)+len(working)+len(done) != 0 {
		t.Fatalf("after Not now: %v %v %v", needs, working, done)
	}
	ws, _ := r.workspaces.Get("quest-project")
	if _, remembered := ws.DismissedSuggestionKeys()[workspace.NormalizeSuggestionTitle("Add a CI workflow that runs the existing pytest suite on every push.")]; !remembered {
		t.Fatalf("ledger = %+v", ws.DismissedSuggestions)
	}
	if _, err := r.actions.StartSuggestion(ctx, "local", "look-1"); !errors.Is(err, ErrFirstLookActedOn) || len(r.started) != 0 {
		t.Fatalf("start after Not now: %v (calls %v)", err, r.started)
	}
	if look := r.look(t); look.Result == "" {
		t.Fatal("the report itself stays readable")
	}
}

// Only the user's own hired look is found, and only once it has finished.
func TestFirstLookReport_ActsOnlyOnTheUsersFinishedHiredLook(t *testing.T) {
	r := newReportFixture(t, assessmentReport)
	ctx := context.Background()
	for _, id := range []string{"someone-elses-look", ""} {
		if _, err := r.actions.StartSuggestion(ctx, "local", id); !errors.Is(err, ErrFirstLookNotFound) {
			t.Fatalf("%q: %v", id, err)
		}
	}
	if err := r.workspaces.Update("quest-project", func(ws *workspace.Workspace) error {
		look := FindFolderFirstTask(ws)
		look.Status, look.TicketState, look.Result, look.CompletedAt = workspace.TaskStatusInProgress, workspace.TicketStateInProgress, "", nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.actions.StartSuggestion(ctx, "local", "look-1"); !errors.Is(err, ErrFirstLookNotFinished) {
		t.Fatalf("a running look: %v", err)
	}
	ws, _ := r.workspaces.Get("quest-project")
	if _, _, _, handled := firstLookReportItems(r.stored(t), ws, "/workspaces/my-song", r.now); handled {
		t.Fatal("a running look keeps its First look item, not a report")
	}
}
