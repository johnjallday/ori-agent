package projectlibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type activationCreatorTest struct {
	store    *workspace.FileStore
	resolver projectconnection.SelectionResolver
	homeID   string
	commits  *int
	result   projectconnection.CommitResult
}

func (c *activationCreatorTest) HomePreparation(projectconnection.Scope) (projectconnection.HomePreparation, error) {
	return projectconnection.HomePreparation{Exists: true, HomeID: c.homeID}, nil
}
func (c *activationCreatorTest) Preview(_ context.Context, _ projectconnection.Scope, request projectconnection.Request) (projectconnection.Preview, error) {
	path, err := c.resolver.Resolve(request.SelectionToken)
	if err != nil {
		return projectconnection.Preview{}, err
	}
	if filepath.Base(path) != "Single" || request.EntryName != "Song.rpp" || request.GroupComposition != "grouped" {
		return projectconnection.Preview{}, projectconnection.ErrInvalid
	}
	return projectconnection.Preview{
		InputDigest: strings.Repeat("a", 64), OwnerDigest: strings.Repeat("b", 64),
		Projection: projectconnection.Projection{EntryName: request.EntryName},
	}, nil
}
func (c *activationCreatorTest) Commit(ctx context.Context, scope projectconnection.Scope, request projectconnection.Request, input, owner string) (projectconnection.CommitResult, error) {
	preview, err := c.Preview(ctx, scope, request)
	if err != nil || input != preview.InputDigest || owner != preview.OwnerDigest {
		return projectconnection.CommitResult{}, projectconnection.ErrChanged
	}
	*c.commits++
	child := &workspace.Workspace{ID: "activation-child", Name: request.WorkspaceName,
		OwnerUserID: scope.OwnerUserID, ParentID: c.homeID}
	home, err := c.store.Get(c.homeID)
	if err != nil {
		return projectconnection.CommitResult{}, err
	}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(c.homeID, child.ID),
		StationWorkspaceID: c.homeID, Key: home.GetAssistantProgramState().Key, StateRevision: 1})
	if err := c.store.Save(child); err != nil {
		return projectconnection.CommitResult{}, err
	}
	if err := c.store.Update(c.homeID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		return projectconnection.CommitResult{}, err
	}
	c.result = projectconnection.CommitResult{HomeWorkspaceID: c.homeID, ProjectWorkspaceID: child.ID,
		ModeID: projecttemplates.ProjectConnectionExistingProject}
	return c.result, nil
}
func (c *activationCreatorTest) Observe(_ projectconnection.Scope, home, project string, mode projecttemplates.ProjectConnectionMode) bool {
	return c.result.HomeWorkspaceID == home && c.result.ProjectWorkspaceID == project && mode == projecttemplates.ProjectConnectionExistingProject
}
func (c *activationCreatorTest) ObservedResult(_ projectconnection.Scope, home, _ string) (projectconnection.CommitResult, bool) {
	return c.result, c.result.HomeWorkspaceID == home && c.result.ProjectWorkspaceID != ""
}

