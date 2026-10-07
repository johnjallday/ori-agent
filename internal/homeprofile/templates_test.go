package homeprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var reaperTemplates = TemplatesApp{AppID: "reaper", AppName: "REAPER", Folders: []string{"ProjectTemplates", "TrackTemplates"},
	PluginInstalled: true, OperationAvailable: true}

const threeTemplates = `{"app":"REAPER","installed":true,"version":"7.28","templates_available":true,"truncated":false,"templates":[
  {"name":"Band Session","kind":"project","file":"Band Session.RPP","modified_at":"2026-10-01T10:00:00Z"},
  {"name":"Vocal Comp","kind":"project","file":"Vocal Comp.RPP"},
  {"name":"Drum Bus","kind":"track","file":"Drum Bus.RTrackTemplate","modified_at":"not a time"}]}`

// templatesFixture is a Home where one application was found and its plugin
// can list templates.
func templatesFixture(t *testing.T) (*fixture, View) {
	t.Helper()
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	f.templatesApp, f.factsRaw = reaperTemplates, `{"app":"REAPER","installed":true,"version":"7.28","templates_available":true,"truncated":false}`
	view := f.detect()
	f.factsRaw = threeTemplates
	return f, view
}

func (f *fixture) review() TemplatesReview {
	f.t.Helper()
	review, err := f.service.TemplatesReview(testOwner, testHome, f.requestID())
	if err != nil {
		f.t.Fatalf("review: %v", err)
	}
	return review
}

func (f *fixture) commit(reviewID string) (View, error) {
	f.t.Helper()
	return f.service.TemplatesCommit(context.Background(), testOwner, testHome, f.requestID(), reviewID)
}

func TestDetectAsksTheApplicationsPluginForItsVersionOnly(t *testing.T) {
	f, view := templatesFixture(t)
	if len(f.factsCalls) != 1 || f.factsCalls[0] {
		t.Fatalf("detect made facts calls %v, want one without templates", f.factsCalls)
	}
	app, _ := view.Profile.App("reaper")
	if app.Version != "7.28" || view.Profile.Templates != nil {
		t.Fatalf("after detect: app %+v templates %+v", app, view.Profile.Templates)
	}
	if view.Templates == nil || view.Templates.State != TemplatesNotRead || view.Templates.AppName != "REAPER" || !view.FactsOperation {
		t.Fatalf("templates row = %+v facts operation = %v", view.Templates, view.FactsOperation)
	}
	// A plugin that fails or is not there only costs the version.
	f.factsErr = errors.New("exit status 1")
	f.now = f.now.Add(time.Hour)
	if _, err := f.service.Detect(context.Background(), testOwner, testHome, f.requestID()); err != nil {
		t.Fatalf("a failed version read stopped detection: %v", err)
	}
	// An application that was not found is never asked about.
	other := newFixture(t)
	other.apps, other.templatesApp = []folderdigest.InstalledApp{appLogic}, reaperTemplates
	other.detect()
	if len(other.factsCalls) != 0 {
		t.Fatalf("the plugin was called for an application that was not found: %v", other.factsCalls)
	}
}

func TestTemplatesAreNeverReadBeforeTheOwnerAgrees(t *testing.T) {
	f, view := templatesFixture(t)
	calls := len(f.factsCalls)
	if _, err := f.service.Read(testOwner, testHome); err != nil {
		t.Fatal(err)
	}
	review := f.review()
	f.mustFields(view, FieldsInput{ConfirmApps: []string{"reaper"}})
	if len(f.factsCalls) != calls {
		t.Fatalf("a read, a review or a save listed templates: %v", f.factsCalls)
	}
	if review.AppID != "reaper" || review.AppName != "REAPER" || len(review.Folders) != 2 || review.ReviewID == "" ||
		review.Sentence != "Ori will list the names of the files in ProjectTemplates and TrackTemplates inside your REAPER settings folder. It opens no template and changes nothing." {
		t.Fatalf("review = %+v", review)
	}
	// Asking again shows the same thing: a review is not a write.
	if again := f.review(); again.ReviewID != review.ReviewID {
		t.Fatalf("review id changed without a change: %q %q", review.ReviewID, again.ReviewID)
	}
	if current, _ := f.service.Read(testOwner, testHome); current.Profile.Templates != nil {
		t.Fatalf("a review stored something: %+v", current.Profile.Templates)
	}
}

