package personalassistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// todayWithFirstLook builds Today over one set-up folder workspace whose first
// look is task, and returns what Today says.
func todayWithFirstLook(t *testing.T, now time.Time, task workspace.Task, ws func(*workspace.Workspace)) *TodayProjection {
	t.Helper()
	store, _ := newTodayWorkspace(t, now)
	folder := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Thesis"})
	folder.ID, folder.FolderSlug = "project-1", "thesis"
	task.WorkspaceID = folder.ID
	folder.Tasks = append(folder.Tasks, task)
	if ws != nil {
		ws(folder)
	}
	if err := store.Save(folder); err != nil {
		t.Fatal(err)
	}
	setUp := now.Add(-2 * time.Hour)
	receipts := &stubFolderReceipts{offers: []FolderOffer{{
		ID: "project", ResolvedAt: &setUp, Subject: FolderCandidateRecord{Name: "thesis-draft"},
		Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "project-1"},
	}}}
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.SetFolderReceiptReader(receipts)
	service.now = func() time.Time { return now }
	got, err := service.Get(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func firstLook(change func(*workspace.Task)) workspace.Task {
	task := workspace.Task{
		ID: "look-1", Description: "Summarize the current draft", To: "Writing Coach",
		Status: workspace.TaskStatusAssigned, TicketState: workspace.TicketStateReady,
		Context: map[string]any{"template_id": FolderFirstTaskTemplateID, "template_starter_task": true},
	}
	change(&task)
	return task
}

func todayKinds(items []TodayItem) []string {
	kinds := make([]string, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.Kind)
	}
	return kinds
}

func findKind(items []TodayItem, kind string) *TodayItem {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

// While the agent looks, Working on says so and links the workspace; nothing
// is listed as done yet (FR20).
func TestToday_FirstLookRunningIsWorkingOn(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, status := range []workspace.TaskStatus{workspace.TaskStatusInProgress, workspace.TaskStatusWaitingForChoice} {
		got := todayWithFirstLook(t, now, firstLook(func(task *workspace.Task) {
			task.Status, task.TicketState = status, workspace.TicketStateInProgress
		}), nil)
		line := findKind(got.WorkingOn.Items, "folder_first_look")
		if line == nil || line.Title != "First look at thesis-draft" || line.Route != "/workspaces/thesis" || line.Attribution != "Writing Coach" {
			t.Fatalf("%s: working on = %+v", status, got.WorkingOn.Items)
		}
		if findKind(got.Done.Items, "folder_result") != nil {
			t.Fatalf("%s: a look still going was listed as done: %+v", status, got.Done.Items)
		}
	}
}

// Once it has finished the line is gone and the result is under Done, first,
// with the server's excerpt and a link to the ticket (FR19).
func TestToday_FirstLookFinishedIsADoneResult(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	done := now.Add(-30 * time.Minute)
	got := todayWithFirstLook(t, now, firstLook(func(task *workspace.Task) {
		task.Status, task.TicketState = workspace.TaskStatusCompleted, workspace.TicketStateReview
		task.CompletedAt = &done
		task.Result = "Three drafts.\n\n" + strings.Repeat("The newest is chapter four. ", 30)
	}), nil)

	if findKind(got.WorkingOn.Items, "folder_first_look") != nil {
		t.Fatalf("a finished look is still under Working on: %+v", got.WorkingOn.Items)
	}
	result := findKind(got.Done.Items, "folder_result")
	if result == nil {
		t.Fatalf("done = %v", todayKinds(got.Done.Items))
	}
	// The result is the news: it comes before the setup receipt of the same folder.
	kinds := todayKinds(got.Done.Items)
	resultAt, setupAt := -1, -1
	for i, kind := range kinds {
		if kind == "folder_result" {
			resultAt = i
		}
		if kind == "folder_setup" {
			setupAt = i
		}
	}
	if setupAt >= 0 && resultAt > setupAt {
		t.Fatalf("the setup receipt came before the result: %v", kinds)
	}
	if result.Title != "Summarize the current draft" || result.Attribution != "Writing Coach" ||
		result.Route != "/workspaces/thesis?ticket=look-1" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(result.Detail, "Three drafts. The newest is chapter four.") ||
		strings.Contains(result.Detail, "\n") || len([]rune(result.Detail)) > FolderFirstTaskExcerptMax {
		t.Fatalf("detail = %q", result.Detail)
	}
	if !result.SourceAt.Equal(done) || len(got.Done.Items) > todayResultCap {
		t.Fatalf("source %v, %d done items", result.SourceAt, len(got.Done.Items))
	}
	// It applies to every folder shape, not only music projects: the offer here
	// is a plain writing folder.
	if got.Studio != nil {
		t.Fatalf("a folder result needed a studio: %+v", got.Studio)
	}
}

// The result stays for seven days and then leaves Done.
func TestToday_FirstLookResultExpiresAfterSevenDays(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for name, age := range map[string]time.Duration{"six days": 6 * 24 * time.Hour, "eight days": 8 * 24 * time.Hour} {
		finished := now.Add(-age)
		got := todayWithFirstLook(t, now, firstLook(func(task *workspace.Task) {
			task.Status, task.TicketState = workspace.TaskStatusCompleted, workspace.TicketStateDone
			task.CompletedAt, task.Result = &finished, "Three drafts."
		}), nil)
		listed := findKind(got.Done.Items, "folder_result") != nil
		if listed != (age < 7*24*time.Hour) {
			t.Fatalf("%s old: listed = %t", name, listed)
		}
	}
}

// A look that has not run, did not finish, or was put away says nothing here:
// the Home card carries its button.
func TestToday_FirstLookThatIsNotNewsIsNotListed(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cases := map[string]func(*workspace.Task){
		"seeded": func(*workspace.Task) {},
		"failed": func(task *workspace.Task) {
			task.Status, task.TicketState, task.Error = workspace.TaskStatusFailed, workspace.TicketStateInProgress, "boom"
		},
		"cancelled": func(task *workspace.Task) {
			task.Status, task.TicketState = workspace.TaskStatusCancelled, workspace.TicketStateCancelled
		},
		"empty": func(task *workspace.Task) {
			task.Status, task.TicketState, task.Result = workspace.TaskStatusCompleted, workspace.TicketStateReview, " "
		},
	}
	for name, change := range cases {
		got := todayWithFirstLook(t, now, firstLook(change), nil)
		if findKind(got.WorkingOn.Items, "folder_first_look") != nil || findKind(got.Done.Items, "folder_result") != nil {
			t.Fatalf("%s: working=%v done=%v", name, todayKinds(got.WorkingOn.Items), todayKinds(got.Done.Items))
		}
		// The folder itself is still a Working on row.
		if findKind(got.WorkingOn.Items, "folder_workspace") == nil {
			t.Fatalf("%s: the folder workspace row went missing", name)
		}
	}
}

// Another blueprint's starter task finishing is not a first look.
func TestToday_OnlyTheFolderFirstLookIsListed(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	done := now.Add(-time.Minute)
	got := todayWithFirstLook(t, now, firstLook(func(task *workspace.Task) {
		task.Context["template_id"] = "writing-project"
		task.Status, task.TicketState, task.CompletedAt, task.Result = workspace.TaskStatusCompleted, workspace.TicketStateReview, &done, "Done."
	}), nil)
	if findKind(got.Done.Items, "folder_result") != nil || findKind(got.WorkingOn.Items, "folder_first_look") != nil {
		t.Fatalf("a blueprint task was listed as a first look: %v / %v", todayKinds(got.WorkingOn.Items), todayKinds(got.Done.Items))
	}
}
