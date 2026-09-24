package setupjourney

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// splitHomeResolver stands in for the host's two-provider join. err selects
// the provider state it reports; nil resolves the Home.
type splitHomeResolver struct{ err error }

func (r *splitHomeResolver) resolve(ownerUserID string, _ projecttemplates.Template, requireHome bool) (grouprequirements.IndependentHomeResolution, error) {
	project := &workspace.AssistantProjectProviderOwner{
		PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1,
		ProjectTeamID: "song-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1,
		ProjectTeamDigest: strings.Repeat("b", 64), PluginGeneration: 3, ComponentFingerprint: strings.Repeat("c", 64),
	}
	if !requireHome {
		return grouprequirements.IndependentHomeResolution{ProjectOwner: project}, nil
	}
	if r.err != nil {
		return grouprequirements.IndependentHomeResolution{}, r.err
	}
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "production", StationName: "Provider Station", DefaultPrimaryName: "Producer", HireTitle: "Hire producer",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "producer", Label: "Producer", Required: true, Primary: true, SystemPrompt: "Coordinate songs."}},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "initial", Label: "Initial"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 2, CadenceHours: 24, MaxProjects: 4, MaxEventsPerProject: 4, MaxCandidates: 2, MaxEvidence: 2, Rubric: "Review."},
	}
	return grouprequirements.IndependentHomeResolution{
		Key:         workspace.AssistantProgramKey{OwnerUserID: ownerUserID, PluginID: "home-provider", ProgramID: "production"}.Normalize(),
		Declaration: home.AssistantProgram(),
		Owner: &workspace.AssistantProgramHomeOwner{
			PluginID: "home-provider", PluginVersion: "0.1.0", ProgramID: "production", HomeSchemaVersion: 1, HomeVersion: 1,
			DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home), PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64),
		},
		ProjectOwner: project,
	}, nil
}

// stubHomeProviderSource returns one fixed derivation, the way the host's
// blueprint readiness would describe the provider.
type stubHomeProviderSource struct {
	state HomeProviderState
	err   error
	calls int
}

func (s *stubHomeProviderSource) HomeProviderState(context.Context, projecttemplates.Template) (HomeProviderState, error) {
	s.calls++
	return s.state, s.err
}

func missingProviderState() HomeProviderState {
	return HomeProviderState{
		Readiness: blueprintreadiness.Readiness{
			State: blueprintreadiness.StateActionRequired, Ownership: blueprintreadiness.OwnershipPlugin,
			Reason:     blueprintreadiness.ReasonPluginInstallRequired,
			Summary:    "Song needs Production Home, which comes from a separate plugin.",
			Detail:     "Install Home Provider to add it. Installing this blueprint's plugin did not add it.",
			Dependency: &blueprintreadiness.Dependency{PluginName: "home-provider", DisplayName: "Home Provider"},
			Actions: []blueprintreadiness.Action{
				blueprintreadiness.ActionInstallPlugin, blueprintreadiness.ActionManagePlugins, blueprintreadiness.ActionChangeBlueprint,
			},
		},
		DisplayName: "Home Provider", MinimumVersion: "0.1.0", Reviewed: true,
	}
}

func splitProjectAdapter(t *testing.T) (*ProjectConnectionAdapter, *splitHomeResolver, *stubHomeProviderSource) {
	t.Helper()
	adapter, _ := setupJourneyProjectAdapter(t)
	template := adapter.templates.(staticProjectTemplateResolver).template
	template.AssistantProgram = nil
	template.Revision = strings.Repeat("a", 64)
	template.AssistantProject = &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: projecttemplates.AssistantProjectSchemaVersion, Version: 1, ID: "song-team",
		Home: projecttemplates.AssistantProjectHomeReference{
			ProviderPluginID: "home-provider", ProgramID: "production", HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1,
		},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "engineer", Label: "Engineer", Required: true, Primary: true, SystemPrompt: "Engineer this song."}},
	}
	template.GroupRequirement = &projecttemplates.GroupRequirement{
		SchemaVersion: projecttemplates.SplitGroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRequired,
		AssistantProjectID: "song-team", MissingHome: projecttemplates.MissingHomeOfferCreate, DefaultHomeName: "Production Home",
	}
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	grouping := grouprequirements.NewService(store, grouprequirements.NewMemoryStore())
	resolver := &splitHomeResolver{}
	grouping.SetIndependentHomeResolver(resolver.resolve)
	owner := projectconnection.NewService(store, pathselection.NewStore())
	owner.SetGroupRequirementService(grouping)
	source := &stubHomeProviderSource{state: missingProviderState()}
	split := NewProjectConnectionAdapter(owner, staticProjectTemplateResolver{template})
	split.SetHomeProviderSource(source)
	return split, resolver, source
}

