package personalassistant

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assessmentTask is a first task seeded under the software assessment recipe.
func assessmentTask(change func(*workspace.Task)) *workspace.Task {
	return firstLookTask(func(task *workspace.Task) {
		task.Description, task.To = "Assess this project and suggest a first task", "Development Assistant"
		task.Context[workspace.TaskContextFirstTaskRecipe] = workspace.FirstTaskRecipeSoftwareAssessment
		if change != nil {
			change(task)
		}
	})
}

// A recipe run the server lost is not running: after a restart its ticket still
// says In Progress while start-up has put its status back to pending. It reads
// as not finished so that it can be tried again. An ordinary first look in the
// same state keeps the reading it always had.
func TestFolderFirstTaskStateOf_AnOrphanedRecipeRunDidNotFinish(t *testing.T) {
	orphan := func(task *workspace.Task) {
		task.TicketState, task.Status = workspace.TicketStateInProgress, workspace.TaskStatusPending
	}
	if got := FolderFirstTaskStateOf(assessmentTask(orphan)); got != FolderFirstTaskFailed {
		t.Fatalf("an orphaned assessment reads as %s", got)
	}
	if got := FolderFirstTaskStateOf(firstLookTask(orphan)); got != FolderFirstTaskRunning {
		t.Fatalf("an ordinary first look in that state now reads as %s", got)
	}
	// A recipe run that really is running reads as running.
	running := assessmentTask(func(task *workspace.Task) {
		task.TicketState, task.Status = workspace.TicketStateInProgress, workspace.TaskStatusInProgress
	})
	if got := FolderFirstTaskStateOf(running); got != FolderFirstTaskRunning {
		t.Fatalf("a running assessment reads as %s", got)
	}
}

// Every reason an assessment cannot start, or did not finish, has one plain
// sentence. None of them calls the assessment a "first look", promises that a
// retry will work when a repair is needed, or shows a code.
func TestFolderFirstTaskRecipeMessage_OneSentencePerReason(t *testing.T) {
	reasons := []string{
		FolderFirstTaskReasonRecipeUnavailable, FolderFirstTaskReasonUnassigned, FolderFirstTaskReasonAssistantMissing,
		FolderFirstTaskReasonNoModel, FolderFirstTaskReasonModelUnavailable, FolderFirstTaskReasonModelUnsupported,
		FolderFirstTaskReasonFolderUnavailable, FolderFirstTaskReasonWorkspaceMissing, FolderFirstTaskReasonLocalActivation,
		FolderFirstTaskReasonSetupOpen, FolderFirstTaskReasonToolRefused, FolderFirstTaskReasonContextTooSmall,
		FolderFirstTaskReasonNothingToAssess, FolderFirstTaskReasonTimedOut, FolderFirstTaskReasonProviderConfig,
		FolderFirstTaskReasonProviderQuota, FolderFirstTaskReasonNeedsInput, FolderFirstTaskReasonStartFailed,
		FolderFirstTaskReasonRunFailed, "a_reason_from_the_future",
	}
	seen := map[string]string{}
	for _, reason := range reasons {
		message := FolderFirstTaskRecipeMessage(reason, "widget", "Development Assistant")
		if message == "" || !strings.HasSuffix(message, ".") {
			t.Errorf("%s => %q", reason, message)
		}
		for _, forbidden := range []string{"first look", "_", reason} {
			if strings.Contains(message, forbidden) {
				t.Errorf("%s: the sentence contains %q: %q", reason, forbidden, message)
			}
		}
		if FolderFirstTaskRecipeBlocks(reason) && strings.Contains(message, "Try again") {
			t.Errorf("%s needs a repair but its sentence says to try again: %q", reason, message)
		}
		// An unassigned task and a missing assistant are the same thing to the user.
		sameThing := reason == FolderFirstTaskReasonAssistantMissing && seen[message] == FolderFirstTaskReasonUnassigned
		if other, dup := seen[message]; dup && reason != FolderFirstTaskReasonRunFailed && reason != "a_reason_from_the_future" && !sameThing {
			t.Errorf("%s and %s share a sentence", reason, other)
		}
		seen[message] = reason
	}
	if FolderFirstTaskRecipeMessage("", "widget", "Dev") != "" {
		t.Error("no reason has a sentence")
	}
	// The assistant and the workspace are named when they are known, and not
	// invented when they are not.
	if got := FolderFirstTaskRecipeMessage(FolderFirstTaskReasonNoModel, "widget", "Robin"); !strings.HasPrefix(got, "Robin has no model chosen.") {
		t.Errorf("no model = %q", got)
	}
	if got := FolderFirstTaskRecipeMessage(FolderFirstTaskReasonNoModel, "", ""); !strings.HasPrefix(got, "The assistant has no model chosen.") {
		t.Errorf("no model with no names = %q", got)
	}
}

