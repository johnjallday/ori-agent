package foldersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestPlacementRule(t *testing.T) {
	prep := func(policy string, exists bool, compositions ...string) *projectconnection.HomePreparation {
		return &projectconnection.HomePreparation{GroupPolicy: policy, Exists: exists, AvailableCompositions: compositions}
	}
	cases := []struct {
		name string
		prep *projectconnection.HomePreparation
		want string
		err  bool
	}{
		{"required Home that exists", prep("required", true, "grouped"), "grouped", false},
		{"required Home still to be created", prep("required", false, "grouped"), "grouped", false},
		{"no Home policy", prep("none", false, "standalone"), "standalone", false},
		{"recommended, Home exists", prep("recommended", true, "grouped", "standalone"), "grouped", false},
		{"recommended, no Home", prep("recommended", false, "grouped", "standalone"), "standalone", false},
		{"nothing available", prep("none", false), "", true},
		{"no preparation at all", nil, "", false},
	}
	for _, tc := range cases {
		got, err := placement(tc.prep)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%s: placement = %q err=%v, want %q err=%v", tc.name, got, err, tc.want, tc.err)
		}
	}
}

const homeTemplate = "plugin:reaper-plugin:reaper-song"

func homeConfig(creates bool, placement string) Config {
	cfg := testConfig()
	cfg.Plan.Lines = append([]personalassistant.FolderPlanLine{
		cfg.Plan.Lines[0],
		{Kind: personalassistant.FolderPlanHome, Name: "Creates the Home this workspace belongs to"},
	}, cfg.Plan.Lines[1:]...)
	cfg.Plan.Intent = personalassistant.FolderSetupIntent{Placement: placement, CreatesHome: creates, HomeTemplate: homeTemplate}
	return cfg
}

func runConfig(t *testing.T, journey *fakeJourney, cfg Config) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: progress}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func requiredHome() *fakeJourney {
	f := newFake()
	f.homeRequired, f.homeTemplate = true, homeTemplate
	return f
}

func TestRunCreatesAndStaffsTheRequiredHomeBeforeConnectingTheProject(t *testing.T) {
	journey := requiredHome()
	result, progress := runConfig(t, journey, homeConfig(true, "grouped"))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	want := []string{
		"review_create_group", "create_group",
		"review_existing_project", "connect_existing_project",
		"review_file_only_mode", "select_file_only_mode",
		"review_home_staffing", "add_home_staffing",
		"review_project_staffing", "add_project_staffing",
	}
	if strings.Join(journey.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v\nwant    %v", journey.calls, want)
	}
	if got := lineStates(progress.last())[personalassistant.FolderPlanHome]; got != personalassistant.FolderLineDone {
		t.Fatalf("the Home line = %s", got)
	}
	// While the Home was being made, the workspace line had not started.
	for _, update := range progress.updates {
		states := lineStates(update)
		if states[personalassistant.FolderPlanHome] == personalassistant.FolderLineWorking &&
			states[personalassistant.FolderPlanWorkspace] != personalassistant.FolderLineWaiting {
			t.Fatalf("the workspace line started before the Home was made: %v", states)
		}
	}
}

func TestRunUsesAnExistingHomeWithoutCreatingAnother(t *testing.T) {
	journey := requiredHome()
	journey.homeExists = true
	result, _ := runConfig(t, journey, homeConfig(false, "grouped"))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	for _, call := range journey.calls {
		if call == "review_create_group" || call == "create_group" {
			t.Fatalf("created a Home that already existed: %v", journey.calls)
		}
	}
}

func TestRunRefusesAHomeTheConfirmedPlanDidNotDescribe(t *testing.T) {
	cases := []struct {
		name   string
		build  func() *fakeJourney
		config Config
	}{
		{"a Home the plan never promised", requiredHome, homeConfig(false, "grouped")},
		{"another Home template", func() *fakeJourney {
			f := requiredHome()
			f.homeShownTmpl = "plugin:other-plugin:other"
			return f
		}, homeConfig(true, "grouped")},
		{"the expected template differs from the journey's own", func() *fakeJourney {
			f := requiredHome()
			f.homeTemplate, f.homeShownTmpl = "plugin:other:other", "plugin:other:other"
			return f
		}, homeConfig(true, "grouped")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			journey := tc.build()
			result, _ := runConfig(t, journey, tc.config)
			if result.StopReason != personalassistant.FolderStopPlanChanged {
				t.Fatalf("result = %+v", result)
			}
			for _, call := range journey.calls {
				if call == "create_group" {
					t.Fatalf("created a Home the plan did not describe: %v", journey.calls)
				}
			}
		})
	}
}

