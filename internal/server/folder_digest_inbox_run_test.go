package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeInboxWorld is the canonical state an inbox run writes and its verifier
// reads back: workspaces by id, one File Janitor's settings, scan receipts and
// batches. Every fake writes through it, so a replay sees what a pass saved.
type fakeInboxWorld struct {
	workspaces map[string]*workspace.Workspace
	settings   map[string]filejanitor.JanitorSettings
	receipts   map[string]filejanitor.ScanOperationReceipt
	batches    []filejanitor.JanitorBatch
	writes     []string
}

func newFakeInboxWorld() *fakeInboxWorld {
	return &fakeInboxWorld{
		workspaces: map[string]*workspace.Workspace{}, settings: map[string]filejanitor.JanitorSettings{},
		receipts: map[string]filejanitor.ScanOperationReceipt{},
	}
}

type fakeInboxTeam struct {
	world     *fakeInboxWorld
	plan      sessionhttp.ReviewedTemplateCreationPlan
	planErr   error
	roster    sessionhttp.FolderOfferRoster
	taken     map[string]bool
	saved     map[string]string
	createErr error
	created   []sessionhttp.ReviewedTemplateCreationRequest
}

func (f *fakeInboxTeam) ReviewFileJanitorCreation(context.Context) (sessionhttp.ReviewedTemplateCreationPlan, error) {
	return f.plan, f.planErr
}

func (f *fakeInboxTeam) CreateReviewedFileJanitor(_ context.Context, request sessionhttp.ReviewedTemplateCreationRequest) (sessionhttp.ReviewedTemplateCreationResult, error) {
	f.created = append(f.created, request)
	if f.createErr != nil {
		return sessionhttp.ReviewedTemplateCreationResult{}, f.createErr
	}
	if _, exists := f.world.workspaces[request.Descriptor.WorkspaceID]; exists {
		return sessionhttp.ReviewedTemplateCreationResult{WorkspaceID: request.Descriptor.WorkspaceID}, nil
	}
	if !reviewedTemplatePlansEqualForTest(f.plan, request.Plan) {
		return sessionhttp.ReviewedTemplateCreationResult{}, sessionhttp.ErrReviewedPlanChanged
	}
	ws := &workspace.Workspace{
		ID: request.Descriptor.WorkspaceID, Name: request.Name, FolderSlug: workspace.Slugify(request.Name),
		Status: workspace.StatusActive, OwnerUserID: request.Descriptor.OwnerUserID, Mission: request.Mission,
		InstalledCapabilities: []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1}},
		AgentInstances:        []workspace.AgentInstance{{ID: "instance-1", Name: request.Plan.Roles[0].Name, RoleID: request.Plan.Roles[0].RoleID, EntryPoint: true}},
	}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "file-janitor", TemplateName: "File Janitor", AssistantSetup: &workspace.AssistantSetupCreation{
		RunID: request.Descriptor.RunID, OperationID: request.Descriptor.OperationID, ReviewDigest: request.Descriptor.ReviewDigest,
	}})
	f.world.workspaces[ws.ID] = ws
	f.world.writes = append(f.world.writes, "workspace")
	if request.Plan.Roles[0].Action == "create" {
		f.saved[request.Plan.Roles[0].Name] = request.Plan.Roles[0].Model
	}
	return sessionhttp.ReviewedTemplateCreationResult{WorkspaceID: ws.ID}, nil
}

func reviewedTemplatePlansEqualForTest(left, right sessionhttp.ReviewedTemplateCreationPlan) bool {
	if left.BlueprintID != right.BlueprintID || left.PlanRevision != right.PlanRevision || len(left.Roles) != len(right.Roles) {
		return false
	}
	for i := range left.Roles {
		if left.Roles[i] != right.Roles[i] {
			return false
		}
	}
	return true
}

func (f *fakeInboxTeam) FolderOfferRoster(string, string) (sessionhttp.FolderOfferRoster, error) {
	return f.roster, nil
}

