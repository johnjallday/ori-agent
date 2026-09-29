package setupjourney

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const roleRepairFixtureRunID = "8d483520-870a-465a-8f7b-0df688fb042e"

func oldSplitChildFixture(t *testing.T) (*workspace.SyncStore, *workspace.InMemoryStore, *workspace.FileStore, []plugin.InstalledPlugin, string, string, string) {
	t.Helper()
	const owner = "local"
	homeDeclaration := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-home", StationName: "Music Home", DefaultPrimaryName: "Portfolio Manager",
		Roles:                     []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Primary: true, Required: true, SystemPrompt: "Home only."}},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-team", ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1}},
	}
	projectDeclaration := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 1, ID: "reaper-team",
		Home:  projecttemplates.AssistantProjectHomeReference{ProviderPluginID: "music-plugin", ProgramID: homeDeclaration.ID, HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "reaper-assistant", Label: "REAPER Assistant", Primary: true, SystemPrompt: "Project only.", Skills: []string{"reaper-skill"}}},
	}
	installed := []plugin.InstalledPlugin{
		{Name: "music-plugin", Version: "0.1.1", Enabled: true, ContentGeneration: 1, ComponentFingerprint: strings.Repeat("a", 64),
			WorkspaceSurfaces: &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1}, AssistantProgramHomes: []projecttemplates.AssistantProgramHome{homeDeclaration}}},
		{Name: "reaper-plugin", Version: "0.9.0", Enabled: true, ContentGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64), Skills: []string{"reaper-skill"},
			WorkspaceSurfaces:  &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1}},
			ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", Version: 10, Template: projecttemplates.Template{AssistantProject: projectDeclaration}}}},
	}
	homePin := &workspace.AssistantProgramHomeOwner{PluginID: installed[0].Name, PluginVersion: installed[0].Version, ProgramID: homeDeclaration.ID, HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(homeDeclaration), PluginGeneration: installed[0].EvidenceGeneration(), ComponentFingerprint: installed[0].ComponentFingerprint}
	projectPin := &workspace.AssistantProjectProviderOwner{PluginID: installed[1].Name, PluginVersion: installed[1].Version, BlueprintID: "reaper-song", BlueprintVersion: 10,
		ProjectTeamID: projectDeclaration.ID, ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: projecttemplates.AssistantProjectDigest(projectDeclaration),
		PluginGeneration: installed[1].EvidenceGeneration(), ComponentFingerprint: installed[1].ComponentFingerprint}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
	home.OwnerUserID, home.Kind = owner, "group"
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing Song"})
	child.ID = projectconnection.ProjectWorkspaceIDForRun(roleRepairFixtureRunID)
	child.OwnerUserID, child.ParentID = owner, home.ID
	key := workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: homePin.PluginID, ProgramID: homePin.ProgramID}.Normalize()
	linkID := workspace.AssistantProjectLinkID(home.ID, child.ID)
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramSchemaVersion, StateRevision: 1,
		Key: key, Declaration: homeDeclaration.AssistantProgram(), HomeProvider: homePin, LinkedProjectIDs: []string{child.ID}, PluginAvailable: true})
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: linkID, SchemaVersion: workspace.AssistantProjectLinkSchemaVersion, StationWorkspaceID: home.ID, Key: key,
		DeclarationVersion: workspace.AssistantProgramSchemaVersion, LinkedAt: time.Now().UTC(), StateRevision: 1, HomeProvider: homePin, ProjectProvider: projectPin})
	source := &workspace.PluginTemplateOwner{PluginID: projectPin.PluginID, PluginVersion: projectPin.PluginVersion, BlueprintID: projectPin.BlueprintID, BlueprintVersion: projectPin.BlueprintVersion}
	child.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "plugin:reaper-plugin:reaper-song", PluginOwner: source,
		AssistantProgram: homeDeclaration.AssistantProgram(), GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: 1, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:reaper-song", DefinitionDigest: strings.Repeat("c", 64), ReviewDigest: strings.Repeat("d", 64), OperationDigest: strings.Repeat("e", 64),
			HomeProvider: homePin, ProjectProvider: projectPin, ProgramKey: &key, HomeWorkspaceID: home.ID, ProjectLinkID: linkID, AppliedAt: time.Now().UTC(),
		}})
	primary := workspace.NewInMemoryStore()
	folder, err := workspace.NewFileStore(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folder.Close() })
	for _, item := range []*workspace.Workspace{home, child} {
		if err := primary.Save(item); err != nil {
			t.Fatal(err)
		}
		if err := folder.Save(item); err != nil {
			t.Fatal(err)
		}
	}
	return workspace.NewSyncStore(primary, folder), primary, folder, installed, owner, home.ID, child.ID
}

