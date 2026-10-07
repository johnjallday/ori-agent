package homeprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	testOwner = "local"
	testHome  = "music-home"
)

// fixture is a Home with a declared profile and host facts a test can change
// between calls.
type fixture struct {
	t        *testing.T
	store    *workspace.InMemoryStore
	service  *Service
	apps     []folderdigest.InstalledApp
	formats  map[string]int
	writable bool
	declared bool
	now      time.Time
	requests int
	// The application whose templates can be listed (none when AppID is ""),
	// what its plugin answers, and each call made to it.
	templatesApp TemplatesApp
	factsRaw     string
	factsErr     error
	factsCalls   []bool
}

var (
	appReaper  = folderdigest.InstalledApp{ToolID: "reaper", Name: "REAPER"}
	appLogic   = folderdigest.InstalledApp{ToolID: "logic-pro", Name: "Logic Pro"}
	appAbleton = folderdigest.InstalledApp{ToolID: "ableton-live", Name: "Ableton Live"}
)

func declaredFixture() Declared {
	return Declared{PluginID: "music-project-management", Version: "0.2.0", Profile: projecttemplates.HomeProfileDeclaration{
		SchemaVersion: 1, Title: "Your studio", Intro: "What Ori knows about where you make music.",
		Fields: []projecttemplates.HomeProfileField{
			{ID: "apps", Kind: projecttemplates.HomeProfileKindApps, Label: "DAWs on this Mac"},
			{ID: "main_app", Kind: projecttemplates.HomeProfileKindMainApp, Label: "Main DAW"},
			{ID: "templates", Kind: projecttemplates.HomeProfileKindTemplates, Label: "Project templates"},
			{ID: "defaults", Kind: projecttemplates.HomeProfileKindDefaults, Label: "New-song defaults"},
		},
	}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, store: workspace.NewInMemoryStore(), writable: true, declared: true,
		now: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	home := &workspace.Workspace{ID: testHome, Name: "Music Production Home", OwnerUserID: testOwner,
		Status: workspace.StatusActive, CreatedAt: f.now, UpdatedAt: f.now}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true,
		Key: workspace.AssistantProgramKey{OwnerUserID: testOwner, PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	})
	if err := f.store.Save(home); err != nil {
		t.Fatal(err)
	}
	f.service = New(Dependencies{
		Workspaces: f.store,
		Declared: func(*workspace.Workspace) (Declared, bool) {
			return declaredFixture(), f.declared
		},
		Writable:       func(*workspace.Workspace) bool { return f.writable },
		InstalledApps:  func() []folderdigest.InstalledApp { return f.apps },
		LibraryFormats: func(*workspace.Workspace) map[string]int { return f.formats },
		TimeSignatures: func(*workspace.Workspace) []Option {
			return []Option{{Value: "4 4", Label: "4/4"}, {Value: "3 4", Label: "3/4"}}
		},
		TemplatesApp: func(*workspace.Workspace) (TemplatesApp, bool) { return f.templatesApp, f.templatesApp.AppID != "" },
		ReadFacts: func(_ context.Context, _ *workspace.Workspace, includeTemplates bool) (json.RawMessage, error) {
			f.factsCalls = append(f.factsCalls, includeTemplates)
			if f.factsErr != nil {
				return nil, f.factsErr
			}
			return json.RawMessage(f.factsRaw), nil
		},
		Now: func() time.Time { return f.now },
	})
	return f
}

func (f *fixture) requestID() string {
	f.requests++
	return fmt.Sprintf("request-%d", f.requests)
}

func (f *fixture) detect() View {
	f.t.Helper()
	view, err := f.service.Detect(context.Background(), testOwner, testHome, f.requestID())
	if err != nil {
		f.t.Fatalf("detect: %v", err)
	}
	return view
}

func (f *fixture) fields(view View, input FieldsInput) (View, error) {
	f.t.Helper()
	input.RequestID, input.IfRevision = f.requestID(), view.Revision
	return f.service.SetFields(testOwner, testHome, input)
}

func (f *fixture) mustFields(view View, input FieldsInput) View {
	f.t.Helper()
	next, err := f.fields(view, input)
	if err != nil {
		f.t.Fatalf("fields %+v: %v", input, err)
	}
	return next
}

func ptr(value string) *string { return &value }

func appIDs(profile *workspace.HomeProfile) []string {
	var ids []string
	for _, app := range profile.Apps {
		ids = append(ids, app.ID)
	}
	return ids
}

