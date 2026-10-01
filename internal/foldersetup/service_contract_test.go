package foldersetup

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These tests run the runner against the REAL setupjourney.Service, so the
// revision, review-token and input-digest rules it depends on are the journey's
// own. Only the owners behind each step (the project, the workspace mode and the
// staffing) are synthetic: stateful adapters that record what they were asked.

const (
	contractUser   = "local"
	contractFolder = "/Users/me/Songs/My Song"
	contractPlugin = "ori-reaper"
	contractQuest  = "reaper_setup"
)

// owners is the synthetic state behind the journey's three steps.
type owners struct {
	project, mode, staffed bool
	// The child run of a further project has its own owners, as in the real
	// journey where project, mode and team belong to each project.
	childProject, childMode, childStaffed bool
	// shownFolder lets a test make the project review disclose another folder.
	shownFolder string
	commits     []string
	requests    []projectconnection.Request
	staffInput  []json.RawMessage
}

// flags selects the project, mode and team state of the run being driven.
func (o *owners) flags(kind setupjourney.RunKind) (project, mode, staffed *bool) {
	if kind == setupjourney.RunKindChild {
		return &o.childProject, &o.childMode, &o.childStaffed
	}
	return &o.project, &o.mode, &o.staffed
}

type contractCatalog struct{ declaration specialist.SetupJourney }

func (c contractCatalog) key() setupjourney.QuestKey {
	return setupjourney.QuestKey{Source: setupjourney.QuestSourcePlugin, PluginID: contractPlugin, ID: contractQuest}
}

func (c contractCatalog) List(context.Context) ([]setupjourney.QuestSummary, error) {
	return []setupjourney.QuestSummary{{QuestKey: c.key(), Title: c.declaration.Title, TemplateID: "plugin:" + contractPlugin + ":reaper-song"}}, nil
}

func (c contractCatalog) Lookup(_ context.Context, key setupjourney.QuestKey) (setupjourney.QuestDefinition, error) {
	if key.PluginID != contractPlugin || key.ID != contractQuest {
		return setupjourney.QuestDefinition{}, errors.New("unknown quest")
	}
	declaration := c.declaration
	return setupjourney.QuestDefinition{Key: c.key(), Declaration: &declaration, Ownership: "plugin"}, nil
}

type contractRelationships struct{}

func (contractRelationships) GetState(context.Context, string) (*personalassistant.State, error) {
	return &personalassistant.State{
		UserID: contractUser, AssistantID: "assistant-1", Status: personalassistant.StatusActive,
		SpecialistOfferState: personalassistant.SpecialistOfferAccepted, SpecialistSlug: "music_production",
	}, nil
}

// stepAdapter is one synthetic owner: a reader and a review/commit adapter.
type stepAdapter struct {
	owners *owners
	kind   specialist.SetupStepKind
}

func (a stepAdapter) InputDigest(_ setupjourney.ActionID, input json.RawMessage) (string, error) {
	return setupjourney.Digest(input), nil
}

func (a stepAdapter) material(commit setupjourney.ActionID, input json.RawMessage) setupjourney.ActionReviewMaterial {
	m := setupjourney.ActionReviewMaterial{
		CommitAction: commit, InputDigest: setupjourney.Digest(input),
		OwnerRevisionDigest: setupjourney.Digest([]byte("owner-" + string(a.kind))),
		DisclosureDigest:    setupjourney.Digest([]byte("disclosure-" + string(commit))),
	}
	o := a.owners
	switch a.kind {
	case specialist.SetupStepProjectConnect:
		folder := contractFolder
		if o.shownFolder != "" {
			folder = o.shownFolder
		}
		m.ProjectConnection = &projectconnection.Projection{
			ModeID: projecttemplates.ProjectConnectionExistingProject, WorkspaceName: "My Song", SelectedFolder: folder,
			EntryName: "My Song.rpp", GroupRequirementState: "ready_standalone", GroupComposition: "standalone",
		}
	case specialist.SetupStepWorkspaceSetup:
		// As the real adapter does, the review leaves mode_id empty until the
		// commit selects the mode.
		m.WorkspaceSetup = &setupjourney.WorkspaceSetupProjection{ModeLabel: "File-only", FilesConnected: true}
	case specialist.SetupStepAssistantProgramStaffing:
		var request struct {
			Roles []staffedRole `json:"roles"`
		}
		_ = json.Unmarshal(input, &request)
		roles := make([]setupjourney.StaffingRoleProjection, 0, len(request.Roles))
		for _, role := range request.Roles {
			roles = append(roles, setupjourney.StaffingRoleProjection{
				RoleID: role.RoleID, Label: "Producer", Required: true, ProfileName: role.Name,
			})
		}
		m.Staffing = &setupjourney.StaffingProjection{Scopes: []setupjourney.StaffingScopeProjection{{
			Scope: workspace.AssistantRoleScopeProject, WorkspaceID: "proj-1", WorkspaceLabel: "My Song",
			ModelsReady: true, Roles: roles,
		}}}
	}
	return m
}

