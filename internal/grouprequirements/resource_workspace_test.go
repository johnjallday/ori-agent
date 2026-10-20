package grouprequirements

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type resourceCreateFixture struct {
	store    *workspace.FileStore
	service  *Service
	template projecttemplates.Template
	home     *workspace.Workspace
	homeDecl *workspace.AssistantProgramDeclaration
	// projectGeneration is the installed REAPER plugin's generation the
	// resolver reports; changing it stands for a plugin update after review.
	projectGeneration *uint64
}

func newResourceCreateFixture(t *testing.T) *resourceCreateFixture {
	return newResourceCreateFixtureWithHome(t, true)
}

func newResourceCreateFixtureWithHome(t *testing.T, withHome bool) *resourceCreateFixture {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	homeOwner := workspace.AssistantProgramHomeOwner{
		PluginID: "music", PluginVersion: "0.4.0", ProgramID: "music_home", HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: strings.Repeat("f", 64), PluginGeneration: 11, ComponentFingerprint: strings.Repeat("a", 64),
	}
	declaration := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "music_home", StationName: "Music Home",
		Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate projects."}},
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "music", ProgramID: "music_home"}
	var home *workspace.Workspace
	if withHome {
		if home, _, err = workspace.NewAssistantProgramStore(store).EnsureNamedIndependentStation(key, declaration, "Music Home", homeOwner); err != nil {
			t.Fatal(err)
		}
	}
	project := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 1, ID: "studio_team", WorkspaceKind: workspace.AssistantWorkspaceKindResource,
		Home:  projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "music", ProgramID: "music_home", HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "studio_assistant", Label: "Studio Assistant", Required: true, Primary: true, SystemPrompt: "Explain the catalog."}},
	}
	template := projecttemplates.Template{
		ID: "plugin:reaper:studio", Revision: strings.Repeat("c", 64), AssistantProject: project,
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper", PluginVersion: "0.11.0", BlueprintID: "studio", BlueprintVersion: 1},
		GroupRequirement: &projecttemplates.GroupRequirement{
			SchemaVersion: projecttemplates.SplitGroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRequired,
			AssistantProjectID: "studio_team", MissingHome: projecttemplates.MissingHomeExistingOnly,
		},
	}
	generation := new(uint64)
	*generation = 19
	service.SetIndependentHomeResolver(func(owner string, _ projecttemplates.Template, requireHome bool) (IndependentHomeResolution, error) {
		projectOwner := &workspace.AssistantProjectProviderOwner{
			PluginID: "reaper", PluginVersion: "0.11.0", BlueprintID: "studio", BlueprintVersion: 1,
			ProjectTeamID: "studio_team", ProjectTeamSchema: 1, ProjectTeamVersion: 1,
			ProjectTeamDigest: strings.Repeat("d", 64), PluginGeneration: *generation, ComponentFingerprint: strings.Repeat("e", 64),
			WorkspaceKind: workspace.AssistantWorkspaceKindResource,
		}
		resolution := IndependentHomeResolution{ProjectOwner: projectOwner}
		if requireHome {
			resolution.Key = workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: "music", ProgramID: "music_home"}
			resolution.Declaration, resolution.Owner = declaration, &homeOwner
		}
		return resolution, nil
	})
	return &resourceCreateFixture{store: store, service: service, template: template, home: home, homeDecl: declaration, projectGeneration: generation}
}

// finish does what the create handler does after a claim: saves the child
// with the claimed snapshot and links it to the Home.
func (f *resourceCreateFixture) finish(t *testing.T, claim Claim) *workspace.Workspace {
	t.Helper()
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Studio"})
	child.ID, child.OwnerUserID = claim.Operation.ChildWorkspaceID, "owner-1"
	child.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: f.template.ID, AssistantProgram: f.homeDecl, GroupRequirement: claim.Snapshot,
		AssistantProjectRoles: f.template.AssistantProject.ProgramRoles(),
	})
	if err := f.store.Save(child); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(f.store).EnsureProjectStation(child.ID); err != nil {
		t.Fatalf("link the Studio: %v", err)
	}
	return child
}

