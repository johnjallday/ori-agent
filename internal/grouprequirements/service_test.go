package grouprequirements

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func testProgramTemplate(policy projecttemplates.GroupPolicy, missing projecttemplates.MissingHomePolicy) projecttemplates.Template {
	requirement := &projecttemplates.GroupRequirement{
		SchemaVersion: projecttemplates.GroupRequirementSchemaVersion,
		Policy:        policy,
	}
	if policy != projecttemplates.GroupPolicyNone {
		requirement.AssistantProgramID = "neutral-program"
		requirement.MissingHome = missing
		if missing == projecttemplates.MissingHomeOfferCreate {
			requirement.DefaultHomeName = "Neutral Program Home"
		}
	}
	return projecttemplates.Template{
		ID: "plugin:neutral:project", Name: "Neutral Project", Revision: strings.Repeat("a", 64),
		PluginOwner: &workspace.PluginTemplateOwner{
			PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1,
		},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "neutral-program", StationName: "Neutral Program Home",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "guide", Label: "Guide", Scope: workspace.AssistantRoleScopeHome, SystemPrompt: "Guide projects."},
				{ID: "maker", Label: "Maker", Scope: workspace.AssistantRoleScopeProject, SystemPrompt: "Make the project."},
			},
		},
		ProjectEntry: &projecttemplates.ProjectEntry{RelativePath: "{{slug}}.demo"},
		ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{
			SchemaVersion:  projecttemplates.ProjectConnectionSchemaVersion,
			SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionNewProject},
		},
		GroupRequirement: requirement,
		StandaloneComposition: &projecttemplates.StandaloneComposition{
			SchemaVersion: projecttemplates.StandaloneCompositionSchemaVersion,
			ProjectRoles:  []projecttemplates.StandaloneRole{{RoleID: "maker", SystemPrompt: "Work only in this project."}},
		},
	}
}

func testInput(t *testing.T, template projecttemplates.Template, composition string, createHome bool) Input {
	t.Helper()
	digest, err := DigestInput(map[string]any{"name": "Project One", "composition": composition, "create_home": createHome})
	if err != nil {
		t.Fatal(err)
	}
	return Input{OwnerUserID: "owner-1", OperationKind: OperationCreateWorkspace, Template: template,
		Composition: composition, CreateHome: createHome, InputDigest: digest}
}

func testHomeInput(t *testing.T, template projecttemplates.Template) Input {
	t.Helper()
	digest, err := DigestInput(map[string]any{"template_id": template.ID})
	if err != nil {
		t.Fatal(err)
	}
	return Input{OwnerUserID: "owner-1", OperationKind: OperationPrepareHome, Template: template,
		Composition: CompositionGrouped, CreateHome: true, InputDigest: digest}
}

func TestRequiredProjectStaysBlockedUntilSeparateHomeClaim(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)

	needsReview, err := service.Review(context.Background(), testInput(t, template, CompositionGrouped, false))
	if err != nil {
		t.Fatal(err)
	}
	if needsReview.State != StateHomeCreationReviewRequired || needsReview.Token != "" {
		t.Fatalf("review = %#v", needsReview)
	}
	ids, _ := store.List()
	if len(ids) != 0 {
		t.Fatalf("inert review created workspaces: %v", ids)
	}

	// Even the old create_required_home intent cannot authorize a project
	// operation to create its own prerequisite.
	projectInput := testInput(t, template, CompositionGrouped, true)
	blocked, err := service.Review(context.Background(), projectInput)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.State != StateHomeCreationReviewRequired || blocked.Token != "" {
		t.Fatalf("project create-home review = %#v", blocked)
	}

	homeInput := testHomeInput(t, template)
	review, err := service.Review(context.Background(), homeInput)
	if err != nil {
		t.Fatal(err)
	}
	if review.State != StateReadyGrouped || review.Token == "" || !review.HomeWillBeCreated {
		t.Fatalf("Home-only review = %#v", review)
	}
	claim, err := service.Claim(context.Background(), homeInput, review.Token, "create-home-one")
	if err != nil {
		t.Fatal(err)
	}
	if claim.Operation.HomeWorkspaceID == "" || claim.Operation.ChildWorkspaceID != "" ||
		claim.Operation.ProjectLinkID != "" || claim.Snapshot != nil || !claim.HomeCreated {
		t.Fatalf("Home-only claim = %#v, snapshot = %#v", claim, claim.Snapshot)
	}
	home, err := store.Get(claim.Operation.HomeWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if home.Name != "Neutral Program Home" || home.Kind != "group" {
		t.Fatalf("home = %#v", home)
	}

	projectInput.CreateHome = false
	projectInput.InputDigest, err = DigestInput(map[string]any{"name": "Project One", "composition": CompositionGrouped})
	if err != nil {
		t.Fatal(err)
	}
	projectReview, err := service.Review(context.Background(), projectInput)
	if err != nil {
		t.Fatal(err)
	}
	if projectReview.State != StateReadyGrouped || projectReview.Token == "" || projectReview.HomeWillBeCreated {
		t.Fatalf("project review after Home = %#v", projectReview)
	}
	projectClaim, err := service.Claim(context.Background(), projectInput, projectReview.Token, "create-project-one")
	if err != nil {
		t.Fatal(err)
	}
	if projectClaim.Operation.HomeWorkspaceID != home.ID || projectClaim.Operation.ChildWorkspaceID == "" ||
		projectClaim.Operation.ProjectLinkID == "" || projectClaim.Snapshot == nil || !projectClaim.Snapshot.StructurallyValid() {
		t.Fatalf("project claim after Home = %#v", projectClaim)
	}
}

