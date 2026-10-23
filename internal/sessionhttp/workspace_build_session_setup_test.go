package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// plannedTeammateReply names the workspace, describes it, sets its goal and
// adds Scout, everything a recognized folder's build may set.
const plannedTeammateReply = `{"say":"Set it up makes Music Production Home with Ori Music Demo inside it.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["name","description","goal","team"],"blueprint_id":"","name":"Ori Music Demo","description":"Demos for the next release.","goal":"Finish three demos.",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":["Scout"]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":true,"create_now":false}`

// plannedBuild is a build whose recognized REAPER folder has a ready plan and
// whose first turn named the workspace.
func (f *buildFixture) plannedBuild(t *testing.T, folders *fakeBuildFolders) personalassistant.WorkspaceBuildSession {
	t.Helper()
	f.withMusicCatalog()
	f.withFolders(t, folders)
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
	return sessionOf(t, body)
}

func (f *buildFixture) setUp(t *testing.T, build personalassistant.WorkspaceBuildSession, body map[string]any) (int, map[string]any) {
	t.Helper()
	body["version"] = build.Version
	return f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/setup", body)
}

func (f *buildFixture) read(t *testing.T, id string) personalassistant.WorkspaceBuildSession {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil || doc.Session(id) == nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return *doc.Session(id)
}

// resolvedSongOffer is the folder digest's offer once the run finished: a new
// Home, the song workspace inside it, every line done.
func resolvedSongOffer(sessionID string) personalassistant.FolderOffer {
	review := personalassistant.BuildReviewTarget(foldercontext.Target{UserID: "local", WorkspaceID: "hq-1", AgentName: "luna", DraftID: "build:" + sessionID}, sessionID)
	return personalassistant.FolderOffer{
		ID: "offer-1", Status: personalassistant.FolderOfferResolved,
		Subject:            personalassistant.FolderCandidateRecord{Name: "Ori Music Demo"},
		ConversationReview: &personalassistant.FolderConversationReview{Target: review, ObservationID: "obs-reaper"},
		Setup: &personalassistant.FolderSetupRun{
			Status: personalassistant.FolderSetupDone,
			Intent: personalassistant.FolderSetupIntent{Destination: &personalassistant.FolderSetupDestination{Status: "new", Name: "Music Production Home"}},
			Lines: []personalassistant.FolderPlanLine{
				{Kind: "home", Name: "Creates Home “Music Production Home”", State: personalassistant.FolderLineDone},
				{Kind: "workspace", Name: "Creates a REAPER song workspace named Ori Music Demo", State: personalassistant.FolderLineDone},
			},
		},
		Outcome: &personalassistant.FolderOutcome{
			Kind: personalassistant.FolderChoiceProject, WorkspaceID: "ws-song",
			Parent:  &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "home-1", Name: "Music Production Home"},
			Receipt: []personalassistant.FolderReceiptRow{{Kind: "workspace", Name: "Ori Music Demo"}},
		},
	}
}