func (a stepAdapter) commitFor(review setupjourney.ActionID) setupjourney.ActionID {
	return map[setupjourney.ActionID]setupjourney.ActionID{
		setupjourney.ActionReviewExistingProject: setupjourney.ActionConnectExistingProject,
		setupjourney.ActionReviewFileOnlyMode:    setupjourney.ActionSelectFileOnlyMode,
		setupjourney.ActionReviewProjectStaffing: setupjourney.ActionAddProjectStaffing,
	}[review]
}

func (a stepAdapter) Review(_ context.Context, _ setupjourney.ReadScope, action setupjourney.ActionID, input json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	commit := a.commitFor(action)
	if commit == "" {
		return setupjourney.ActionReviewMaterial{}, setupjourney.ErrInvalid
	}
	return a.material(commit, input), nil
}

func (a stepAdapter) PrepareCommit(_ context.Context, _ setupjourney.ReadScope, action setupjourney.ActionID, input json.RawMessage) (setupjourney.ActionReviewMaterial, error) {
	return a.material(action, input), nil
}

func (a stepAdapter) Commit(_ context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, input json.RawMessage, _ setupjourney.ActionReviewMaterial) (setupjourney.CanonicalResult, error) {
	o := a.owners
	project, mode, staffed := o.flags(scope.RunKind)
	o.commits = append(o.commits, string(action))
	switch a.kind {
	case specialist.SetupStepProjectConnect:
		var request projectconnection.Request
		_ = json.Unmarshal(input, &request)
		o.requests = append(o.requests, request)
		*project = true
		return setupjourney.CanonicalResult{ProjectWorkspaceID: projectID(scope.RunKind)}, nil
	case specialist.SetupStepWorkspaceSetup:
		*mode = true
	case specialist.SetupStepAssistantProgramStaffing:
		o.staffInput = append(o.staffInput, input)
		*staffed = true
	}
	return setupjourney.CanonicalResult{}, nil
}

func projectID(kind setupjourney.RunKind) string {
	if kind == setupjourney.RunKindChild {
		return "proj-2"
	}
	return "proj-1"
}

func (a stepAdapter) ConsequenceObserved(_ setupjourney.ActionID, read setupjourney.CanonicalStepRead) bool {
	return read.Complete
}

func (a stepAdapter) Read(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	project, mode, staffed := a.owners.flags(scope.RunKind)
	switch a.kind {
	case specialist.SetupStepProjectConnect:
		if *project {
			return setupjourney.CanonicalStepRead{Complete: true, AvailableActions: []setupjourney.ActionID{setupjourney.ActionOpenProject},
				Result: setupjourney.CanonicalResult{ProjectWorkspaceID: projectID(scope.RunKind)}}, nil
		}
		return setupjourney.CanonicalStepRead{
			AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewExistingProject},
			Preparation: &projectconnection.HomePreparation{
				Name: "Home", TemplateID: "plugin:" + contractPlugin + ":reaper-song",
				GroupPolicy: "none", AvailableCompositions: []string{"standalone"},
			},
		}, nil
	case specialist.SetupStepWorkspaceSetup:
		if *mode {
			return setupjourney.CanonicalStepRead{Complete: true, AvailableActions: []setupjourney.ActionID{setupjourney.ActionOpenProject},
				WorkspaceSetup: &setupjourney.WorkspaceSetupProjection{ModeID: fileOnlyModeID, ModeLabel: "File-only", FilesConnected: true}}, nil
		}
		return setupjourney.CanonicalStepRead{AvailableActions: []setupjourney.ActionID{setupjourney.ActionReviewFileOnlyMode}}, nil
	case specialist.SetupStepAssistantProgramStaffing:
		role := setupjourney.StaffingRoleProjection{RoleID: "producer", Label: "Producer", Required: true, Configured: *staffed}
		projection := &setupjourney.StaffingProjection{Scopes: []setupjourney.StaffingScopeProjection{
			{Scope: workspace.AssistantRoleScopeHome, WorkspaceID: "home-1", WorkspaceLabel: "Home", RequiredComplete: true},
			{Scope: workspace.AssistantRoleScopeProject, WorkspaceID: projectID(scope.RunKind), WorkspaceLabel: "My Song",
				RequiredComplete: *staffed, Roles: []setupjourney.StaffingRoleProjection{role}},
		}}
		if *staffed {
			return setupjourney.CanonicalStepRead{Complete: true, Staffing: projection,
				AvailableActions: []setupjourney.ActionID{setupjourney.ActionOpenHomeStaffing, setupjourney.ActionOpenProjectStaffing}}, nil
		}
		return setupjourney.CanonicalStepRead{Staffing: projection,
			AvailableActions: []setupjourney.ActionID{setupjourney.ActionOpenHomeStaffing, setupjourney.ActionReviewProjectStaffing}}, nil
	}
	return setupjourney.CanonicalStepRead{}, nil
}