func (f *fakeInboxTeam) WorkspaceNameTaken(_ context.Context, name string) bool { return f.taken[name] }

func (f *fakeInboxTeam) SavedAgentModel(name string) (string, string, bool) {
	model, found := f.saved[name]
	return "openai", model, found
}

type fakeInboxJanitor struct {
	world       *fakeInboxWorld
	owner       *filejanitor.RootOwner
	reviewErr   error
	grantErr    error
	decisionErr error
	grants      []filejanitor.SetupRequest
	decisions   int
}

func (j *fakeInboxJanitor) FolderOwner(string) (*filejanitor.RootOwner, bool) {
	return j.owner, j.owner != nil
}

func (j *fakeInboxJanitor) ReviewSelectedSetup(string, string, []workspace.AutomationRecipe) (filejanitor.SelectedSetupReview, error) {
	return filejanitor.SelectedSetupReview{}, j.reviewErr
}

func (j *fakeInboxJanitor) ConfirmSetup(req filejanitor.SetupRequest) (filejanitor.Status, error) {
	j.grants = append(j.grants, req)
	if j.grantErr != nil {
		return filejanitor.Status{}, j.grantErr
	}
	current := j.world.settings[req.WorkspaceID]
	if current.LastFolderGrant != nil && current.LastFolderGrant.OperationID == req.Operation.OperationID {
		return filejanitor.Status{Settings: current}, nil
	}
	ws := j.world.workspaces[req.WorkspaceID]
	_ = ws.AddDirectoryReference(workspace.DirectoryReference{ID: "dir-1", Name: filepath.Base(req.Path), Path: req.Path})
	settings := filejanitor.JanitorSettings{
		WorkspaceID: req.WorkspaceID, RootID: "root-1", RootPath: req.Path, DirectoryReferenceID: "dir-1",
		ContentMode: filejanitor.ContentModeMetadataOnly, Paused: *req.Paused, DailyScanLocalTime: "09:00",
		SetupCompletedAt: time.Unix(100, 0), UpdatedAt: time.Unix(100, 0),
		LastFolderGrant: &filejanitor.FolderGrantReceipt{RunID: req.Operation.RunID, OperationID: req.Operation.OperationID, RootID: "root-1"},
	}
	j.world.settings[req.WorkspaceID] = settings
	j.world.writes = append(j.world.writes, "grant")
	return filejanitor.Status{Settings: settings}, nil
}

func (j *fakeInboxJanitor) Status(workspaceID string) (filejanitor.Status, error) {
	return filejanitor.Status{Settings: j.world.settings[workspaceID]}, nil
}

func (j *fakeInboxJanitor) RecordManualSetupDecision(workspaceID, rootID string, reviewedAt time.Time) (filejanitor.Status, error) {
	j.decisions++
	if j.decisionErr != nil {
		return filejanitor.Status{}, j.decisionErr
	}
	settings := j.world.settings[workspaceID]
	if settings.RootID != rootID || !settings.UpdatedAt.Equal(reviewedAt) || !settings.AutomationApprovedAt.IsZero() {
		return filejanitor.Status{}, errors.New("settings changed")
	}
	settings.SetupDecision = &filejanitor.SetupDecision{Mode: filejanitor.SetupModeManual, RootID: rootID, DecidedAt: time.Unix(200, 0)}
	j.world.settings[workspaceID] = settings
	j.world.writes = append(j.world.writes, "decision")
	return filejanitor.Status{Settings: settings}, nil
}

func (j *fakeInboxJanitor) ScanOperationReceipt(workspaceID, operationID string) (filejanitor.ScanOperationReceipt, bool, error) {
	receipt, found := j.world.receipts[workspaceID+"|"+operationID]
	return receipt, found, nil
}

func (j *fakeInboxJanitor) ListBatches(string) ([]filejanitor.JanitorBatch, error) {
	return j.world.batches, nil
}