func TestWorkspaceBuildSetup_RunsTheConfirmedPlanAndRecordsWhatItMade(t *testing.T) {
	f := newBuildFixture(t, true, plannedTeammateReply)
	files, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.handler.SetWorkspaceStore(files)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan(),
		run: &personalassistant.FolderSetupView{
			Status:      personalassistant.FolderSetupRunning,
			Visual:      &personalassistant.FolderSetupVisual{Entity: &personalassistant.FolderSetupEntity{Kind: "workspace", Name: "Reviewed workspace", Operation: "create"}},
			Hire:        &personalassistant.FolderHire{Mode: personalassistant.FolderHireCreate, AgentName: "Reviewed agent"},
			Destination: &personalassistant.FolderSetupDestination{Status: "standalone"},
			Lines:       []personalassistant.FolderPlanLine{{Kind: "home", Name: "Creates Home “Music Production Home”", State: personalassistant.FolderLineWorking}},
		}}
	build := f.plannedBuild(t, folders)

	// A digest the server would not show now is a changed plan: nothing runs.
	status, body := f.setUp(t, build, map[string]any{"plan_digest": strings.Repeat("e", 64)})
	if status != http.StatusConflict || body["code"] != "plan_changed" || planOf(t, body) == nil || len(folders.reviewed) != 0 {
		t.Fatalf("stale digest: %d %v (reviewed %d)", status, body, len(folders.reviewed))
	}

	status, body = f.setUp(t, build, map[string]any{"plan_digest": songDigest})
	if status != http.StatusOK {
		t.Fatalf("set it up: %d %v", status, body)
	}
	running := sessionOf(t, body)
	if running.Status != personalassistant.WorkspaceBuildPreparing || running.Setup == nil || running.Setup.OfferID != "offer-1" ||
		running.Setup.PlanDigest != songDigest || running.Folder.Recognition.OfferID != "offer-1" {
		t.Fatalf("claimed session %+v setup %+v", running, running.Setup)
	}
	draft := foldercontext.Target{UserID: "local", WorkspaceID: "hq-1", AgentName: "luna", DraftID: "build:" + build.ID}
	if len(folders.reviewed) != 1 || folders.reviewed[0] != (reviewedSetup{draft, build.ID, "obs-reaper", "candidate-0", "Ori Music Demo"}) {
		t.Fatalf("reviewed %+v", folders.reviewed)
	}
	if len(folders.started) != 1 || folders.started[0].offerID != "offer-1" || folders.started[0].digest != songDigest ||
		folders.started[0].userID != "local" || folders.started[0].requestID == "" {
		t.Fatalf("started %+v", folders.started)
	}
	session, _ := body["session"].(map[string]any)
	run, _ := session["setup_run"].(map[string]any)
	if run == nil || run["status"] != "running" || session["folder_expires_at"] == nil {
		t.Fatalf("the response carries the run and the pick's expiry: %v", session)
	}
	if run["visual"] == nil || run["hire"] == nil || run["destination"] == nil {
		t.Fatalf("run lost display snapshot: %v", run)
	}
	projected := f.handler.readBuildSetupRun(context.Background(), "local", nil, &running)
	projected.Visual.Entity.Name = "Client display"
	projected.Hire.AgentName = "Client agent"
	projected.Destination.Status = "new"
	if folders.run.Visual.Entity.Name != "Reviewed workspace" || folders.run.Hire.AgentName != "Reviewed agent" || folders.run.Destination.Status != "standalone" {
		t.Fatal("build projection aliases the canonical offer")
	}
	if got := folders.reads[len(folders.reads)-1]; got != personalassistant.BuildReviewTarget(draft, build.ID) {
		t.Fatalf("the run is read through the build's own review: %+v", got)
	}
	// While it runs the build can be read, not changed.
	if status, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+build.ID, nil); status != http.StatusOK || sessionOf(t, body).Status != personalassistant.WorkspaceBuildPreparing {
		t.Fatalf("read while running: %d %v", status, body)
	}
	if status, body := f.setUp(t, running, map[string]any{"plan_digest": songDigest}); status != http.StatusConflict || body["code"] != "closed" {
		t.Fatalf("a second press while it runs: %d %v", status, body)
	}

	// The run made the song workspace; the digest settles it, and the build
	// closes with what was made.
	if err := files.Save(&agentworkspace.Workspace{ID: "ws-song", Name: "Ori Music Demo", Kind: "workspace", FolderSlug: "ori-music-demo", Status: agentworkspace.StatusActive}); err != nil {
		t.Fatal(err)
	}
	f.handler.WorkspaceBuildSetupSettled(context.Background(), "local", resolvedSongOffer(build.ID))
	done := f.read(t, build.ID)
	want := "Created Music Production Home and Ori Music Demo inside it. Linked folder “Ori Music Demo”."
	if done.Status != personalassistant.WorkspaceBuildCreated || done.CreatedWorkspaceID != "ws-song" || done.HomeWorkspaceID != "home-1" ||
		done.SetupReceipt != want || done.Setup != nil || done.Folder != nil {
		t.Fatalf("finished session %+v", done)
	}
	// A later wizard create cannot reopen it.
	f.handler.finishWorkspaceBuild(context.Background(), build.ID, "ws-other", personalassistant.BuildDraft{})
	if again := f.read(t, build.ID); again.CreatedWorkspaceID != "ws-song" {
		t.Fatalf("a later create reopened the build: %+v", again)
	}
	// The workspace the run made gets the draft's description, goal and teammate,
	// and "How this was set up" comes from the plan and the receipt.
	made, err := files.Get("ws-song")
	if err != nil {
		t.Fatal(err)
	}
	if made.Description != "Demos for the next release." || made.Mission != "Finish three demos." || !made.HasAgent("Scout") {
		t.Fatalf("details on the made workspace: description %q mission %q agents %+v", made.Description, made.Mission, made.AgentInstances)
	}
	summary := made.GetTemplateProvenance().BuildSummary
	var texts []string
	for _, decision := range summary.Decisions {
		texts = append(texts, decision.Text)
	}
	joined := strings.Join(texts, " | ")
	for _, wantText := range []string{"Creates Home “Music Production Home”", "Added Scout as a teammate.", want} {
		if !strings.Contains(joined, wantText) {
			t.Fatalf("How this was set up lacks %q: %s", wantText, joined)
		}
	}
	if strings.Contains(joined, "Set it up makes") {
		t.Fatalf("the model's narration is not the record: %s", joined)
	}
}

