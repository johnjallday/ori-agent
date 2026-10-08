package foldersetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

type destinationJourney struct {
	*fakeJourney
	homeID, homeName, reviewName string
}

func (j destinationJourney) Read(ctx context.Context, id string) (*setupjourney.JourneyProjection, error) {
	projection, err := j.fakeJourney.Read(ctx, id)
	if err == nil {
		for i := range projection.Steps {
			if prep := projection.Steps[i].Preparation; prep != nil {
				prep.HomeID, prep.Name = j.homeID, j.homeName
			}
		}
	}
	return projection, err
}

func (j destinationJourney) Mutate(ctx context.Context, id string, action setupjourney.ActionID, mutation setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	result, err := j.fakeJourney.Mutate(ctx, id, action, mutation)
	if err == nil && result.Review != nil && result.Review.ProjectConnection != nil {
		result.Review.ProjectConnection.ParentWorkspaceName = j.reviewName
	}
	return result, err
}

func TestRunDestinationWitness_RechecksBetweenOwnerReviewAndCommit(t *testing.T) {
	journey := newFake()
	checks := 0
	runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: &recorder{},
		ValidateDestination: func(context.Context) error {
			checks++
			if checks == 2 {
				return errors.New("destination revoked after review")
			}
			return nil
		},
	}
	result, err := runner.Run(context.Background(), testConfig())
	if err != nil || result.StopReason != personalassistant.FolderStopPlanChanged || checks != 2 {
		t.Fatal(result, err, checks)
	}
	if len(journey.calls) != 1 || journey.calls[0] != string(setupjourney.ActionReviewExistingProject) {
		t.Fatal("revocation reached an owner commit", journey.calls)
	}
}

func TestRunDestinationWitness_RejectsReplacementOrChangedReviewBeforeCommit(t *testing.T) {
	for _, test := range []struct {
		name, homeID, homeName, reviewName string
		wantDone                           bool
	}{
		{"exact canonical Home", "home-a", "Music Home", "Music Home", true},
		{"same name different Home", "home-b", "Music Home", "Music Home", false},
		{"deleted Home", "", "Music Home", "Music Home", false},
		{"renamed Home", "home-a", "Other Home", "Other Home", false},
		{"changed after preparation", "home-a", "Music Home", "Other Home", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFake()
			fake.homeRequired, fake.homeExists, fake.homeStaffed = true, true, true
			journey := destinationJourney{fakeJourney: fake, homeID: test.homeID, homeName: test.homeName, reviewName: test.reviewName}
			cfg := testConfig()
			cfg.Plan.Intent.Placement = "grouped"
			cfg.Plan.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "home-a", Name: "Music Home", Kind: "home", OwnerUserID: "local"}
			cfg.Plan = cfg.Plan.Stamped()
			runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: &recorder{}}
			result, err := runner.Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			committed := false
			for _, call := range fake.calls {
				if call == string(setupjourney.ActionConnectExistingProject) {
					committed = true
				}
			}
			if test.wantDone {
				if result.Status != personalassistant.FolderSetupDone || !committed {
					t.Fatal(result, fake.calls)
				}
			} else if result.StopReason != personalassistant.FolderStopPlanChanged || committed {
				t.Fatal("changed destination reached project commit", result, fake.calls)
			}
		})
	}
}