func TestReadShowsTheDeclaredRowsBeforeAnythingIsStored(t *testing.T) {
	f := newFixture(t)
	view, err := f.service.Read(testOwner, testHome)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || view.ReadOnly || view.Title != "Your studio" || len(view.Fields) != 4 ||
		view.Profile != nil || view.Revision != 0 {
		t.Fatalf("view = %+v", view)
	}
	if view.Choices == nil || len(view.Choices.TimeSignatures) != 2 || len(view.Choices.SampleRates) != 6 || len(view.Choices.BitDepths) != 3 {
		t.Fatalf("choices = %+v", view.Choices)
	}
	// Reading never detects: the fixture has an application the card has not
	// been asked to look for.
	f.apps = []folderdigest.InstalledApp{appReaper}
	if again, _ := f.service.Read(testOwner, testHome); again.Profile != nil {
		t.Fatalf("a read detected applications: %+v", again.Profile)
	}
}

func TestReadIsOwnerOnlyAndHomeOnly(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.Read("someone-else", testHome); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user: %v", err)
	}
	if _, err := f.service.Read(testOwner, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing workspace: %v", err)
	}
	if _, err := f.service.Read("", testHome); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no user: %v", err)
	}
	// A linked project and a plain workspace are not Homes.
	project := &workspace.Workspace{ID: "song", Name: "Song", OwnerUserID: testOwner, Status: workspace.StatusActive}
	project.SetAssistantProjectLink(&workspace.AssistantProjectLink{StationWorkspaceID: testHome})
	plain := &workspace.Workspace{ID: "plain", Name: "Plain", OwnerUserID: testOwner, Status: workspace.StatusActive}
	for _, ws := range []*workspace.Workspace{project, plain} {
		if err := f.store.Save(ws); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Read(testOwner, ws.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", ws.ID, err)
		}
		if _, err := f.service.Detect(context.Background(), testOwner, ws.ID, "request-x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("detect on %s: %v", ws.ID, err)
		}
	}
	// A Home in the Trash is not served. The store only trashes a Home through
	// its removal review, so the rule is checked on a record directly.
	home, _ := f.store.Get(testHome)
	if _, ok := ownedHome(testOwner, home); !ok {
		t.Fatal("the owner's live Home was refused")
	}
	home.Status = workspace.StatusTrashed
	if _, ok := ownedHome(testOwner, home); ok {
		t.Fatal("a trashed Home was served")
	}
}

func TestAHomeWhosePackageDeclaresNoProfileIsUnavailable(t *testing.T) {
	f := newFixture(t)
	f.declared = false
	view, err := f.service.Read(testOwner, testHome)
	if err != nil || view.Available || view.Profile != nil || view.Fields != nil {
		t.Fatalf("view = %+v err = %v", view, err)
	}
	if _, err := f.service.Detect(context.Background(), testOwner, testHome, "request-1"); !errors.Is(err, ErrNotDeclared) {
		t.Fatalf("detect: %v", err)
	}
	if _, err := f.service.SetFields(testOwner, testHome, FieldsInput{RequestID: "request-2", MainApp: ptr("reaper")}); !errors.Is(err, ErrNotDeclared) {
		t.Fatalf("fields: %v", err)
	}
}

func TestDetectOneApplicationProposesItAsMain(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	view := f.detect()
	profile := view.Profile
	if profile == nil || view.Revision != 1 || profile.Revision != 1 || profile.Requests != nil {
		t.Fatalf("view = %+v", view)
	}
	declared := profile.DeclaredBy
	if declared.PluginID != "music-project-management" || declared.Version != "0.2.0" || declared.Title != "Your studio" ||
		len(declared.Labels) != 4 || declared.Label(workspace.HomeProfileKindMainApp, "") != "Main DAW" ||
		declared.Label("hardware", "fallback") != "fallback" {
		t.Fatalf("declared_by = %+v", declared)
	}
	if len(profile.Apps) != 1 || !profile.Apps[0].Detected || profile.Apps[0].ConfirmedAt != nil || !profile.Apps[0].DetectedAt.Equal(f.now) {
		t.Fatalf("apps = %+v", profile.Apps)
	}
	main := profile.MainApp
	if main == nil || main.ID != "reaper" || main.Source != workspace.HomeProfileSourceDetected ||
		main.Reason != workspace.HomeProfileMainAppOnly || main.ConfirmedAt != nil {
		t.Fatalf("main app = %+v", main)
	}
	if profile.DetectedAt == nil || !profile.DetectedAt.Equal(f.now) {
		t.Fatalf("detected_at = %v", profile.DetectedAt)
	}
}

