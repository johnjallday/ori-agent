package personalassistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// markNotInbox gives a fixture folder a marker of a kind Ori knows but has no
// blueprint for (an Obsidian vault), so it is reviewed as a generic workspace
// rather than as an inbox folder's hire.
func markNotInbox(t *testing.T, f *folderDigestFixture, folder string) {
	t.Helper()
	writeFolderFiles(t, filepath.Join(f.home, folder), ".obsidian/app.json")
}

// inboxServiceFixture is the Desktop chip (two loose files: an inbox folder)
// with conversation reviews wired, a one-card host that plans an inbox setup,
// and a first-look starter that must never be called.
func inboxServiceFixture(t *testing.T) (*folderDigestFixture, *FolderObservationService, foldercontext.Target, *fakeFolderSetup, *fakeFirstLook) {
	t.Helper()
	f, service, source := observationFixture(t)
	look := &fakeFirstLook{}
	f.service.deps.FirstLook = look
	f.service.deps.Creator = &fakeFolderCreator{}
	service.ConfigureConversationReviews(func(context.Context, FolderOffer) error { return nil }, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	setup := &fakeFolderSetup{
		lines: []FolderPlanLine{
			{Kind: FolderPlanAgents, Name: "Hire File Curator"},
			{Kind: FolderPlanWorkspace, Name: "A File Janitor workspace named Desktop"},
			{Kind: FolderPlanFolder, Name: "Linked where it is"},
			{Kind: FolderPlanTask, Name: "A first look: a metadata-only scan that proposes the first filing batch"},
		},
		hire:         &FolderHire{Mode: FolderHireCreate, AgentName: "File Curator", RoleLabel: "specialist"},
		briefDefault: "Prepare reviewable filing batches for the approved folder, explain uncertain classifications, and require approval before moving anything.",
	}
	f.service.deps.Setup = setup
	return f, service, source, setup, look
}

func TestFolderInbox_TheAttachAnswersWithTheHireOfFileCurator(t *testing.T) {
	_, service, source, setup, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if got := service.HireCandidate(ctx, source, observed.ID); got != (FolderHireCandidate{CandidateID: folderRootCandidateID}) {
		t.Fatalf("hire candidate = %+v", got)
	}
	options := service.ReviewOptions(ctx, source, observed.ID)
	if len(options) != 1 || options[0].WorkspaceType != "File Janitor workspace" || options[0].Presentation == nil || options[0].Presentation.State == "unavailable" {
		t.Fatalf("options = %+v", options)
	}
	planned := setup.planned[len(setup.planned)-1].Offer
	if !IsInboxOffer(planned) || planned.Subject.Shape != string(folderdigest.ShapeInbox) || !planned.Subject.IsRoot {
		t.Fatalf("the host planned %+v", planned.Subject)
	}
}

func TestFolderInbox_AFolderTheUserSaidNeverToAskAboutStaysUnrecognized(t *testing.T) {
	f, service, source, _, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		d.Tombstones = append(d.Tombstones, FolderTombstone{Key: FolderKey(filepath.Join(f.home, "Desktop")), Name: "Desktop", CreatedAt: f.now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Recognize(ctx, source, observed.ID); err != nil || got.Verdict != BuildFolderUnrecognized {
		t.Fatalf("tombstoned recognition = %+v %v", got, err)
	}
	if got := service.HireCandidate(ctx, source, observed.ID); got != (FolderHireCandidate{}) {
		t.Fatalf("tombstoned hire candidate = %+v", got)
	}
}

func TestFolderInbox_IsSetUpOnlyThroughItsReviewedPlan(t *testing.T) {
	f, service, source, _, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Review(ctx, source, "conversation", observed.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if view.CreateAvailable || view.ReviewDigest != "" || view.Remember || view.Subject.Shape != string(folderdigest.ShapeInbox) {
		t.Fatalf("an inbox review offers a plain create: %+v", view)
	}
	if view.Recognition == nil || view.Recognition.Label != "inbox folder" {
		t.Fatalf("recognition = %+v", view.Recognition)
	}
	service.BindSaved(source, observed.ID, "conversation")
	if _, err := f.service.Decide(ctx, "local", view.ID, FolderDecisionInput{Decision: FolderDecisionYes, RequestID: "bare-yes"}); !errors.Is(err, ErrFolderReviewRequired) {
		t.Fatalf("a bare yes was not refused: %v", err)
	}
}

func TestFolderInbox_ABuildKeepsItsOwnJanitorReview(t *testing.T) {
	_, service, source, _, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	// The build pane is told the folder suggests File Janitor (FR3) ...
	if got, err := service.Recognize(ctx, source, observed.ID); err != nil || got.BlueprintID != "file-janitor" {
		t.Fatalf("recognition = %+v %v", got, err)
	}
	// ... but a build's review of the root is never the hire card's setup.
	held, _, err := service.resolve(ctx, source, observed.ID)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := service.prepareReviewOffer(ctx, held, BuildReviewTarget(source, "build-1"), folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if IsInboxOffer(offer) {
		t.Fatal("a build's review became an inbox hire")
	}
}

func TestFolderInbox_HireAndStartResolvesWithTheFirstBatchAndStartsNoTask(t *testing.T) {
	f, service, source, setup, look := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Review(ctx, source, "conversation", observed.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	stored := doc.Offer(view.ID)
	setup.intent = FolderSetupIntent{Placement: "standalone", Recipe: folderdigest.BuiltinInboxFolder, RecipeVersion: 1, Inbox: &FolderInboxIntent{
		WorkspaceName: "Desktop", SubjectKey: stored.Subject.Key, SubjectIdentity: stored.ConversationReview.CandidateIdentity,
		BlueprintID: "file-janitor", BlueprintVersion: 5, BlueprintDigest: "blueprint-1", PlanRevision: "plan-1",
		Agent: FolderInboxAgent{RoleID: "file-curator", Name: "File Curator", Action: FolderSoftwareCreate, ConfigDigest: "config-1"},
	}}
	batch := &FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-1", Eligible: 23, NeedsReview: 4, Ineligible: 2,
		Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-1"}
	var verified []string
	f.service.deps.InboxProject = func(_ context.Context, _ string, offer FolderOffer, _, identity string) (FolderCreateResult, error) {
		verified = append(verified, identity)
		return FolderCreateResult{WorkspaceID: "ws-desktop", Route: "/workspaces/desktop", Created: true, FirstBatch: batch,
			Receipt: []FolderReceiptRow{{Kind: "batch", Name: "First batch", Detail: "23 proposed · 4 need review", Route: batch.Route}}}, nil
	}
	service.BindSaved(source, observed.ID, "conversation")
	target := source
	target.ConversationID, target.DraftID = "conversation", ""
	read, err := service.ReadReview(ctx, target, view.ID)
	if err != nil || read.Plan == nil {
		t.Fatalf("review plan = %+v %v", read, err)
	}
	if _, err := f.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "hire-1", PlanDigest: read.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the inbox hire to resolve", func() bool {
		doc, err := f.store.Read(ctx, "local")
		offer := doc.Offer(view.ID)
		if offer != nil && offer.Setup != nil && offer.Setup.Status == FolderSetupStopped {
			t.Fatalf("the run stopped: %s (verified %v)", offer.Setup.StopReason, verified)
		}
		return err == nil && offer != nil && offer.Status == FolderOfferResolved
	})
	doc, err = f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	outcome := doc.Offer(view.ID).Outcome
	if outcome == nil || outcome.WorkspaceID != "ws-desktop" || outcome.FirstLook == nil || outcome.FirstLook.Batch == nil ||
		*outcome.FirstLook.Batch != *batch || outcome.FirstLook.Started || outcome.FirstLook.TaskID != "" {
		t.Fatalf("outcome = %+v first look %+v", outcome, outcome.FirstLook)
	}
	if look.callCount() != 0 {
		t.Fatalf("a model task was started for an inbox hire: %v", look.calls)
	}
	if len(verified) != 1 || verified[0] != stored.ConversationReview.CandidateIdentity {
		t.Fatalf("verified against %v", verified)
	}
	// The run was given the plan's brief as the workspace goal.
	if ran := setup.ran[0]; !strings.HasPrefix(ran.Brief, "Prepare reviewable filing batches") || ran.Plan.Intent.Inbox == nil {
		t.Fatalf("run = brief %q intent %+v", ran.Brief, ran.Plan.Intent)
	}
	// A replayed settle starts nothing either.
	f.service.startHiredLook(ctx, "local", view.ID)
	if look.callCount() != 0 {
		t.Fatal("a replayed settle started a look")
	}
}

func TestFolderInbox_DefaultBriefIsTheBlueprintGoalWithAFallback(t *testing.T) {
	goal := "Prepare reviewable filing batches for the approved folder, explain uncertain classifications, and require approval before moving anything."
	if got := FolderDefaultBrief(folderdigest.ShapeInbox, goal); got != goal {
		t.Fatalf("brief = %q", got)
	}
	if got := FolderDefaultBrief(folderdigest.ShapeInbox, ""); got != "Keep this folder tidy: propose filing batches from file names and dates, explain anything uncertain, and ask before anything moves." {
		t.Fatalf("fallback = %q", got)
	}
	for _, row := range folderdigest.AllCapabilities() {
		if row.Shape != "" && FolderDefaultBrief(row.Shape, "") == "" {
			t.Errorf("shape %q has no default brief", row.Shape)
		}
	}
}

func TestFolderInbox_IntentAndFirstBatchAreBoundedAndPathFree(t *testing.T) {
	valid := FolderInboxIntent{
		WorkspaceName: "Downloads", SubjectKey: FolderKey("/Users/me/Downloads"), SubjectIdentity: "dev:1/ino:2",
		BlueprintID: "file-janitor", BlueprintVersion: 5, BlueprintDigest: "d", PlanRevision: "p",
		Agent: FolderInboxAgent{RoleID: "file-curator", Name: "File Curator", Action: FolderSoftwareCreate, ConfigDigest: "c"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid intent refused: %v", err)
	}
	for name, change := range map[string]func(i *FolderInboxIntent){
		"another blueprint":  func(i *FolderInboxIntent) { i.BlueprintID = "code-project" },
		"an unknown action":  func(i *FolderInboxIntent) { i.Agent.Action = "keep" },
		"a path as a name":   func(i *FolderInboxIntent) { i.WorkspaceName = "Users/me" },
		"a path as identity": func(i *FolderInboxIntent) { i.SubjectIdentity = "/Users/me" },
		"no agent":           func(i *FolderInboxIntent) { i.Agent.Name = "" },
	} {
		intent := valid
		change(&intent)
		if intent.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for name, bad := range map[string]FolderFirstBatch{
		"an unknown outcome":        {Outcome: "maybe"},
		"more to review than found": {Outcome: FolderFirstBatchReady, Eligible: 1, NeedsReview: 2},
		"a route elsewhere":         {Outcome: FolderFirstBatchReady, Route: "/settings"},
		"a path as batch id":        {Outcome: FolderFirstBatchReady, BatchID: "../x"},
	} {
		batch := bad
		if (&FolderFirstLookRecord{Batch: &batch}).validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if (&FolderFirstLookRecord{Started: true, Batch: &FolderFirstBatch{Outcome: FolderFirstBatchEmpty}}).validate() == nil {
		t.Error("a first batch recorded as a started task")
	}
}

func TestFolderSetupVisual_NotNeededModelAndBuiltinAreBounded(t *testing.T) {
	cloned := CloneFolderSetupVisual(&FolderSetupVisual{
		RequirementsChecked: true, Builtin: "File Janitor",
		Model: &FolderSetupModel{State: FolderSetupModelNotNeeded, Provider: "openai", Name: "gpt-5", Settings: "system"},
	})
	if cloned == nil || cloned.Builtin != "File Janitor" || *cloned.Model != (FolderSetupModel{State: FolderSetupModelNotNeeded}) {
		t.Fatalf("clone = %+v model %+v", cloned, cloned.Model)
	}
	// A built-in is never claimed beside a plugin to download, nor unchecked.
	for name, visual := range map[string]*FolderSetupVisual{
		"with a plugin": {RequirementsChecked: true, Builtin: "File Janitor", Plugins: []FolderSetupPlugin{{Kind: FolderPlanIntegration, Name: "REAPER", Operation: FolderInstallInstall}}},
		"unchecked":     {Builtin: "File Janitor", Placement: "standalone"},
		"a path":        {RequirementsChecked: true, Builtin: "/Applications/File Janitor"},
	} {
		if got := CloneFolderSetupVisual(visual); got != nil && got.Builtin != "" {
			t.Errorf("%s: builtin kept: %+v", name, got)
		}
	}
	// Existing model states are unchanged.
	if got := CloneFolderSetupVisual(&FolderSetupVisual{Model: &FolderSetupModel{State: "bogus"}}); got.Model.State != "not_checked" {
		t.Fatalf("unknown state = %q", got.Model.State)
	}
}
