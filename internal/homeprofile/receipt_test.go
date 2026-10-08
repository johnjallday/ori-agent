package homeprofile

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func storedProfile(t *testing.T, f *fixture) *workspace.HomeProfile {
	t.Helper()
	home, err := f.store.Get(testHome)
	if err != nil {
		t.Fatal(err)
	}
	return home.GetAssistantProgramState().GetHomeProfile()
}

func TestReceiptSummaryWordsWhatAFinishedSetupLeftOnTheHome(t *testing.T) {
	// One application and its templates.
	f := newFixture(t)
	f.apps, f.templatesApp, f.factsRaw = []folderdigest.InstalledApp{appReaper}, reaperTemplates, threeTemplates
	f.setup(SetupInput{OfferID: "offer-1", GrantTemplates: true})
	if title, detail, ok := ReceiptSummary(storedProfile(t, f)); !ok || title != "Your studio" || detail != "REAPER · 3 templates" {
		t.Fatalf("summary = %q %q %v", title, detail, ok)
	}

	// Several found and nothing picked: say so in the package's own words.
	two := newFixture(t)
	two.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	two.setup(SetupInput{OfferID: "offer-1"})
	if _, detail, ok := ReceiptSummary(storedProfile(t, two)); !ok || detail != "REAPER and Logic Pro · pick your main DAW" {
		t.Fatalf("summary = %q %v", detail, ok)
	}

	// One template reads in the singular; a hidden application is not named.
	one := newFixture(t)
	one.apps, one.templatesApp = []folderdigest.InstalledApp{appReaper, appLogic}, reaperTemplates
	one.factsRaw = `{"app":"REAPER","installed":true,"templates_available":true,"truncated":false,"templates":[{"name":"A","kind":"project","file":"A.RPP"}]}`
	view := one.setup(SetupInput{OfferID: "offer-1", GrantTemplates: true})
	one.mustFields(view, FieldsInput{HideApps: []string{"logic-pro"}})
	if _, detail, ok := ReceiptSummary(storedProfile(t, one)); !ok || detail != "REAPER · 1 template" {
		t.Fatalf("summary = %q %v", detail, ok)
	}

	// Nothing found, no profile, or an invalid one: no line at all.
	none := newFixture(t)
	none.setup(SetupInput{OfferID: "offer-1"})
	for name, profile := range map[string]*workspace.HomeProfile{
		"nothing found": storedProfile(t, none), "no profile": nil, "invalid": {SchemaVersion: 9},
	} {
		if _, _, ok := ReceiptSummary(profile); ok {
			t.Errorf("%s produced a line", name)
		}
	}
	// A forgotten list is not counted.
	if _, err := f.service.TemplatesForget(testOwner, testHome, f.requestID()); err != nil {
		t.Fatal(err)
	}
	if _, detail, _ := ReceiptSummary(storedProfile(t, f)); detail != "REAPER" {
		t.Fatalf("summary after Forget = %q", detail)
	}
}
