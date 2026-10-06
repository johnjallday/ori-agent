package personalassistant

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// firstLookTask is a seeded first look, as the starter-task seeding leaves it.
func firstLookTask(change func(*workspace.Task)) *workspace.Task {
	task := &workspace.Task{
		ID: "task-1", Description: "Tell me what is in this folder", To: "Researcher",
		Status: workspace.TaskStatusAssigned, TicketState: workspace.TicketStateReady,
		Context: map[string]any{"template_id": FolderFirstTaskTemplateID, "template_starter_task": true},
	}
	if change != nil {
		change(task)
	}
	return task
}

func TestIsFolderFirstTask_NeedsBothTheTemplateAndTheStarterMark(t *testing.T) {
	cases := []struct {
		name    string
		context map[string]any
		want    bool
	}{
		{"the seeded first look", map[string]any{"template_id": "folder-digest", "template_starter_task": true}, true},
		{"another blueprint's starter task", map[string]any{"template_id": "writing-project", "template_starter_task": true}, false},
		{"a folder-digest task that is not the starter", map[string]any{"template_id": "folder-digest"}, false},
		{"a starter mark that is not a real true", map[string]any{"template_id": "folder-digest", "template_starter_task": "true"}, false},
		{"no context", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFolderFirstTask(&workspace.Task{Context: tc.context}); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
	if IsFolderFirstTask(nil) {
		t.Fatal("a nil task is not a first look")
	}
}

func TestFindFolderFirstTask_ReturnsTheWorkspacesOwnTask(t *testing.T) {
	ws := &workspace.Workspace{Tasks: []workspace.Task{
		{ID: "setup", Context: map[string]any{"template_id": "writing-project", "template_starter_task": true}},
		*firstLookTask(nil),
	}}
	found := FindFolderFirstTask(ws)
	if found == nil || found.ID != "task-1" {
		t.Fatalf("found = %+v", found)
	}
	// The pointer is into the workspace, so a caller can read live fields.
	ws.Tasks[1].Result = "done"
	if found.Result != "done" {
		t.Fatal("the returned task is a copy")
	}
	if FindFolderFirstTask(&workspace.Workspace{}) != nil || FindFolderFirstTask(nil) != nil {
		t.Fatal("a workspace with no first look found one")
	}
}

// A run's outcome and the ticket's state are different things: success sends
// the ticket to Review with a result, while a failure leaves it In Progress
// with an error. The first look's state has to read both.
func TestFolderFirstTaskStateOf(t *testing.T) {
	inProgress := func(task *workspace.Task) {
		task.Status, task.TicketState = workspace.TaskStatusInProgress, workspace.TicketStateInProgress
	}
	cases := []struct {
		name   string
		task   *workspace.Task
		want   FolderFirstTaskState
		reason string
	}{
		{"seeded", firstLookTask(nil), FolderFirstTaskSeeded, ""},
		{"seeded and unassigned", firstLookTask(func(task *workspace.Task) {
			task.To, task.Status = "", workspace.TaskStatusPending
		}), FolderFirstTaskSeeded, ""},
		{"running", firstLookTask(inProgress), FolderFirstTaskRunning, ""},
		// A run that paused to ask something is not still working: left as
		// "running" it would read "Working on…" for as long as nobody answers.
		{"paused to ask the user", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState = workspace.TaskStatusWaitingForChoice, workspace.TicketStateInProgress
		}), FolderFirstTaskWaiting, ""},
		{"finished, in review", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState, task.Result = workspace.TaskStatusCompleted, workspace.TicketStateReview, "Three drafts."
		}), FolderFirstTaskFinished, ""},
		{"finished and accepted", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState, task.Result = workspace.TaskStatusCompleted, workspace.TicketStateDone, "Three drafts."
		}), FolderFirstTaskFinished, ""},
		{"finished with nothing to read", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState, task.Result = workspace.TaskStatusCompleted, workspace.TicketStateReview, "  \n"
		}), FolderFirstTaskFailed, FolderFirstTaskReasonRunFailed},
		{"run failed", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState, task.Error = workspace.TaskStatusFailed, workspace.TicketStateInProgress, "provider error"
		}), FolderFirstTaskFailed, FolderFirstTaskReasonRunFailed},
		{"run timed out", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState = workspace.TaskStatusTimeout, workspace.TicketStateInProgress
		}), FolderFirstTaskFailed, FolderFirstTaskReasonRunFailed},
		{"the one start was spent and nothing ran", firstLookTask(func(task *workspace.Task) {
			task.Context[FolderFirstTaskConsumedKey] = "2026-10-06T00:00:00Z"
		}), FolderFirstTaskFailed, FolderFirstTaskReasonStartFailed},
		{"cancelled by the user", firstLookTask(func(task *workspace.Task) {
			task.Status, task.TicketState = workspace.TaskStatusCancelled, workspace.TicketStateCancelled
		}), FolderFirstTaskNone, ""},
		{"a legacy record with no canonical state", firstLookTask(func(task *workspace.Task) {
			task.TicketState, task.Status, task.Result = "", workspace.TaskStatusCompleted, "Three drafts."
		}), FolderFirstTaskFinished, ""},
		{"not a first look at all", &workspace.Task{ID: "other", Status: workspace.TaskStatusCompleted, Result: "x"}, FolderFirstTaskNone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FolderFirstTaskStateOf(tc.task)
			if got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
			if got == FolderFirstTaskFailed {
				if reason := FolderFirstTaskFailureReason(tc.task); reason != tc.reason {
					t.Fatalf("failure reason = %q, want %q", reason, tc.reason)
				}
			}
			if FolderFirstTaskFinishedWithResult(tc.task) != (tc.want == FolderFirstTaskFinished) {
				t.Fatalf("FinishedWithResult disagrees with state %q", got)
			}
		})
	}
	if FolderFirstTaskStateOf(nil) != FolderFirstTaskNone {
		t.Fatal("a nil task has a state")
	}
}

