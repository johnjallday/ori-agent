package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/homeprofile"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type homeProfileHTTP struct {
	t       *testing.T
	handler *Handler
	store   *workspace.InMemoryStore
	home    *workspace.Workspace
	project *workspace.Workspace
	user    string
	apps    []folderdigest.InstalledApp
	// installed is what the plugin lister reports; a test may change it.
	installed []plugin.InstalledPlugin
}

// homeProfileHTTPFixture is a Home pinned to an installed package. withProfile
// says whether that package's Home declaration has the profile section.
func homeProfileHTTPFixture(t *testing.T, withProfile bool) *homeProfileHTTP {
	t.Helper()
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	f := &homeProfileHTTP{t: t, handler: handler, store: store, home: station, project: project, user: station.OwnerUserID}
	handler.currentUserID = func(context.Context) (string, error) { return f.user, nil }
	handler.SetHomeProfileAppDetector(func() []folderdigest.InstalledApp { return f.apps })

	state := station.GetAssistantProgramState()
	declaration := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: state.Key.ProgramID,
		StationName: "Portfolio Home", DefaultPrimaryName: "Guide", HireTitle: "Staff guide",
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "guide", Label: "Guide", Required: true,
			Primary: true, SystemPrompt: "Coordinate reviewed Home work."}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168,
			MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		}},
	}
	features := []string{plugin.HostFeatureIndependentProgramHomesV1}
	if withProfile {
		features = append(features, plugin.HostFeatureHomeProfileV1)
		declaration.HomeProfile = &projecttemplates.HomeProfileDeclaration{
			SchemaVersion: 1, Title: "Your studio", Intro: "What Ori knows about where you make music.",
			Fields: []projecttemplates.HomeProfileField{
				{ID: "apps", Kind: projecttemplates.HomeProfileKindApps, Label: "DAWs on this Mac"},
				{ID: "main_app", Kind: projecttemplates.HomeProfileKindMainApp, Label: "Main DAW"},
				{ID: "templates", Kind: projecttemplates.HomeProfileKindTemplates, Label: "Project templates"},
				{ID: "defaults", Kind: projecttemplates.HomeProfileKindDefaults, Label: "New-song defaults"},
			},
		}
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&declaration); err != nil {
		t.Fatal(err)
	}
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: state.Key.PluginID, PluginVersion: "0.2.0", ProgramID: state.Key.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration),
		PluginGeneration: 4, ComponentFingerprint: strings.Repeat("a", 64),
	}
	if err := store.Update(station.ID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.HomeProvider = &owner
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.installed = []plugin.InstalledPlugin{
		{
			Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: owner.PluginGeneration,
			ComponentFingerprint: owner.ComponentFingerprint,
			WorkspaceSurfaces: &plugin.SurfaceContribution{
				SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
				Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: features,
				AssistantProgramHomes: []projecttemplates.AssistantProgramHome{declaration},
			},
		},
		{
			Name: "reaper-plugin", Version: "0.10.0", Enabled: true,
			ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", Version: 10, Template: projecttemplates.Template{
				Inputs: &projecttemplates.InputsDeclaration{Fields: []projecttemplates.InputField{
					{ID: "tempo", Label: "Tempo", Type: projecttemplates.InputFieldNumber, Min: 40, Max: 240, Step: 1, Default: "120"},
					{ID: "time_signature", Label: "Time signature", Type: projecttemplates.InputFieldSelect, Default: "4 4",
						Options: []projecttemplates.InputOption{{Value: "4 4", Label: "4/4"}, {Value: "3 4", Label: "3/4"}}},
				}},
			}}},
		},
	}
	handler.SetInstalledPluginLister(installedPluginListerFunc(func() ([]plugin.InstalledPlugin, error) { return f.installed, nil }))
	return f
}

// call runs one profile route and returns its status and decoded body.
func (f *homeProfileHTTP) call(handler func(http.ResponseWriter, *http.Request), method, workspaceID, body string) (int, map[string]any) {
	f.t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, assistantProgramRequest(method, "/assistant-program/profile", workspaceID, body))
	decoded := map[string]any{}
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			f.t.Fatalf("decode %s: %v", recorder.Body.String(), err)
		}
	}
	return recorder.Code, decoded
}

