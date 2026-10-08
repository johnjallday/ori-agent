package foldersetup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// journeyProfile is the profile step over a single-folder run's call log.
type journeyProfile struct {
	journey *fakeJourney
	err     error
}

func (p journeyProfile) Setup(_ context.Context, homeID string, grantTemplates bool) error {
	p.journey.calls = append(p.journey.calls, fmt.Sprintf("profile:%s:%t", homeID, grantTemplates))
	return p.err
}

// profileConfig is a single-folder plan whose profile line promised a
// templates read, as only a plan that creates the Home may.
func profileConfig(creates, grants bool) Config {
	cfg := homeConfig(creates, "grouped")
	cfg.Plan.Lines = append(cfg.Plan.Lines, personalassistant.FolderPlanLine{Kind: personalassistant.FolderPlanProfile, Name: "Your studio"})
	cfg.Plan.Intent.SetsProfile, cfg.Plan.Intent.GrantsTemplates = true, grants
	return cfg
}

func runProfile(t *testing.T, journey *fakeJourney, cfg Config, stepErr error) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: progress, Profile: journeyProfile{journey: journey, err: stepErr}}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func TestRunFillsTheProfileOfTheHomeItCreated(t *testing.T) {
	journey := requiredHome()
	result, progress := runProfile(t, journey, profileConfig(true, true), nil)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	calls := strings.Join(journey.calls, ",")
	// This run made the Home, so its card is the templates consent; the step
	// runs once, right after the Home exists.
	if !strings.Contains(calls, "create_group,profile:home-1:true,review_existing_project") || strings.Count(calls, "profile:") != 1 {
		t.Fatalf("calls = %s", calls)
	}
	if state := lineStates(progress.last())[personalassistant.FolderPlanProfile]; state != personalassistant.FolderLineDone {
		t.Fatalf("profile line = %q, want done", state)
	}
}

// The consent belongs to the card that created the Home. A Home this pass did
// not create never gains it, even from a plan whose stored intent promised a
// read: a plan served from before the Home existed, or a resumed run.
func TestRunNeverGrantsTheTemplatesConsentToAHomeItDidNotCreate(t *testing.T) {
	for name, cfg := range map[string]Config{
		"a plan that joins the Home":           profileConfig(false, false),
		"a stale plan that promised a read":    profileConfig(false, true),
		"a resumed run whose Home is in place": profileConfig(true, true),
	} {
		t.Run(name, func(t *testing.T) {
			journey := requiredHome()
			journey.homeExists, journey.homeStaffed = true, true
			result, _ := runProfile(t, journey, cfg, nil)
			if result.Status != personalassistant.FolderSetupDone {
				t.Fatalf("result = %+v cause=%v", result, result.Cause)
			}
			calls := strings.Join(journey.calls, ",")
			if !strings.Contains(calls, "profile:home-1:false") || strings.Contains(calls, "profile:home-1:true") {
				t.Fatalf("calls = %s", calls)
			}
		})
	}
}

func TestRunIsNotStoppedByAProfileThatCouldNotBeSaved(t *testing.T) {
	journey := requiredHome()
	result, progress := runProfile(t, journey, profileConfig(true, true), errors.New("disk full"))
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("a failed profile step stopped the run: %+v cause=%v", result, result.Cause)
	}
	states := lineStates(progress.last())
	if states[personalassistant.FolderPlanProfile] != personalassistant.FolderLineFailed || states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineDone {
		t.Fatalf("line states = %v", states)
	}
}

func TestRunWithoutAProfileLineNeverCallsTheStep(t *testing.T) {
	journey := requiredHome()
	result, _ := runProfile(t, journey, homeConfig(true, "grouped"), nil)
	if result.Status != personalassistant.FolderSetupDone || strings.Contains(strings.Join(journey.calls, ","), "profile:") {
		t.Fatalf("result %+v calls %v", result, journey.calls)
	}
}
