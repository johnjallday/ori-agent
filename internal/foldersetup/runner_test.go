package foldersetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const testFolder = "/Users/me/Songs/My Song"

// fakeJourney is a tiny stand-in for the setup journey: three steps that each
// complete when their commit lands, a revision that moves on every commit, and
// a record of every call so tests can prove what was (not) done.
type fakeJourney struct {
	revision int64
	done     map[specialist.SetupStepKind]bool
	calls    []string

	shownFolder     string
	shownEntry      string
	candidates      []string
	modeID          string
	modelsReady     bool
	commitErr       map[setupjourney.ActionID]error
	busy            bool
	staffingScope   workspace.AssistantRoleScope
	staffingBound   bool
	staffingOutRole string
}

func newFake() *fakeJourney {
	return &fakeJourney{
		revision: 1, done: map[specialist.SetupStepKind]bool{},
		shownFolder: testFolder, shownEntry: "My Song.rpp", modeID: fileOnlyModeID, modelsReady: true,
		staffingScope: workspace.AssistantRoleScopeProject, commitErr: map[setupjourney.ActionID]error{},
	}
}

var order = []specialist.SetupStepKind{
	specialist.SetupStepProjectConnect, specialist.SetupStepWorkspaceSetup, specialist.SetupStepAssistantProgramStaffing,
}

func (f *fakeJourney) Read(context.Context, string) (*setupjourney.JourneyProjection, error) {
	projection := &setupjourney.JourneyProjection{RunID: "run-1", StateRevision: f.revision, Busy: f.busy}
	ready := true
	for _, kind := range order {
		step := setupjourney.StepProjection{ID: string(kind), Kind: kind, Status: setupjourney.StepPending}
		if f.done[kind] {
			step.Status = setupjourney.StepComplete
		} else {
			ready = false
			switch kind {
			case specialist.SetupStepProjectConnect:
				step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewExistingProject}}
				step.Preparation = &projectconnection.HomePreparation{
					GroupPolicy: "none", AvailableCompositions: []string{"standalone"},
				}
			case specialist.SetupStepWorkspaceSetup:
				step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewFileOnlyMode}}
			case specialist.SetupStepAssistantProgramStaffing:
				step.Actions = []setupjourney.ActionDefinition{{ID: setupjourney.ActionReviewProjectStaffing}}
				step.Staffing = &setupjourney.StaffingProjection{Scopes: []setupjourney.StaffingScopeProjection{{
					Scope: workspace.AssistantRoleScopeProject, WorkspaceLabel: "My Song",
					Roles: []setupjourney.StaffingRoleProjection{{RoleID: "producer", Label: "Producer", Required: true}},
				}}}
			}
		}
		projection.Steps = append(projection.Steps, step)
	}
	if ready {
		projection.Lifecycle = setupjourney.LifecycleReady
	} else {
		projection.Lifecycle = setupjourney.LifecycleInProgress
	}
	return projection, nil
}

func (f *fakeJourney) Mutate(_ context.Context, _ string, action setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	f.calls = append(f.calls, string(action))
	switch action {
	case setupjourney.ActionReviewExistingProject:
		return &setupjourney.ActionResult{Review: &setupjourney.ReviewProjection{
			Token: "t-project", CommitAction: setupjourney.ActionConnectExistingProject,
			ProjectConnection: &projectconnection.Projection{
				ModeID: projecttemplates.ProjectConnectionExistingProject, WorkspaceName: "My Song",
				SelectedFolder: f.shownFolder, EntryName: f.shownEntry, EntryCandidates: f.candidates,
				GroupComposition: "standalone",
			},
		}}, nil
	case setupjourney.ActionReviewFileOnlyMode:
		return &setupjourney.ActionResult{Review: &setupjourney.ReviewProjection{
			Token: "t-mode", CommitAction: setupjourney.ActionSelectFileOnlyMode,
			WorkspaceSetup: &setupjourney.WorkspaceSetupProjection{ModeID: f.modeID},
		}}, nil
	case setupjourney.ActionReviewProjectStaffing:
		role := setupjourney.StaffingRoleProjection{RoleID: "producer", Label: "Producer", Required: true,
			ProfileName: "Producer · My Song", Bound: f.staffingBound}
		if f.staffingOutRole != "" {
			role.RoleID = f.staffingOutRole
		}
		return &setupjourney.ActionResult{Review: &setupjourney.ReviewProjection{
			Token: "t-staff", CommitAction: setupjourney.ActionAddProjectStaffing,
			Staffing: &setupjourney.StaffingProjection{Scopes: []setupjourney.StaffingScopeProjection{{
				Scope: f.staffingScope, ModelsReady: f.modelsReady, Roles: []setupjourney.StaffingRoleProjection{role},
			}}},
		}}, nil
	}
	if err := f.commitErr[action]; err != nil {
		return nil, err
	}
	kind := map[setupjourney.ActionID]specialist.SetupStepKind{
		setupjourney.ActionConnectExistingProject: specialist.SetupStepProjectConnect,
		setupjourney.ActionSelectFileOnlyMode:     specialist.SetupStepWorkspaceSetup,
		setupjourney.ActionAddProjectStaffing:     specialist.SetupStepAssistantProgramStaffing,
	}[action]
	f.done[kind] = true
	f.revision++
	return &setupjourney.ActionResult{}, nil
}

