package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/testutil/softwarefixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

func (f *softwareCardFixture) setModel(t *testing.T) {
	t.Helper()
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
}

// setUp presses Set up on the plan the offer shows now.
func (f *softwareCardFixture) setUp(t *testing.T, requestID string) (int, map[string]any) {
	t.Helper()
	plan, _ := f.read(t)["plan"].(map[string]any)
	digest := ""
	if plan != nil {
		digest, _ = plan["digest"].(string)
	}
	if setup, _ := f.read(t)["setup"].(map[string]any); setup != nil {
		digest, _ = setup["plan_digest"].(string)
	}
	return f.post(t, "setup", map[string]any{"request_id": requestID, "plan_digest": digest})
}

// settled waits for the run to end: the offer resolved, or the run stopped.
func (f *softwareCardFixture) settled(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		offer := f.read(t)
		if offer["status"] == "resolved" {
			return offer
		}
		if setup, _ := offer["setup"].(map[string]any); setup != nil && setup["status"] == "stopped" && setup["stop_reason"] != "interrupted" {
			return offer
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the setup did not settle: %v", f.read(t))
	return nil
}

// softwareWorkspaces are the user's workspaces linked to the fixture folder.
func (f *softwareCardFixture) softwareWorkspaces(t *testing.T) []*workspace.Workspace {
	t.Helper()
	var found []*workspace.Workspace
	for id := range f.builder.workspaceFileStore.CachedWorkspaces() {
		// The cache holds summaries; the whole record is read for its tasks.
		ws, err := f.builder.workspaceFileStore.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		if ref, err := ws.GetDirectoryReference(primary); primary != "" && err == nil && ref != nil && ref.Path == f.dir {
			found = append(found, ws)
		}
	}
	return found
}

func firstTasks(ws *workspace.Workspace) []workspace.Task {
	var tasks []workspace.Task
	for i := range ws.Tasks {
		if personalassistant.IsFolderFirstTask(&ws.Tasks[i]) {
			tasks = append(tasks, ws.Tasks[i])
		}
	}
	return tasks
}

func receiptRow(offer map[string]any, kind string) map[string]any {
	outcome, _ := offer["outcome"].(map[string]any)
	if outcome == nil {
		return nil
	}
	rows, _ := outcome["receipt"].([]any)
	for _, row := range rows {
		if item := row.(map[string]any); item["kind"] == kind {
			return item
		}
	}
	return nil
}

// The reviewed plan, carried out by the production host: one workspace over
// the folder where it is, the assistant, the planning-first workflow, and one
// assessment that waits for the user. A second click makes nothing more, and
// the project folder is byte-for-byte what it was.
func TestSoftwareSetup_CarriesOutTheReviewedPlanExactlyOnce(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	f.setModel(t)
	before, err := softwarefixture.Hash(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	workspacesBefore := len(f.builder.workspaceFileStore.CachedWorkspaces())
	f.review(t)

	if status, body := f.setUp(t, "setup-1"); status != http.StatusOK {
		t.Fatalf("set up: %d %v", status, body)
	}
	offer := f.settled(t)
	if offer["status"] != "resolved" {
		t.Fatalf("the setup stopped: %v", offer["setup"])
	}

	linked := f.softwareWorkspaces(t)
	if len(linked) != 1 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspacesBefore+1 {
		t.Fatalf("workspaces linked to the folder = %d", len(linked))
	}
	ws := linked[0]
	if ws.Name != "widget" || ws.SharedData[projecttemplates.FolderOfferIDKey] != f.offer {
		t.Fatalf("workspace = %q made for %v", ws.Name, ws.SharedData[projecttemplates.FolderOfferIDKey])
	}
	// The folder is recorded as the confirmed directory, and it is the code root.
	binding, err := workspace.GetSoftwareProjectBinding(ws.SharedData)
	if err != nil || binding == nil || binding.DirectoryIdentity != directoryWitness(t, f.dir) || binding.Recipe != "software_project" ||
		binding.Remote != "example.invalid/acme/widget.git" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	if got := f.builder.workspacePlanPolicy.CodeFolder(ws.ID); got != f.dir {
		t.Fatalf("code root = %q, want the linked project %q", got, f.dir)
	}
	if got := f.builder.workspacePlanArtifactRoot(ws.ID, "tasks/prd-example.md"); got != f.dir {
		t.Fatalf("planning documents would go to %q, want the linked project", got)
	}
	// The session row carries the same record, so a later save from it cannot
	// drop the binding from workspace.json.
	row, err := f.builder.sessionStore.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mirrored, err := workspace.GetSoftwareProjectBinding(row.SharedData); err != nil || mirrored == nil || mirrored.DirectoryID != binding.DirectoryID {
		t.Fatalf("session row binding = %+v, %v", mirrored, err)
	}

	// One development assistant, on the model that was reviewed.
	if entry := ws.EntryAgentName(); entry != "Development Assistant" || len(ws.AgentInstances) != 1 {
		t.Fatalf("entry agent = %q of %d", entry, len(ws.AgentInstances))
	}
	if provider, model, found := f.builder.sessionHandler.SavedAgentModel("Development Assistant"); !found || provider != "claude_code" || model != "sonnet" {
		t.Fatalf("assistant model = %q %q %v", provider, model, found)
	}

	// The effective workflow, not its label: planning first, nothing starts on
	// its own, destructive actions confirm, and no document goes into the project.
	settings := workspacesettings.Extract(ws.SharedData)
	policy := workspacesettings.BuildEffectivePolicy(settings, workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true})
	confirm, _ := policy.Control(workspacesettings.ControlDestructiveConfirm)
	artifacts, _ := policy.Control(workspacesettings.ControlArtifactWrite)
	if policy.Profile != "software_project" || !policy.PlanningEnabled || settings.Planning.DefaultExecutionMode != "step_through" ||
		!confirm.Active() || artifacts.Enabled || policy.Artifacts.WritePRD || policy.Artifacts.WriteTaskList {
		t.Fatalf("effective workflow = %+v / %+v", settings, policy)
	}
	// Nothing is scheduled, and no tool server is pointed at the project: the one
	// binding every workspace has reaches Ori's own folder for the workspace.
	if len(ws.ScheduledTasks) != 0 || len(ws.MCPBindings) != 1 || ws.MCPBindings[0].Alias != "workspace-files" {
		t.Fatalf("setup scheduled work or bound tools: %d scheduled, MCP %s", len(ws.ScheduledTasks), mustJSON(t, ws.MCPBindings))
	}
	if bound := mustJSON(t, ws.MCPBindings); strings.Contains(bound, f.dir) || strings.Contains(bound, filepath.Dir(f.dir)+`"`) {
		t.Fatalf("a tool server was given the project folder: %s", bound)
	}

	// One assessment under the host recipe, waiting, and not startable as an
	// ordinary agent task.
	tasks := firstTasks(ws)
	if len(tasks) != 1 || len(ws.Tasks) != 1 {
		t.Fatalf("tasks = %d first of %d", len(tasks), len(ws.Tasks))
	}
	task := tasks[0]
	if workspace.FirstTaskRecipe(&task) != workspace.FirstTaskRecipeSoftwareAssessment || task.Status != workspace.TaskStatusPending ||
		task.To != "Development Assistant" || task.StartedAt != nil {
		t.Fatalf("assessment = %+v", task)
	}
	status, started := f.call(t, http.MethodPost, "/api/workspaces/"+ws.ID+"/folder-first-task/start", nil)
	if status != http.StatusOK || started["started"] != false || started["reason"] != "assessment_unavailable" {
		t.Fatalf("starting the assessment: %d %v", status, started)
	}
	if after, _ := f.builder.workspaceFileStore.Get(ws.ID); firstTasks(after)[0].Status != workspace.TaskStatusPending ||
		firstTasks(after)[0].Context[personalassistant.FolderFirstTaskConsumedKey] != nil {
		t.Fatal("a refused start spent the assessment's one start")
	}

	// The receipt is what was saved.
	if offer["outcome"].(map[string]any)["workspace_id"] != ws.ID || offer["outcome"].(map[string]any)["existing"] == true {
		t.Fatalf("outcome = %v", offer["outcome"])
	}
	for kind, want := range map[string][2]string{
		"workspace": {"widget", "created"},
		"folder":    {"widget", "linked where it is, read-only"},
		"blueprint": {"Code Project", "planning first"},
		"agent":     {"Development Assistant", "development assistant, added"},
		"task":      {"Assess this project and suggest a first task", "Cannot start here yet"},
	} {
		row := receiptRow(offer, kind)
		if row == nil || row["name"] != want[0] || !strings.Contains(row["detail"].(string), want[1]) {
			t.Errorf("receipt %s = %v, want %q with %q", kind, row, want[0], want[1])
		}
	}
	// The card shows the task's row as it stands now, not as it was stored. An
	// assessment that cannot start is never shown as ready.
	live, _ := offer["first_task"].(map[string]any)
	if live == nil || live["state"] != "seeded" || live["can_start"] != false || live["recipe"] != workspace.FirstTaskRecipeSoftwareAssessment ||
		live["detail"] != "Cannot start here yet" || strings.Contains(strings.ToLower(mustJSON(t, live)), "ready to start") {
		t.Fatalf("live first task = %v", live)
	}
	if encoded := mustJSON(t, offer); strings.Contains(encoded, os.Getenv("HOME")) {
		t.Fatal("the resolved offer carries a filesystem path")
	}

	// A second click, the same or a new one, makes nothing more.
	for _, request := range []string{"setup-1", "setup-2"} {
		if status, _ := f.setUp(t, request); status == http.StatusOK && request == "setup-2" {
			t.Fatal("a resolved offer accepted another Set up")
		}
	}
	if again := f.softwareWorkspaces(t); len(again) != 1 || len(firstTasks(again[0])) != 1 ||
		len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspacesBefore+1 {
		t.Fatal("a repeated click made a second workspace or assessment")
	}
	if names := f.agentNames(t); strings.Count(names, "Development Assistant") != 1 {
		t.Fatalf("agents = %s", names)
	}
	if len(f.provider.requests) != 0 {
		t.Fatalf("setup made %d model calls", len(f.provider.requests))
	}
	if after, err := softwarefixture.Hash(f.dir); err != nil || after != before {
		t.Fatalf("setup changed the project folder: %v", err)
	}

	// Showing the same folder again finds the workspace instead of offering another.
	status, body := f.call(t, http.MethodPost, softwareDigestAPI+"/scan", map[string]any{"chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("scan again: %d %v", status, body)
	}
	again := body["offer"].(map[string]any)
	existing, _ := again["software"].(map[string]any)["existing"].([]any)
	if len(existing) != 1 || existing[0].(map[string]any)["workspace_id"] != ws.ID || again["plan"] != nil {
		t.Fatalf("the same folder shown again: existing=%v plan=%v", existing, again["plan"])
	}
}

// With no model the run stops before it creates anything, says why, and
// carries on from the same confirmation once a model is chosen.
func TestSoftwareSetup_AMissingModelStopsBeforeAnythingIsCreatedAndResumes(t *testing.T) {
	f := newSoftwareCardFixture(t, "widget")
	workspacesBefore := len(f.builder.workspaceFileStore.CachedWorkspaces())
	agentsBefore := f.agentNames(t)
	f.review(t)

	if status, body := f.setUp(t, "no-model"); status != http.StatusOK {
		t.Fatalf("set up: %d %v", status, body)
	}
	stopped := f.settled(t)
	setup, _ := stopped["setup"].(map[string]any)
	if stopped["status"] == "resolved" || setup == nil || setup["stop_reason"] != "needs_model" {
		t.Fatalf("a setup with no model: %v", stopped)
	}
	for _, line := range setup["lines"].([]any) {
		if item := line.(map[string]any); item["state"] == "done" {
			t.Errorf("a step finished before the model was chosen: %v", item)
		}
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspacesBefore || f.agentNames(t) != agentsBefore {
		t.Fatal("a setup that stopped for a model created a workspace or an agent")
	}
	if stopped["outcome"].(map[string]any)["receipt"] != nil {
		t.Fatalf("a stopped setup has a receipt: %v", stopped["outcome"])
	}

	// Continuing without the repair stops in the same place.
	if status, body := f.setUp(t, "still-no-model"); status != http.StatusOK {
		t.Fatalf("continue: %d %v", status, body)
	}
	if again := f.settled(t); again["setup"].(map[string]any)["stop_reason"] != "needs_model" {
		t.Fatalf("continuing without a model: %v", again["setup"])
	}

	f.setModel(t)
	if status, body := f.setUp(t, "with-model"); status != http.StatusOK {
		t.Fatalf("continue with a model: %d %v", status, body)
	}
	done := f.settled(t)
	if done["status"] != "resolved" || len(f.softwareWorkspaces(t)) != 1 {
		t.Fatalf("the repaired setup did not finish: %v", done)
	}
	if _, model, found := f.builder.sessionHandler.SavedAgentModel("Development Assistant"); !found || model != "sonnet" {
		t.Fatalf("the assistant was created without the chosen model: %q %v", model, found)
	}
}

// softwareRunFixture drives the host directly, the way the folder service does,
// so a test can hold two offers about one folder and can act between steps.
type softwareRunFixture struct {
	*softwareCardFixture
	host     *folderSetupHost
	identity string
	review   personalassistant.FolderSoftwareReview
}

func newSoftwareRunFixture(t *testing.T) *softwareRunFixture {
	t.Helper()
	f := &softwareRunFixture{softwareCardFixture: newSoftwareCardFixture(t, "widget")}
	f.setModel(t)
	f.host = &folderSetupHost{builder: f.builder}
	f.identity = directoryWitness(t, f.dir)
	inspection, err := inspectSoftwareProject(context.Background(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.review = inspection.Review
	return f
}

// request plans a setup for one offer about the fixture folder, as reviewed
// now. onUpdate sees every progress report the run sends.
func (f *softwareRunFixture) request(t *testing.T, offerID string, onUpdate func(personalassistant.FolderSetupUpdate)) personalassistant.FolderSetupRequest {
	t.Helper()
	review := f.review
	req := personalassistant.FolderSetupRequest{
		UserID: "local", Path: f.dir,
		Offer: personalassistant.FolderOffer{
			ID: offerID, Status: personalassistant.FolderOfferPending,
			Subject: personalassistant.FolderCandidateRecord{
				Key: personalassistant.FolderKey(f.dir), Name: "widget", Kind: personalassistant.FolderChoiceProject,
				Shape: string(folderdigest.ShapeCode), MarkerName: "go.mod", Repository: string(folderdigest.RepositoryDirectory), IsRoot: true,
			},
			SoftwareReview: &review,
		},
		Software: &personalassistant.FolderSoftwareRequest{Identity: f.identity, Review: &review},
	}
	existing, err := softwareExistingProjects(f.builder.workspaceFileStore, "local", f.dir, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	req.Software.Existing = existing
	plan, err := f.host.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan for %s: %v", offerID, err)
	}
	req.Plan = plan
	req.Update = func(_ context.Context, update personalassistant.FolderSetupUpdate) error {
		if onUpdate != nil {
			onUpdate(update)
		}
		return nil
	}
	return req
}

// outcome runs one pass and returns its last report.
func (f *softwareRunFixture) run(t *testing.T, req personalassistant.FolderSetupRequest) personalassistant.FolderSetupUpdate {
	t.Helper()
	var mu sync.Mutex
	var last personalassistant.FolderSetupUpdate
	inner := req.Update
	req.Update = func(ctx context.Context, update personalassistant.FolderSetupUpdate) error {
		mu.Lock()
		last = update
		mu.Unlock()
		return inner(ctx, update)
	}
	if err := f.host.softwareRun(context.Background(), req); err != nil {
		t.Fatalf("run %s: %v", req.Offer.ID, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return last
}

func (f *softwareRunFixture) verify(t *testing.T, req personalassistant.FolderSetupRequest) (personalassistant.FolderCreateResult, error) {
	t.Helper()
	offer := req.Offer
	offer.Setup = &personalassistant.FolderSetupRun{Status: personalassistant.FolderSetupDone, Intent: req.Plan.Intent}
	return f.host.softwareProject(context.Background(), "local", offer, f.dir, f.identity)
}

// Two offers about one folder, confirmed at the same moment, end with one
// workspace, one assistant and one assessment. Both resolve to it; only the
// offer that made it reports having created it, and its record is not
// rewritten by the other.
func TestSoftwareSetup_TwoOffersForOneFolderMakeOneWorkspace(t *testing.T) {
	f := newSoftwareRunFixture(t)
	requests := []personalassistant.FolderSetupRequest{f.request(t, "offer-a", nil), f.request(t, "offer-b", nil)}
	results := make([]personalassistant.FolderSetupUpdate, len(requests))
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = f.run(t, requests[i])
		}(i)
	}
	wg.Wait()

	for i, result := range results {
		if result.Status != personalassistant.FolderSetupDone {
			t.Fatalf("run %d = %s (%s)", i, result.Status, result.StopReason)
		}
	}
	linked := f.softwareWorkspaces(t)
	if len(linked) != 1 || len(firstTasks(linked[0])) != 1 || len(linked[0].AgentInstances) != 1 {
		t.Fatalf("workspaces = %d", len(linked))
	}
	if names := f.agentNames(t); strings.Count(names, "Development Assistant") != 1 {
		t.Fatalf("agents = %s", names)
	}
	maker, _ := linked[0].SharedData[projecttemplates.FolderOfferIDKey].(string)
	created := 0
	for _, req := range requests {
		verified, err := f.verify(t, req)
		if err != nil || verified.WorkspaceID != linked[0].ID {
			t.Fatalf("%s resolves to %q, %v", req.Offer.ID, verified.WorkspaceID, err)
		}
		if verified.Created != (req.Offer.ID == maker) {
			t.Errorf("%s created=%v but the workspace was made for %s", req.Offer.ID, verified.Created, maker)
		}
		if verified.Created {
			created++
		} else if row := verified.Receipt[0]; !strings.Contains(row.Detail, "no second one was made") {
			t.Errorf("the reused receipt says %q", row.Detail)
		}
	}
	if created != 1 {
		t.Fatalf("%d offers claim to have created the workspace", created)
	}
}

// A sibling worktree or a clone of the same repository is a different folder.
// It never resolves to this folder's workspace, and it gets its own.
func TestSoftwareSetup_AnotherCheckoutOfTheRepositoryIsNotThisProject(t *testing.T) {
	f := newSoftwareRunFixture(t)
	reviewed := f.request(t, "offer-a", nil)
	if result := f.run(t, reviewed); result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("first setup: %+v", result)
	}
	first := f.softwareWorkspaces(t)[0]

	// A clone and a sibling worktree: the same repository, other directories.
	for _, other := range []string{f.set.Clone, f.set.SiblingWorktree} {
		witness := directoryWitness(t, other)
		found, err := softwareExistingProjects(f.builder.workspaceFileStore, "local", other, witness)
		if err != nil || len(found) != 0 {
			t.Fatalf("%s matched the first checkout's workspace: %v %v", filepath.Base(other), found, err)
		}
		// A finished run about that other folder is not proved by this workspace.
		intent := reviewed.Plan.Intent
		software := *intent.Software
		software.SubjectIdentity, software.SubjectKey = witness, personalassistant.FolderKey(other)
		intent.Software = &software
		offer := personalassistant.FolderOffer{ID: "offer-other", Setup: &personalassistant.FolderSetupRun{Status: personalassistant.FolderSetupDone, Intent: intent}}
		if verified, err := f.host.softwareProject(context.Background(), "local", offer, other, witness); err == nil {
			t.Fatalf("%s resolved to workspace %q", filepath.Base(other), verified.WorkspaceID)
		}
	}
	if after := f.softwareWorkspaces(t); len(after) != 1 || after[0].ID != first.ID {
		t.Fatal("the first checkout's workspace was disturbed")
	}
}

// lockWorkspaceFolder makes a workspace's own folder unwritable, so the next
// save of its record fails the way a full or read-only disk would.
func lockWorkspaceFolder(t *testing.T, files *workspace.FileStore, workspaceID string) (unlock func()) {
	t.Helper()
	folder, err := files.GetFolderPath(workspaceID)
	if err != nil || folder == "" {
		t.Fatalf("workspace folder: %q %v", folder, err)
	}
	if err := os.Chmod(folder, 0o500); err != nil { // #nosec G302 -- a test making a save fail
		t.Fatal(err)
	}
	unlock = func() { _ = os.Chmod(folder, 0o750) } // #nosec G302 -- restores the test's own folder
	t.Cleanup(unlock)
	return unlock
}

// A save that fails part-way stops the run at that step and keeps what
// finished. The next pass continues on the same workspace: no second
// workspace, assistant or assessment, and the project folder is untouched.
func TestSoftwareSetup_AFailedStepIsResumedOnTheSameWorkspace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only folder does not stop root")
	}
	for _, failAt := range []string{personalassistant.FolderPlanFolder, personalassistant.FolderPlanTask} {
		t.Run("failing at "+failAt, func(t *testing.T) {
			f := newSoftwareRunFixture(t)
			before, err := softwarefixture.Hash(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			creator := folderWorkspaceCreator{linker: folderWorkspaceLinker{files: f.builder.workspaceFileStore}}
			var unlock func()
			req := f.request(t, "offer-a", func(update personalassistant.FolderSetupUpdate) {
				for _, line := range update.Lines {
					if line.Kind == failAt && line.State == personalassistant.FolderLineWorking && unlock == nil {
						unlock = lockWorkspaceFolder(t, f.builder.workspaceFileStore, creator.existingForOffer("local", "offer-a"))
					}
				}
			})

			stopped := f.run(t, req)
			if stopped.Status != personalassistant.FolderSetupStopped || stopped.StopReason != personalassistant.FolderStopFailed {
				t.Fatalf("a failed save: %s (%s)", stopped.Status, stopped.StopReason)
			}
			states := map[string]string{}
			for _, line := range stopped.Lines {
				states[line.Kind] = line.State
			}
			if states[personalassistant.FolderPlanWorkspace] != personalassistant.FolderLineDone || states[failAt] != personalassistant.FolderLineFailed {
				t.Fatalf("lines after the failure: %v", states)
			}
			if _, err := f.verify(t, req); failAt == personalassistant.FolderPlanFolder && err == nil {
				t.Fatal("a setup whose folder was never linked was verified")
			}
			own := creator.existingForOffer("local", "offer-a")
			if own == "" {
				t.Fatal("the workspace made before the failure is gone")
			}

			unlock()
			// The next pass is planned from the stored run, as a resume is.
			req.Plan.Lines = stopped.Lines
			done := f.run(t, req)
			if done.Status != personalassistant.FolderSetupDone {
				t.Fatalf("the resumed setup: %s (%s)", done.Status, done.StopReason)
			}
			linked := f.softwareWorkspaces(t)
			if len(linked) != 1 || linked[0].ID != own || len(firstTasks(linked[0])) != 1 || len(linked[0].AgentInstances) != 1 {
				t.Fatalf("after the resume: %d workspaces", len(linked))
			}
			if names := f.agentNames(t); strings.Count(names, "Development Assistant") != 1 {
				t.Fatalf("agents = %s", names)
			}
			if verified, err := f.verify(t, req); err != nil || verified.WorkspaceID != own || !verified.Created {
				t.Fatalf("verified = %+v, %v", verified, err)
			}
			if after, err := softwarefixture.Hash(f.dir); err != nil || after != before {
				t.Fatalf("a failed and resumed setup changed the project folder: %v", err)
			}
			if len(f.provider.requests) != 0 {
				t.Fatal("setup called a model")
			}
		})
	}
}

// A setup whose promise no longer matches what is saved stops; it does not
// carry on with something the user did not review.
func TestSoftwareSetup_StopsWhenSavedStateIsNotWhatWasReviewed(t *testing.T) {
	t.Run("a saved agent of the reviewed name appeared", func(t *testing.T) {
		f := newSoftwareRunFixture(t)
		req := f.request(t, "offer-a", nil) // reviewed: a new agent is created
		if status, body := f.call(t, http.MethodPost, "/api/agents", map[string]any{"name": "Development Assistant", "system_prompt": "mine, and different"}); status >= 300 {
			t.Fatalf("create the user's own agent: %d %v", status, body)
		}
		stopped := f.run(t, req)
		if stopped.Status != personalassistant.FolderSetupStopped || stopped.StopReason != personalassistant.FolderStopPlanChanged {
			t.Fatalf("run = %s (%s)", stopped.Status, stopped.StopReason)
		}
		if len(f.softwareWorkspaces(t)) != 0 {
			t.Fatal("a workspace was created on a stale review")
		}
		// The user's agent is what it was.
		if saved, found := f.builder.st.GetAgent("Development Assistant"); !found || saved.Settings.SystemPrompt != "mine, and different" {
			t.Fatalf("the user's saved agent was replaced: %+v", saved)
		}
	})

	t.Run("the folder was replaced", func(t *testing.T) {
		f := newSoftwareRunFixture(t)
		req := f.request(t, "offer-a", nil)
		if err := softwarefixture.Replace(f.dir); err != nil {
			t.Fatal(err)
		}
		// The service would refuse before the run; the run itself must not link
		// the directory that is there now either.
		stopped := f.run(t, req)
		if stopped.Status != personalassistant.FolderSetupStopped {
			t.Fatalf("run = %s", stopped.Status)
		}
		if len(f.softwareWorkspaces(t)) != 0 {
			t.Fatal("a replaced directory was linked")
		}
	})

	t.Run("another workspace was linked to the folder", func(t *testing.T) {
		f := newSoftwareRunFixture(t)
		req := f.request(t, "offer-a", nil) // reviewed: a new workspace
		other := &workspace.Workspace{ID: "by-hand", Name: "By hand", FolderSlug: "by-hand", OwnerUserID: "local", SharedData: map[string]any{}, Status: workspace.StatusActive}
		if _, err := projecttemplates.AttachLinkedDirectory(other, "widget", f.dir); err != nil {
			t.Fatal(err)
		}
		if err := f.builder.workspaceFileStore.Save(other); err != nil {
			t.Fatal(err)
		}
		stopped := f.run(t, req)
		if stopped.Status != personalassistant.FolderSetupStopped || stopped.StopReason != personalassistant.FolderStopPlanChanged {
			t.Fatalf("run = %s (%s)", stopped.Status, stopped.StopReason)
		}
		if linked := f.softwareWorkspaces(t); len(linked) != 1 || linked[0].ID != "by-hand" {
			t.Fatalf("workspaces on the folder = %d", len(linked))
		}
	})
}

// A workspace the user already linked to this folder is completed, not
// replaced: its own assistant, settings and tasks stay exactly as they were.
func TestSoftwareSetup_CompletesAnExistingWorkspaceWithoutChangingItsChoices(t *testing.T) {
	// handMade is a workspace the user made themselves and linked to the folder.
	handMade := func(t *testing.T, f *softwareRunFixture, create map[string]any) string {
		t.Helper()
		create["name"], create["workspace_preset"] = "Widget by hand", "general"
		status, created := f.call(t, http.MethodPost, "/api/workspaces", create)
		if status != http.StatusCreated {
			t.Fatalf("create: %d %v", status, created)
		}
		id := created["folder"].(map[string]any)["id"].(string)
		ws, err := f.builder.workspaceFileStore.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := projecttemplates.AttachLinkedDirectory(ws, "widget", f.dir); err != nil {
			t.Fatal(err)
		}
		if err := f.builder.workspaceFileStore.Save(ws); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// completed runs the reviewed setup and returns the one workspace on the folder.
	completed := func(t *testing.T, f *softwareRunFixture, id string, req personalassistant.FolderSetupRequest) *workspace.Workspace {
		t.Helper()
		if done := f.run(t, req); done.Status != personalassistant.FolderSetupDone {
			t.Fatalf("run = %s (%s)", done.Status, done.StopReason)
		}
		linked := f.softwareWorkspaces(t)
		if len(linked) != 1 || linked[0].ID != id {
			t.Fatalf("workspaces on the folder = %d", len(linked))
		}
		after := linked[0]
		if recorded, _ := after.SharedData[projecttemplates.FolderOfferIDKey].(string); recorded == req.Offer.ID {
			t.Fatal("the existing workspace was re-labelled as made for this offer")
		}
		if binding, err := workspace.GetSoftwareProjectBinding(after.SharedData); err != nil || binding == nil {
			t.Fatalf("the completed workspace has no binding: %v", err)
		}
		if len(firstTasks(after)) != 1 {
			t.Fatalf("first tasks = %d", len(firstTasks(after)))
		}
		verified, err := f.verify(t, req)
		if err != nil || verified.Created || verified.WorkspaceID != id {
			t.Fatalf("verified = %+v, %v", verified, err)
		}
		if !strings.Contains(verified.Receipt[0].Detail, "your existing workspace") {
			t.Fatalf("the receipt calls a reused workspace %q", verified.Receipt[0].Detail)
		}
		return after
	}

	t.Run("its own assistant and settings are kept", func(t *testing.T) {
		f := newSoftwareRunFixture(t)
		if status, body := f.call(t, http.MethodPost, "/api/agents", map[string]any{"name": "Robin", "system_prompt": "the user's own"}); status >= 300 {
			t.Fatalf("create the user's agent: %d %v", status, body)
		}
		id := handMade(t, f, map[string]any{"entry_agent_name": "Robin"})
		before, err := f.builder.workspaceFileStore.Get(id)
		if err != nil || before.EntryAgentName() != "Robin" {
			t.Fatalf("the hand-made workspace's assistant = %q, %v", before.EntryAgentName(), err)
		}
		settingsBefore := mustJSON(t, workspacesettings.Extract(before.SharedData).Workflow)

		req := f.request(t, "offer-a", nil)
		software := req.Plan.Intent.Software
		if software.Outcome != personalassistant.FolderSoftwareReuse || software.WorkspaceID != id || software.Assistant != "Robin" ||
			software.AssistantAction != personalassistant.FolderSoftwareKeep || software.Workflow != personalassistant.FolderSoftwareKeep {
			t.Fatalf("intent for an existing workspace = %+v", software)
		}
		after := completed(t, f, id, req)
		if after.EntryAgentName() != "Robin" || len(after.AgentInstances) != 1 ||
			mustJSON(t, workspacesettings.Extract(after.SharedData).Workflow) != settingsBefore {
			t.Fatalf("the existing workspace's assistant or settings changed: %q", after.EntryAgentName())
		}
		if strings.Contains(f.agentNames(t), "Development Assistant") {
			t.Fatal("an assistant was created for a workspace that already had one")
		}
		if saved, _ := f.builder.st.GetAgent("Robin"); saved.Settings.SystemPrompt != "the user's own" {
			t.Fatalf("the user's agent was rewritten: %+v", saved.Settings)
		}
	})

	t.Run("a workspace with no assistant gets the reviewed one, once", func(t *testing.T) {
		f := newSoftwareRunFixture(t)
		id := handMade(t, f, map[string]any{"create_template_agents": false})
		before, err := f.builder.workspaceFileStore.Get(id)
		if err != nil || before.EntryAgentName() != "" {
			t.Fatalf("the hand-made workspace already has an assistant: %q, %v", before.EntryAgentName(), err)
		}
		settingsBefore := mustJSON(t, workspacesettings.Extract(before.SharedData).Workflow)

		req := f.request(t, "offer-a", nil)
		software := req.Plan.Intent.Software
		if software.Outcome != personalassistant.FolderSoftwareReuse || software.Assistant != "Development Assistant" ||
			software.AssistantAction != personalassistant.FolderSoftwareCreate || software.Workflow != personalassistant.FolderSoftwareKeep {
			t.Fatalf("intent for a workspace with no assistant = %+v", software)
		}
		after := completed(t, f, id, req)
		if after.EntryAgentName() != "Development Assistant" || len(after.AgentInstances) != 1 ||
			mustJSON(t, workspacesettings.Extract(after.SharedData).Workflow) != settingsBefore {
			t.Fatalf("after the setup: assistant %q of %d", after.EntryAgentName(), len(after.AgentInstances))
		}
		if task := firstTasks(after)[0]; task.To != "Development Assistant" {
			t.Fatalf("the assessment belongs to %q", task.To)
		}
		// A second pass of the same confirmation changes nothing.
		again := completed(t, f, id, req)
		if len(again.AgentInstances) != 1 || strings.Count(f.agentNames(t), "Development Assistant") != 1 {
			t.Fatalf("a second pass added an assistant: %s", f.agentNames(t))
		}
	})
}