func TestActivation_ReviewedSelectionAndCanonicalReceiptNeverUseBrowserPath(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	commits := 0
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		return &activationCreatorTest{store: file, resolver: resolver, homeID: scope.HomeID, commits: &commits}
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Review(t.Context(), scope, "alternates", doc.Revision, "", "Take A"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous file auto-selected: %v", err)
	}
	if _, err := service.Review(t.Context(), scope, "single", doc.Revision, "../Song.rpp", "Song"); !errors.Is(err, ErrConflict) {
		t.Fatalf("browser-supplied path was used: %v", err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil || review.ProjectFile != "Song.rpp" || review.BlueprintID == "" || len(review.ProjectRoleLabels) != 1 || commits != 0 {
		t.Fatalf("inert reviewed creator preview: %+v commits=%d err=%v", review, commits, err)
	}
	if _, err := service.Commit(t.Context(), Scope{OwnerUserID: "foreign", HomeID: scope.HomeID,
		ProviderID: scope.ProviderID, ProgramID: scope.ProgramID}, "single", review.Token, "commit-song"); err == nil {
		t.Fatal("foreign owner committed a selected project")
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "commit-song")
	if err != nil || result.WorkspaceID != "activation-child" || result.LinkID == "" || result.Replay || commits != 1 {
		t.Fatalf("one exact project attach: %+v commits=%d err=%v", result, commits, err)
	}
	replay, err := service.Commit(t.Context(), scope, "single", review.Token, "commit-song")
	if err != nil || !replay.Replay || replay.WorkspaceID != result.WorkspaceID || commits != 1 {
		t.Fatalf("creator replay was not inert: %+v commits=%d err=%v", replay, commits, err)
	}
	if _, err := service.Commit(t.Context(), scope, "single", review.Token, "different-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("same review accepted a different creator key: %v", err)
	}
	saved, err := a.library.Read(scope)
	if err != nil || len(saved.Entries) != 3 || sessionEntry(saved, "single").Link == nil ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("catalog association or source changed: %+v %v", saved, err)
	}
}

func TestActivation_RealCreatorPreviewsAndAttachesOnePinnedSongWithoutStaffing(t *testing.T) {
	a, scope, _, file, tree, installed := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	homeDecl := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion,
		ID: scope.ProgramID, StationName: "Music Home",
		Roles: []workspace.AssistantProgramRoleSpec{{ID: "portfolio_manager", Label: "Portfolio Manager",
			Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate music."}}}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		home.Kind = "group"
		state := home.GetAssistantProgramState()
		state.Declaration = homeDecl
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	grouping := grouprequirements.NewService(file, grouprequirements.NewMemoryStore())
	grouping.SetIndependentHomeResolver(func(owner string, template projecttemplates.Template, requireHome bool) (grouprequirements.IndependentHomeResolution, error) {
		if owner != scope.OwnerUserID || template.PluginOwner == nil || template.PluginOwner.PluginID != "reaper-plugin" {
			return grouprequirements.IndependentHomeResolution{}, grouprequirements.ErrIndependentHomeInvalid
		}
		project := template.AssistantProject
		projectOwner := &workspace.AssistantProjectProviderOwner{PluginID: "reaper-plugin", PluginVersion: "0.9.0",
			BlueprintID: "reaper-song", BlueprintVersion: 10, ProjectTeamID: project.ID,
			ProjectTeamSchema: project.SchemaVersion, ProjectTeamVersion: project.Version,
			ProjectTeamDigest: projecttemplates.AssistantProjectDigest(project), PluginGeneration: installed[1].EvidenceGeneration(),
			ComponentFingerprint: installed[1].ComponentFingerprint}
		if !requireHome {
			return grouprequirements.IndependentHomeResolution{ProjectOwner: projectOwner}, nil
		}
		home, err := file.Get(scope.HomeID)
		if err != nil {
			return grouprequirements.IndependentHomeResolution{}, err
		}
		return grouprequirements.IndependentHomeResolution{Key: home.GetAssistantProgramState().Key,
			Declaration: homeDecl, Owner: home.GetAssistantProgramState().HomeProvider, ProjectOwner: projectOwner}, nil
	})
	service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
		creator := projectconnection.NewService(file, resolver)
		creator.SetGroupRequirementService(grouping)
		return creator
	})
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatalf("real creator preview: %v", err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 1 {
		t.Fatalf("review created a project: %v %v", ids, err)
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "real-creator-song")
	if err != nil {
		t.Fatalf("real creator commit: %v", err)
	}
	if result.WorkspaceID == "" || result.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, result.WorkspaceID) {
		t.Fatalf("project did not attach to this Home: %+v", result)
	}
	child, err := file.Get(result.WorkspaceID)
	if err != nil || child.ParentID != scope.HomeID || child.OwnerUserID != scope.OwnerUserID ||
		len(child.GetAgentInstances()) != 0 || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("unexpected creator consequence: child=%+v err=%v", child, err)
	}
	replay, err := service.Commit(t.Context(), scope, "single", review.Token, "real-creator-song")
	if err != nil || !replay.Replay || replay.WorkspaceID != child.ID {
		t.Fatalf("real creator replay: %+v %v", replay, err)
	}
	ids, err = file.List()
	if err != nil || len(ids) != 2 {
		t.Fatalf("creator duplicated Home or project: %v %v", ids, err)
	}
}

func TestActivation_RevocationAndSameNamedReplacementRefuseReviewedCreator(t *testing.T) {
	for _, pathChange := range []string{"replace-folder", "replace-file", "revoke"} {
		t.Run(pathChange, func(t *testing.T) {
			a, scope, roots, file, tree, _ := activationFixture(t)
			commits := 0
			service := NewActivationService(a, func(resolver projectconnection.SelectionResolver) ActivationCreator {
				return &activationCreatorTest{store: file, resolver: resolver, homeID: scope.HomeID, commits: &commits}
			})
			doc, err := a.library.Read(scope)
			if err != nil {
				t.Fatal(err)
			}
			review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
			if err != nil {
				t.Fatal(err)
			}
			switch pathChange {
			case "replace-folder":
				if err := os.Rename(tree.single, tree.single+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(tree.single, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tree.single, "Song.rpp"), []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replace-file":
				if err := os.Rename(filepath.Join(tree.single, "Song.rpp"), filepath.Join(tree.single, "Song.rpp-old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(tree.single, "Song.rpp-old"), filepath.Join(tree.single, "Song.rpp")); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				doc, err = a.library.Read(scope)
				if err != nil {
					t.Fatal(err)
				}
				revoke, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = roots.CommitRevoke(scope, doc.Roots[0].ID, revoke.Token, "revoke-before-creator"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.Commit(t.Context(), scope, "single", review.Token, "new-project"); err == nil || commits != 0 {
				t.Fatalf("stale creator changed state: err=%v commits=%d", err, commits)
			}
			ids, err := file.List()
			if err != nil || len(ids) != 1 || ids[0] != scope.HomeID {
				t.Fatalf("stale selection created a child: %v %v", ids, err)
			}
		})
	}
}