// A reason is repaired on an existing page, or by trying again. Nothing points
// off the app, and a reason with no repair here gets no link.
func TestFolderFirstTaskRecipeRepair_PointsAtExistingPagesOnly(t *testing.T) {
	for reason, want := range map[string][2]string{
		FolderFirstTaskReasonNoModel:           {"/agents", "Choose a model"},
		FolderFirstTaskReasonModelUnsupported:  {"/agents", "Choose a model"},
		FolderFirstTaskReasonModelUnavailable:  {"/agents", "Choose a model"},
		FolderFirstTaskReasonContextTooSmall:   {"/agents", "Choose a model"},
		FolderFirstTaskReasonFolderUnavailable: {"/?panel=today&folder=show", "Show the folder"},
		FolderFirstTaskReasonProviderConfig:    {"/settings#api-keys", "Open AI provider settings"},
		FolderFirstTaskReasonProviderQuota:     {"/settings#api-keys", "Open AI provider settings"},
		FolderFirstTaskReasonUnassigned:        {"/workspaces/widget", "Open widget"},
		FolderFirstTaskReasonLocalActivation:   {"/workspaces/widget", "Open widget"},
		FolderFirstTaskReasonToolRefused:       {"", ""},
		FolderFirstTaskReasonTimedOut:          {"", ""},
		FolderFirstTaskReasonRunFailed:         {"", ""},
		FolderFirstTaskReasonRecipeUnavailable: {"", ""},
		"":                                     {"", ""},
	} {
		route, label := FolderFirstTaskRecipeRepair(reason, "/workspaces/widget", "widget")
		if route != want[0] || label != want[1] {
			t.Errorf("%s => %q %q, want %q %q", reason, route, label, want[0], want[1])
		}
		if route != "" && (!strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//")) {
			t.Errorf("%s points off the app: %q", reason, route)
		}
	}
	// A workspace route that is not one is not used as a link.
	if route, _ := FolderFirstTaskRecipeRepair(FolderFirstTaskReasonUnassigned, "https://elsewhere.example/x", "widget"); route != "" {
		t.Errorf("an off-app workspace route was used: %q", route)
	}
}

// The receipt row of an assessment says where it stands. One that cannot start
// never reads "Ready to start"; an ordinary first look keeps its wording.
func TestFolderFirstTaskViewDetail_AnAssessmentNeverReadsReadyWhenItIsNot(t *testing.T) {
	recipe := workspace.FirstTaskRecipeSoftwareAssessment
	for name, tc := range map[string]struct {
		view FolderFirstTaskView
		want string
	}{
		"ready":            {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskSeeded, CanStart: true}, "Starts when you press Start assessment"},
		"no model":         {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskSeeded, Reason: FolderFirstTaskReasonNoModel}, "Needs a model before it can start"},
		"wrong model":      {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskSeeded, Reason: FolderFirstTaskReasonModelUnsupported}, "Needs a different model before it can start"},
		"folder gone":      {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskSeeded, Reason: FolderFirstTaskReasonFolderUnavailable}, "Needs the project folder back before it can start"},
		"unknown reason":   {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskSeeded, Reason: "something_new"}, "Not ready to start yet"},
		"failed":           {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskFailed, CanRetry: true, Reason: FolderFirstTaskReasonTimedOut}, "Did not finish"},
		"failed, blocked":  {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskFailed, Reason: FolderFirstTaskReasonNoModel}, "Did not finish · needs a model before it can start"},
		"running":          {FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskRunning}, "Running…"},
		"an ordinary look": {FolderFirstTaskView{State: FolderFirstTaskSeeded}, "Ready to start"},
	} {
		got := FolderFirstTaskViewDetail(tc.view, "")
		if got != tc.want {
			t.Errorf("%s => %q, want %q", name, got, tc.want)
		}
		if tc.view.Recipe != "" && tc.view.State == FolderFirstTaskSeeded && !tc.view.CanStart && strings.Contains(got, "Ready") {
			t.Errorf("%s: an assessment that cannot start reads %q", name, got)
		}
	}
	// A finished assessment reads as what it found, not as the recipe's own
	// first heading; an ordinary look keeps its first line as it was.
	report := "## What it is\n\nA service for widgets (README.md).\nIt has two commands.\n\n## Built with\nGo."
	if got := FolderFirstTaskViewDetail(FolderFirstTaskView{Recipe: recipe, State: FolderFirstTaskFinished}, report); got != "Done · A service for widgets (README.md). It has two commands." {
		t.Errorf("a finished assessment's row = %q", got)
	}
	if got := FolderFirstTaskViewDetail(FolderFirstTaskView{State: FolderFirstTaskFinished}, report); got != "Done · What it is" {
		t.Errorf("an ordinary finished look's row = %q", got)
	}
	if got := FolderFirstTaskRecipeExcerpt(report); got != "A service for widgets (README.md). It has two commands." {
		t.Errorf("excerpt = %q", got)
	}
	// A report that is only headings, or has none, is not emptied.
	for report, want := range map[string]string{
		"## What it is":       "## What it is",
		"Just one paragraph.": "Just one paragraph.",
		"":                    "",
	} {
		if got := FolderFirstTaskRecipeExcerpt(report); got != want {
			t.Errorf("excerpt of %q = %q, want %q", report, got, want)
		}
	}
	if got := FolderFirstTaskRecipeExcerpt("## What it is\n" + strings.Repeat("word ", 200)); len([]rune(got)) > FolderFirstTaskExcerptMax || !strings.HasSuffix(got, "…") {
		t.Errorf("a long first paragraph is not trimmed to the card's limit: %d runes", len([]rune(got)))
	}
}

// The ticket of a software project's first assessment says what it is and that
// it changes nothing, in plain words.
func TestFolderSoftwareAssessmentTask_SaysWhatItIsAndThatNothingRuns(t *testing.T) {
	description, details := FolderSoftwareAssessmentTask()
	if description != "Assess this project and suggest a first task" {
		t.Fatalf("description = %q", description)
	}
	for _, want := range []string{"read-only", "Nothing in the project is run or changed", "declared, not as tested"} {
		if !strings.Contains(details, want) {
			t.Errorf("details lack %q", want)
		}
	}
	// The step heuristic for ordinary tasks would split a description that names
	// files or asks for a summary. This one is run by its recipe regardless, and
	// is also worded so that the heuristic would leave it whole.
	if steps := workspace.InferTaskExecutionSteps(workspace.Task{Description: description}); len(steps) != 0 {
		t.Fatalf("the assessment's description would be split into %d steps", len(steps))
	}
}
