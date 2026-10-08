package foldersetup

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// The Home's profile is a write to the Home, so it is held to the reviewed
// destination like every other write of a setup run: it is not written once
// that destination no longer stands, nor to a Home other than the reviewed one.

func TestPortfolioDestination_ProfileIsNotWrittenOnceTheDestinationNoLongerStands(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID = "home-1"
	facts := profileFacts(true)
	facts.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: w.homeID, Name: facts.HomeName, Kind: "home", OwnerUserID: "local"}
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Profile = worldProfile{w: w}
		// The destination is withdrawn after staffing, just before the profile.
		r.ValidateDestination = func(context.Context, string) error {
			if w.called("staff:Portfolio Manager") {
				return personalassistant.ErrFolderPlanChanged
			}
			return nil
		}
	})
	calls := strings.Join(w.log, ",")
	if !w.called("staff:Portfolio Manager") {
		t.Fatalf("fixture: staffing did not run, so the profile step was never reached: %s", calls)
	}
	if result.StopReason != personalassistant.FolderStopPlanChanged || strings.Contains(calls, "profile:") || w.called("library:review") || w.receiptFacts != nil {
		t.Fatalf("a profile was written, or the run went on, after the destination was withdrawn: %+v %s", result, calls)
	}
}

// profileRun drives a single-folder run whose journey names the Home "home-1"
// ("Music Home"), against the given reviewed destination.
func profileRun(t *testing.T, journey *fakeJourney, cfg Config, destination *personalassistant.FolderSetupDestination, validate func(context.Context) error) (Result, string) {
	t.Helper()
	cfg.Plan.Destination = destination
	named := destinationJourney{fakeJourney: journey, homeID: "home-1", homeName: "Music Home", reviewName: "Music Home"}
	runner := &Runner{Journey: named, Selections: fakeSelections{}, Progress: &recorder{}, Profile: journeyProfile{journey: journey}, ValidateDestination: validate}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, strings.Join(journey.calls, ",")
}

func TestRunDestination_ProfileIsWrittenOnlyToTheReviewedHome(t *testing.T) {
	existing := func(id string) *personalassistant.FolderSetupDestination {
		return &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: id, Name: "Music Home", Kind: "home", OwnerUserID: "local"}
	}
	promised := &personalassistant.FolderSetupDestination{Status: "new", Name: "Music Home", Kind: "home", OwnerUserID: "local"}
	joined := func() *fakeJourney {
		journey := requiredHome()
		journey.homeExists, journey.homeStaffed = true, true
		return journey
	}

	// The review named another Home than the one the journey would profile.
	if result, calls := profileRun(t, joined(), profileConfig(false, false), existing("another-home"), nil); result.StopReason != personalassistant.FolderStopPlanChanged || strings.Contains(calls, "profile:") {
		t.Fatalf("a Home other than the reviewed one was profiled: %+v %s", result, calls)
	}
	// The reviewed Home is no longer usable when the profile is about to be written.
	withdrawn := func(context.Context) error { return personalassistant.ErrFolderPlanChanged }
	if result, calls := profileRun(t, joined(), profileConfig(false, false), existing("home-1"), withdrawn); result.StopReason != personalassistant.FolderStopPlanChanged || strings.Contains(calls, "profile:") {
		t.Fatalf("a withdrawn destination was profiled: %+v %s", result, calls)
	}
	// A plan that promised a new Home meets one that already exists and is not
	// this run's own: it is not profiled, and nothing is created or connected.
	if result, calls := profileRun(t, joined(), profileConfig(true, true), promised, nil); result.StopReason != personalassistant.FolderStopPlanChanged || strings.Contains(calls, "profile:") || strings.Contains(calls, "create_group") {
		t.Fatalf("a Home this run did not create was profiled or adopted: %+v %s", result, calls)
	}

	// The reviewed Home, still standing, is profiled and the run finishes.
	if result, calls := profileRun(t, joined(), profileConfig(false, false), existing("home-1"), nil); result.Status != personalassistant.FolderSetupDone || !strings.Contains(calls, "profile:home-1:false") {
		t.Fatalf("the reviewed Home was not profiled: %+v %s", result, calls)
	}
	// A new Home this run created is profiled, with its card's consent.
	if result, calls := profileRun(t, requiredHome(), profileConfig(true, true), promised, nil); result.Status != personalassistant.FolderSetupDone || !strings.Contains(calls, "create_group,profile:home-1:true") {
		t.Fatalf("the Home this run created was not profiled: %+v cause=%v %s", result, result.Cause, calls)
	}
}