func TestDetectNothingRecordsThatItLooked(t *testing.T) {
	f := newFixture(t)
	profile := f.detect().Profile
	if profile == nil || len(profile.Apps) != 0 || profile.MainApp != nil || profile.DetectedAt == nil {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestDetectSeveralApplicationsUsesTheLibraryOrAsks(t *testing.T) {
	tests := map[string]struct {
		formats map[string]int
		want    string
	}{
		"empty library":       {nil, ""},
		"a tie":               {map[string]int{"reaper": 4, "logic": 4}, ""},
		"only other formats":  {map[string]int{"ableton": 9}, ""},
		"first has the most":  {map[string]int{"reaper": 12, "logic": 3}, "reaper"},
		"second has the most": {map[string]int{"reaper": 1, "logic": 7}, "logic-pro"},
		"only one has any":    {map[string]int{"logic": 1}, "logic-pro"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.apps, f.formats = []folderdigest.InstalledApp{appReaper, appLogic}, tc.formats
			profile := f.detect().Profile
			if tc.want == "" {
				if profile.MainApp != nil {
					t.Fatalf("main app = %+v, want none", profile.MainApp)
				}
				return
			}
			main := profile.MainApp
			if main == nil || main.ID != tc.want || main.Source != workspace.HomeProfileSourceDetected ||
				main.Reason != workspace.HomeProfileMainAppLibrary || main.ConfirmedAt != nil {
				t.Fatalf("main app = %+v, want %s", main, tc.want)
			}
		})
	}
}

func TestDetectListsApplicationsInTableOrder(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appAbleton, appReaper}
	f.detect()
	f.apps = []folderdigest.InstalledApp{appLogic, appAbleton, appReaper}
	profile := f.detect().Profile
	if got := appIDs(profile); len(got) != 3 || got[0] != "reaper" || got[1] != "logic-pro" || got[2] != "ableton-live" {
		t.Fatalf("order = %v", got)
	}
}

func TestDetectAgainKeepsWhatTheOwnerSaid(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic, appAbleton}
	view := f.detect()
	view = f.mustFields(view, FieldsInput{ConfirmApps: []string{"reaper"}, HideApps: []string{"ableton-live"}, MainApp: ptr("logic-pro")})
	confirmedAt := *view.Profile.Apps[0].ConfirmedAt

	// Later only one application is still installed, and the library now
	// leans the other way.
	f.now = f.now.Add(24 * time.Hour)
	f.apps, f.formats = []folderdigest.InstalledApp{appReaper}, map[string]int{"reaper": 50}
	profile := f.detect().Profile

	reaper, _ := profile.App("reaper")
	if !reaper.Detected || !reaper.ConfirmedAt.Equal(confirmedAt) || !reaper.DetectedAt.Equal(f.now) {
		t.Fatalf("confirmed app = %+v", reaper)
	}
	logic, listed := profile.App("logic-pro")
	if !listed || logic.Detected || logic.DetectedAt.Equal(f.now) {
		t.Fatalf("the owner's main app was dropped or re-dated: %+v %v", logic, listed)
	}
	ableton, listed := profile.App("ableton-live")
	if !listed || !ableton.Hidden || ableton.Detected {
		t.Fatalf("a hidden app came back or was dropped: %+v %v", ableton, listed)
	}
	if main := profile.MainApp; main == nil || main.ID != "logic-pro" || main.Source != workspace.HomeProfileSourceOwner {
		t.Fatalf("the owner's main app was replaced: %+v", main)
	}
}

func TestDetectAgainDropsUnconfirmedApplicationsThatAreGone(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	f.detect()
	f.apps = []folderdigest.InstalledApp{appLogic}
	profile := f.detect().Profile
	if got := appIDs(profile); len(got) != 1 || got[0] != "logic-pro" {
		t.Fatalf("apps = %v", got)
	}
	// The proposal followed the application that is still there.
	if main := profile.MainApp; main == nil || main.ID != "logic-pro" || main.Reason != workspace.HomeProfileMainAppOnly {
		t.Fatalf("main app = %+v", main)
	}
	f.apps = nil
	if profile = f.detect().Profile; len(profile.Apps) != 0 || profile.MainApp != nil {
		t.Fatalf("profile after everything was removed = %+v", profile)
	}
}