func (f *homeProfileHTTP) view(handler func(http.ResponseWriter, *http.Request), method, body string) homeprofile.View {
	f.t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, assistantProgramRequest(method, "/assistant-program/profile", f.home.ID, body))
	if recorder.Code != http.StatusOK {
		f.t.Fatalf("%s %s = %d: %s", method, body, recorder.Code, recorder.Body.String())
	}
	var view homeprofile.View
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		f.t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return view
}

func TestAssistantProfileHTTPDetectPickAndConfirm(t *testing.T) {
	f := homeProfileHTTPFixture(t, true)
	h := f.handler

	view := f.view(h.GetAssistantProfile, http.MethodGet, "")
	if !view.Available || view.ReadOnly || view.Title != "Your studio" || len(view.Fields) != 4 || view.Profile != nil || view.Revision != 0 {
		t.Fatalf("first read = %+v", view)
	}
	if view.Choices == nil || len(view.Choices.TimeSignatures) != 2 || view.Choices.TimeSignatures[1].Label != "3/4" {
		t.Fatalf("choices = %+v", view.Choices)
	}

	f.apps = []folderdigest.InstalledApp{{ToolID: "reaper", Name: "REAPER"}, {ToolID: "logic-pro", Name: "Logic Pro"}}
	view = f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)
	if view.Revision != 1 || len(view.Profile.Apps) != 2 || view.Profile.MainApp != nil {
		t.Fatalf("after detect = %+v", view.Profile)
	}

	view = f.view(h.SetAssistantProfileFields, http.MethodPost,
		`{"request_id":"save-1","if_revision":1,"main_app":"reaper","confirm_apps":["reaper"]}`)
	main := view.Profile.MainApp
	if view.Revision != 2 || main == nil || main.ID != "reaper" || main.Source != workspace.HomeProfileSourceOwner || main.ConfirmedAt == nil {
		t.Fatalf("after pick = %+v", view.Profile)
	}
	if app, _ := view.Profile.App("reaper"); app.ConfirmedAt == nil {
		t.Fatalf("confirmed app = %+v", app)
	}

	view = f.view(h.SetAssistantProfileFields, http.MethodPost,
		`{"request_id":"save-2","if_revision":2,"defaults":{"tempo_bpm":96,"time_signature":"3 4","sample_rate_hz":48000,"bit_depth":24},"hide_apps":["logic-pro"]}`)
	if view.Profile.Defaults == nil || view.Profile.Defaults.TempoBPM != 96 || view.Profile.Defaults.TimeSignature != "3 4" {
		t.Fatalf("defaults = %+v", view.Profile.Defaults)
	}
	if app, _ := view.Profile.App("logic-pro"); !app.Hidden {
		t.Fatalf("hidden app = %+v", app)
	}
	view = f.view(h.SetAssistantProfileFields, http.MethodPost, `{"request_id":"save-3","if_revision":3,"show_apps":["logic-pro"]}`)
	if app, _ := view.Profile.App("logic-pro"); app.Hidden {
		t.Fatalf("shown app = %+v", app)
	}

	// The stored record carries no request receipts to the browser.
	recorder := httptest.NewRecorder()
	h.GetAssistantProfile(recorder, assistantProgramRequest(http.MethodGet, "/assistant-program/profile", f.home.ID, ""))
	if strings.Contains(recorder.Body.String(), "request_id") || strings.Contains(recorder.Body.String(), `"requests"`) {
		t.Fatalf("the card response leaks receipts: %s", recorder.Body.String())
	}
	// It is the Home's own state that changed.
	stored, _ := f.store.Get(f.home.ID)
	if profile := stored.GetAssistantProgramState().GetHomeProfile(); profile == nil || profile.Revision != 4 || len(profile.Requests) != 4 {
		t.Fatalf("stored profile = %+v", profile)
	}
}

