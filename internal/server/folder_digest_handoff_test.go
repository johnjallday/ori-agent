package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func songOffer(name, shape, marker string) personalassistant.FolderOffer {
	return personalassistant.FolderOffer{ID: "offer-" + name, Verdict: string(folderdigest.KindProject),
		Subject: personalassistant.FolderCandidateRecord{Name: name, Kind: personalassistant.FolderChoiceProject, Shape: shape, MarkerName: marker, IsRoot: true}}
}

// FR1: only a recognized song whose Home lists its projects is handed to the
// Home's Portfolio Manager; every other single project keeps its own plan.
func TestFolderHandoff_OnlyASongWhoseHomeListsProjectsIsHandedOver(t *testing.T) {
	host := &folderSetupHost{builder: &ServerBuilder{}}
	collection := songOffer("Albums", string(folderdigest.ShapeAudio), "*.rpp")
	collection.Portfolio = &personalassistant.FolderPortfolioEvidence{Shape: string(folderdigest.ShapeAudio), Projects: 6, ProviderKey: "music_project_management"}
	dump := songOffer("Downloads", string(folderdigest.ShapeAudio), "*.rpp")
	dump.Verdict, dump.Subject.Kind = string(folderdigest.KindDump), personalassistant.FolderChoiceTidy
	cases := []struct {
		name  string
		offer personalassistant.FolderOffer
		want  bool
	}{
		{"a REAPER song", songOffer("Album-5", string(folderdigest.ShapeAudio), "*.rpp"), true},
		{"a collection keeps its own card", collection, false},
		{"a loose-file dump", dump, false},
		{"an Ableton set", songOffer("Set", string(folderdigest.ShapeAudio), "*.als"), false},
		{"a manuscript", songOffer("Book", string(folderdigest.ShapeManuscript), "main.tex"), false},
		{"a code project", songOffer("Repo", string(folderdigest.ShapeCode), "go.mod"), false},
		{"a Blank folder", songOffer("Stuff", "", ""), false},
	}
	for _, tc := range cases {
		target, err := host.target(tc.offer)
		if got := err == nil && handoffOffer(tc.offer, target); got != tc.want {
			t.Errorf("%s: handoff = %v (target err %v), want %v", tc.name, got, err, tc.want)
		}
	}
}

// FR2: the run reads the plan's decision. A song planned the old way is a
// changed plan; a handoff plan reaches the song's own run.
func TestFolderHandoff_TheRunIsHeldToThePlansDecision(t *testing.T) {
	f := newDraftServerFixture(t)
	host := &folderSetupHost{builder: f.builder}
	song := songOffer("Album-5", string(folderdigest.ShapeAudio), "*.rpp")
	old := personalassistant.FolderSetupRequest{UserID: "local", Offer: song, Plan: personalassistant.FolderSetupPlan{
		Intent: personalassistant.FolderSetupIntent{Placement: "grouped"}}}
	if err := host.Run(t.Context(), old); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("a song planned the old way ran: %v", err)
	}
	handoff := old
	handoff.Plan.Intent = personalassistant.FolderSetupIntent{Portfolio: true, Placement: "grouped", OpensProjects: []string{"Album-5"}}
	handoff.Update = func(context.Context, personalassistant.FolderSetupUpdate) error { return nil }
	if err := host.Run(t.Context(), handoff); errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("a handoff plan was refused as changed: %v", err)
	}
	// A collection-shaped plan for a song that is not one is a changed plan.
	manuscript := personalassistant.FolderSetupRequest{UserID: "local", Offer: songOffer("Book", string(folderdigest.ShapeManuscript), "main.tex"),
		Plan: handoff.Plan, Update: handoff.Update}
	if err := host.Run(t.Context(), manuscript); err == nil {
		t.Fatal("a handoff intent ran for an offer that is no song")
	}
	if ids, err := f.builder.workspaceFileStore.List(); err != nil {
		t.Fatal(err)
	} else {
		for _, id := range ids {
			if ws, _ := f.builder.workspaceFileStore.Get(id); ws != nil && ws.Name == "Album-5" {
				t.Fatalf("a refused run made the song: %+v", ws)
			}
		}
	}
}