type fakeSelections struct{ err error }

func (s fakeSelections) Select(context.Context) (string, string, error) {
	return "token", testFolder, s.err
}

type recorder struct{ updates []Update }

func (r *recorder) Update(_ context.Context, update Update) error {
	r.updates = append(r.updates, update)
	return nil
}

func (r *recorder) last() Update { return r.updates[len(r.updates)-1] }

func testConfig() Config {
	return Config{
		WorkspaceName: "My Song",
		Plan: personalassistant.NewFolderSetupPlan([]personalassistant.FolderPlanLine{
			{Kind: personalassistant.FolderPlanIntegration, Name: "Already installed"},
			{Kind: personalassistant.FolderPlanWorkspace, Name: "Creates a workspace named My Song"},
			{Kind: personalassistant.FolderPlanFolder, Name: "Links My Song where it is"},
			{Kind: personalassistant.FolderPlanMode, Name: "Uses File-only mode"},
			{Kind: personalassistant.FolderPlanAgents, Name: "Adds the required agents"},
			{Kind: personalassistant.FolderPlanTask, Name: "Queues a first task"},
		}),
	}
}

func runWith(t *testing.T, journey *fakeJourney, selections Selections) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{Journey: journey, Selections: selections, Progress: progress}
	result, err := runner.Run(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func lineStates(update Update) map[string]string {
	states := map[string]string{}
	for _, line := range update.Lines {
		states[line.Kind] = line.State
	}
	return states
}

func TestRunHappyPathDrivesEveryStepInOrder(t *testing.T) {
	journey := newFake()
	result, progress := runWith(t, journey, fakeSelections{})
	if result.Status != personalassistant.FolderSetupDone || result.StopReason != "" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{
		"review_existing_project", "connect_existing_project",
		"review_file_only_mode", "select_file_only_mode",
		"review_project_staffing", "add_project_staffing",
	}
	if len(journey.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", journey.calls, want)
	}
	for i := range want {
		if journey.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", journey.calls, want)
		}
	}
	for kind, state := range lineStates(progress.last()) {
		if state != personalassistant.FolderLineDone {
			t.Errorf("line %s = %s, want done", kind, state)
		}
	}
	if result.RunID != "run-1" || progress.last().RunID != "run-1" {
		t.Fatalf("run id not recorded: %+v", result)
	}
}

func TestRunResumedSkipsFinishedSteps(t *testing.T) {
	journey := newFake()
	journey.done[specialist.SetupStepProjectConnect] = true
	journey.done[specialist.SetupStepWorkspaceSetup] = true
	result, _ := runWith(t, journey, fakeSelections{})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	if len(journey.calls) != 2 || journey.calls[0] != "review_project_staffing" {
		t.Fatalf("a finished step was repeated: %v", journey.calls)
	}
}