func TestTemplatesCommitRecordsConsentAndListsOnce(t *testing.T) {
	f, _ := templatesFixture(t)
	calls := len(f.factsCalls)
	view, err := f.commit(f.review().ReviewID)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := f.factsCalls[calls:]; len(got) != 1 || !got[0] {
		t.Fatalf("commit made facts calls %v, want one with templates", got)
	}
	templates := view.Profile.Templates
	if templates == nil || !templates.Consent.Active() || templates.Consent.Source != workspace.HomeProfileTemplatesHomeReview ||
		!templates.Consent.GrantedAt.Equal(f.now) || templates.AppID != "reaper" || !templates.ReadAt.Equal(f.now) || templates.Problem != "" {
		t.Fatalf("templates = %+v", templates)
	}
	if len(templates.Items) != 3 || templates.Items[0].Name != "Band Session" || templates.Items[0].Kind != workspace.HomeProfileTemplateProject ||
		templates.Items[0].File != "Band Session.RPP" || templates.Items[0].ModifiedAt == nil ||
		templates.Items[2].Kind != workspace.HomeProfileTemplateTrack || templates.Items[2].ModifiedAt != nil {
		t.Fatalf("items = %+v", templates.Items)
	}
	if view.Templates.State != TemplatesListed {
		t.Fatalf("row state = %q", view.Templates.State)
	}
	// Reading again keeps the same consent and replaces the list.
	granted := templates.Consent.GrantedAt
	f.now = f.now.Add(time.Hour)
	f.factsRaw = `{"app":"REAPER","installed":true,"version":"7.30","templates_available":true,"truncated":true,"templates":[{"name":"Solo","kind":"project","file":"Solo.RPP"}]}`
	view, err = f.commit(f.review().ReviewID)
	if err != nil {
		t.Fatalf("read again: %v", err)
	}
	templates = view.Profile.Templates
	if !templates.Consent.GrantedAt.Equal(granted) || len(templates.Items) != 1 || !templates.Truncated || !templates.ReadAt.Equal(f.now) {
		t.Fatalf("after reading again = %+v", templates)
	}
	if app, _ := view.Profile.App("reaper"); app.Version != "7.30" {
		t.Fatalf("version after reading again = %q", app.Version)
	}
}

func TestTemplatesForgetClearsTheListAndTheConsentInOneWrite(t *testing.T) {
	f, _ := templatesFixture(t)
	view, err := f.commit(f.review().ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := f.review().ReviewID
	f.now = f.now.Add(time.Hour)
	forgotten, err := f.service.TemplatesForget(testOwner, testHome, f.requestID())
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	templates := forgotten.Profile.Templates
	if forgotten.Revision != view.Revision+1 || len(templates.Items) != 0 || templates.ReadAt != nil ||
		templates.Consent.Active() || !templates.Consent.RevokedAt.Equal(f.now) {
		t.Fatalf("after forget = %+v (revision %d -> %d)", templates, view.Revision, forgotten.Revision)
	}
	if forgotten.Templates.State != TemplatesNotRead {
		t.Fatalf("row state = %q", forgotten.Templates.State)
	}
	// Forgetting twice changes nothing more.
	again, err := f.service.TemplatesForget(testOwner, testHome, f.requestID())
	if err != nil || again.Revision != forgotten.Revision {
		t.Fatalf("second forget: %+v %v", again.Revision, err)
	}
	// A review from before Forget no longer stands: a later read asks anew.
	calls := len(f.factsCalls)
	if _, err := f.commit(reviewed); !errors.Is(err, ErrChanged) {
		t.Fatalf("a review from before Forget was accepted: %v", err)
	}
	if len(f.factsCalls) != calls {
		t.Fatal("a stale review listed templates")
	}
	f.now = f.now.Add(time.Hour)
	fresh, err := f.commit(f.review().ReviewID)
	if err != nil || !fresh.Profile.Templates.Consent.Active() || !fresh.Profile.Templates.Consent.GrantedAt.Equal(f.now) ||
		len(fresh.Profile.Templates.Items) != 3 {
		t.Fatalf("a new consent after Forget: %+v %v", fresh.Profile.Templates, err)
	}
}

func TestTemplatesCommitNeedsAReviewOfThisHome(t *testing.T) {
	f, _ := templatesFixture(t)
	calls := len(f.factsCalls)
	for _, reviewID := range []string{"", "made-up", strings.Repeat("0", 32)} {
		if _, err := f.commit(reviewID); !errors.Is(err, ErrChanged) {
			t.Fatalf("review id %q: %v", reviewID, err)
		}
	}
	if len(f.factsCalls) != calls {
		t.Fatal("an unreviewed commit listed templates")
	}
	if view, _ := f.service.Read(testOwner, testHome); view.Profile.Templates != nil {
		t.Fatalf("an unreviewed commit recorded a consent: %+v", view.Profile.Templates)
	}
	if _, err := f.service.TemplatesCommit(context.Background(), "someone-else", testHome, "request-x", f.review().ReviewID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user: %v", err)
	}
}

func TestTemplatesCommitReplaysARepeatedRequestWithoutReadingAgain(t *testing.T) {
	f, _ := templatesFixture(t)
	review := f.review()
	first, err := f.service.TemplatesCommit(context.Background(), testOwner, testHome, "commit-1", review.ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(f.factsCalls)
	again, err := f.service.TemplatesCommit(context.Background(), testOwner, testHome, "commit-1", review.ReviewID)
	if err != nil || !again.Replayed || again.Revision != first.Revision || len(f.factsCalls) != calls {
		t.Fatalf("replay: %+v err %v calls %d->%d", again.Replayed, err, calls, len(f.factsCalls))
	}
	if _, err := f.service.TemplatesForget(testOwner, testHome, "commit-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("request_id reused for another action: %v", err)
	}
}

func TestTemplatesWithoutAUsableOperation(t *testing.T) {
	tests := map[string]struct {
		app   TemplatesApp
		state string
	}{
		"plugin too old":       {TemplatesApp{AppID: "reaper", AppName: "REAPER", Folders: reaperTemplates.Folders, PluginInstalled: true}, TemplatesUpdatePlugin},
		"plugin not installed": {TemplatesApp{AppID: "reaper", AppName: "REAPER", Folders: reaperTemplates.Folders}, TemplatesPluginMissing},
		"no such application":  {TemplatesApp{}, TemplatesUnsupported},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.apps, f.templatesApp = []folderdigest.InstalledApp{appReaper}, tc.app
			view := f.detect()
			if view.Templates.State != tc.state || view.FactsOperation {
				t.Fatalf("row = %+v facts operation = %v", view.Templates, view.FactsOperation)
			}
			if _, err := f.service.TemplatesReview(testOwner, testHome, f.requestID()); !errors.Is(err, ErrOperationUnavailable) {
				t.Fatalf("review: %v", err)
			}
			if _, err := f.commit("anything"); !errors.Is(err, ErrOperationUnavailable) {
				t.Fatalf("commit: %v", err)
			}
			if len(f.factsCalls) != 0 {
				t.Fatalf("an unavailable operation was called: %v", f.factsCalls)
			}
			if current, _ := f.service.Read(testOwner, testHome); current.Profile.Templates != nil {
				t.Fatalf("a refused commit recorded a consent: %+v", current.Profile.Templates)
			}
		})
	}
}