func TestRecommendedStandaloneCreatesNoHomeAndDropsHomeBehavior(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRecommended, projecttemplates.MissingHomeOfferCreate)
	input := testInput(t, template, CompositionStandalone, false)

	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(context.Background(), input, review.Token, "standalone-one")
	if err != nil {
		t.Fatal(err)
	}
	if review.State != StateReadyStandalone || claim.EffectiveTemplate.AssistantProgram != nil || claim.EffectiveTemplate.SetupQuestID != "" {
		t.Fatalf("standalone result = %#v / %#v", review, claim.EffectiveTemplate)
	}
	if !claim.Snapshot.StructurallyValid() || claim.Snapshot.ProgramKey != nil || claim.Operation.HomeWorkspaceID != "" {
		t.Fatalf("standalone snapshot = %#v", claim.Snapshot)
	}
	ids, _ := store.List()
	if len(ids) != 0 {
		t.Fatalf("standalone claim created Home state: %v", ids)
	}
}

func TestSplitProvidersAreBoundIntoReviewAndStaleEitherProvider(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	project := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 3, ID: "reaper_team",
		Home:  projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "music", ProgramID: "music_home", HomeSchemaVersion: 1, MinHomeVersion: 2, MaxHomeVersion: 2},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "engineer", Label: "Engineer", Required: true, Primary: true, SystemPrompt: "Work in this project."}},
	}
	template := projecttemplates.Template{
		ID: "plugin:reaper:song", Revision: strings.Repeat("c", 64), AssistantProject: project,
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "song", BlueprintVersion: 7},
		GroupRequirement: &projecttemplates.GroupRequirement{
			SchemaVersion: projecttemplates.GroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRequired,
			MissingHome: projecttemplates.MissingHomeOfferCreate, DefaultHomeName: "Music Home",
		},
	}
	homeGeneration, projectGeneration := uint64(11), uint64(19)
	service.SetIndependentHomeResolver(func(owner string, got projecttemplates.Template, requireHome bool) (IndependentHomeResolution, error) {
		projectOwner := &workspace.AssistantProjectProviderOwner{
			PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "song", BlueprintVersion: 7,
			ProjectTeamID: "reaper_team", ProjectTeamSchema: 1, ProjectTeamVersion: 3,
			ProjectTeamDigest: strings.Repeat("d", 64), PluginGeneration: projectGeneration, ComponentFingerprint: strings.Repeat("e", 64),
		}
		if !requireHome {
			return IndependentHomeResolution{ProjectOwner: projectOwner}, nil
		}
		homeOwner := &workspace.AssistantProgramHomeOwner{
			PluginID: "music", PluginVersion: "2.0.0", ProgramID: "music_home", HomeSchemaVersion: 1, HomeVersion: 2,
			DeclarationDigest: strings.Repeat("f", 64), PluginGeneration: homeGeneration, ComponentFingerprint: strings.Repeat("a", 64),
		}
		declaration := &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "music_home", StationName: "Music Home",
			Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate projects."}},
		}
		return IndependentHomeResolution{
			Key:         workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: "music", ProgramID: "music_home"},
			Declaration: declaration, Owner: homeOwner, ProjectOwner: projectOwner,
		}, nil
	})

	missingHome := service.Evaluate(testInput(t, template, CompositionGrouped, false))
	if missingHome.State != StateHomeCreationReviewRequired || missingHome.ProgramKey == nil || missingHome.HomeOwner == nil ||
		missingHome.ProjectOwner == nil || missingHome.EffectiveTemplate.ResolvedAssistantHome == nil ||
		len(missingHome.EffectiveTemplate.ResolvedAssistantHome.Roles) != 1 || missingHome.EffectiveTemplate.ResolvedAssistantHome.Roles[0].ID != "producer" {
		t.Fatalf("missing split Home disclosure = %#v", missingHome)
	}

	homeInput := testHomeInput(t, template)
	homeKey := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "music", ProgramID: "music_home"}
	homeInput.GroupTemplateID = projecttemplates.GroupTemplateIDForKey(homeKey)
	homeInput.GroupTemplateRevision = strings.Repeat("9", 64)
	homeReview, err := service.Review(context.Background(), homeInput)
	if err != nil || homeReview.State != StateReadyGrouped {
		t.Fatalf("Home review = %#v, %v", homeReview, err)
	}
	homeClaim, err := service.Claim(context.Background(), homeInput, homeReview.Token, "split-home")
	if err != nil || !homeClaim.HomeCreated {
		t.Fatalf("Home claim = %#v, %v", homeClaim, err)
	}
	createdHome, err := store.Get(homeClaim.Operation.HomeWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	createdState := createdHome.GetAssistantProgramState()
	if createdState == nil || createdState.GroupTemplate == nil || createdState.GroupTemplate.ProgramHomeOwner == nil || createdState.GroupTemplate.HomeDigest == "" {
		t.Fatalf("split Group Template provenance = %#v", createdState)
	}

	projectInput := testInput(t, template, CompositionGrouped, false)
	_, committedHomeID, committedSnapshot, err := service.CommitReviewed(
		projectInput, "reviewed-child", strings.Repeat("7", 64), strings.Repeat("8", 64),
	)
	if err != nil || committedHomeID != createdHome.ID || committedSnapshot == nil ||
		committedSnapshot.HomeProvider == nil || committedSnapshot.ProjectProvider == nil {
		t.Fatalf("reviewed split commit = home %q snapshot %#v err %v", committedHomeID, committedSnapshot, err)
	}
	projectReview, err := service.Review(context.Background(), projectInput)
	if err != nil || projectReview.Token == "" {
		t.Fatalf("project review = %#v, %v", projectReview, err)
	}
	homeGeneration++
	if got := service.Evaluate(projectInput); got.State != StateTargetAmbiguous {
		t.Fatalf("changed Home provider existing-Home state = %q, want %q", got.State, StateTargetAmbiguous)
	}
	if _, err := service.Claim(context.Background(), projectInput, projectReview.Token, "split-home-provider-stale"); err != ErrReviewStale {
		t.Fatalf("Home provider generation change error = %v", err)
	}
	homeGeneration--
	projectReview, err = service.Review(context.Background(), projectInput)
	if err != nil {
		t.Fatal(err)
	}
	projectGeneration++
	if _, err := service.Claim(context.Background(), projectInput, projectReview.Token, "split-project-stale"); err != ErrReviewStale {
		t.Fatalf("project provider generation change error = %v", err)
	}
	projectGeneration--
	projectReview, err = service.Review(context.Background(), projectInput)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(context.Background(), projectInput, projectReview.Token, "split-project")
	if err != nil {
		t.Fatal(err)
	}
	if claim.Snapshot.HomeProvider == nil || claim.Snapshot.ProjectProvider == nil ||
		claim.Snapshot.HomeProvider.PluginGeneration != homeGeneration || claim.Snapshot.ProjectProvider.PluginGeneration != projectGeneration {
		t.Fatalf("split snapshot = %#v", claim.Snapshot)
	}

	standaloneTemplate := template
	standaloneTemplate.GroupRequirement = &projecttemplates.GroupRequirement{
		SchemaVersion: projecttemplates.SplitGroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRecommended,
		AssistantProjectID: project.ID, MissingHome: projecttemplates.MissingHomeOfferCreate, DefaultHomeName: "Music Home",
	}
	standaloneTemplate.StandaloneComposition = &projecttemplates.StandaloneComposition{
		SchemaVersion: projecttemplates.SplitStandaloneCompositionSchemaVersion,
		ProjectRoles:  []projecttemplates.StandaloneRole{{RoleID: "engineer", SystemPrompt: "Work only in this standalone project."}},
	}
	standaloneInput := testInput(t, standaloneTemplate, CompositionStandalone, false)
	standaloneReview, err := service.Review(context.Background(), standaloneInput)
	if err != nil || standaloneReview.Token == "" || standaloneReview.HomeOwner != nil || standaloneReview.ProjectOwner == nil {
		t.Fatalf("standalone split review = %#v, %v", standaloneReview, err)
	}
	if standaloneReview.EffectiveTemplate.AssistantProject != nil || standaloneReview.EffectiveTemplate.GroupRequirement != nil ||
		len(standaloneReview.EffectiveTemplate.Agents) != 1 || standaloneReview.EffectiveTemplate.Agents[0].Name != "Engineer" {
		t.Fatalf("standalone split transformation retained grouped authority: %#v", standaloneReview.EffectiveTemplate)
	}
	projectGeneration++
	if _, err := service.Claim(context.Background(), standaloneInput, standaloneReview.Token, "standalone-project-stale"); err != ErrReviewStale {
		t.Fatalf("standalone provider generation change error = %v", err)
	}
	projectGeneration--
}