func TestRunStopsWhenTheWorkspaceWouldBePlacedDifferentlyThanPlanned(t *testing.T) {
	journey := newFake() // standalone
	result, _ := runConfig(t, journey, homeConfig(false, "grouped"))
	if result.StopReason != personalassistant.FolderStopPlanChanged {
		t.Fatalf("result = %+v", result)
	}
	if len(journey.calls) != 0 {
		t.Fatalf("something ran before the placement was checked: %v", journey.calls)
	}
	// A recommended Home that does not exist yet is standalone, per the rule.
	recommended := newFake()
	recommended.recommendedBoth, recommended.homeTemplate = true, homeTemplate
	result, _ = runConfig(t, recommended, homeConfig(false, "standalone"))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("recommended without a Home = %+v", result)
	}
}

func TestRunHomeStaffingNeedingAModelStopsBeforeAnyAgentIsMade(t *testing.T) {
	journey := requiredHome()
	journey.homeExists, journey.modelsReady = true, false
	journey.done[specialist.SetupStepProjectConnect] = true
	journey.done[specialist.SetupStepWorkspaceSetup] = true
	result, _ := runConfig(t, journey, homeConfig(false, "grouped"))
	if result.StopReason != personalassistant.FolderStopNeedsModel {
		t.Fatalf("result = %+v", result)
	}
	for _, call := range journey.calls {
		if call == string(setupjourney.ActionAddHomeStaffing) || call == string(setupjourney.ActionAddProjectStaffing) {
			t.Fatalf("an agent was made without a model: %v", journey.calls)
		}
	}
}

func TestRunAFurtherProjectIsItsOwnChildRun(t *testing.T) {
	journey := newFake()
	journey.rootHasProject = true
	result, progress := runConfig(t, journey, testConfig())
	if result.Status != personalassistant.FolderSetupDone || result.RunID != "child-1" {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	if journey.calls[0] != "child" || !journey.childStarted {
		t.Fatalf("the child run was not started first: %v", journey.calls)
	}
	if progress.last().RunID != "child-1" {
		t.Fatalf("the child run id must be recorded for a resume: %+v", progress.last())
	}
	// A resume names the run it already used and never starts another.
	resumed := newFake()
	resumed.rootHasProject = true
	cfg := testConfig()
	cfg.RunID = "child-1"
	if result, _ := runConfig(t, resumed, cfg); result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("resume = %+v", result)
	}
	for _, call := range resumed.calls {
		if call == "child" {
			t.Fatalf("a resume started another child run: %v", resumed.calls)
		}
	}
}

func TestRunStopsWhenAFurtherProjectCannotGetItsOwnRun(t *testing.T) {
	journey := newFake()
	journey.rootHasProject = true
	journey.childErr = errors.New("connect_another_project is unavailable")
	result, _ := runConfig(t, journey, testConfig())
	if result.StopReason != personalassistant.FolderStopFailed || result.Cause == nil {
		t.Fatalf("result = %+v", result)
	}
	for _, call := range journey.calls {
		if call == "connect_existing_project" {
			t.Fatal("a project was connected on the wrong run")
		}
	}
}

func TestRunStopsWhenTheRequiredHomeCannotBeCreated(t *testing.T) {
	journey := requiredHome()
	journey.commitErr[setupjourney.ActionCreateGroup] = errors.New("boom")
	result, progress := runConfig(t, journey, homeConfig(true, "grouped"))
	if result.StopReason != personalassistant.FolderStopFailed || result.Cause == nil {
		t.Fatalf("result = %+v", result)
	}
	if got := lineStates(progress.last())[personalassistant.FolderPlanHome]; got != personalassistant.FolderLineFailed {
		t.Fatalf("the Home line = %s", got)
	}
	for _, call := range journey.calls {
		if call == "connect_existing_project" {
			t.Fatal("the project was connected without its Home")
		}
	}
}