func TestTemplatesAreForOneApplicationOnly(t *testing.T) {
	// Not detected yet.
	f := newFixture(t)
	f.templatesApp = reaperTemplates
	if view, _ := f.service.Read(testOwner, testHome); view.Templates.State != TemplatesDetectFirst {
		t.Fatalf("before detection: %+v", view.Templates)
	}
	if _, err := f.service.TemplatesReview(testOwner, testHome, f.requestID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("review before detection: %v", err)
	}
	// Another application is the only one found.
	f.apps = []folderdigest.InstalledApp{appLogic}
	if view := f.detect(); view.Templates.State != TemplatesOtherApp || view.Templates.AppName != "REAPER" {
		t.Fatalf("other application only: %+v", view.Templates)
	}
	// Both found and nothing picked: readable, as on the setup card.
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	view := f.detect()
	if view.Templates.State != TemplatesNotRead {
		t.Fatalf("two found, none picked: %+v", view.Templates)
	}
	// The owner's main application is the other one.
	view = f.mustFields(view, FieldsInput{MainApp: ptr("logic-pro")})
	if view.Templates.State != TemplatesOtherApp {
		t.Fatalf("another main application: %+v", view.Templates)
	}
	if _, err := f.service.TemplatesReview(testOwner, testHome, f.requestID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("review for another main application: %v", err)
	}
}

func TestAFailedReadKeepsTheConsentAndSaysWhatHappened(t *testing.T) {
	f, _ := templatesFixture(t)
	if _, err := f.commit(f.review().ReviewID); err != nil {
		t.Fatal(err)
	}
	f.factsErr = errors.New("exit status 1")
	f.now = f.now.Add(time.Hour)
	view, err := f.commit(f.review().ReviewID)
	if err != nil {
		t.Fatalf("a failed read was an error instead of a state: %v", err)
	}
	templates := view.Profile.Templates
	if !templates.Consent.Active() || len(templates.Items) != 0 || templates.ReadAt != nil ||
		templates.Problem != workspace.HomeProfileTemplatesFailed || !templates.ProblemAt.Equal(f.now) {
		t.Fatalf("after a failed read = %+v", templates)
	}
	if view.Templates.State != TemplatesProblem || view.Templates.Problem != workspace.HomeProfileTemplatesFailed {
		t.Fatalf("row = %+v", view.Templates)
	}
	// The next good read clears the problem.
	f.factsErr = nil
	if view, err = f.commit(f.review().ReviewID); err != nil || view.Templates.State != TemplatesListed || view.Profile.Templates.Problem != "" {
		t.Fatalf("recovery: %+v %v", view.Templates, err)
	}
}