type fakeInboxReviewer struct {
	world      *fakeInboxWorld
	eligible   int
	review     int
	prepareErr error
	prepared   []bool
}

func (r *fakeInboxReviewer) ReviewMonitoring(_ context.Context, _, workspaceID string) (assistantsetup.MonitoringFacts, error) {
	settings := r.world.settings[workspaceID]
	return assistantsetup.MonitoringFacts{
		RootGenerationID: settings.RootID, SettingsRevision: "settings-1", AuthorityRevision: "authority-1",
		PrivacyMode: string(settings.ContentMode), DailyScanLocalTime: settings.DailyScanLocalTime, Timezone: "UTC",
		WasPaused: settings.Paused, WasApproved: !settings.AutomationApprovedAt.IsZero(),
	}, nil
}

func (r *fakeInboxReviewer) prepareFirstReview(_ context.Context, request assistantsetup.PrepareReviewRequest, monitoring bool) (assistantsetup.PrepareReviewResult, error) {
	r.prepared = append(r.prepared, monitoring)
	if r.prepareErr != nil {
		return assistantsetup.PrepareReviewResult{}, r.prepareErr
	}
	key := request.WorkspaceID + "|" + request.ScanOperation
	receipt, found := r.world.receipts[key]
	if !found {
		if !monitoring && !r.world.settings[request.WorkspaceID].ManualScansOnly() {
			return assistantsetup.PrepareReviewResult{}, errors.New("confirm manual scans first")
		}
		receipt = filejanitor.ScanOperationReceipt{
			OperationID: request.ScanOperation, RootID: request.Review.RootGenerationID, PrivacyMode: filejanitor.ContentModeMetadataOnly,
			Outcome: filejanitor.ScanOutcomeNoEligible, EligibleCount: r.eligible, IneligibleCount: 2,
		}
		if r.eligible > 0 {
			receipt.Outcome, receipt.BatchID = filejanitor.ScanOutcomeBatch, "batch-1"
			r.world.batches = append(r.world.batches, filejanitor.JanitorBatch{ID: "batch-1", Summary: filejanitor.BatchSummary{Proposed: r.eligible, NeedsReview: r.review}})
		}
		r.world.receipts[key] = receipt
		r.world.writes = append(r.world.writes, "scan")
	}
	return assistantsetup.PrepareReviewResult{ScanOutcome: receipt.Outcome, BatchID: receipt.BatchID, EligibleCount: receipt.EligibleCount, IneligibleCount: receipt.IneligibleCount}, nil
}

// inboxFixture is one inbox folder on disk and a host wired to fakes.
type inboxFixture struct {
	host     *folderSetupHost
	world    *fakeInboxWorld
	team     *fakeInboxTeam
	janitor  *fakeInboxJanitor
	reviewer *fakeInboxReviewer
	path     string
	identity string
	offer    personalassistant.FolderOffer
}