func TestExactIdentityIgnoresSameNamedOrdinaryAndRejectsWrongParent(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ordinary := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Neutral Program Home"})
	ordinary.Kind = "group"
	ordinary.OwnerUserID = "owner-1"
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeExistingOnly)
	input := testInput(t, template, CompositionGrouped, false)
	if got := service.Evaluate(input); got.State != StateHomeRequired {
		t.Fatalf("same-name ordinary state = %q, want %q", got.State, StateHomeRequired)
	}

	input.RequestedParentID = ordinary.ID
	if got := service.Evaluate(input); got.State != StateContractInvalid {
		t.Fatalf("forged parent state = %q, want %q", got.State, StateContractInvalid)
	}
}

func TestCanonicalHomeResolutionIsOwnerScoped(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	foreignKey, err := programKey("owner-2", template)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := workspace.NewAssistantProgramStore(store).EnsureNamedStation(foreignKey, template.AssistantProgram, "Neutral Program Home")
	if err != nil {
		t.Fatal(err)
	}
	result := NewService(store, NewMemoryStore()).Evaluate(testInput(t, template, CompositionGrouped, false))
	if result.State != StateHomeCreationReviewRequired || result.HomeWorkspaceID != "" || foreign.OwnerUserID != "owner-2" {
		t.Fatalf("foreign Home crossed owner boundary: evaluation=%#v foreign=%#v", result, foreign)
	}
}