func TestConfirmingTheProposedMainAppKeepsItDetected(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	view := f.detect()
	view = f.mustFields(view, FieldsInput{MainApp: ptr("reaper")})
	main := view.Profile.MainApp
	if main.Source != workspace.HomeProfileSourceDetected || main.ConfirmedAt == nil || main.Reason != workspace.HomeProfileMainAppOnly {
		t.Fatalf("confirmed proposal = %+v", main)
	}
	if view.Revision != 2 {
		t.Fatalf("revision = %d", view.Revision)
	}
	// Saying it again changes nothing and writes nothing.
	again := f.mustFields(view, FieldsInput{MainApp: ptr("reaper")})
	if again.Revision != 2 {
		t.Fatalf("an unchanged save moved the revision to %d", again.Revision)
	}
}

func TestPickingAnotherMainAppIsTheOwnersInstruction(t *testing.T) {
	f := newFixture(t)
	f.apps, f.formats = []folderdigest.InstalledApp{appReaper, appLogic}, map[string]int{"reaper": 9}
	view := f.detect()
	view = f.mustFields(view, FieldsInput{MainApp: ptr("logic-pro")})
	main := view.Profile.MainApp
	if main.ID != "logic-pro" || main.Source != workspace.HomeProfileSourceOwner || main.ConfirmedAt == nil || main.Reason != "" {
		t.Fatalf("main app = %+v", main)
	}
	// Clearing it is allowed; the next detection proposes again.
	view = f.mustFields(view, FieldsInput{MainApp: ptr("")})
	if view.Profile.MainApp != nil {
		t.Fatalf("main app after clearing = %+v", view.Profile.MainApp)
	}
	if main = f.detect().Profile.MainApp; main == nil || main.ID != "reaper" || main.Source != workspace.HomeProfileSourceDetected {
		t.Fatalf("proposal after clearing = %+v", main)
	}
}

