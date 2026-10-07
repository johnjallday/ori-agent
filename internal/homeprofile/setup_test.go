package homeprofile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func (f *fixture) setup(input SetupInput) View {
	f.t.Helper()
	if input.RequestID == "" {
		input.RequestID = f.requestID()
	}
	view, err := f.service.Setup(context.Background(), testOwner, testHome, input)
	if err != nil {
		f.t.Fatalf("setup: %v", err)
	}
	return view
}

func TestSetupThatCreatedTheHomeFillsTheProfileWithTemplates(t *testing.T) {
	f := newFixture(t)
	f.apps, f.templatesApp, f.factsRaw = []folderdigest.InstalledApp{appReaper}, reaperTemplates, threeTemplates
	view := f.setup(SetupInput{OfferID: "offer-1", GrantTemplates: true})

	if len(f.factsCalls) != 1 || !f.factsCalls[0] {
		t.Fatalf("facts calls = %v, want one with templates", f.factsCalls)
	}
	profile := view.Profile
	if main := profile.MainApp; main == nil || main.ID != "reaper" || main.Source != workspace.HomeProfileSourceDetected || main.ConfirmedAt != nil {
		t.Fatalf("main app = %+v", main)
	}
	if app, _ := profile.App("reaper"); app.Version != "7.28" || app.ConfirmedAt != nil {
		t.Fatalf("app = %+v", app)
	}
	templates := profile.Templates
	if templates == nil || len(templates.Items) != 3 || templates.Consent.Source != workspace.HomeProfileTemplatesFolderOffer ||
		templates.Consent.OfferID != "offer-1" || !templates.Consent.Active() || templates.AppID != "reaper" {
		t.Fatalf("templates = %+v", templates)
	}
	if view.Templates.State != TemplatesListed {
		t.Fatalf("row = %+v", view.Templates)
	}
}

func TestSetupOnAnExistingHomeNeverGrantsTheTemplatesConsent(t *testing.T) {
	f := newFixture(t)
	f.apps, f.templatesApp, f.factsRaw = []folderdigest.InstalledApp{appReaper}, reaperTemplates, threeTemplates
	view := f.setup(SetupInput{OfferID: "offer-1", GrantTemplates: false})
	if view.Profile.Templates != nil || view.Templates.State != TemplatesNotRead {
		t.Fatalf("templates = %+v row = %+v", view.Profile.Templates, view.Templates)
	}
	// Only the version was asked for.
	if len(f.factsCalls) != 1 || f.factsCalls[0] {
		t.Fatalf("facts calls = %v, want one without templates", f.factsCalls)
	}
	if app, _ := view.Profile.App("reaper"); app.Version != "7.28" {
		t.Fatalf("version = %q", app.Version)
	}
}

func TestSetupNeverTurnsAConsentBackOnOrReplacesOne(t *testing.T) {
	f, _ := templatesFixture(t)
	if _, err := f.commit(f.review().ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TemplatesForget(testOwner, testHome, f.requestID()); err != nil {
		t.Fatal(err)
	}
	calls := len(f.factsCalls)
	view := f.setup(SetupInput{OfferID: "offer-2", GrantTemplates: true})
	if view.Profile.Templates.Consent.Active() || len(view.Profile.Templates.Items) != 0 {
		t.Fatalf("a card turned a forgotten consent back on: %+v", view.Profile.Templates)
	}
	for _, withTemplates := range f.factsCalls[calls:] {
		if withTemplates {
			t.Fatal("a card listed templates after Forget")
		}
	}
}

func TestSetupToleratesAMissingOrFailingOperation(t *testing.T) {
	tests := map[string]struct {
		app     TemplatesApp
		factErr error
		problem string
		state   string
	}{
		"plugin too old": {TemplatesApp{AppID: "reaper", AppName: "REAPER", Folders: reaperTemplates.Folders, PluginInstalled: true},
			nil, workspace.HomeProfileTemplatesUnavailable, TemplatesUpdatePlugin},
		"read fails": {reaperTemplates, errors.New("exit status 1"), workspace.HomeProfileTemplatesFailed, TemplatesProblem},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.apps, f.templatesApp, f.factsErr = []folderdigest.InstalledApp{appReaper, appLogic}, tc.app, tc.factErr
			view := f.setup(SetupInput{OfferID: "offer-1", GrantTemplates: true})
			// The run is not stopped, and the profile keeps the applications.
			if len(view.Profile.Apps) != 2 || view.Profile.MainApp != nil {
				t.Fatalf("profile = %+v", view.Profile)
			}
			templates := view.Profile.Templates
			if templates == nil || !templates.Consent.Active() || templates.Problem != tc.problem || len(templates.Items) != 0 {
				t.Fatalf("templates = %+v", templates)
			}
			if view.Templates.State != tc.state {
				t.Fatalf("row = %+v, want %s", view.Templates, tc.state)
			}
		})
	}
}