func TestProjectRoleRepairReviewPersistsWithoutTouchingChildOrReissuingAnExpiredKey(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	path := filepath.Join(t.TempDir(), "review.db")
	db, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	reviewer := NewProjectRoleRepairReviewer(store, db, list)
	beforePrimary, err := primary.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	beforeFolder, err := folder.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reviewer.Review(t.Context(), owner, homeID, projectID, "review-1")
	if err != nil || first.Token == "" || len(first.Inspection.Roles) != 1 || first.Inspection.RoleDigest == "" {
		t.Fatalf("durable exact-child review: %+v %v", first, err)
	}
	if again, err := reviewer.Review(t.Context(), owner, homeID, projectID, "review-1"); err != nil || again.Token != first.Token {
		t.Fatalf("lost reply reissued review: %+v %v", again, err)
	}
	if pending, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, first.Token); err != nil || pending.Token != first.Token {
		t.Fatalf("unchanged exact review was not readable: %+v %v", pending, err)
	}
	if _, err := reviewer.InspectPendingReview(t.Context(), "foreign", homeID, projectID, first.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner read a review token: %v", err)
	}
	if _, err := reviewer.InspectPendingReview(t.Context(), owner, homeID, projectID, "not-a-token"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("untrusted token read a review: %v", err)
	}
	for _, source := range []struct {
		store   workspace.Store
		version int64
	}{{primary, beforePrimary.Version}, {folder, beforeFolder.Version}} {
		current, getErr := source.store.Get(projectID)
		if getErr != nil || current.Version != source.version || len(current.GetAssistantProjectLink().ProjectRoles) != 0 || len(current.GetAgentInstances()) != 0 {
			t.Fatalf("review mutated a child or saved a role: %+v %v", current, getErr)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(context.Background(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	second := NewProjectRoleRepairReviewer(store, reopened, list)
	if again, err := second.Review(t.Context(), owner, homeID, projectID, "review-1"); err != nil || again.Token != first.Token {
		t.Fatalf("server restart lost the exact review: %+v %v", again, err)
	}
	if pending, err := second.InspectPendingReview(t.Context(), owner, homeID, projectID, first.Token); err != nil || pending.Token != first.Token {
		t.Fatalf("server restart lost the pending review: %+v %v", pending, err)
	}
	if _, err := second.Review(t.Context(), "foreign", homeID, projectID, "review-2"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner reviewed child: %v", err)
	}
	second.now = func() time.Time { return first.ExpiresAt.Add(time.Second) }
	if _, err := second.Review(t.Context(), owner, homeID, projectID, "review-1"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("expired key renewed consent: %v", err)
	}
	if _, err := second.InspectPendingReview(t.Context(), owner, homeID, projectID, first.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("expired review remained inspectable: %v", err)
	}
	fresh, err := second.Review(t.Context(), owner, homeID, projectID, "review-2")
	if err != nil || fresh.Token == first.Token {
		t.Fatalf("new explicit review failed after expiry: %+v %v", fresh, err)
	}
	if _, err := second.Review(t.Context(), owner, homeID, projectID, "review-1"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("old key was recycled after a newer review: %v", err)
	}
	// SQLite-primary reads can omit portable template provenance. Changing
	// only the creation snapshot on matching mirrors must still stale the
	// original review even if the primary child Version did not move.
	original, err := folder.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	originalReviewDigest := original.GetTemplateProvenance().GroupRequirement.ReviewDigest
	portableReviewDigest := strings.Repeat("f", 64)
	changePortable := func(child *workspace.Workspace) error {
		provenance := child.GetTemplateProvenance()
		provenance.GroupRequirement.ReviewDigest = portableReviewDigest
		child.SetTemplateProvenance(provenance)
		return nil
	}
	for _, source := range []workspace.Store{primary, folder} {
		if err := source.Update(projectID, changePortable); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := second.Review(t.Context(), owner, homeID, projectID, "review-2"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("changed portable creation snapshot replayed a previous review: %v", err)
	}
	if _, err := second.InspectPendingReview(t.Context(), owner, homeID, projectID, fresh.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("portable provenance drift kept a pending review live: %v", err)
	}
	if _, err := second.Review(t.Context(), owner, homeID, projectID, "review-3"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("fresh review accepted changed original creator digest: %v", err)
	}
	portableReviewDigest = originalReviewDigest
	for _, source := range []workspace.Store{primary, folder} {
		if err := source.Update(projectID, changePortable); err != nil {
			t.Fatal(err)
		}
	}
	third, err := second.Review(t.Context(), owner, homeID, projectID, "review-3")
	if err != nil {
		t.Fatalf("restoring the original creator evidence still refused review: %v", err)
	}
	if err := store.Update(homeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.StateRevision++
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Review(t.Context(), owner, homeID, projectID, "review-3"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("changed Home replayed a previous review: %v", err)
	}
	if _, err := second.InspectPendingReview(t.Context(), owner, homeID, projectID, third.Token); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("changed Home retained a pending review: %v", err)
	}
}

func TestInspectMissingSplitProjectRolesRefusesBoundOrChangedChildEvidence(t *testing.T) {
	cases := map[string]func(*workspace.SyncStore, *workspace.InMemoryStore, *workspace.FileStore, string, string) error{
		"legacy link schema": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.SchemaVersion = workspace.AssistantProjectLinkLegacySchemaVersion
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"changed declaration version": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.DeclarationVersion++
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"partial binding revision": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.ProjectBindings.StateRevision = 1
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"already bound role": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.ProjectBindings.Bindings = []workspace.AssistantRoleBinding{{RoleID: "reaper-assistant", AgentInstanceID: "agent", AgentName: "REAPER Assistant"}}
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"roles already present": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Scope: workspace.AssistantRoleScopeProject}}
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"changed project provider on both mirrors": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				link := child.GetAssistantProjectLink()
				link.ProjectProvider.PluginGeneration++
				child.SetAssistantProjectLink(link)
				return nil
			})
		},
		"changed creation source": func(store *workspace.SyncStore, _ *workspace.InMemoryStore, _ *workspace.FileStore, _, projectID string) error {
			return store.Update(projectID, func(child *workspace.Workspace) error {
				provenance := child.GetTemplateProvenance()
				provenance.GroupRequirement.SourcePlugin = &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", BlueprintID: "different-blueprint"}
				child.SetTemplateProvenance(provenance)
				return nil
			})
		},
		"missing group snapshot": func(_ *workspace.SyncStore, primary *workspace.InMemoryStore, folder *workspace.FileStore, _, projectID string) error {
			clear := func(child *workspace.Workspace) error {
				provenance := child.GetTemplateProvenance()
				provenance.GroupRequirement = nil
				child.SetTemplateProvenance(provenance)
				return nil
			}
			if err := primary.Update(projectID, clear); err != nil {
				return err
			}
			return folder.Update(projectID, clear)
		},
		"Home mirror changed": func(_ *workspace.SyncStore, _ *workspace.InMemoryStore, folder *workspace.FileStore, homeID, _ string) error {
			return folder.Update(homeID, func(home *workspace.Workspace) error {
				state := home.GetAssistantProgramState()
				state.HomeProvider.DeclarationDigest = strings.Repeat("f", 64)
				home.SetAssistantProgramState(state)
				return nil
			})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
			if err := mutate(store, primary, folder, homeID, projectID); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectMissingSplitProjectRoles(store, installed, owner, homeID, projectID); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
				t.Fatalf("changed or already staffed child offered an old-role snapshot: %v", err)
			}
		})
	}
}