func TestClaimRejectsTemplateChangeAfterReview(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := testHomeInput(t, template)
	review, err := service.Review(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Template.Revision = strings.Repeat("b", 64)
	if _, err := service.Claim(context.Background(), input, review.Token, "stale-one"); err != ErrReviewStale {
		t.Fatalf("Claim error = %v, want %v", err, ErrReviewStale)
	}
	ids, _ := store.List()
	if len(ids) != 0 {
		t.Fatalf("stale review created workspaces: %v", ids)
	}
}

func TestConcurrentHomePreparationClaimsConvergeWithoutProjectConsequences(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	inputs := []Input{testHomeInput(t, template), testHomeInput(t, template)}
	reviews := make([]Review, len(inputs))
	for index := range inputs {
		reviews[index], err = service.Review(context.Background(), inputs[index])
		if err != nil {
			t.Fatal(err)
		}
	}

	claims := make([]Claim, len(inputs))
	errs := make([]error, len(inputs))
	var wg sync.WaitGroup
	for index := range inputs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			keys := []string{"project-a", "project-b"}
			claims[index], errs[index] = service.Claim(context.Background(), inputs[index], reviews[index].Token, keys[index])
		}(index)
	}
	wg.Wait()
	for index, claimErr := range errs {
		if claimErr != nil {
			t.Fatalf("claim %d: %v", index, claimErr)
		}
	}
	if claims[0].Operation.HomeWorkspaceID == "" || claims[0].Operation.HomeWorkspaceID != claims[1].Operation.HomeWorkspaceID {
		t.Fatalf("claims selected different Homes: %#v / %#v", claims[0].Operation, claims[1].Operation)
	}
	if claims[0].Operation.ChildWorkspaceID != "" || claims[1].Operation.ChildWorkspaceID != "" ||
		claims[0].Operation.ProjectLinkID != "" || claims[1].Operation.ProjectLinkID != "" {
		t.Fatalf("Home preparation created project identity: %#v / %#v", claims[0].Operation, claims[1].Operation)
	}
	if claims[0].HomeCreated == claims[1].HomeCreated {
		t.Fatalf("exactly one concurrent claim must create the Home: %#v / %#v", claims[0], claims[1])
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != claims[0].Operation.HomeWorkspaceID {
		t.Fatalf("concurrent claims created duplicate Homes: %v", ids)
	}
}