// The brief typed under the plan is the hire's goal: it goes to the run, and
// the draft's goal does not replace it on the workspace the run made.
func TestWorkspaceBuildSetup_TheTypedBriefIsTheMadeWorkspacesGoal(t *testing.T) {
	f := newBuildFixture(t, true, plannedTeammateReply)
	files, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.handler.SetWorkspaceStore(files)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	const brief = "Mix the chorus first."
	if status, body := f.setUp(t, build, map[string]any{"plan_digest": songDigest, "brief": brief}); status != http.StatusOK {
		t.Fatalf("hire and start: %d %v", status, body)
	}
	if len(folders.started) != 1 || folders.started[0].brief != brief {
		t.Fatalf("started %+v", folders.started)
	}
	if err := files.Save(&agentworkspace.Workspace{ID: "ws-song", Name: "Ori Music Demo", Kind: "workspace", FolderSlug: "ori-music-demo",
		Status: agentworkspace.StatusActive, Mission: brief}); err != nil {
		t.Fatal(err)
	}
	settled := resolvedSongOffer(build.ID)
	settled.Setup.Brief = brief
	f.handler.WorkspaceBuildSetupSettled(context.Background(), "local", settled)
	made, err := files.Get("ws-song")
	if err != nil {
		t.Fatal(err)
	}
	if made.Mission != brief || made.Description != "Demos for the next release." {
		t.Fatalf("made workspace: mission %q description %q", made.Mission, made.Description)
	}
}