func TestMainAppMustBeADetectedVisibleApplication(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	view := f.detect()
	if _, err := f.fields(view, FieldsInput{MainApp: ptr("ableton-live")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an application that was not detected: %v", err)
	}
	view = f.mustFields(view, FieldsInput{HideApps: []string{"logic-pro"}})
	if _, err := f.fields(view, FieldsInput{MainApp: ptr("logic-pro")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a hidden application: %v", err)
	}
	for _, input := range []FieldsInput{
		{ConfirmApps: []string{"ableton-live"}},
		{HideApps: []string{"unknown"}},
		{ShowApps: []string{"unknown"}},
		{ConfirmApps: []string{"reaper"}, HideApps: []string{"reaper"}},
		{ConfirmApps: []string{"reaper", "reaper"}},
	} {
		if _, err := f.fields(view, input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", input, err)
		}
	}
	if current, _ := f.service.Read(testOwner, testHome); current.Revision != view.Revision {
		t.Fatalf("a refused save moved the revision to %d", current.Revision)
	}
}

func TestNotMineHidesAnApplicationAndRerunsTheRule(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	view := f.detect()
	if view.Profile.MainApp != nil {
		t.Fatalf("two applications and an empty library proposed %+v", view.Profile.MainApp)
	}
	view = f.mustFields(view, FieldsInput{HideApps: []string{"logic-pro"}})
	logic, _ := view.Profile.App("logic-pro")
	if !logic.Hidden || !logic.Detected || logic.ConfirmedAt != nil {
		t.Fatalf("hidden app = %+v", logic)
	}
	if main := view.Profile.MainApp; main == nil || main.ID != "reaper" || main.Reason != workspace.HomeProfileMainAppOnly {
		t.Fatalf("with one application left the rule proposed %+v", main)
	}
	// Bringing it back leaves two candidates, so the unconfirmed proposal goes.
	view = f.mustFields(view, FieldsInput{ShowApps: []string{"logic-pro"}})
	if logic, _ = view.Profile.App("logic-pro"); logic.Hidden || view.Profile.MainApp != nil {
		t.Fatalf("after Show again: app %+v main %+v", logic, view.Profile.MainApp)
	}
}

func TestNotMineOnTheMainAppClearsIt(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	view := f.detect()
	view = f.mustFields(view, FieldsInput{MainApp: ptr("logic-pro"), ConfirmApps: []string{"logic-pro"}})
	view = f.mustFields(view, FieldsInput{HideApps: []string{"logic-pro"}})
	if main := view.Profile.MainApp; main == nil || main.ID != "reaper" || main.Source != workspace.HomeProfileSourceDetected {
		t.Fatalf("main app after hiding the owner's pick = %+v", main)
	}
	if logic, _ := view.Profile.App("logic-pro"); logic.ConfirmedAt != nil {
		t.Fatalf("a hidden app stayed confirmed: %+v", logic)
	}
}

func TestConfirmingAnApplicationDoesNotDisturbTheLibraryProposal(t *testing.T) {
	f := newFixture(t)
	f.apps, f.formats = []folderdigest.InstalledApp{appReaper, appLogic}, map[string]int{"reaper": 9}
	view := f.detect()
	// The library no longer answers, as when a Home's library is unreadable.
	f.formats = nil
	view = f.mustFields(view, FieldsInput{ConfirmApps: []string{"logic-pro"}})
	if main := view.Profile.MainApp; main == nil || main.ID != "reaper" || main.Reason != workspace.HomeProfileMainAppLibrary {
		t.Fatalf("confirming one row changed the proposal to %+v", main)
	}
}

func TestStaleRevisionIsRefused(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	first := f.detect()
	f.mustFields(first, FieldsInput{ConfirmApps: []string{"reaper"}})
	// A second card still showing the first revision.
	if _, err := f.fields(first, FieldsInput{MainApp: ptr("logic-pro")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale save: %v", err)
	}
	// A card that has never seen a record cannot overwrite one either.
	if _, err := f.service.SetFields(testOwner, testHome, FieldsInput{RequestID: "fresh-card", MainApp: ptr("logic-pro")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("revision 0 against a stored record: %v", err)
	}
}

func TestFieldsCanCreateTheRecord(t *testing.T) {
	f := newFixture(t)
	view, err := f.service.SetFields(testOwner, testHome, FieldsInput{RequestID: "request-1", Defaults: &DefaultsInput{TempoBPM: 96}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != 1 || view.Profile.Defaults.TempoBPM != 96 || view.Profile.DetectedAt != nil {
		t.Fatalf("view = %+v", view.Profile)
	}
}

func TestARepeatedRequestIDReplays(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper, appLogic}
	first, err := f.service.Detect(context.Background(), testOwner, testHome, "detect-1")
	if err != nil || first.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}
	// The same click arriving twice does not detect again.
	f.now, f.apps = f.now.Add(time.Hour), nil
	again, err := f.service.Detect(context.Background(), testOwner, testHome, "detect-1")
	if err != nil || !again.Replayed || again.Revision != 1 || len(again.Profile.Apps) != 2 {
		t.Fatalf("replay: %+v %v", again, err)
	}
	saved, err := f.service.SetFields(testOwner, testHome, FieldsInput{RequestID: "save-1", IfRevision: 1, MainApp: ptr("logic-pro")})
	if err != nil {
		t.Fatal(err)
	}
	// A replayed save is not refused as stale, although the revision moved.
	replayed, err := f.service.SetFields(testOwner, testHome, FieldsInput{RequestID: "save-1", IfRevision: 1, MainApp: ptr("logic-pro")})
	if err != nil || !replayed.Replayed || replayed.Revision != saved.Revision {
		t.Fatalf("replayed save: %+v %v", replayed, err)
	}
	// One request_id is one action.
	if _, err := f.service.Detect(context.Background(), testOwner, testHome, "save-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("request_id reused for another action: %v", err)
	}
	for _, bad := range []string{"", " padded ", "two\nlines", string(make([]byte, 121))} {
		if _, err := f.service.Detect(context.Background(), testOwner, testHome, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("request_id %q: %v", bad, err)
		}
	}
}

func TestRequestReceiptsAreBounded(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	for i := 0; i < workspace.HomeProfileMaxRequests+5; i++ {
		f.now = f.now.Add(time.Minute)
		f.detect()
	}
	home, _ := f.store.Get(testHome)
	profile := home.GetAssistantProgramState().GetHomeProfile()
	if profile == nil || len(profile.Requests) != workspace.HomeProfileMaxRequests || profile.Revision != int64(workspace.HomeProfileMaxRequests+5) {
		t.Fatalf("receipts = %d revision = %d", len(profile.Requests), profile.Revision)
	}
	if profile.Requests[len(profile.Requests)-1].ID != fmt.Sprintf("request-%d", f.requests) {
		t.Fatalf("the newest receipt was not kept: %+v", profile.Requests)
	}
}

func TestAReadOnlyHomeShowsValuesAndRefusesEveryWrite(t *testing.T) {
	f := newFixture(t)
	f.apps = []folderdigest.InstalledApp{appReaper}
	view := f.detect()
	f.writable = false
	read, err := f.service.Read(testOwner, testHome)
	if err != nil || !read.ReadOnly || read.Profile == nil || read.Profile.MainApp == nil {
		t.Fatalf("read-only read = %+v %v", read, err)
	}
	if _, err := f.service.Detect(context.Background(), testOwner, testHome, f.requestID()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("detect: %v", err)
	}
	if _, err := f.fields(view, FieldsInput{MainApp: ptr("reaper")}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("fields: %v", err)
	}
}

func TestDefaultsAreBoundedAndReplacedAsAWhole(t *testing.T) {
	f := newFixture(t)
	view := f.detect()
	view = f.mustFields(view, FieldsInput{Defaults: &DefaultsInput{TempoBPM: 120, TimeSignature: "4 4", SampleRateHz: 48000, BitDepth: 24}})
	defaults := view.Profile.Defaults
	if defaults == nil || defaults.TempoBPM != 120 || defaults.TimeSignature != "4 4" || defaults.SampleRateHz != 48000 ||
		defaults.BitDepth != 24 || defaults.Source != workspace.HomeProfileSourceOwner || defaults.ConfirmedAt == nil {
		t.Fatalf("defaults = %+v", defaults)
	}
	for name, input := range map[string]DefaultsInput{
		"slow tempo":      {TempoBPM: 39},
		"fast tempo":      {TempoBPM: 241},
		"unknown meter":   {TimeSignature: "13 8"},
		"odd sample rate": {SampleRateHz: 44000},
		"odd bit depth":   {BitDepth: 20},
	} {
		if _, err := f.fields(view, FieldsInput{Defaults: &input}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A save names every part: what it leaves out is no longer set.
	view = f.mustFields(view, FieldsInput{Defaults: &DefaultsInput{TempoBPM: 92}})
	if defaults = view.Profile.Defaults; defaults.TempoBPM != 92 || defaults.TimeSignature != "" || defaults.SampleRateHz != 0 || defaults.BitDepth != 0 {
		t.Fatalf("defaults after a partial save = %+v", defaults)
	}
	unchanged := f.mustFields(view, FieldsInput{Defaults: &DefaultsInput{TempoBPM: 92}})
	if unchanged.Revision != view.Revision {
		t.Fatal("saving the same defaults wrote the Home")
	}
	view = f.mustFields(view, FieldsInput{Defaults: &DefaultsInput{}})
	if view.Profile.Defaults != nil {
		t.Fatalf("cleared defaults = %+v", view.Profile.Defaults)
	}
}

func TestProfileWritesLeaveTheHomeStateRevisionAlone(t *testing.T) {
	f := newFixture(t)
	before, _ := f.store.Get(testHome)
	f.apps = []folderdigest.InstalledApp{appReaper}
	view := f.detect()
	f.mustFields(view, FieldsInput{ConfirmApps: []string{"reaper"}})
	after, _ := f.store.Get(testHome)
	// Pending library reviews bind the Home's state revision; a profile edit
	// must not cancel them.
	if before.GetAssistantProgramState().StateRevision != after.GetAssistantProgramState().StateRevision {
		t.Fatal("a profile write moved the Home's state revision")
	}
}

func TestAnInvalidStoredProfileIsReplacedByTheNextWrite(t *testing.T) {
	f := newFixture(t)
	if err := f.store.Update(testHome, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.HomeProfile = &workspace.HomeProfile{SchemaVersion: 99, Revision: 7}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view, _ := f.service.Read(testOwner, testHome); view.Profile != nil || view.Revision != 0 {
		t.Fatalf("an invalid stored profile was served: %+v", view)
	}
	f.apps = []folderdigest.InstalledApp{appReaper}
	if view := f.detect(); view.Revision != 1 || view.Profile.SchemaVersion != workspace.HomeProfileSchemaVersion {
		t.Fatalf("view = %+v", view)
	}
}
