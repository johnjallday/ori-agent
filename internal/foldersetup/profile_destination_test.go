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

func TestRunDestination_ProfileIsNotWrittenToAHomeOtherThanTheReviewedOne(t *testing.T) {
	for name, test := range map[string]struct {
		homeID   string
		validate func(context.Context) error
	}{
		"the review named another Home":         {homeID: "another-home"},
		"the reviewed Home is no longer usable": {homeID: "home-1", validate: func(context.Context) error { return personalassistant.ErrFolderPlanChanged }},
	} {
		t.Run(name, func(t *testing.T) {
			journey := requiredHome()
			journey.homeExists, journey.homeStaffed = true, true
			cfg := profileConfig(false, false)
			cfg.Plan.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: test.homeID, Name: "Music Production Home", Kind: "home", OwnerUserID: "local"}
			runner := &Runner{Journey: journey, Selections: fakeSelections{}, Progress: &recorder{}, Profile: journeyProfile{journey: journey}, ValidateDestination: test.validate}
			result, err := runner.Run(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if calls := strings.Join(journey.calls, ","); result.StopReason != personalassistant.FolderStopPlanChanged || strings.Contains(calls, "profile:") {
				t.Fatalf("a profile was written to an unreviewed Home: %+v %s", result, calls)
			}
		})
	}
	// The reviewed Home, still standing, is profiled as before.
	journey := requiredHome()
	journey.homeExists, journey.homeStaffed = true, true
	cfg := profileConfig(false, false)
	cfg.Plan.Destination = &personalassistant.FolderSetupDestination{Status: "existing", WorkspaceID: "home-1", Name: "Music Production Home", Kind: "home", OwnerUserID: "local"}
	if _, _ = runProfile(t, journey, cfg, nil); !strings.Contains(strings.Join(journey.calls, ","), "profile:home-1:false") {
		t.Fatalf("the reviewed Home was not profiled: %v", journey.calls)
	}
}