func TestWorkspaceBuildSetup_AStoppedRunReopensTheBuildAndRetryContinuesIt(t *testing.T) {
	for _, tc := range []struct {
		reason, line string
	}{
		{personalassistant.FolderStopInstallFailed, "A plugin could not be installed or enabled, so nothing after it ran. Retry continues from there."},
		{personalassistant.FolderStopNeedsPick, "Ori no longer holds the attached folder. Add the folder again, then Retry."},
		{personalassistant.FolderStopNeedsChoice, "The folder has more than one project file. Choose the one to set up."},
		{personalassistant.FolderStopPlanChanged, "What Set it up would do has changed, so it stopped before going further. Review the plan, then Retry."},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			f := newBuildFixture(t, true, plannedReply)
			folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
			build := f.plannedBuild(t, folders)
			if status, body := f.setUp(t, build, map[string]any{"plan_digest": songDigest}); status != http.StatusOK {
				t.Fatalf("set it up: %d %v", status, body)
			}
			stopped := resolvedSongOffer(build.ID)
			stopped.Status, stopped.Outcome = personalassistant.FolderOfferAwaitingOutcome, nil
			stopped.Setup.Status, stopped.Setup.StopReason = personalassistant.FolderSetupStopped, tc.reason
			f.handler.WorkspaceBuildSetupSettled(context.Background(), "local", stopped)

			open := f.read(t, build.ID)
			if open.Status != personalassistant.WorkspaceBuildOpen || open.Setup == nil || open.Setup.StopReason != tc.reason || open.Setup.OfferID != "offer-1" {
				t.Fatalf("reopened %+v setup %+v", open, open.Setup)
			}
			if last := lastEntry(open); !last.Host || last.Text != tc.line {
				t.Fatalf("stop line %+v, want %q", last, tc.line)
			}
			// Retry continues the run the user confirmed, on its own digest,
			// whatever the plan reads now.
			folders.plan.Digest = strings.Repeat("f", 64)
			body := map[string]any{"plan_digest": "anything"}
			if tc.reason == personalassistant.FolderStopNeedsChoice {
				body["entry_name"] = "Take Two.rpp"
			}
			if status, body := f.setUp(t, open, body); status != http.StatusOK {
				t.Fatalf("retry: %d %v", status, body)
			}
			if len(folders.started) != 2 || folders.started[1].digest != songDigest || folders.started[1].requestID == folders.started[0].requestID {
				t.Fatalf("retry started %+v", folders.started)
			}
			if tc.reason == personalassistant.FolderStopNeedsChoice && folders.started[1].entryName != "Take Two.rpp" {
				t.Fatalf("the chosen project file goes to the run: %+v", folders.started[1])
			}
			if again := f.read(t, build.ID); again.Status != personalassistant.WorkspaceBuildPreparing || again.Setup.StopReason != "" {
				t.Fatalf("retrying %+v", again.Setup)
			}
		})
	}
}

func TestWorkspaceBuildSetup_RefusesWhatItCannotRunAndCreatesNothing(t *testing.T) {
	// A pick the server no longer holds: the folder must be attached again.
	f := newBuildFixture(t, true, plannedReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	folders.plan, folders.planErr = nil, personalassistant.ErrFolderSelection
	status, body := f.setUp(t, build, map[string]any{"plan_digest": songDigest})
	if status != http.StatusConflict || !strings.Contains(body["error"].(string), "a pick lasts 30 minutes") || len(folders.reviewed) != 0 || len(folders.started) != 0 {
		t.Fatalf("lost pick: %d %v", status, body)
	}
	if got := f.read(t, build.ID); got.Status != personalassistant.WorkspaceBuildOpen || got.Setup != nil {
		t.Fatalf("nothing changes on a lost pick: %+v", got)
	}

	// Another review waiting on Home, and a run that cannot start.
	folders.plan, folders.planErr = songPlan(), nil
	folders.reviewErr = personalassistant.ErrFolderReviewPending
	if status, body := f.setUp(t, build, map[string]any{"plan_digest": songDigest}); status != http.StatusConflict || body["code"] != "review_pending" {
		t.Fatalf("review pending: %d %v", status, body)
	}
	folders.reviewErr, folders.startErr = nil, personalassistant.ErrFolderPlanChanged
	status, body = f.setUp(t, build, map[string]any{"plan_digest": songDigest})
	reopened := sessionOf(t, body)
	if status != http.StatusConflict || body["code"] != personalassistant.FolderStopPlanChanged || reopened.Status != personalassistant.WorkspaceBuildOpen ||
		reopened.Setup == nil || reopened.Setup.StopReason != personalassistant.FolderStopPlanChanged {
		t.Fatalf("plan changed at start: %d %+v", status, reopened)
	}

	// A folder with no plan is not set up.
	g := newBuildFixture(t, true, talkOnlyReply, talkOnlyReply)
	other := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	g.withMusicCatalog()
	g.withFolders(t, other)
	unnamed := g.start(t)
	_, body = g.attachFolder(t, unnamed, map[string]any{"mode": "chip", "chip": "documents"})
	unnamed = sessionOf(t, body)
	other.plan = &personalassistant.BuildFolderPlan{Unavailable: "plan_unavailable"}
	if status, body := g.setUp(t, unnamed, map[string]any{"plan_digest": songDigest}); status != http.StatusConflict || body["code"] != "no_plan" {
		t.Fatalf("no plan: %d %v", status, body)
	}

	// On 2026-10-20 a Codex model never named the draft, and Set it up stayed
	// disabled under a plan that said it names the workspace after its folder.
	// An unnamed draft is set up, and the journey names it after the folder.
	other.plan = songPlan()
	status, body = g.setUp(t, unnamed, map[string]any{"plan_digest": songDigest})
	if status != http.StatusOK || sessionOf(t, body).Status != personalassistant.WorkspaceBuildPreparing {
		t.Fatalf("unnamed: %d %v", status, body)
	}
	if len(other.reviewed) != 1 || other.reviewed[0].workspaceName != "" {
		t.Fatalf("the journey names an unnamed workspace itself: %+v", other.reviewed)
	}
	// The finished build keeps the name the run gave it, so the Map's
	// placement and Today say it rather than "New workspace".
	g.handler.WorkspaceBuildSetupSettled(context.Background(), "local", resolvedSongOffer(unnamed.ID))
	if done := g.read(t, unnamed.ID); done.Status != personalassistant.WorkspaceBuildCreated || done.Draft.Name != "Ori Music Demo" ||
		done.SetupReceipt != "Created Music Production Home and Ori Music Demo inside it. Linked folder “Ori Music Demo”." {
		t.Fatalf("finished unnamed build %+v", done)
	}
}

func TestWorkspaceBuildSetup_TheWizardNeverAlsoCreatesARecognizedFolder(t *testing.T) {
	f := newBuildFixture(t, true, plannedReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan(), root: "/Users/me/Ori Music Demo"}
	build := f.plannedBuild(t, folders)
	ctx := context.Background()
	if _, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, "workspace"); err != errBuildFolderSetItUp {
		t.Fatalf("a ready plan refuses the wizard's create: %v", err)
	}
	// Without a plan the wizard links the folder as before.
	folders.plan = &personalassistant.BuildFolderPlan{Unavailable: "plan_unavailable"}
	plan, err := f.handler.planCreateWorkspaceBuildFolder(ctx, createWorkspaceRequest{BuildSessionID: build.ID}, "workspace")
	if err != nil || plan == nil || plan.root != "/Users/me/Ori Music Demo" {
		t.Fatalf("no plan: %+v %v", plan, err)
	}
}