// PRD FR3 / AC1: one create/reuse path. The Studio is created once; another
// review answers "already exists" with its ID, a replay of the same create
// is still that create, and a second key that was reviewed earlier loses.
func TestResourceWorkspace_CreateConvergesOnOneStudio(t *testing.T) {
	ctx := context.Background()
	f := newResourceCreateFixture(t)
	input := testInput(t, f.template, CompositionGrouped, false)

	first, err := f.service.Review(ctx, input)
	if err != nil || first.State != StateReadyGrouped || first.HomeWorkspaceID != f.home.ID || first.Token == "" {
		t.Fatalf("first review = %+v, %v", first, err)
	}
	// A second tab reviews before the first one creates.
	second, err := f.service.Review(ctx, input)
	if err != nil || second.Token == "" {
		t.Fatalf("second review = %+v, %v", second, err)
	}
	claim, err := f.service.Claim(ctx, input, first.Token, "studio-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Snapshot == nil || claim.Snapshot.ProjectProvider == nil || !claim.Snapshot.ProjectProvider.IsResource() {
		t.Fatalf("snapshot = %+v", claim.Snapshot)
	}
	studio := f.finish(t, claim)

	again, err := f.service.Review(ctx, input)
	if err != nil || again.State != StateResourceWorkspaceExists || again.ExistingWorkspaceID != studio.ID || again.Token != "" ||
		len(again.Actions) != 1 || again.Actions[0] != ActionOpenExistingWorkspace {
		t.Fatalf("review after create = %+v, %v", again, err)
	}
	replay, err := f.service.Claim(ctx, input, first.Token, "studio-a")
	if err != nil || !replay.Replayed || replay.Operation.ChildWorkspaceID != studio.ID {
		t.Fatalf("replay of the same create = %+v, %v", replay, err)
	}
	_, err = f.service.Claim(ctx, input, second.Token, "studio-b")
	var exists *workspace.ResourceWorkspaceExistsError
	if !errors.As(err, &exists) || exists.WorkspaceID != studio.ID {
		t.Fatalf("second key err = %v", err)
	}
}

func TestResourceWorkspace_ReviewGoesStaleWhenTheProviderChanges(t *testing.T) {
	ctx := context.Background()
	f := newResourceCreateFixture(t)
	input := testInput(t, f.template, CompositionGrouped, false)
	review, err := f.service.Review(ctx, input)
	if err != nil || review.Token == "" {
		t.Fatalf("review = %+v, %v", review, err)
	}
	*f.projectGeneration = 20 // the REAPER plugin was updated after the review
	if _, err := f.service.Claim(ctx, input, review.Token, "studio-a"); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("claim after a provider change err = %v, want ErrReviewStale", err)
	}
	if linked, err := workspace.NewAssistantProgramStore(f.store).LinkedProjects(f.home.ID); err != nil || len(linked) != 0 {
		t.Fatalf("a stale review left a child: %d, %v", len(linked), err)
	}
}

func TestResourceWorkspace_AnotherOwnerNeitherSeesNorReusesTheStudio(t *testing.T) {
	ctx := context.Background()
	f := newResourceCreateFixture(t)
	input := testInput(t, f.template, CompositionGrouped, false)
	review, err := f.service.Review(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.service.Claim(ctx, input, review.Token, "studio-a")
	if err != nil {
		t.Fatal(err)
	}
	f.finish(t, claim)

	other := testInput(t, f.template, CompositionGrouped, false)
	other.OwnerUserID = "owner-2"
	evaluation := f.service.Evaluate(other)
	if evaluation.State != StateHomeRequired || evaluation.ExistingWorkspaceID != "" || evaluation.HomeWorkspaceID != "" {
		t.Fatalf("another owner's evaluation = %+v", evaluation)
	}
}

func TestResourceWorkspace_NeedsAnExistingHome(t *testing.T) {
	f := newResourceCreateFixtureWithHome(t, false)
	evaluation := f.service.Evaluate(testInput(t, f.template, CompositionGrouped, false))
	if evaluation.State != StateHomeRequired || evaluation.HomeWillBeCreated {
		t.Fatalf("evaluation without a Home = %+v", evaluation)
	}
}
