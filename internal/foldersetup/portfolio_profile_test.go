package foldersetup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// worldProfile is the profile step over the test world's call log.
type worldProfile struct {
	w   *portfolioWorld
	err error
}

func (p worldProfile) Setup(_ context.Context, homeID string, grantTemplates bool) error {
	p.w.log = append(p.w.log, fmt.Sprintf("profile:%s:%t", homeID, grantTemplates))
	return p.err
}

func profileFacts(homeExists bool) PortfolioFacts {
	facts := freshFacts()
	facts.HomeExists = homeExists
	facts.Profile = &ProfileFacts{Title: "Your studio", MainLabel: "Main DAW", Apps: []string{"REAPER"}, TemplatesApp: "REAPER"}
	return facts
}

func TestPortfolioRunFillsTheProfileAfterTheHomeAndBeforeTheListing(t *testing.T) {
	w := newPortfolioWorld()
	result, progress := runPortfolio(t, w, portfolioPlanFor(profileFacts(false)), func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Profile = worldProfile{w: w}
	})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	calls := strings.Join(w.log, ",")
	// The plan created this Home, so its card is the templates consent.
	want := "home:commit,staff:Portfolio Manager,profile:home-1:true,details:grant,library:review"
	if !strings.Contains(calls, want) {
		t.Fatalf("calls =\n %s\nwant them to contain\n %s", calls, want)
	}
	if strings.Count(calls, "profile:") != 1 {
		t.Fatalf("the profile step ran more than once: %s", calls)
	}
	if state := lineStates(progress.last())[personalassistant.FolderPlanProfile]; state != personalassistant.FolderLineDone {
		t.Fatalf("profile line = %q, want done", state)
	}
}

func TestPortfolioRunNeverGrantsTheTemplatesConsentOnAnExistingHome(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing, w.sharing = "home-1", nil, SharingOn
	facts := profileFacts(true)
	facts.HomeStaffed, facts.Sharing = true, SharingOn
	plan := portfolioPlanFor(facts)
	if plan.Intent.GrantsTemplates {
		t.Fatal("a plan for an existing Home promised a templates read")
	}
	result, _ := runPortfolio(t, w, plan, func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Profile = worldProfile{w: w}
	})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	if calls := strings.Join(w.log, ","); !strings.Contains(calls, "profile:home-1:false") {
		t.Fatalf("calls = %s", calls)
	}
}

// A resumed run whose earlier pass created the Home still carries that card's
// consent; the step itself replays by its request id.
func TestPortfolioRunResumedAfterCreatingTheHomeKeepsTheConsent(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing = "home-1", nil
	_, _ = runPortfolio(t, w, portfolioPlanFor(profileFacts(false)), func(r *PortfolioRunner, cfg *PortfolioConfig) {
		r.Profile = worldProfile{w: w}
		cfg.HomeID = "home-1"
	})
	if calls := strings.Join(w.log, ","); !strings.Contains(calls, "profile:home-1:true") {
		t.Fatalf("calls = %s", calls)
	}
}

func TestPortfolioRunIsNotStoppedByAProfileThatCouldNotBeSaved(t *testing.T) {
	w := newPortfolioWorld()
	result, progress := runPortfolio(t, w, portfolioPlanFor(profileFacts(false)), func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Profile = worldProfile{w: w, err: errors.New("disk full")}
	})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("a failed profile step stopped the run: %+v cause=%v", result, result.Cause)
	}
	states := lineStates(progress.last())
	if states[personalassistant.FolderPlanProfile] != personalassistant.FolderLineFailed || states[personalassistant.FolderPlanLibrary] != personalassistant.FolderLineDone {
		t.Fatalf("line states = %v", states)
	}
	if calls := strings.Join(w.log, ","); !strings.Contains(calls, "scan:commit") {
		t.Fatalf("the listing did not run after the profile step failed: %s", calls)
	}
}

func TestPortfolioRunWithoutAProfileLineNeverCallsTheStep(t *testing.T) {
	w := newPortfolioWorld()
	result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Profile = worldProfile{w: w}
	})
	if result.Status != personalassistant.FolderSetupDone || strings.Contains(strings.Join(w.log, ","), "profile:") {
		t.Fatalf("result %+v calls %v", result, w.log)
	}
}

func TestPortfolioRunRefusesAProfilePlanWithoutTheStepWired(t *testing.T) {
	w := newPortfolioWorld()
	runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Sharing: worldSharing{w},
		Receipts: worldReceipts{w}, SongDetails: worldSongDetails{w}, Folder: portfolioFolder, Progress: &recorder{}}
	if _, err := runner.Run(context.Background(), PortfolioConfig{Plan: portfolioPlanFor(profileFacts(false))}); !errors.Is(err, ErrPortfolioNotWired) {
		t.Fatalf("err = %v, want not wired", err)
	}
}