func newInboxFixture(t *testing.T, action string) *inboxFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Downloads")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := projectlibrary.DirectoryIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	world := newFakeInboxWorld()
	role := sessionhttp.ReviewedTemplateRole{RoleID: "file-curator", Name: "File Curator", Action: action, Provider: "openai", Model: "gpt-5", ModelConfigured: true, ConfigDigest: "config-1"}
	team := &fakeInboxTeam{
		world: world, saved: map[string]string{}, taken: map[string]bool{},
		plan: sessionhttp.ReviewedTemplateCreationPlan{BlueprintID: "file-janitor", BlueprintVersion: 5, BlueprintDigest: "blueprint-1", PlanRevision: "plan-1", Roles: []sessionhttp.ReviewedTemplateRole{role}},
		roster: sessionhttp.FolderOfferRoster{Assistant: "File Curator", Action: action, Role: "specialist",
			Goal: "Prepare reviewable filing batches for the approved folder, explain uncertain classifications, and require approval before moving anything."},
	}
	if action == "reuse" {
		team.saved["File Curator"] = "gpt-5-mini"
	}
	janitor := &fakeInboxJanitor{world: world}
	reviewer := &fakeInboxReviewer{world: world, eligible: 23, review: 4}
	host := &folderSetupHost{builder: &ServerBuilder{}, inbox: &inboxCollaborators{
		team: team, janitor: janitor, review: reviewer,
		workspace: func(id string) (*workspace.Workspace, error) {
			if ws, ok := world.workspaces[id]; ok {
				return ws, nil
			}
			return nil, errors.New("not found")
		},
	}}
	offer := personalassistant.FolderOffer{
		ID: "offer-1", Status: personalassistant.FolderOfferPending, FolderName: "Downloads",
		Verdict: string(folderdigest.KindProject),
		Subject: personalassistant.FolderCandidateRecord{
			Key: personalassistant.FolderKey(path), Name: "Downloads", Kind: personalassistant.FolderChoiceProject,
			IsRoot: true, Shape: string(folderdigest.ShapeInbox),
		},
		ConversationReview: &personalassistant.FolderConversationReview{
			Target:        foldercontext.Target{UserID: "local", WorkspaceID: "hq", AgentName: "Assistant", ConversationID: "conversation-1"},
			ObservationID: "observation-1", CandidateID: "candidate-0", CandidateIdentity: identity,
		},
	}
	return &inboxFixture{host: host, world: world, team: team, janitor: janitor, reviewer: reviewer, path: path, identity: identity, offer: offer}
}