func TestAssistantProfileHTTPConflictsAndBadRequests(t *testing.T) {
	f := homeProfileHTTPFixture(t, true)
	h := f.handler
	f.apps = []folderdigest.InstalledApp{{ToolID: "reaper", Name: "REAPER"}}
	f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)

	status, body := f.call(h.SetAssistantProfileFields, http.MethodPost, f.home.ID, `{"request_id":"stale","if_revision":0,"main_app":"reaper"}`)
	if status != http.StatusConflict || body["code"] != "home_profile_changed" {
		t.Fatalf("stale save = %d %v", status, body)
	}
	for name, payload := range map[string]string{
		"app that was not detected": `{"request_id":"a","if_revision":1,"main_app":"logic-pro"}`,
		"tempo out of bounds":       `{"request_id":"b","if_revision":1,"defaults":{"tempo_bpm":300}}`,
		"unknown time signature":    `{"request_id":"c","if_revision":1,"defaults":{"time_signature":"13 8"}}`,
		"missing if_revision":       `{"request_id":"d","main_app":"reaper"}`,
		"negative if_revision":      `{"request_id":"e","if_revision":-1,"main_app":"reaper"}`,
		"missing request_id":        `{"if_revision":1,"main_app":"reaper"}`,
		"unknown key":               `{"request_id":"f","if_revision":1,"templates":{"items":[]}}`,
		"unknown defaults key":      `{"request_id":"g","if_revision":1,"defaults":{"key":"C"}}`,
		"duplicate key":             `{"request_id":"h","request_id":"i","if_revision":1}`,
		"trailing JSON":             `{"request_id":"j","if_revision":1}{}`,
		"not an object":             `["request_id"]`,
		"empty body":                ``,
	} {
		if status, body := f.call(h.SetAssistantProfileFields, http.MethodPost, f.home.ID, payload); status != http.StatusBadRequest {
			t.Errorf("%s = %d %v, want 400", name, status, body)
		}
	}
	for name, payload := range map[string]string{
		"unknown key":        `{"request_id":"k","include_templates":true}`,
		"missing request_id": `{}`,
	} {
		if status, body := f.call(h.DetectAssistantProfile, http.MethodPost, f.home.ID, payload); status != http.StatusBadRequest {
			t.Errorf("detect %s = %d %v, want 400", name, status, body)
		}
	}
	if view := f.view(h.GetAssistantProfile, http.MethodGet, ""); view.Revision != 1 {
		t.Fatalf("a refused request moved the revision to %d", view.Revision)
	}

	// A repeated request_id replays instead of writing again.
	first := f.view(h.SetAssistantProfileFields, http.MethodPost, `{"request_id":"once","if_revision":1,"confirm_apps":["reaper"]}`)
	again := f.view(h.SetAssistantProfileFields, http.MethodPost, `{"request_id":"once","if_revision":1,"confirm_apps":["reaper"]}`)
	if first.Replayed || !again.Replayed || again.Revision != first.Revision {
		t.Fatalf("replay: first %+v again %+v", first, again)
	}
}

func TestAssistantProfileHTTPIsOwnerOnlyOnThatExactHome(t *testing.T) {
	f := homeProfileHTTPFixture(t, true)
	h := f.handler
	routes := map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		method  string
		body    string
	}{
		"read":   {h.GetAssistantProfile, http.MethodGet, ""},
		"detect": {h.DetectAssistantProfile, http.MethodPost, `{"request_id":"x"}`},
		"fields": {h.SetAssistantProfileFields, http.MethodPost, `{"request_id":"y","if_revision":0,"defaults":{"tempo_bpm":100}}`},
	}
	for name, route := range routes {
		// The Home's linked project, a workspace that does not exist, and no id.
		for _, id := range []string{f.project.ID, "missing", ""} {
			if status, body := f.call(route.handler, route.method, id, route.body); status != http.StatusNotFound {
				t.Errorf("%s on %q = %d %v, want 404", name, id, status, body)
			}
		}
		// Another signed-in user, and nobody.
		for _, user := range []string{"someone-else", ""} {
			f.user = user
			if status, body := f.call(route.handler, route.method, f.home.ID, route.body); status != http.StatusNotFound {
				t.Errorf("%s as %q = %d %v, want 404", name, user, status, body)
			}
		}
		f.user = f.home.OwnerUserID
	}
	stored, _ := f.store.Get(f.home.ID)
	if stored.GetAssistantProgramState().HomeProfile != nil {
		t.Fatal("a refused request wrote a profile")
	}
}