func TestInspectMissingSplitProjectRolesRequiresUnchangedOwnerPinsAndMirrors(t *testing.T) {
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	inspect := func() (ProjectRoleRepairInspection, error) {
		return InspectMissingSplitProjectRoles(store, installed, owner, homeID, projectID)
	}
	before, err := primary.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := inspect()
	if err != nil || preview.HomeID != homeID || preview.ProjectID != projectID || preview.LinkID != before.GetAssistantProjectLink().ID ||
		preview.LinkRevision != 1 || preview.RoleDigest == "" || len(preview.Roles) != 1 || preview.Roles[0].ID != "reaper-assistant" {
		t.Fatalf("original provider evidence was not disclosed: %+v %v", preview, err)
	}
	after, err := primary.Get(projectID)
	if err != nil || len(after.GetAssistantProjectLink().ProjectRoles) != 0 || len(after.GetAgentInstances()) != 0 || len(after.GetTemplateProvenance().AssistantProjectRoles) != 0 {
		t.Fatalf("inspection repaired or staffed a child: %+v %v", after, err)
	}
	if _, err := InspectMissingSplitProjectRoles(store, installed, "other-owner", homeID, projectID); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner inspected old child: %v", err)
	}
	installed[1].ContentGeneration++
	if _, err := inspect(); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("new provider generation supplied old roles: %v", err)
	}
	installed[1].ContentGeneration--
	if err := folder.Update(projectID, func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: "unverified", Scope: workspace.AssistantRoleScopeProject}}
		child.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := inspect(); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("split child mirror was chosen as the old child's roles: %v", err)
	}
}