func (f *inboxFixture) plan(t *testing.T) personalassistant.FolderSetupPlan {
	t.Helper()
	plan, err := f.host.Plan(context.Background(), personalassistant.FolderSetupRequest{UserID: "local", Offer: f.offer})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

// run carries out one pass, as the service would, and returns the last update.
// The run id the pass saves is kept on the offer, as recordSetup keeps it.
func (f *inboxFixture) run(t *testing.T, plan personalassistant.FolderSetupPlan) (personalassistant.FolderSetupUpdate, error) {
	t.Helper()
	if f.offer.Setup == nil {
		f.offer.Setup = &personalassistant.FolderSetupRun{PlanDigest: plan.Digest, Intent: plan.Intent}
	}
	var last personalassistant.FolderSetupUpdate
	err := f.host.Run(context.Background(), personalassistant.FolderSetupRequest{
		UserID: "local", Offer: f.offer, Path: f.path, Plan: plan, Brief: "Keep my downloads tidy.",
		Update: func(_ context.Context, update personalassistant.FolderSetupUpdate) error {
			if update.RunID != "" {
				f.offer.Setup.RunID = update.RunID
			}
			last = update
			return nil
		},
	})
	return last, err
}

func lineState(update personalassistant.FolderSetupUpdate, kind string) personalassistant.FolderPlanLine {
	for _, line := range update.Lines {
		if line.Kind == kind {
			return line
		}
	}
	return personalassistant.FolderPlanLine{}
}

func TestInboxRun_CreatesTheWorkspaceGrantsTheFolderAndPreparesTheFirstBatch(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	update, err := f.run(t, plan)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if update.Status != personalassistant.FolderSetupDone {
		t.Fatalf("status = %s (%s), lines %+v", update.Status, update.StopReason, update.Lines)
	}
	for _, kind := range []string{personalassistant.FolderPlanAgents, personalassistant.FolderPlanWorkspace, personalassistant.FolderPlanFolder, personalassistant.FolderPlanTask} {
		if lineState(update, kind).State != personalassistant.FolderLineDone {
			t.Errorf("%s line = %+v", kind, lineState(update, kind))
		}
	}
	if got := lineState(update, personalassistant.FolderPlanTask).Detail; got != "23 proposed" {
		t.Errorf("first look detail = %q", got)
	}
	if strings.Join(f.world.writes, ",") != "workspace,grant,decision,scan" {
		t.Fatalf("writes = %v, want the card's order", f.world.writes)
	}
	created := f.team.created[0]
	if created.Name != "Downloads" || created.Mission != "Keep my downloads tidy." || created.Descriptor.RunID != f.offer.Setup.RunID ||
		created.Descriptor.ReviewDigest != plan.Digest || created.Plan.Roles[0].Action != "create" {
		t.Fatalf("create request = %+v", created)
	}
	grant := f.janitor.grants[0]
	if grant.Path != f.path || grant.Paused == nil || !*grant.Paused || grant.Operation == nil || grant.Operation.RunID != f.offer.Setup.RunID {
		t.Fatalf("grant = %+v", grant)
	}
	settings := f.world.settings[created.Descriptor.WorkspaceID]
	if !settings.AutomationApprovedAt.IsZero() || !settings.Paused || !settings.ManualScansOnly() {
		t.Fatalf("monitoring was not left unapproved: %+v", settings)
	}
	if len(f.reviewer.prepared) != 1 || f.reviewer.prepared[0] {
		t.Fatalf("first review prepared with monitoring = %v", f.reviewer.prepared)
	}

	// The verifier reads the same state back, with the batch's counts.
	f.offer.Setup.Status = personalassistant.FolderSetupDone
	verified, err := f.host.inboxProject(context.Background(), "local", f.offer, f.path, f.identity)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	batch := verified.FirstBatch
	if batch == nil || batch.Outcome != personalassistant.FolderFirstBatchReady || batch.BatchID != "batch-1" || batch.Eligible != 23 || batch.NeedsReview != 4 ||
		batch.Route != "/workspaces/downloads?panel=file-janitor&batch_id=batch-1" {
		t.Fatalf("first batch = %+v", batch)
	}
	if verified.Route != "/workspaces/downloads" || !verified.Created {
		t.Fatalf("verified = %+v", verified)
	}
	kinds := []string{}
	for _, row := range verified.Receipt {
		kinds = append(kinds, row.Kind+"="+row.Detail)
		if strings.Contains(row.Name+row.Detail, f.path) {
			t.Fatalf("receipt row names a path: %+v", row)
		}
	}
	if got := strings.Join(kinds, "|"); got != "agent=file curator, added|workspace=created|folder=linked where it is; names, types, sizes and dates only; Filed is inside it|batch=23 proposed · 4 need review" {
		t.Fatalf("receipt = %s", got)
	}
}

func TestInboxRun_ReplayOfASettledRunWritesNothingAgain(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	if _, err := f.run(t, plan); err != nil {
		t.Fatal(err)
	}
	writes := len(f.world.writes)
	// The roster now reads "reuse": File Curator exists. The replay is held to
	// the reviewed intent and observes the workspace it already made.
	f.team.plan.Roles[0].Action = "reuse"
	update, err := f.run(t, plan)
	if err != nil || update.Status != personalassistant.FolderSetupDone {
		t.Fatalf("replay = %+v, %v", update, err)
	}
	if len(f.world.writes) != writes {
		t.Fatalf("replay wrote again: %v", f.world.writes)
	}
	if len(f.team.created) != 2 || f.team.created[0].Descriptor.WorkspaceID != f.team.created[1].Descriptor.WorkspaceID {
		t.Fatalf("replay used a different workspace id: %+v", f.team.created)
	}
}

func TestInboxRun_RefusesBeforeAnyWrite(t *testing.T) {
	cases := map[string]func(f *inboxFixture){
		"another File Janitor tidies it": func(f *inboxFixture) {
			f.janitor.owner = &filejanitor.RootOwner{WorkspaceID: "other", WorkspaceName: "Old Downloads"}
		},
		"File Janitor refuses the root": func(f *inboxFixture) {
			f.janitor.reviewErr = &filejanitor.SetupError{Code: filejanitor.CodeInvalidPath, Message: "Choose a folder inside your home folder."}
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInboxFixture(t, "create")
			plan := f.plan(t)
			arrange(f)
			update, err := f.run(t, plan)
			if err != nil {
				t.Fatal(err)
			}
			if update.Status != personalassistant.FolderSetupStopped || len(f.world.writes) != 0 || len(f.team.created) != 0 || len(f.janitor.grants) != 0 {
				t.Fatalf("status %s, writes %v, created %d, grants %d", update.Status, f.world.writes, len(f.team.created), len(f.janitor.grants))
			}
		})
	}
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	f.janitor.reviewErr = &filejanitor.SetupError{Code: filejanitor.CodeInvalidPath, Message: "Choose a folder inside your home folder."}
	update, _ := f.run(t, plan)
	if got := lineState(update, personalassistant.FolderPlanFolder).Detail; got != "Choose a folder inside your home folder." {
		t.Fatalf("refusal detail = %q", got)
	}
}