// contractService builds a real Service, scoped to the plugin's quest.
func contractService(t *testing.T, o *owners) *setupjourney.Service {
	t.Helper()
	entry, ok := reviewedintegration.Get("ori_reaper")
	if !ok {
		t.Fatal("reviewed REAPER integration is not registered")
	}
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	readers := map[specialist.SetupStepKind]setupjourney.CanonicalReader{}
	for _, kind := range specialist.SetupStepKinds() {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{}, nil
		})
	}
	readers[specialist.SetupStepIntegrationInstall] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		return setupjourney.CanonicalStepRead{Complete: true, Result: setupjourney.CanonicalResult{
			IntegrationPluginID: entry.PluginID, IntegrationVersion: entry.MinimumVersion}}, nil
	})
	for _, kind := range []specialist.SetupStepKind{
		specialist.SetupStepProjectConnect, specialist.SetupStepWorkspaceSetup, specialist.SetupStepAssistantProgramStaffing,
	} {
		readers[kind] = stepAdapter{owners: o, kind: kind}
	}
	readers[specialist.SetupStepSummary] = setupjourney.CanonicalReaderFunc(func(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
		// A finished root offers to connect another project, which is what lets a
		// further folder have its own child run.
		actions := []setupjourney.ActionID{setupjourney.ActionReviewSetup}
		if scope.RunKind == setupjourney.RunKindRoot {
			actions = append(actions, setupjourney.ActionConnectAnotherProject)
		}
		return setupjourney.CanonicalStepRead{AvailableActions: actions}, nil
	})
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := setupjourney.NewService(setupjourney.NewSQLiteStore(db), contractRelationships{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []specialist.SetupStepKind{
		specialist.SetupStepProjectConnect, specialist.SetupStepWorkspaceSetup, specialist.SetupStepAssistantProgramStaffing,
	} {
		if err := service.SetActionAdapter(kind, stepAdapter{owners: o, kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	service.SetQuestCatalog(contractCatalog{declaration: specialist.SetupJourney{
		OwnerPluginID: contractPlugin, SchemaVersion: specialist.SetupJourneySchemaVersion, Version: 1, ID: contractQuest,
		Title: "Set up REAPER", Description: "Connect a REAPER project.",
		IntegrationKey: "ori_reaper", ExpectedBlueprintID: "reaper-song", ExpectedAssistantProgramID: "music-producer-assistant",
		Steps: []specialist.SetupJourneyStep{
			{ID: "project", Kind: specialist.SetupStepProjectConnect, Title: "Connect a project", Description: "Connect a project."},
			{ID: "workspace", Kind: specialist.SetupStepWorkspaceSetup, Title: "Choose how Ori works", Description: "Choose a mode."},
			{ID: "staffing", Kind: specialist.SetupStepAssistantProgramStaffing, Title: "Add your team", Description: "Add roles."},
			{ID: "summary", Kind: specialist.SetupStepSummary, Title: "Review setup", Description: "Review setup."},
		},
		WorkspaceLaunch: &specialist.WorkspaceLaunchCopy{GroupTitle: "Group", GroupName: "Group"},
	}})
	scoped, err := service.ForQuest(context.Background(), contractUser, contractPlugin, contractQuest)
	if err != nil {
		t.Fatal(err)
	}
	return scoped
}

type contractJourney struct{ service *setupjourney.Service }

func (j contractJourney) Read(ctx context.Context, runID string) (*setupjourney.JourneyProjection, error) {
	return j.service.Read(ctx, contractUser, runID)
}

func (j contractJourney) Child(ctx context.Context, rootRevision int64, key string) (*setupjourney.JourneyProjection, error) {
	return j.service.CreateOrResumeChild(ctx, contractUser, setupjourney.PresentationMutation{IfRevision: rootRevision, IdempotencyKey: key})
}

func (j contractJourney) Mutate(ctx context.Context, runID string, action setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	return j.service.Mutate(ctx, contractUser, runID, action, request)
}

func contractRun(t *testing.T, service *setupjourney.Service) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{Journey: contractJourney{service}, Selections: fakeSelections{}, Progress: progress}
	result, err := runner.Run(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func TestRealJourneyIsDrivenToReadyThroughReviewAndCommit(t *testing.T) {
	o := &owners{}
	service := contractService(t, o)
	result, progress := contractRun(t, service)
	if result.Status != personalassistant.FolderSetupDone || result.StopReason != "" {
		t.Fatalf("result = %+v last = %+v", result, progress.last())
	}
	want := []string{"connect_existing_project", "select_file_only_mode", "add_project_staffing"}
	if len(o.commits) != len(want) {
		t.Fatalf("commits = %v, want %v", o.commits, want)
	}
	for i := range want {
		if o.commits[i] != want[i] {
			t.Fatalf("commits = %v, want %v", o.commits, want)
		}
	}
	if len(o.requests) != 1 {
		t.Fatalf("project requests = %+v", o.requests)
	}
	request := o.requests[0]
	if request.ModeID != projecttemplates.ProjectConnectionExistingProject || request.SelectionToken != "token" ||
		request.WorkspaceName != "My Song" || request.GroupComposition != "standalone" {
		t.Fatalf("the project step got %+v", request)
	}
	var staffing struct {
		Roles []staffedRole `json:"roles"`
	}
	if err := json.Unmarshal(o.staffInput[0], &staffing); err != nil || len(staffing.Roles) != 1 ||
		staffing.Roles[0].RoleID != "producer" || staffing.Roles[0].Name != "Producer · My Song" {
		t.Fatalf("staffing input = %s", o.staffInput[0])
	}
	journey, err := service.Read(context.Background(), contractUser, "")
	if err != nil || journey.Lifecycle != setupjourney.LifecycleReady || journey.Receipts.ProjectWorkspaceID != "proj-1" {
		t.Fatalf("journey = %+v, %v", journey, err)
	}
	if result.RunID != journey.RunID {
		t.Fatalf("run id = %q, want %q", result.RunID, journey.RunID)
	}
}

func TestRealJourneyResumeOfAFinishedRunRepeatsNothing(t *testing.T) {
	o := &owners{}
	service := contractService(t, o)
	first, _ := contractRun(t, service)
	if first.Status != personalassistant.FolderSetupDone {
		t.Fatalf("first run = %+v", first)
	}
	before := len(o.commits)
	cfg := testConfig()
	cfg.RunID = first.RunID // what a resume carries
	runner := &Runner{Journey: contractJourney{service}, Selections: fakeSelections{}, Progress: &recorder{}}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil || result.Status != personalassistant.FolderSetupDone || len(o.commits) != before {
		t.Fatalf("resume = %+v, %v, commits %d -> %d", result, err, before, len(o.commits))
	}
}

func TestRealJourneyConnectsAFurtherProjectOnItsOwnChildRun(t *testing.T) {
	o := &owners{}
	service := contractService(t, o)
	first, _ := contractRun(t, service)
	if first.Status != personalassistant.FolderSetupDone {
		t.Fatalf("first project = %+v cause=%v", first, first.Cause)
	}
	// The first project stays done; the second folder's child run starts empty.
	o.commits = nil
	second, _ := contractRun(t, service)
	if second.Status != personalassistant.FolderSetupDone || second.RunID == "" || second.RunID == first.RunID {
		t.Fatalf("second project = %+v cause=%v (first run %q)", second, second.Cause, first.RunID)
	}
	if len(o.commits) != 3 {
		t.Fatalf("commits = %v", o.commits)
	}
	child, err := service.Read(context.Background(), contractUser, second.RunID)
	if err != nil || child.RunKind != setupjourney.RunKindChild || child.Lifecycle != setupjourney.LifecycleReady {
		t.Fatalf("child journey = %+v, %v", child, err)
	}
	// The same run id is what a resume would read, and it repeats nothing.
	progress := &recorder{}
	runner := &Runner{Journey: contractJourney{service}, Selections: fakeSelections{}, Progress: progress}
	cfg := testConfig()
	cfg.RunID = second.RunID
	before := len(o.commits)
	if result, err := runner.Run(context.Background(), cfg); err != nil || result.Status != personalassistant.FolderSetupDone || len(o.commits) != before {
		t.Fatalf("resume = %+v, %v, commits %d -> %d", result, err, before, len(o.commits))
	}
}

func TestRealJourneyResumesAfterAnEarlyStop(t *testing.T) {
	o := &owners{shownFolder: "/Users/me/Somewhere Else"}
	service := contractService(t, o)
	result, _ := contractRun(t, service)
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopPlanChanged || len(o.commits) != 0 {
		t.Fatalf("a different folder must stop before any commit: %+v commits=%v", result, o.commits)
	}
	o.shownFolder = ""
	result, _ = contractRun(t, service)
	if result.Status != personalassistant.FolderSetupDone || len(o.commits) != 3 {
		t.Fatalf("resume = %+v commits=%v", result, o.commits)
	}
}
