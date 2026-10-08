package chathttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// profileToolHome is a Home with a bound primary Manager, an initialized
// library and the given profile.
func profileToolHome(t *testing.T, profile *workspace.HomeProfile) (*workspace.FileStore, *workspace.Workspace) {
	t.Helper()
	file, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID = "local"
	key := workspace.AssistantProgramKey{OwnerUserID: home.OwnerUserID, PluginID: "music-project-management", ProgramID: "music-producer-assistant"}
	home.AgentInstances = []workspace.AgentInstance{{ID: "manager-instance", Name: "Manager", RoleID: "portfolio_manager"},
		{ID: "sample-instance", Name: "Sample", RoleID: "sample_library_manager"}}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true, Key: key,
		Declaration: &workspace.AssistantProgramDeclaration{Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "portfolio_manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true},
			{ID: "sample_library_manager", Scope: workspace.AssistantRoleScopeHome}}},
		HomeBindings: workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "manager-instance", AgentName: "Manager"},
			{RoleID: "sample_library_manager", AgentInstanceID: "sample-instance", AgentName: "Sample"}}},
		HomeProfile: profile,
	})
	if err := file.Save(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Manager", "Sample"} {
		if err := file.SaveWorkspaceAgent(home.ID, name, &agent.Agent{}); err != nil {
			t.Fatal(err)
		}
	}
	scope := projectlibrary.Scope{OwnerUserID: home.OwnerUserID, HomeID: home.ID, ProviderID: key.PluginID, ProgramID: key.ProgramID}
	library := projectlibrary.NewStore(file).WithProviderEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "profile-tool-init"); err != nil {
		t.Fatal(err)
	}
	return file, home
}

func profileToolFixture() *workspace.HomeProfile {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	return &workspace.HomeProfile{
		SchemaVersion: workspace.HomeProfileSchemaVersion, Revision: 2, DetectedAt: &at,
		DeclaredBy: workspace.HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0", Title: "Your studio"},
		Apps: []workspace.HomeProfileApp{
			{ID: "reaper", Name: "REAPER", Version: "7.28", Detected: true, DetectedAt: &at, ConfirmedAt: &at},
			{ID: "logic-pro", Name: "Logic Pro", Detected: true, DetectedAt: &at},
			{ID: "ableton-live", Name: "Ableton Live", Detected: true, DetectedAt: &at, Hidden: true},
		},
		MainApp: &workspace.HomeProfileMainApp{ID: "reaper", Source: workspace.HomeProfileSourceDetected,
			Reason: workspace.HomeProfileMainAppOnly, ConfirmedAt: &at},
		Templates: &workspace.HomeProfileTemplates{
			Consent: &workspace.HomeProfileTemplatesConsent{GrantedAt: at, Source: workspace.HomeProfileTemplatesHomeReview},
			ReadAt:  &at, AppID: "reaper",
			Items: []workspace.HomeProfileTemplate{
				{Name: "Band Session", Kind: workspace.HomeProfileTemplateProject, File: "Band Session.RPP"},
				{Name: "Drum Bus", Kind: workspace.HomeProfileTemplateTrack, File: "Drum Bus.RTrackTemplate"},
			},
		},
		Defaults: &workspace.HomeProfileDefaults{TempoBPM: 120, TimeSignature: "4 4", SampleRateHz: 48000, BitDepth: 24,
			Source: workspace.HomeProfileSourceOwner, ConfirmedAt: &at},
		Requests: []workspace.HomeProfileRequest{{ID: "secret-request", Action: "fields", Revision: 2, RecordedAt: at}},
	}
}