func TestInboxRun_AFailureStopsAtItsStepAndKeepsWhatFinished(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(f *inboxFixture)
		stopped string
		writes  string
	}{
		{"create", func(f *inboxFixture) { f.team.createErr = errors.New("disk full") }, personalassistant.FolderPlanWorkspace, ""},
		{"plan changed", func(f *inboxFixture) { f.team.plan.PlanRevision = "plan-2" }, personalassistant.FolderPlanAgents, ""},
		{"grant", func(f *inboxFixture) { f.janitor.grantErr = errors.New("permission denied") }, personalassistant.FolderPlanFolder, "workspace"},
		{"decision", func(f *inboxFixture) { f.janitor.decisionErr = errors.New("busy") }, personalassistant.FolderPlanTask, "workspace,grant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newInboxFixture(t, "create")
			plan := f.plan(t)
			tc.arrange(f)
			update, err := f.run(t, plan)
			if err != nil {
				t.Fatal(err)
			}
			if update.Status != personalassistant.FolderSetupStopped || lineState(update, tc.stopped).State != personalassistant.FolderLineFailed {
				t.Fatalf("status %s (%s), %s line %+v", update.Status, update.StopReason, tc.stopped, lineState(update, tc.stopped))
			}
			if got := strings.Join(f.world.writes, ","); got != tc.writes {
				t.Fatalf("writes = %q, want %q", got, tc.writes)
			}
			// The retry starts from saved state and finishes without a second
			// workspace or grant.
			f.team.createErr, f.janitor.grantErr, f.janitor.decisionErr, f.reviewer.prepareErr = nil, nil, nil, nil
			if tc.name == "plan changed" {
				return // a changed plan is reviewed again, never resumed
			}
			update, err = f.run(t, plan)
			if err != nil || update.Status != personalassistant.FolderSetupDone {
				t.Fatalf("retry = %+v, %v", update, err)
			}
			if got := strings.Join(f.world.writes, ","); got != "workspace,grant,decision,scan" {
				t.Fatalf("writes after retry = %q", got)
			}
		})
	}
}