func TestRequiredDeletionReviewIsExactAndConsumedOnce(t *testing.T) {
	ctx := context.Background()
	store := workspace.NewInMemoryStore()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Detached Required"})
	project.ID = "detached-required"
	project.OwnerUserID = "owner-1"
	key := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	digest := strings.Repeat("a", 64)
	now := time.Date(2026, time.September, 19, 9, 0, 0, 0, time.UTC)
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:neutral:project",
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required",
			SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID:          "plugin:neutral:project", TemplateRevision: digest, DefinitionDigest: digest,
			ProgramKey: &key, HomeWorkspaceID: "removed-home",
			ProjectLinkID: workspace.AssistantProjectLinkID("removed-home", project.ID),
			ReviewDigest:  digest, OperationDigest: digest, AppliedAt: now,
		},
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, NewMemoryStore())
	service.now = func() time.Time { return now }
	input := DeletionInput{
		OwnerUserID: "owner-1", WorkspaceID: project.ID, DeleteSessions: true,
	}
	review, err := service.ReviewDeletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if review.Token == "" || review.WorkspaceID != project.ID || review.Trashes || len(review.Impact) != 4 {
		t.Fatalf("review = %#v", review)
	}
	if _, err := service.ClaimDeletion(ctx, DeletionInput{
		OwnerUserID: "owner-1", WorkspaceID: project.ID, TrashWorkspace: true,
	}, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("changed deletion mode claim = %v, want stale", err)
	}
	claim, err := service.ClaimDeletion(ctx, input, review.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claim.WorkspaceID != project.ID || claim.ContractOperationDigest != digest || !claim.DeleteSessions || claim.TrashWorkspace {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := service.ClaimDeletion(ctx, input, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("reused deletion review = %v, want stale", err)
	}
	unchanged, err := store.Get(project.ID)
	if err != nil || unchanged.GetTemplateProvenance().GroupRequirement.OperationDigest != digest {
		t.Fatalf("review mutated project: %#v, %v", unchanged, err)
	}

	changedReview, err := service.ReviewDeletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	changedProvenance := unchanged.GetTemplateProvenance()
	changedProvenance.GroupRequirement.HomeWorkspaceID = "different-removed-home"
	changedProvenance.GroupRequirement.ProjectLinkID = workspace.AssistantProjectLinkID("different-removed-home", project.ID)
	unchanged.SetTemplateProvenance(changedProvenance)
	if err := store.Save(unchanged); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimDeletion(ctx, input, changedReview.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("changed contract claim = %v, want stale", err)
	}

	expiringReview, err := service.ReviewDeletion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(ReviewTTL)
	if _, err := service.ClaimDeletion(ctx, input, expiringReview.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("expired deletion review = %v, want stale", err)
	}
}

func TestSQLiteReceiptReplaySurvivesServiceRestartAndConverges(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("Close database: %v", closeErr)
		}
	})
	receipts, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	template := testProgramTemplate(projecttemplates.GroupPolicyRequired, projecttemplates.MissingHomeOfferCreate)
	input := testHomeInput(t, template)
	firstService := NewService(workspaces, receipts)
	review, err := firstService.Review(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstService.Claim(ctx, input, review.Token, "durable-one")
	if err != nil {
		t.Fatal(err)
	}

	secondService := NewService(workspaces, receipts)
	second, err := secondService.Claim(ctx, input, review.Token, "durable-one")
	if err != nil {
		t.Fatal(err)
	}
	if second.Operation.ChildWorkspaceID != "" || first.Operation.ChildWorkspaceID != "" ||
		second.Operation.ProjectLinkID != "" || first.Operation.ProjectLinkID != "" ||
		second.Operation.HomeWorkspaceID != first.Operation.HomeWorkspaceID ||
		second.Operation.OperationDigest != first.Operation.OperationDigest || !second.Replayed {
		t.Fatalf("Home-only replay diverged or gained project identity: first=%#v second=%#v", first, second)
	}
	ids, _ := workspaces.List()
	if len(ids) != 1 || ids[0] != first.Operation.HomeWorkspaceID {
		t.Fatalf("replay created duplicate Homes: %v", ids)
	}
}