func TestHomeProfileReadTool_OnlyTheBoundManagerReadsTheSameFactsAsItsContext(t *testing.T) {
	file, home := profileToolHome(t, profileToolFixture())
	available := true
	provider := NewWorkspaceToolProvider(nil, file, home.ID)
	provider.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return available })
	provider.SetExecutingAgent("Manager")
	if findLibraryTool(provider.Tools(), "home_profile_read") != nil {
		t.Fatal("a name without a proven instance got the profile tool")
	}
	provider.SetExecutingInstanceID("manager-instance")
	tool := findLibraryTool(provider.Tools(), "home_profile_read")
	if tool == nil {
		t.Fatal("the bound Manager has no home_profile_read tool")
	}
	for _, name := range []string{"home_profile_write", "home_profile_detect", "home_profile_set"} {
		if findLibraryTool(provider.Tools(), name) != nil {
			t.Fatalf("an agent write tool %q exists", name)
		}
	}

	output, err := tool.Call(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var facts struct {
		Available bool `json:"available"`
		workspace.HomeProfileFacts
	}
	if err := json.Unmarshal([]byte(output), &facts); err != nil {
		t.Fatalf("decode %s: %v", output, err)
	}
	if !facts.Available || facts.Title != "Your studio" || facts.MainApp == nil || facts.MainApp.Name != "REAPER" ||
		facts.MainApp.Version != "7.28" || facts.MainApp.Source != "confirmed by the owner" {
		t.Fatalf("main app facts = %s", output)
	}
	if len(facts.OtherApps) != 1 || facts.OtherApps[0].Name != "Logic Pro" || facts.OtherApps[0].Source != "detected, not confirmed" {
		t.Fatalf("other apps = %s", output)
	}
	if facts.Templates == nil || facts.Templates.App != "REAPER" || facts.Templates.ReadOn != "2026-10-07" ||
		len(facts.Templates.Project) != 1 || facts.Templates.Project[0] != "Band Session" || len(facts.Templates.Track) != 1 {
		t.Fatalf("templates = %s", output)
	}
	if facts.Defaults == nil || facts.Defaults.TempoBPM != 120 || facts.Defaults.TimeSignature != "4/4" || facts.Defaults.Source != "set by the owner" {
		t.Fatalf("defaults = %s", output)
	}
	// Nothing beyond the context block: no hidden app, file name or receipt.
	for _, leak := range []string{"Ableton", "secret-request", ".RPP", ".RTrackTemplate", "revision", "request"} {
		if strings.Contains(output, leak) {
			t.Fatalf("the tool leaked %q: %s", leak, output)
		}
	}

	// It takes no arguments.
	if _, err := tool.Call(context.Background(), `{"home_id":"other"}`); err == nil {
		t.Fatal("an argument was accepted")
	}
	// The gate is checked on every call, not only when the tool was listed.
	available = false
	if _, err := tool.Call(context.Background(), `{}`); err == nil {
		t.Fatal("the tool answered after the Home's provider became unavailable")
	}
	available = true

	// The Home's other agent never gets it.
	sample := NewWorkspaceToolProvider(nil, file, home.ID)
	sample.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	sample.SetExecutingAgent("Sample")
	sample.SetExecutingInstanceID("sample-instance")
	if findLibraryTool(sample.Tools(), "home_profile_read") != nil {
		t.Fatal("a non-primary Home agent got the profile tool")
	}
}

func TestHomeProfileReadTool_SaysWhenTheHomeHasNoProfile(t *testing.T) {
	file, home := profileToolHome(t, nil)
	provider := NewWorkspaceToolProvider(nil, file, home.ID)
	provider.SetProjectLibraryEvidence(func(projectlibrary.Scope, *workspace.Workspace) bool { return true })
	provider.SetExecutingAgent("Manager")
	provider.SetExecutingInstanceID("manager-instance")
	tool := findLibraryTool(provider.Tools(), "home_profile_read")
	if tool == nil {
		t.Fatal("no tool")
	}
	output, err := tool.Call(context.Background(), `{}`)
	if err != nil || !strings.Contains(output, `"available":false`) {
		t.Fatalf("output = %s err = %v", output, err)
	}
}