func TestSetupDoesNotReadTemplatesForAnApplicationThatWasNotFound(t *testing.T) {
	f := newFixture(t)
	f.apps, f.templatesApp, f.factsRaw = []folderdigest.InstalledApp{appLogic}, reaperTemplates, threeTemplates
	view := f.setup(SetupInput{OfferID: "offer-1", GrantTemplates: true})
	if len(f.factsCalls) != 0 || view.Profile.Templates != nil {
		t.Fatalf("facts calls %v templates %+v", f.factsCalls, view.Profile.Templates)
	}
	if main := view.Profile.MainApp; main == nil || main.ID != "logic-pro" {
		t.Fatalf("main app = %+v", main)
	}
}

func TestSetupReplaysAResumedRunAndKeepsWhatTheOwnerSaid(t *testing.T) {
	f := newFixture(t)
	f.apps, f.templatesApp, f.factsRaw = []folderdigest.InstalledApp{appReaper}, reaperTemplates, threeTemplates
	first := f.setup(SetupInput{RequestID: "setup-offer-1", OfferID: "offer-1", GrantTemplates: true})
	calls := len(f.factsCalls)
	f.now = f.now.Add(time.Hour)
	again := f.setup(SetupInput{RequestID: "setup-offer-1", OfferID: "offer-1", GrantTemplates: true})
	if !again.Replayed || again.Revision != first.Revision || len(f.factsCalls) != calls {
		t.Fatalf("a resumed run did the step twice: replayed %v revision %d->%d calls %d->%d",
			again.Replayed, first.Revision, again.Revision, calls, len(f.factsCalls))
	}
	// A later card on the same Home merges: the owner's pick and confirmations stay.
	view := f.mustFields(again, FieldsInput{ConfirmApps: []string{"reaper"}, MainApp: ptr("reaper")})
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	later := f.setup(SetupInput{OfferID: "offer-2"})
	if app, _ := later.Profile.App("reaper"); app.ConfirmedAt == nil || later.Profile.MainApp.ConfirmedAt == nil || len(later.Profile.Apps) != 2 {
		t.Fatalf("a later card lost the owner's answers: %+v (before %+v)", later.Profile, view.Profile)
	}
	if len(later.Profile.Templates.Items) != 3 {
		t.Fatalf("a later card dropped the templates list: %+v", later.Profile.Templates)
	}
}

func TestSetupRefusesWhatEveryWriteRefuses(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	if _, err := f.service.Setup(context.Background(), "someone-else", testHome, SetupInput{RequestID: "s1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user: %v", err)
	}
	if _, err := f.service.Setup(context.Background(), testOwner, testHome, SetupInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no request id: %v", err)
	}
	f.declared = false
	if _, err := f.service.Setup(context.Background(), testOwner, testHome, SetupInput{RequestID: "s2"}); !errors.Is(err, ErrNotDeclared) {
		t.Fatalf("no declared profile: %v", err)
	}
	f.declared, f.writable = true, false
	if _, err := f.service.Setup(context.Background(), testOwner, testHome, SetupInput{RequestID: "s3"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only Home: %v", err)
	}
}
