package foldersetup

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func studioFacts(templatesApp string, apps ...string) *ProfileFacts {
	return &ProfileFacts{Title: "Your studio", MainLabel: "Main DAW", Apps: apps, TemplatesApp: templatesApp}
}

func TestPortfolioPlanProfileLineWordings(t *testing.T) {
	tests := map[string]struct {
		profile    *ProfileFacts
		homeExists bool
		detail     string
		grants     bool
	}{
		"one application whose templates can be listed": {studioFacts("REAPER", "REAPER"), false,
			"REAPER is your main DAW. Reads your REAPER templates folder so new projects can start from them. Nothing is changed.", true},
		"one application, nothing to list": {studioFacts("", "Logic Pro"), false,
			"Logic Pro is your main DAW. Nothing is read.", false},
		"several found": {studioFacts("REAPER", "REAPER", "Logic Pro"), false,
			"Found REAPER and Logic Pro. Pick your main DAW on the Home after setup. Reads your REAPER templates folder; nothing is changed.", true},
		"several found, nothing to list": {studioFacts("", "Logic Pro", "Ableton Live"), false,
			"Found Logic Pro and Ableton Live. Pick your main DAW on the Home after setup. Nothing is read.", false},
		"three found": {studioFacts("REAPER", "REAPER", "Logic Pro", "Ableton Live"), false,
			"Found REAPER, Logic Pro and Ableton Live. Pick your main DAW on the Home after setup. Reads your REAPER templates folder; nothing is changed.", true},
		"none found": {studioFacts("REAPER"), false,
			"No main DAW was found on this Mac. You can tell the Home later.", false},
		// An existing Home never gains the templates consent from a card.
		"existing Home, one application": {studioFacts("REAPER", "REAPER"), true,
			"REAPER is your main DAW. Nothing is read.", false},
		"existing Home, several": {studioFacts("REAPER", "REAPER", "Logic Pro"), true,
			"Found REAPER and Logic Pro. Pick your main DAW on the Home after setup. Nothing is read.", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			facts := portfolioFacts()
			facts.Profile, facts.HomeExists = tc.profile, tc.homeExists
			plan := BuildPortfolioPlan(facts)
			got := line(plan, personalassistant.FolderPlanProfile)
			if got.Name != "Your studio" || got.Detail != tc.detail {
				t.Fatalf("profile line = %+v\nwant detail %q", got, tc.detail)
			}
			if !plan.Intent.SetsProfile || plan.Intent.GrantsTemplates != tc.grants {
				t.Fatalf("intent = sets %v grants %v, want grants %v", plan.Intent.SetsProfile, plan.Intent.GrantsTemplates, tc.grants)
			}
		})
	}
}

func TestPortfolioPlanProfileLineFollowsTheLibraryLine(t *testing.T) {
	facts := portfolioFacts()
	facts.Profile = studioFacts("REAPER", "REAPER")
	kinds := kindsOf(BuildPortfolioPlan(facts))
	if !strings.Contains(kinds, personalassistant.FolderPlanLibrary+" "+personalassistant.FolderPlanProfile+" "+personalassistant.FolderPlanSongs) &&
		!strings.Contains(kinds, personalassistant.FolderPlanLibrary+","+personalassistant.FolderPlanProfile+","+personalassistant.FolderPlanSongs) {
		t.Fatalf("line order = %s", kinds)
	}
}

// A Home whose installed package declares no profile gets no line and no step.
func TestPlansWithoutADeclaredProfileAreUnchanged(t *testing.T) {
	portfolio := BuildPortfolioPlan(portfolioFacts())
	if line(portfolio, personalassistant.FolderPlanProfile).Kind != "" || portfolio.Intent.SetsProfile || portfolio.Intent.GrantsTemplates {
		t.Fatalf("portfolio plan grew a profile line: %+v", portfolio.Lines)
	}
	single := BuildPlan(baseFacts())
	if line(single, personalassistant.FolderPlanProfile).Kind != "" || single.Intent.SetsProfile {
		t.Fatalf("single plan grew a profile line: %+v", single.Lines)
	}
}

// The line's text is part of the digest, so a card showing older text is
// refused as plan_changed, like the library line.
func TestProfileLineTextIsPartOfThePlanDigest(t *testing.T) {
	facts := portfolioFacts()
	without := BuildPortfolioPlan(facts).Digest
	facts.Profile = studioFacts("REAPER", "REAPER")
	one := BuildPortfolioPlan(facts).Digest
	facts.Profile = studioFacts("REAPER", "REAPER", "Logic Pro")
	two := BuildPortfolioPlan(facts).Digest
	facts.Profile = studioFacts("", "REAPER", "Logic Pro")
	noTemplates := BuildPortfolioPlan(facts).Digest
	seen := map[string]bool{}
	for _, digest := range []string{without, one, two, noTemplates} {
		if digest == "" || seen[digest] {
			t.Fatalf("digests are not distinct: %q %q %q %q", without, one, two, noTemplates)
		}
		seen[digest] = true
	}
}

func TestSingleSongPlanCarriesTheProfileLineOnlyInAHome(t *testing.T) {
	facts := baseFacts()
	facts.Profile = studioFacts("REAPER", "REAPER")
	// A standalone workspace has no Home, so no Home profile.
	if plan := BuildPlan(facts); line(plan, personalassistant.FolderPlanProfile).Kind != "" || plan.Intent.SetsProfile {
		t.Fatalf("a standalone plan has a profile line: %+v", plan.Lines)
	}
	facts.Grouped, facts.HomeTemplate = true, "plugin-home:mpm:music-producer-assistant"
	creating := BuildPlan(facts)
	got := line(creating, personalassistant.FolderPlanProfile)
	if got.Name != "Your studio" || !strings.Contains(got.Detail, "Reads your REAPER templates folder") ||
		!creating.Intent.SetsProfile || !creating.Intent.GrantsTemplates {
		t.Fatalf("plan that creates the Home: line %+v intent %+v", got, creating.Intent)
	}
	// It sits with the Home's lines, before the workspace's.
	kinds := kindsOf(creating)
	if strings.Index(kinds, personalassistant.FolderPlanProfile) > strings.Index(kinds, personalassistant.FolderPlanWorkspace) ||
		strings.Index(kinds, personalassistant.FolderPlanProfile) < strings.Index(kinds, personalassistant.FolderPlanHome) {
		t.Fatalf("line order = %s", kinds)
	}
	facts.HomeExists = true
	joining := BuildPlan(facts)
	if got = line(joining, personalassistant.FolderPlanProfile); got.Detail != "REAPER is your main DAW. Nothing is read." || joining.Intent.GrantsTemplates {
		t.Fatalf("plan that joins a Home: line %+v intent %+v", got, joining.Intent)
	}
}

func TestProfileLineFallsBackToHostWordsAndNamesNoPath(t *testing.T) {
	facts := portfolioFacts()
	facts.Profile = &ProfileFacts{Apps: []string{"REAPER"}}
	got := line(BuildPortfolioPlan(facts), personalassistant.FolderPlanProfile)
	if got.Name != "Home profile" || got.Detail != "REAPER is your main application. Nothing is read." {
		t.Fatalf("line = %+v", got)
	}
	for _, text := range []string{got.Name, got.Detail} {
		if strings.ContainsAny(text, "/\\") {
			t.Fatalf("the profile line names a path: %q", text)
		}
	}
}
