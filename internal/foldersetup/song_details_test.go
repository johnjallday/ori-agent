package foldersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const (
	readsSongDetails = "Reads each REAPER project's tempo, length and track count. Nothing is moved, copied or changed."
	namesOnly        = "Names and project files only. Nothing is opened, moved or copied."
)

// T6: the library line says what is read, and follows the Home.
func TestBuildPortfolioPlanSaysWhatTheLibraryReads(t *testing.T) {
	existing := func(songDetails string) PortfolioFacts {
		facts := freshFacts()
		facts.HomeExists, facts.HomeStaffed, facts.Sharing, facts.SongDetails = true, true, SharingOn, songDetails
		return facts
	}
	for _, tc := range []struct {
		name          string
		facts         PortfolioFacts
		detail        string
		reads, grants bool
	}{
		{"a Home this Set up creates", freshFacts(), readsSongDetails, true, true},
		{"an existing Home without the consent", existing(""), namesOnly, false, false},
		{"an existing Home whose switch is off", existing("off"), namesOnly, false, false},
		{"an existing Home whose switch is on", existing(SongDetailsOn), readsSongDetails, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := BuildPortfolioPlan(tc.facts)
			if got := line(plan, personalassistant.FolderPlanLibrary).Detail; got != tc.detail {
				t.Fatalf("library detail = %q, want %q", got, tc.detail)
			}
			if plan.Intent.ReadsSongDetails != tc.reads || plan.Intent.GrantsSongDetails != tc.grants {
				t.Fatalf("intent reads=%v grants=%v, want %v %v", plan.Intent.ReadsSongDetails, plan.Intent.GrantsSongDetails, tc.reads, tc.grants)
			}
			for _, l := range plan.Lines {
				if strings.Contains(strings.ToLower(l.Name+" "+l.Detail), "progress") {
					t.Fatalf("a line calls facts progress: %+v", l)
				}
			}
		})
	}
	// The line is part of the plan digest: a card showing the old wording is
	// refused as plan_changed once the Home reads song details.
	if BuildPortfolioPlan(existing("")).Digest == BuildPortfolioPlan(existing(SongDetailsOn)).Digest {
		t.Fatal("the two library wordings share a plan digest")
	}
}

func TestPortfolioRunGrantsSongDetailsOnTheHomeItCreatesBeforeListing(t *testing.T) {
	w := newPortfolioWorld()
	result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.Status != personalassistant.FolderSetupDone || !w.songDetails {
		t.Fatalf("result = %+v cause=%v details=%v", result, result.Cause, w.songDetails)
	}
	log := strings.Join(w.log, ",")
	if grant, scan := strings.Index(log, "details:grant"), strings.Index(log, "scan:review"); grant < 0 || grant > scan ||
		strings.Index(log, "home:commit") > grant {
		t.Fatalf("the consent must follow the Home and precede the listing: %s", log)
	}
}

func TestPortfolioRunNeverGrantsSongDetailsOnAnExistingHome(t *testing.T) {
	for _, state := range []string{"", "off", SongDetailsOn} {
		t.Run("song details "+state, func(t *testing.T) {
			w := newPortfolioWorld()
			w.homeID, w.missing, w.sharing = "home-1", nil, SharingOn
			w.songDetails = state == SongDetailsOn
			facts := freshFacts()
			facts.HomeExists, facts.HomeStaffed, facts.Sharing, facts.SongDetails = true, true, SharingOn, state
			result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, _ *PortfolioConfig) { r.SongDetails = nil })
			if result.Status != personalassistant.FolderSetupDone || w.called("details:grant") {
				t.Fatalf("result = %+v calls = %v", result, w.log)
			}
		})
	}
}

func TestPortfolioRunStopsWhenTheListingReadsSongDetailsThePlanDidNotMention(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing, w.sharing = "home-1", nil, SharingOn
	w.reviewReadsSong = true // switched on after the card was drawn
	facts := freshFacts()
	facts.HomeExists, facts.HomeStaffed, facts.Sharing = true, true, SharingOn
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), nil)
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopPlanChanged ||
		w.called("scan:commit") {
		t.Fatalf("result = %+v calls = %v", result, w.log)
	}
}

func TestPortfolioRunStopsWhenTheSongDetailsConsentFails(t *testing.T) {
	w := newPortfolioWorld()
	w.detailsErr = errors.New("disk full")
	result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopFailed ||
		w.called("scan:review") {
		t.Fatalf("result = %+v calls = %v", result, w.log)
	}
}

func TestPortfolioRunNeedsTheSongDetailsSeamWhenThePlanGrantsIt(t *testing.T) {
	w := newPortfolioWorld()
	runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Sharing: worldSharing{w},
		Receipts: worldReceipts{w}, Progress: &recorder{}}
	if _, err := runner.Run(context.Background(), PortfolioConfig{Plan: portfolioPlanFor(freshFacts())}); !errors.Is(err, ErrPortfolioNotWired) {
		t.Fatalf("unwired song details = %v", err)
	}
	// A forged intent that grants on a Home it does not create is refused.
	plan := portfolioPlanFor(freshFacts())
	plan.Intent.CreatesHome = false
	w2 := newPortfolioWorld()
	w2.homeID, w2.missing = "home-1", nil
	result, _ := runPortfolio(t, w2, plan, nil)
	if result.StopReason != personalassistant.FolderStopPlanChanged || w2.called("details:grant") {
		t.Fatalf("result = %+v calls = %v", result, w2.log)
	}
}