func TestRunStopsWhenADisclosureDiffersFromThePlan(t *testing.T) {
	connected := func(f *fakeJourney) {
		f.done[specialist.SetupStepProjectConnect] = true
		f.done[specialist.SetupStepWorkspaceSetup] = true
	}
	cases := []struct {
		name      string
		mutate    func(*fakeJourney)
		forbidden string
	}{
		{"another folder", func(f *fakeJourney) { f.shownFolder = "/Users/me/Other" }, "connect_existing_project"},
		{"another mode", func(f *fakeJourney) { f.done[specialist.SetupStepProjectConnect] = true; f.modeID = "live_control" }, "select_file_only_mode"},
		{"a bound agent", func(f *fakeJourney) { connected(f); f.staffingBound = true }, "add_project_staffing"},
		{"another role", func(f *fakeJourney) { connected(f); f.staffingOutRole = "mixer" }, "add_project_staffing"},
		{"another scope", func(f *fakeJourney) { connected(f); f.staffingScope = workspace.AssistantRoleScopeHome }, "add_project_staffing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			journey := newFake()
			tc.mutate(journey)
			result, _ := runWith(t, journey, fakeSelections{})
			if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopPlanChanged {
				t.Fatalf("result = %+v", result)
			}
			for _, call := range journey.calls {
				if call == tc.forbidden {
					t.Fatalf("committed something the plan did not describe: %v", journey.calls)
				}
			}
		})
	}
}

func TestRunCommitErrorStopsAndKeepsEarlierLinesDone(t *testing.T) {
	journey := newFake()
	journey.commitErr[setupjourney.ActionSelectFileOnlyMode] = errors.New("boom")
	result, progress := runWith(t, journey, fakeSelections{})
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopFailed {
		t.Fatalf("result = %+v", result)
	}
	states := lineStates(progress.last())
	if states[personalassistant.FolderPlanWorkspace] != personalassistant.FolderLineDone ||
		states[personalassistant.FolderPlanFolder] != personalassistant.FolderLineDone {
		t.Fatalf("earlier lines were not kept done: %v", states)
	}
	if states[personalassistant.FolderPlanMode] != personalassistant.FolderLineFailed {
		t.Fatalf("the failing line = %s, want failed", states[personalassistant.FolderPlanMode])
	}
	if states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineWaiting {
		t.Fatalf("a later line ran: %v", states)
	}
}

func TestRunStopReasons(t *testing.T) {
	t.Run("several project files", func(t *testing.T) {
		journey := newFake()
		journey.shownEntry = ""
		journey.candidates = []string{"A.rpp", "B.rpp", "../C.rpp"}
		result, progress := runWith(t, journey, fakeSelections{})
		if result.StopReason != personalassistant.FolderStopNeedsChoice {
			t.Fatalf("result = %+v", result)
		}
		got := progress.last().EntryCandidates
		if len(got) != 2 || got[0] != "A.rpp" || got[1] != "B.rpp" {
			t.Fatalf("candidates must be bare file names: %v", got)
		}
		for _, call := range journey.calls {
			if call == "connect_existing_project" {
				t.Fatal("committed without a chosen project file")
			}
		}
	})
	t.Run("no model", func(t *testing.T) {
		journey := newFake()
		journey.done[specialist.SetupStepProjectConnect] = true
		journey.done[specialist.SetupStepWorkspaceSetup] = true
		journey.modelsReady = false
		result, _ := runWith(t, journey, fakeSelections{})
		if result.StopReason != personalassistant.FolderStopNeedsModel {
			t.Fatalf("result = %+v", result)
		}
		for _, call := range journey.calls {
			if call == "add_project_staffing" {
				t.Fatal("staffed without a model")
			}
		}
	})
	t.Run("folder path lost", func(t *testing.T) {
		result, _ := runWith(t, newFake(), fakeSelections{err: ErrNeedsPick})
		if result.StopReason != personalassistant.FolderStopNeedsPick {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("a busy journey is not driven further", func(t *testing.T) {
		journey := newFake()
		journey.busy = true
		result, _ := runWith(t, journey, fakeSelections{})
		if result.StopReason != personalassistant.FolderStopFailed || len(journey.calls) != 0 {
			t.Fatalf("result = %+v calls = %v", result, journey.calls)
		}
	})
	t.Run("an integration failure", func(t *testing.T) {
		journey := newFake()
		journey.commitErr[setupjourney.ActionConnectExistingProject] = setupjourney.FailureFor(setupjourney.ReasonIntegrationDisabled, 1)
		result, _ := runWith(t, journey, fakeSelections{})
		if result.StopReason != personalassistant.FolderStopInstallFailed {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestRunRecordsRunningBeforeAnyStep(t *testing.T) {
	_, progress := runWith(t, newFake(), fakeSelections{})
	if len(progress.updates) < 2 || progress.updates[0].Status != personalassistant.FolderSetupRunning {
		t.Fatalf("first update = %+v", progress.updates[0])
	}
}
