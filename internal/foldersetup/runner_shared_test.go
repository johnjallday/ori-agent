package foldersetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

// fakeShared stands in for the Home's standing consent.
type fakeShared struct {
	fills   []projectstaffing.Fill
	err     error
	asked   []string // project IDs Fill was asked about
	settled []string
}

func (f *fakeShared) Fill(_ context.Context, projectID string, roleIDs []string) ([]projectstaffing.Fill, error) {
	f.asked = append(f.asked, projectID)
	if f.err != nil {
		return nil, f.err
	}
	return f.fills, nil
}

func (f *fakeShared) Settle(_ context.Context, projectID string) error {
	f.settled = append(f.settled, projectID)
	return nil
}

func runShared(t *testing.T, journey *fakeJourney, shared *fakeShared) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: progress, Shared: shared}
	result, err := runner.Run(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func connectedFake() *fakeJourney {
	journey := newFake()
	journey.done[specialist.SetupStepProjectConnect] = true
	journey.done[specialist.SetupStepWorkspaceSetup] = true
	return journey
}

func called(journey *fakeJourney, action setupjourney.ActionID) bool {
	for _, call := range journey.calls {
		if call == string(action) {
			return true
		}
	}
	return false
}

// The first song creates the shared assistant under its own name, never
// "<role> · <song>"; the run then records it so the next song binds it.
func TestRunFirstSongCreatesTheSharedAssistant(t *testing.T) {
	journey := connectedFake()
	shared := &fakeShared{fills: []projectstaffing.Fill{{RoleID: "producer", Name: "Producer", Mode: projectstaffing.ModeCreate}}}
	result, _ := runShared(t, journey, shared)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	if len(journey.staffRequests) != 1 || journey.staffRequests[0].Name != "Producer" || journey.staffRequests[0].Mode != "" {
		t.Fatalf("a create must send the shared name and no mode: %+v", journey.staffRequests)
	}
	if len(shared.asked) != 1 || shared.asked[0] != "song-1" {
		t.Fatalf("the consent was asked about %v", shared.asked)
	}
	if len(shared.settled) != 1 || shared.settled[0] != "song-1" {
		t.Fatalf("the created assistant was not recorded: %v", shared.settled)
	}
}

// Every later song binds the agent the consent recorded.
func TestRunLaterSongBindsTheSharedAssistant(t *testing.T) {
	journey := connectedFake()
	shared := &fakeShared{fills: []projectstaffing.Fill{{RoleID: "producer", Name: "Producer", Mode: projectstaffing.ModeBind}}}
	result, _ := runShared(t, journey, shared)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	if len(journey.staffRequests) != 1 || journey.staffRequests[0].Mode != setupjourney.StaffingModeBind || journey.staffRequests[0].Name != "Producer" {
		t.Fatalf("a bind must say so: %+v", journey.staffRequests)
	}
	if !called(journey, setupjourney.ActionAddProjectStaffing) {
		t.Fatalf("the bind was not committed: %v", journey.calls)
	}
}

// The review is still compared with what was asked: a review that would bind a
// different agent, or create where a bind was asked, commits nothing.
func TestRunSharedStaffingStillChecksTheReview(t *testing.T) {
	cases := map[string]func(*fakeJourney){
		"another agent":         func(f *fakeJourney) { f.shownProfile = "Someone Else" },
		"bound where created":   func(f *fakeJourney) { f.staffingBound = true },
		"another role entirely": func(f *fakeJourney) { f.staffingOutRole = "mixer" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			journey := connectedFake()
			mutate(journey)
			shared := &fakeShared{fills: []projectstaffing.Fill{{RoleID: "producer", Name: "Producer", Mode: projectstaffing.ModeCreate}}}
			result, _ := runShared(t, journey, shared)
			if result.StopReason != personalassistant.FolderStopPlanChanged || called(journey, setupjourney.ActionAddProjectStaffing) {
				t.Fatalf("result = %+v calls = %v", result, journey.calls)
			}
		})
	}
}

func TestRunSharedStaffingStops(t *testing.T) {
	cases := []struct {
		err    error
		reason string
	}{
		{projectstaffing.ErrConsentStale, personalassistant.FolderStopConsentStale},
		{projectstaffing.ErrAssistantMissing, personalassistant.FolderStopAssistantMissing},
		{errors.New("disk full"), personalassistant.FolderStopFailed},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			journey := connectedFake()
			result, progress := runShared(t, journey, &fakeShared{err: tc.err})
			if result.Status != personalassistant.FolderSetupStopped || result.StopReason != tc.reason {
				t.Fatalf("result = %+v", result)
			}
			if called(journey, setupjourney.ActionReviewProjectStaffing) || called(journey, setupjourney.ActionAddProjectStaffing) {
				t.Fatalf("staffed after the consent said no: %v", journey.calls)
			}
			states := lineStates(progress.last())
			if tc.reason != personalassistant.FolderStopFailed && states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineWaiting {
				t.Fatalf("a consent stop is a question, not a failure: %v", states)
			}
			// What finished before stays finished.
			if states[personalassistant.FolderPlanWorkspace] != personalassistant.FolderLineDone {
				t.Fatalf("earlier lines = %v", states)
			}
		})
	}
}

// A program with no shared team keeps one agent per project, named as before.
func TestRunNotSharedKeepsPerProjectNaming(t *testing.T) {
	journey := connectedFake()
	result, _ := runShared(t, journey, &fakeShared{err: projectstaffing.ErrNotShared})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v", result)
	}
	if len(journey.staffRequests) != 1 || journey.staffRequests[0].Name != "Producer · My Song" || journey.staffRequests[0].Mode != "" {
		t.Fatalf("staffing = %+v", journey.staffRequests)
	}
}

// Without the seam at all the runner behaves exactly as it did.
func TestRunWithoutSharedStaffingIsUnchanged(t *testing.T) {
	journey := connectedFake()
	result, _ := runWith(t, journey, fakeSelections{})
	if result.Status != personalassistant.FolderSetupDone || journey.staffRequests[0].Name != "Producer · My Song" {
		t.Fatalf("result = %+v staffing = %+v", result, journey.staffRequests)
	}
}

// A resume after the team was staffed but before the name was recorded records
// it then, without staffing again.
func TestRunResumeSettlesWithoutStaffingAgain(t *testing.T) {
	journey := connectedFake()
	journey.done[specialist.SetupStepAssistantProgramStaffing] = true
	shared := &fakeShared{}
	result, _ := runShared(t, journey, shared)
	if result.Status != personalassistant.FolderSetupDone || len(journey.calls) != 0 {
		t.Fatalf("result = %+v calls = %v", result, journey.calls)
	}
	if len(shared.asked) != 0 || len(shared.settled) != 1 || shared.settled[0] != "song-1" {
		t.Fatalf("asked = %v settled = %v", shared.asked, shared.settled)
	}
}