func TestNotMineOnTheTemplatesApplicationForgetsItsTemplates(t *testing.T) {
	f, _ := templatesFixture(t)
	view, err := f.commit(f.review().ReviewID)
	if err != nil {
		t.Fatal(err)
	}
	view = f.mustFields(view, FieldsInput{HideApps: []string{"reaper"}})
	templates := view.Profile.Templates
	if len(templates.Items) != 0 || templates.Consent.Active() {
		t.Fatalf("templates of a hidden application = %+v", templates)
	}
	if view.Templates.State != TemplatesOtherApp {
		t.Fatalf("row = %+v", view.Templates)
	}
}

func TestAReadOnlyHomeRefusesTemplateActions(t *testing.T) {
	f, _ := templatesFixture(t)
	review := f.review()
	f.writable = false
	if _, err := f.service.TemplatesReview(testOwner, testHome, f.requestID()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("review: %v", err)
	}
	if _, err := f.commit(review.ReviewID); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("commit: %v", err)
	}
	if _, err := f.service.TemplatesForget(testOwner, testHome, f.requestID()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("forget: %v", err)
	}
}

func TestDecodeFactsBoundsEverythingThePluginSays(t *testing.T) {
	facts, err := decodeFacts(json.RawMessage(threeTemplates), reaperTemplates)
	if err != nil || len(facts.Templates) != 3 || facts.Version != "7.28" || facts.Truncated {
		t.Fatalf("facts = %+v err = %v", facts, err)
	}
	refused := map[string]string{
		"another application": `{"app":"Logic Pro","installed":true,"templates_available":false,"truncated":false}`,
		"an unknown key":      `{"app":"REAPER","installed":true,"templates_available":false,"truncated":false,"resource_path":"/Users/me/Library"}`,
		"trailing data":       `{"app":"REAPER","installed":true,"templates_available":false,"truncated":false}{}`,
		"not an object":       `["REAPER"]`,
		"too many": `{"app":"REAPER","installed":true,"templates_available":true,"truncated":false,"templates":[` +
			strings.TrimSuffix(strings.Repeat(`{"name":"A","kind":"project","file":"A.RPP"},`, workspace.HomeProfileMaxTemplates+1), ",") + `]}`,
	}
	for name, raw := range refused {
		if _, err := decodeFacts(json.RawMessage(raw), reaperTemplates); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// One unusable item is dropped and the list marked incomplete; nothing
	// path-like is ever kept.
	odd := `{"app":"reaper","installed":true,"version":"7.28\nrm -rf","templates_available":true,"truncated":false,"templates":[
	  {"name":"Good","kind":"project","file":"Good.RPP"},
	  {"name":"Absolute","kind":"project","file":"/Users/me/ProjectTemplates/Secret.RPP"},
	  {"name":"Parent","kind":"project","file":"../Secret.RPP"},
	  {"name":"Preset","kind":"preset","file":"Preset.RPP"},
	  {"name":"Two\nlines","kind":"track","file":"Lines.RTrackTemplate"},
	  {"name":"Good","kind":"project","file":"Good.RPP"}]}`
	facts, err = decodeFacts(json.RawMessage(odd), reaperTemplates)
	if err != nil || len(facts.Templates) != 1 || facts.Templates[0].File != "Good.RPP" || !facts.Truncated || facts.Version != "" {
		t.Fatalf("facts = %+v err = %v", facts, err)
	}
	// An application that is not installed lists nothing, whatever it says.
	gone := `{"app":"REAPER","installed":false,"version":"7.28","templates_available":false,"truncated":false,"templates":[{"name":"A","kind":"project","file":"A.RPP"}]}`
	if facts, err = decodeFacts(json.RawMessage(gone), reaperTemplates); err != nil || len(facts.Templates) != 0 || facts.Version != "" {
		t.Fatalf("not installed: %+v %v", facts, err)
	}
}

func TestTemplatesListIsCappedByTheStoredRecord(t *testing.T) {
	f, _ := templatesFixture(t)
	var items []string
	for i := 0; i < workspace.HomeProfileMaxTemplates; i++ {
		items = append(items, fmt.Sprintf(`{"name":"T%02d","kind":"project","file":"T%02d.RPP"}`, i, i))
	}
	f.factsRaw = `{"app":"REAPER","installed":true,"templates_available":true,"truncated":true,"templates":[` + strings.Join(items, ",") + `]}`
	view, err := f.commit(f.review().ReviewID)
	if err != nil || len(view.Profile.Templates.Items) != workspace.HomeProfileMaxTemplates || !view.Profile.Templates.Truncated {
		t.Fatalf("64 templates: %v %v", err, view.Profile.Templates)
	}
}