func TestSplitGroupReadExplainsTheProviderThenBuildsAndReusesTheHome(t *testing.T) {
	adapter, resolver, source := splitProjectAdapter(t)
	scope := ReadScope{OwnerUserID: "owner-1", RunKind: RunKindRoot, RunID: "split-journey", WorkspaceLaunch: true}

	// 1. The Home provider is not installed: the read names it.
	resolver.err = grouprequirements.ErrHomeProviderNotInstalled
	missing, err := adapter.Read(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	want := &HomeProviderProjection{
		PluginID: "home-provider", DisplayName: "Home Provider", Reviewed: true, MinimumVersion: "0.1.0", TemplateID: "plugin:neutral:project",
		Reason:  blueprintreadiness.ReasonPluginInstallRequired,
		Summary: "Song needs Production Home, which comes from a separate plugin.",
		Detail:  "Install Home Provider to add it. Installing this blueprint's plugin did not add it.",
		Actions: []blueprintreadiness.Action{blueprintreadiness.ActionInstallPlugin, blueprintreadiness.ActionManagePlugins, blueprintreadiness.ActionChangeBlueprint},
	}
	if missing.BlockedReason != ReasonHomeProviderMissing || !reflect.DeepEqual(missing.HomeProvider, want) || len(missing.AvailableActions) != 0 {
		t.Fatalf("missing provider read = %+v provider=%+v", missing, missing.HomeProvider)
	}
	if missing.Preparation == nil || missing.Preparation.Name != "Production Home" || missing.Preparation.Exists {
		t.Fatalf("partial preparation = %+v", missing.Preparation)
	}
	if !validCanonicalRead(specialist.SetupStepProjectConnect, missing) {
		t.Fatal("the registry would refuse the missing-provider read")
	}
	if safeGuidance[ReasonHomeProviderMissing] != "Install the plugin that provides this Home, then check again." {
		t.Fatalf("guidance = %q", safeGuidance[ReasonHomeProviderMissing])
	}
	encoded, _ := json.Marshal(missing.HomeProvider)
	if !strings.Contains(string(encoded), `"plugin_id":"home-provider"`) || strings.Contains(string(encoded), "source") {
		t.Fatalf("projection JSON = %s", encoded)
	}

	// 2. The provider is installed and enabled, with no Home yet.
	resolver.err = nil
	ready, err := adapter.Read(context.Background(), scope)
	if err != nil || ready.BlockedReason != "" || ready.HomeProvider != nil || ready.Preparation == nil || ready.Preparation.Exists ||
		!reflect.DeepEqual(ready.AvailableActions, []ActionID{ActionReviewCreateGroup}) {
		t.Fatalf("ready provider read = %+v err=%v", ready, err)
	}
	raw := json.RawMessage(`{"name":"My Production"}`)
	material, err := adapter.Review(context.Background(), scope, ActionReviewCreateGroup, raw)
	if err != nil || material.Group == nil || material.Group.Name != "My Production" {
		t.Fatalf("group review = %+v err=%v", material, err)
	}
	result, err := adapter.Commit(context.Background(), scope, ActionCreateGroup, raw, material)
	if err != nil || result.HomeWorkspaceID == "" {
		t.Fatalf("group commit = %+v err=%v", result, err)
	}

	// 3. The Home exists: the read reports and reuses it.
	scope.HomeWorkspaceID = result.HomeWorkspaceID
	existing, err := adapter.Read(context.Background(), scope)
	if err != nil || existing.Preparation == nil || !existing.Preparation.Exists || existing.Preparation.HomeID != result.HomeWorkspaceID ||
		!adapter.ConsequenceObserved(ActionCreateGroup, existing) {
		t.Fatalf("existing Home read = %+v err=%v", existing, err)
	}
	for _, action := range existing.AvailableActions {
		if action == ActionReviewCreateGroup {
			t.Fatal("an existing Home still offered Build Group")
		}
	}
	if source.calls != 1 {
		t.Fatalf("provider state was derived %d times; only the missing read needs it", source.calls)
	}
}

func TestSplitGroupReadStaysUnexplainedWithoutAProviderDerivation(t *testing.T) {
	scope := ReadScope{OwnerUserID: "owner-1", RunKind: RunKindRoot, RunID: "split-journey", WorkspaceLaunch: true}
	cases := map[string]func(*ProjectConnectionAdapter, *stubHomeProviderSource){
		"no source wired":   func(a *ProjectConnectionAdapter, _ *stubHomeProviderSource) { a.SetHomeProviderSource(nil) },
		"derivation failed": func(_ *ProjectConnectionAdapter, s *stubHomeProviderSource) { s.err = errors.New("unavailable") },
		"derivation says ready": func(_ *ProjectConnectionAdapter, s *stubHomeProviderSource) {
			s.state = HomeProviderState{Readiness: blueprintreadiness.Ready(blueprintreadiness.OwnershipPlugin)}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, resolver, source := splitProjectAdapter(t)
			resolver.err = grouprequirements.ErrHomeProviderDisabled
			mutate(adapter, source)
			read, err := adapter.Read(context.Background(), scope)
			if err != nil || read.BlockedReason != ReasonOwnerUnavailable || read.HomeProvider != nil {
				t.Fatalf("read = %+v err=%v", read, err)
			}
		})
	}
	adapter, resolver, _ := splitProjectAdapter(t)
	resolver.err = grouprequirements.ErrIndependentHomeInvalid
	if read, _ := adapter.Read(context.Background(), scope); read.BlockedReason != ReasonOwnerUnavailable || read.HomeProvider != nil {
		t.Fatalf("an ambiguous join was explained as a missing provider: %+v", read)
	}
}

