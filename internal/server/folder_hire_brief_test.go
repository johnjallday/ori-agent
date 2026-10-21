package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// firstLookRecord waits for what the hire did about its agent's first look,
// which is written just after the offer resolves.
func (f *softwareCardFixture) firstLookRecord(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		outcome, _ := f.read(t)["outcome"].(map[string]any)
		if look, _ := outcome["first_look"].(map[string]any); look != nil {
			return look
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the hire recorded nothing about its first look")
	return nil
}

// hireAndStart presses Hire and start on the plan the offer shows now, with the
// brief the user typed.
func (f *softwareCardFixture) hireAndStart(t *testing.T, brief string) (int, map[string]any) {
	const requestID = "hire-1"
	t.Helper()
	digest, _ := f.read(t)["plan"].(map[string]any)["digest"].(string)
	return f.post(t, "setup", map[string]any{"request_id": requestID, "plan_digest": digest, "brief": brief})
}

// The brief typed on the hire card is the new workspace's goal, in place of the
// blueprint's, in both copies of the workspace; its first goal run is pending
// and nothing starts it.
func TestFolderHire_TheTypedBriefBecomesTheWorkspaceGoal(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	f.setModel(t)
	f.review(t)
	if status, body := f.hireAndStart(t, "Add a CI workflow\nthat runs the existing tests."); status != http.StatusOK {
		t.Fatalf("hire and start: %d %v", status, body)
	}
	if offer := f.settled(t); offer["status"] != "resolved" {
		t.Fatalf("the setup stopped: %v", offer["setup"])
	}
	linked := f.softwareWorkspaces(t)
	if len(linked) != 1 {
		t.Fatalf("workspaces = %d", len(linked))
	}
	const want = "Add a CI workflow that runs the existing tests."
	ws := linked[0]
	if ws.Mission != want || !ws.GoalFirstRunPending {
		t.Fatalf("workspace.json goal = %q pending=%v", ws.Mission, ws.GoalFirstRunPending)
	}
	row, err := f.builder.workspaceStore.Get(ws.ID)
	if err != nil || row.Mission != want {
		t.Fatalf("stored goal = %q, %v", row.Mission, err)
	}
	// The goal's first run waits for the workspace to be opened: the setup
	// leaves only its own first look.
	if len(ws.Tasks) != 1 || len(firstTasks(ws)) != 1 || len(f.provider.requests) != 0 {
		t.Fatalf("tasks = %d, model calls = %d", len(ws.Tasks), len(f.provider.requests))
	}
}

// The hire starts its agent's first look once the workspace is verified, but
// only a look that could start: on a model the assessment cannot hold to a
// tool-free call, the workspace is still made, the look is left with that
// reason and its repair, and nothing is sent to a model. The look is the
// hired agent's.
func TestFolderHire_ALookTheModelCannotRunIsLeftWithItsReason(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	f.setModel(t)
	f.review(t)
	if status, body := f.hireAndStart(t, "Find the risky parts."); status != http.StatusOK {
		t.Fatalf("hire and start: %d %v", status, body)
	}
	offer := f.settled(t)
	if offer["status"] != "resolved" {
		t.Fatalf("the setup stopped: %v", offer["setup"])
	}
	look := f.firstLookRecord(t)
	if look == nil || look["started"] != false || look["reason"] != personalassistant.FolderFirstTaskReasonModelUnsupported {
		t.Fatalf("first look record = %v", look)
	}
	first, _ := f.read(t)["first_task"].(map[string]any)
	if first == nil || first["state"] != string(personalassistant.FolderFirstTaskSeeded) || first["agent"] != "Development Assistant" ||
		first["reason"] != personalassistant.FolderFirstTaskReasonModelUnsupported || first["repair_route"] == "" {
		t.Fatalf("first_task = %v", first)
	}
	ws := f.softwareWorkspaces(t)[0]
	if tasks := firstTasks(ws); len(tasks) != 1 || tasks[0].To != "Development Assistant" || len(f.provider.requests) != 0 {
		t.Fatalf("first look = %+v, model calls = %d", tasks, len(f.provider.requests))
	}
}

// Start this, through the wired route: the finished look's suggestion becomes a
// Ready task in its workspace, assigned to the hired agent, under the
// workspace's own autonomy, and a second click makes nothing more.
func TestFolderHire_StartThisMakesTheSuggestionTheHiredAgentsTask(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	f.setModel(t)
	f.review(t)
	if status, body := f.hireAndStart(t, "Find the risky parts."); status != http.StatusOK {
		t.Fatalf("hire and start: %d %v", status, body)
	}
	if offer := f.settled(t); offer["status"] != "resolved" {
		t.Fatalf("the setup stopped: %v", offer["setup"])
	}
	f.firstLookRecord(t)
	ws := f.softwareWorkspaces(t)[0]
	look := firstTasks(ws)[0]
	// The fixture's model cannot run the assessment; its report is written here
	// as a finished run leaves it.
	finished := time.Now().Add(-time.Minute)
	if err := workspace.CanonicalUpdate(f.builder.workspaceStore, ws.ID, func(current *workspace.Workspace) error {
		for i := range current.Tasks {
			if current.Tasks[i].ID == look.ID {
				task := &current.Tasks[i]
				task.Status, task.TicketState, task.CompletedAt = workspace.TaskStatusCompleted, workspace.TicketStateReview, &finished
				task.Result = "## What it is\nWidget counts widgets.\n\n## Suggested first task\nAdd a health check endpoint.\n\nNext task: Add a health check endpoint."
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	route := "/api/personal-assistant/first-look/" + look.ID + "/start-suggestion"
	status, body := f.call(t, http.MethodPost, route, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("start this: %d %v", status, body)
	}
	created, _ := body["task"].(map[string]any)
	taskID, _ := created["task_id"].(string)
	current, err := f.builder.workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	var task *workspace.Task
	for i := range current.Tasks {
		if current.Tasks[i].ID == taskID {
			task = &current.Tasks[i]
		}
	}
	if task == nil || task.Description != "Add a health check endpoint." || task.To != "Development Assistant" ||
		task.SourceType != workspace.TicketSourceFirstLookSuggestion || task.SourceID != look.ID ||
		task.Context[workspace.TaskContextAutonomyGate] != workspace.TaskAutonomyGateWorkspace {
		t.Fatalf("task = %+v", task)
	}
	if status, again := f.call(t, http.MethodPost, route, map[string]any{}); status != http.StatusConflict || again["code"] != "already_acted" {
		t.Fatalf("second click: %d %v", status, again)
	}
	if status, gone := f.call(t, http.MethodPost, "/api/personal-assistant/first-look/not-a-look/dismiss", map[string]any{}); status != http.StatusNotFound {
		t.Fatalf("a look that is not the user's: %d %v", status, gone)
	}
}

// A cleared field takes the blueprint's goal: a workspace is never created
// without one.
func TestFolderHire_AClearedBriefCreatesTheBlueprintGoal(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	f.setModel(t)
	f.review(t)
	if status, body := f.hireAndStart(t, "   "); status != http.StatusOK {
		t.Fatalf("hire and start: %d %v", status, body)
	}
	if offer := f.settled(t); offer["status"] != "resolved" {
		t.Fatalf("the setup stopped: %v", offer["setup"])
	}
	linked := f.softwareWorkspaces(t)
	if len(linked) != 1 || linked[0].Mission != "Understand this project, identify evidence-backed risks, and propose prioritized improvements before changing code." {
		t.Fatalf("goal = %v", linked)
	}
}

func TestFolderHire_RoleLabelFallsBackToTheBlueprint(t *testing.T) {
	for role, want := range map[string]string{"": "Code project", "general": "Code project", " Producer ": "Producer"} {
		if got := hireRoleLabel(role, "Code project"); got != want {
			t.Errorf("role %q: label = %q, want %q", role, got, want)
		}
	}
}

// A software plan names the hire from what the reviewed intent will do: a new
// assistant, a saved agent of the same name, or the existing workspace's own,
// which asks no question because no workspace is created.
func TestFolderHire_SoftwareHireFollowsTheIntent(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	host := &folderSetupHost{builder: f.builder}
	row := folderdigest.CapabilityRow{Shape: folderdigest.ShapeCode, Blueprint: folderdigest.ShapeBlueprint{Label: "Code project"}}
	roster := sessionhttp.FolderOfferRoster{Role: "general", Goal: "The blueprint's goal."}
	plan := func(outcome, action, assistant, model string) personalassistant.FolderSetupPlan {
		return personalassistant.FolderSetupPlan{Intent: personalassistant.FolderSetupIntent{Software: &personalassistant.FolderSoftwareIntent{
			Outcome: outcome, AssistantAction: action, Assistant: assistant, Model: model,
		}}}
	}
	hire, brief := host.softwareHire(plan(personalassistant.FolderSoftwareCreate, personalassistant.FolderSoftwareCreate, "Development Assistant", "sonnet"), roster, row)
	if hire == nil || hire.Mode != personalassistant.FolderHireCreate || hire.AgentName != "Development Assistant" ||
		hire.Model != "sonnet" || hire.RoleLabel != "Code project" || brief != "The blueprint's goal." {
		t.Fatalf("create: %+v %q", hire, brief)
	}
	hire, _ = host.softwareHire(plan(personalassistant.FolderSoftwareCreate, personalassistant.FolderSoftwareReuse, "Pat", "sonnet"), roster, row)
	if hire == nil || hire.Mode != personalassistant.FolderHireBind || hire.AgentName != "Pat" {
		t.Fatalf("a saved agent of the same name is assigned: %+v", hire)
	}
	hire, brief = host.softwareHire(plan(personalassistant.FolderSoftwareReuse, personalassistant.FolderSoftwareKeep, "Existing Helper", ""), roster, row)
	if hire == nil || hire.Mode != personalassistant.FolderHireBind || hire.AgentName != "Existing Helper" || brief != "" {
		t.Fatalf("an existing workspace keeps its assistant and asks nothing: %+v %q", hire, brief)
	}
	if hire, brief := host.softwareHire(personalassistant.FolderSetupPlan{}, roster, row); hire != nil || brief != "" {
		t.Fatalf("no software intent, no hire: %+v %q", hire, brief)
	}
}

// While the integration's plugin is not ready its blueprint cannot be read:
// the card names the capability and the shape's brief.
func TestFolderHire_APluginNotInstalledIsAPendingHire(t *testing.T) {
	host := &folderSetupHost{}
	target := setupTarget{row: folderdigest.CapabilityRow{
		Shape: folderdigest.ShapeAudio, Blueprint: folderdigest.ShapeBlueprint{Label: "Song"},
	}}
	for _, state := range []string{personalassistant.FolderInstallInstall, personalassistant.FolderInstallEnable, personalassistant.FolderInstallUpdate} {
		hire, brief := host.pluginHire("local", target, foldersetup.Plugin{State: state})
		if hire == nil || hire.Mode != personalassistant.FolderHirePending || hire.RoleLabel != "Song" || hire.AgentName != "" ||
			brief != personalassistant.FolderDefaultBrief(folderdigest.ShapeAudio, "") {
			t.Fatalf("%s: %+v %q", state, hire, brief)
		}
	}
}