// A first scan that does not complete does not undo the hire (FR20): the
// setup finishes with the workspace, File Curator and the link, the first batch
// is a failed one, and Retry runs the scan alone under an attempt of its own.
func TestInboxRun_AFirstScanThatDidNotCompleteIsRetriedAlone(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	f.reviewer.prepareErr = errors.New("scan failed")
	update, err := f.run(t, plan)
	if err != nil {
		t.Fatal(err)
	}
	task := lineState(update, personalassistant.FolderPlanTask)
	if update.Status != personalassistant.FolderSetupDone || task.State != personalassistant.FolderLineFailed || !strings.Contains(task.Detail, "did not complete") {
		t.Fatalf("status %s, task line %+v", update.Status, task)
	}
	if got := strings.Join(f.world.writes, ","); got != "workspace,grant,decision" {
		t.Fatalf("writes = %q", got)
	}
	f.offer.Setup.Status = personalassistant.FolderSetupDone
	verified, err := f.host.inboxProject(context.Background(), "local", f.offer, f.path, f.identity)
	if err != nil {
		t.Fatalf("a setup whose scan failed must still verify: %v", err)
	}
	if verified.FirstBatch == nil || verified.FirstBatch.Outcome != personalassistant.FolderFirstBatchFailed ||
		verified.FirstBatch.Route != "/workspaces/downloads?panel=file-janitor" {
		t.Fatalf("first batch = %+v", verified.FirstBatch)
	}
	if last := verified.Receipt[len(verified.Receipt)-1]; last.Kind != "batch" || !strings.Contains(last.Detail, "did not complete") {
		t.Fatalf("receipt batch row = %+v", last)
	}

	// Retry, still failing: nothing but the scan is attempted, under its own id.
	retried, err := f.host.inboxRetryFirstBatch(context.Background(), "local", f.offer, 1)
	if err != nil || retried.Outcome != personalassistant.FolderFirstBatchFailed {
		t.Fatalf("failing retry = %+v, %v", retried, err)
	}
	// Retry once the scan can run.
	f.reviewer.prepareErr = nil
	retried, err = f.host.inboxRetryFirstBatch(context.Background(), "local", f.offer, 2)
	if err != nil || retried.Outcome != personalassistant.FolderFirstBatchReady || retried.Eligible != 23 || retried.NeedsReview != 4 {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
	if got := strings.Join(f.world.writes, ","); got != "workspace,grant,decision,scan" {
		t.Fatalf("writes after retry = %q (no second workspace, grant or decision)", got)
	}
	if _, found := f.world.receipts[inboxOperationIDs(f.offer.Setup.RunID).workspace+"|"+inboxOperationIDs(f.offer.Setup.RunID).scanAttempt(2)]; !found {
		t.Fatal("the retry did not scan under its own attempt's operation")
	}
	if len(f.team.created) != 1 || len(f.janitor.grants) != 1 {
		t.Fatalf("retry created %d / granted %d", len(f.team.created), len(f.janitor.grants))
	}
	// A folder replaced since is not scanned.
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.inboxRetryFirstBatch(context.Background(), "local", f.offer, 3); err == nil {
		t.Fatal("a replaced folder was scanned")
	}
}

func TestInboxRun_RefusesAStaleOrForeignRecipe(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	stale := plan
	stale.Intent.RecipeVersion = 99
	if err := f.host.Run(context.Background(), personalassistant.FolderSetupRequest{UserID: "local", Offer: f.offer, Path: f.path, Plan: stale,
		Update: func(context.Context, personalassistant.FolderSetupUpdate) error { return nil }}); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("stale recipe: %v", err)
	}
	foreign := plan
	foreign.Intent.Recipe = folderdigest.BuiltinSoftwareProject
	if err := f.host.Run(context.Background(), personalassistant.FolderSetupRequest{UserID: "local", Offer: f.offer, Path: f.path, Plan: foreign,
		Update: func(context.Context, personalassistant.FolderSetupUpdate) error { return nil }}); !errors.Is(err, errSetupUnavailable) {
		t.Fatalf("foreign recipe: %v", err)
	}
	if len(f.world.writes) != 0 {
		t.Fatalf("a refused recipe wrote: %v", f.world.writes)
	}
}

func TestInboxRun_AFolderReplacedSinceTheReviewNeedsAFreshPick(t *testing.T) {
	f := newInboxFixture(t, "create")
	plan := f.plan(t)
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0o750); err != nil {
		t.Fatal(err)
	}
	update, err := f.run(t, plan)
	if err != nil || update.StopReason != personalassistant.FolderStopNeedsPick || len(f.world.writes) != 0 {
		t.Fatalf("update = %+v, err %v, writes %v", update, err, f.world.writes)
	}
}