func TestAssistantProfileHTTPHomeWithoutADeclaredProfile(t *testing.T) {
	f := homeProfileHTTPFixture(t, false)
	h := f.handler
	// The owner's own Home answers, saying there is no card.
	view := f.view(h.GetAssistantProfile, http.MethodGet, "")
	if view.Available || view.Profile != nil || view.Fields != nil || view.Title != "" {
		t.Fatalf("view = %+v", view)
	}
	// Nothing can be written to it.
	if status, _ := f.call(h.DetectAssistantProfile, http.MethodPost, f.home.ID, `{"request_id":"x"}`); status != http.StatusNotFound {
		t.Fatalf("detect = %d, want 404", status)
	}
	// A read takes no parameters: nothing can ask it to detect.
	request := httptest.NewRequest(http.MethodGet, "/assistant-program/profile?detect=1", nil)
	request.SetPathValue("workspaceID", f.home.ID)
	recorder := httptest.NewRecorder()
	h.GetAssistantProfile(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("read with a query = %d, want 400", recorder.Code)
	}
}

func TestAssistantProfileHTTPReadOnlyHome(t *testing.T) {
	f := homeProfileHTTPFixture(t, true)
	h := f.handler
	f.apps = []folderdigest.InstalledApp{{ToolID: "reaper", Name: "REAPER"}}
	f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)

	// The package is switched off: the Home keeps its words and its values.
	f.installed[0].Enabled = false
	view := f.view(h.GetAssistantProfile, http.MethodGet, "")
	if !view.Available || !view.ReadOnly || view.Title != "Your studio" || view.Profile == nil || view.Profile.MainApp == nil {
		t.Fatalf("read-only view = %+v", view)
	}
	for name, route := range map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		body    string
	}{
		"detect": {h.DetectAssistantProfile, `{"request_id":"detect-2"}`},
		"fields": {h.SetAssistantProfileFields, `{"request_id":"save-1","if_revision":1,"main_app":"reaper"}`},
	} {
		status, body := f.call(route.handler, http.MethodPost, f.home.ID, route.body)
		if status != http.StatusConflict || body["code"] != "home_read_only" {
			t.Errorf("%s on a read-only Home = %d %v", name, status, body)
		}
	}

	// A different release of the package is not this Home's declaration.
	f.installed[0].Enabled, f.installed[0].Version = true, "0.3.0"
	if view := f.view(h.GetAssistantProfile, http.MethodGet, ""); view.Available {
		t.Fatalf("a Home pinned to 0.2.0 showed the card of 0.3.0: %+v", view)
	}
}

func TestAssistantProfileHTTPMainAppFollowsTheLibrary(t *testing.T) {
	f := homeProfileHTTPFixture(t, true)
	// No library: two applications leave the choice to the owner. The library
	// path itself is covered in internal/homeprofile; here the handler's reader
	// must simply not invent counts for a Home without one.
	home, err := f.store.Get(f.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts := f.handler.homeProfileLibraryFormats(home); counts != nil {
		t.Fatalf("a Home without a library counted %v", counts)
	}
	if options := f.handler.homeProfileTimeSignatures(home); len(options) != 2 || options[0].Value != "4 4" {
		t.Fatalf("time signatures = %+v", options)
	}
	// Without the project plugin there is no list to choose from, so a time
	// signature cannot be saved.
	f.installed = f.installed[:1]
	if options := f.handler.homeProfileTimeSignatures(home); options != nil {
		t.Fatalf("time signatures without the project plugin = %+v", options)
	}
	status, _ := f.call(f.handler.SetAssistantProfileFields, http.MethodPost, f.home.ID,
		`{"request_id":"a","if_revision":0,"defaults":{"time_signature":"4 4"}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("time signature without choices = %d, want 400", status)
	}
}