// FR3–FR7 on the real host: the plan is the Home's lines for a portfolio of
// one, then the manager's open, with the hire and its brief; with the Home
// there, it adds the song to it.
func TestFolderHandoff_PlanOnTheRealHost(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := reviewedintegration.HomeProviders()[0]
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.4.0", ProgramID: provider.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID,
		StationName: "Music Production Home", Roles: []workspace.AssistantProgramRoleSpec{{ID: "portfolio_manager", Label: "Portfolio Manager",
			Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Keep the catalog."}}}
	template := projecttemplates.Template{ID: "plugin-home:" + provider.PluginID + ":" + provider.ProgramID, Name: "Music Production Home",
		AssistantProgram: declaration, ProgramHomeOwner: &owner, GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1,
			Policy: projecttemplates.GroupPolicyRequired, AssistantProgramID: provider.ProgramID, DefaultHomeName: declaration.StationName}}
	f.builder.sessionHandler.SetGroupTemplateCatalog(func(string) ([]sessionhttp.GroupTemplateCatalogEntry, bool, error) {
		return []sessionhttp.GroupTemplateCatalogEntry{{Template: template, Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin), Active: true}}, false, nil
	})
	if err := f.builder.configManager.SetSystemModel("openai", "gpt-5-mini"); err != nil {
		t.Fatal(err)
	}
	host := &folderSetupHost{builder: f.builder}
	ready := func(name, id string) foldersetup.Plugin {
		return foldersetup.Plugin{Name: name, PluginID: id, State: personalassistant.FolderInstallReady, Version: "1.0.0", InstalledVersion: "1.0.0", Source: "johnjallday/" + id}
	}
	provided := ready(provider.DisplayName, provider.PluginID)
	// The release facts are read once and cached; seed them so the test reads
	// no release.
	host.cache = map[string]cachedPlanFacts{"local|ori_reaper": {at: time.Now(), facts: foldersetup.PlanFacts{
		AppName: "REAPER", BlueprintLabel: "REAPER song", Integration: ready("REAPER", "reaper-plugin"), Provider: &provided,
	}}}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: songOffer("Album-5", string(folderdigest.ShapeAudio), "*.rpp")}
	plan, err := host.Plan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(plan.Lines))
	for _, line := range plan.Lines {
		kinds = append(kinds, line.Kind)
	}
	if got := strings.Join(kinds, ","); got != "integration,provider,home,agents,library,handoff,workspace,folder,mode,agents,task" {
		t.Fatalf("new Home plan = %s", got)
	}
	if plan.Lines[5].Name != "Your Portfolio Manager then suggests opening Album-5" ||
		plan.Lines[6].Name != "Creates a REAPER song workspace named Album-5 as a separate project in new Home “Music Production Home”" {
		t.Fatalf("lines = %+v", plan.Lines)
	}
	// The Default-model sentence is on the Home's agents line only.
	if !strings.Contains(plan.Lines[3].Detail, "openai · gpt-5-mini") || strings.Contains(plan.Lines[9].Detail, "Default model") {
		t.Fatalf("model sentence = %q / %q", plan.Lines[3].Detail, plan.Lines[9].Detail)
	}
	intent := plan.Intent
	if !intent.Portfolio || strings.Join(intent.OpensProjects, ",") != "Album-5" || !intent.CreatesHome || !intent.StaffsHome ||
		intent.Model == nil || intent.Model.Source != "system" || intent.Model.Model != "gpt-5-mini" ||
		plan.Destination == nil || plan.Destination.Status != "new" || plan.Digest == "" {
		t.Fatalf("intent = %+v destination = %+v", intent, plan.Destination)
	}
	if plan.Hire == nil || plan.BriefDefault == "" {
		t.Fatalf("the hire card lost its hire or brief: %+v %q", plan.Hire, plan.BriefDefault)
	}
	if err := host.validateSetupModel(personalassistant.FolderSetupRequest{Plan: plan}); err != nil {
		t.Fatalf("the bound model is refused: %v", err)
	}
	// With the Home there (not yet staffed), nothing is created for it and the
	// song joins it.
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}
	if _, _, err := workspace.NewAssistantProgramStore(f.builder.workspaceStore).EnsureNamedIndependentStation(key, declaration, "Music Production Home", owner); err != nil {
		t.Fatal(err)
	}
	existing, err := host.Plan(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	kinds = kinds[:0]
	for _, line := range existing.Lines {
		kinds = append(kinds, line.Kind)
	}
	if got := strings.Join(kinds, ","); got != "integration,provider,agents,library,handoff,workspace,folder,mode,agents,task" {
		t.Fatalf("existing Home plan = %s", got)
	}
	if existing.Intent.CreatesHome || !existing.Intent.StaffsHome || existing.Destination == nil || existing.Destination.Status != "existing" ||
		existing.Lines[5].Name != "Creates a REAPER song workspace named Album-5 as a separate project in Home “Music Production Home”" ||
		existing.Digest == plan.Digest {
		t.Fatalf("existing Home = %+v %+v", existing.Intent, existing.Lines)
	}
	// A Home not yet staffed is set up from the assistant's card; only a ready
	// Home leads there instead.
	if plan.SuggestedIn != nil || existing.SuggestedIn != nil {
		t.Fatalf("a Home still to set up was offered as the place to open: %+v %+v", plan.SuggestedIn, existing.SuggestedIn)
	}
	if route, ok := homeSuggestionsRoute(&workspace.Workspace{FolderSlug: "music-production-home"}); !ok || route != "/workspaces/music-production-home?mode=map&panel=assistant" {
		t.Fatalf("Home route = %q %v", route, ok)
	}
	if _, ok := homeSuggestionsRoute(&workspace.Workspace{FolderSlug: "../x"}); ok {
		t.Fatal("a non-canonical slug made a route")
	}
}