func TestWorkspaceBuildSetup_TheGuardVouchesOnlyForItsOwnBuild(t *testing.T) {
	f := newBuildFixture(t, true, plannedReply)
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	build := f.plannedBuild(t, folders)
	guard, lease := f.handler.WorkspaceBuildReviewAuthority()
	ctx := context.Background()
	offer := resolvedSongOffer(build.ID)
	offer.Status, offer.Outcome, offer.DecidedAt = personalassistant.FolderOfferPending, nil, nil
	if err := guard(ctx, offer); err != nil {
		t.Fatalf("its own undecided review: %v", err)
	}
	decided := offer
	at := time.Now()
	decided.DecidedAt = &at
	if err := guard(ctx, decided); !errors.Is(err, personalassistant.ErrBuildSetupNotOwned) {
		t.Fatalf("a decided offer the build never claimed: %v", err)
	}
	for name, mutate := range map[string]func(*personalassistant.FolderOffer){
		"another observation": func(o *personalassistant.FolderOffer) { o.ConversationReview.ObservationID = "obs-other" },
		"another assistant":   func(o *personalassistant.FolderOffer) { o.ConversationReview.Target.AgentName = "atlas" },
		"another build":       func(o *personalassistant.FolderOffer) { o.ConversationReview.Target.ConversationID = "build:other" },
	} {
		copied := offer
		review := *offer.ConversationReview
		copied.ConversationReview = &review
		mutate(&copied)
		if err := guard(ctx, copied); !errors.Is(err, personalassistant.ErrBuildSetupNotOwned) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	release, ok := lease(offer.ConversationReview.Target)
	if !ok {
		t.Fatal("the lease is free")
	}
	if _, again := lease(offer.ConversationReview.Target); again {
		t.Fatal("one setup request at a time per build")
	}
	release()
	if release, ok := lease(offer.ConversationReview.Target); !ok {
		t.Fatal("released")
	} else {
		release()
	}
}