func TestHomeProviderProjectionOnlyTravelsWithItsBlockedReason(t *testing.T) {
	projection := newHomeProviderProjection(projecttemplates.Template{
		ID: "plugin:neutral:project",
		AssistantProject: &projecttemplates.AssistantProjectDeclaration{
			Home: projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "home-provider"},
		},
	}, missingProviderState())
	if projection == nil {
		t.Fatal("projection was not built")
	}
	if validCanonicalRead(specialist.SetupStepProjectConnect, CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable, HomeProvider: projection}) {
		t.Fatal("a Home provider without its reason was accepted")
	}
	if validCanonicalRead(specialist.SetupStepWorkspaceSetup, CanonicalStepRead{BlockedReason: ReasonHomeProviderMissing, HomeProvider: projection}) {
		t.Fatal("a Home provider on another step kind was accepted")
	}
	unreviewed := missingProviderState()
	unreviewed.Reviewed, unreviewed.DisplayName, unreviewed.MinimumVersion = false, "", ""
	unreviewed.Readiness.Actions = []blueprintreadiness.Action{blueprintreadiness.ActionManagePlugins, blueprintreadiness.ActionChangeBlueprint}
	plain := newHomeProviderProjection(projecttemplates.Template{
		ID: "plugin:neutral:project",
		AssistantProject: &projecttemplates.AssistantProjectDeclaration{
			Home: projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "home-provider"},
		},
	}, unreviewed)
	if plain == nil || plain.Reviewed || plain.DisplayName != "" {
		t.Fatalf("unreviewed projection = %+v", plain)
	}
}