func TestFolderFirstTaskExcerpt_OneLineWithinTheLimit(t *testing.T) {
	if got := FolderFirstTaskExcerpt("  Three drafts.\n\n- chapter one\n\t- chapter two  "); got != "Three drafts. - chapter one - chapter two" {
		t.Fatalf("excerpt = %q", got)
	}
	if got := FolderFirstTaskExcerpt(""); got != "" {
		t.Fatalf("empty result excerpt = %q", got)
	}

	exact := strings.Repeat("a", FolderFirstTaskExcerptMax)
	if got := FolderFirstTaskExcerpt(exact); got != exact {
		t.Fatalf("a result at the limit was changed (%d runes)", utf8.RuneCountInString(got))
	}

	// Multi-byte text is cut on a rune, never inside one, and still fits.
	long := strings.Repeat("résumé ", 80)
	got := FolderFirstTaskExcerpt(long)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > FolderFirstTaskExcerptMax {
		t.Fatalf("excerpt is %d runes, valid=%t", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	if !strings.HasSuffix(got, "…") || strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("a trimmed excerpt must end in an ellipsis on one line: %q", got)
	}
}

func TestFolderFirstTaskMessage_OneSentencePerReason(t *testing.T) {
	want := map[string]string{
		FolderFirstTaskReasonSetupOpen:       "Open Thesis to finish its setup first.",
		FolderFirstTaskReasonUnassigned:      "The first task has no agent yet. Open Thesis to assign one.",
		FolderFirstTaskReasonNoModel:         "Add a model in Settings to run the first look.",
		FolderFirstTaskReasonLocalActivation: "Open Thesis and activate it on this computer.",
		FolderFirstTaskReasonNeedsInput:      "The first look is waiting for your answer. Open Thesis to continue.",
		FolderFirstTaskReasonStartFailed:     "The first look could not start. Try again.",
		FolderFirstTaskReasonRunFailed:       "The first look did not finish. Try again.",
	}
	for reason, sentence := range want {
		if got := FolderFirstTaskMessage(reason, " Thesis "); got != sentence {
			t.Errorf("%s = %q, want %q", reason, got, sentence)
		}
	}
	if got := FolderFirstTaskMessage("", "Thesis"); got != "" {
		t.Fatalf("no reason said %q", got)
	}
	if got := FolderFirstTaskMessage("something_new", "Thesis"); got != "" {
		t.Fatalf("an unknown reason guessed %q", got)
	}
	if got := FolderFirstTaskMessage(FolderFirstTaskReasonSetupOpen, ""); got != "Open the workspace to finish its setup first." {
		t.Fatalf("a missing workspace name = %q", got)
	}
}
