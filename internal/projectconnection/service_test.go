package projectconnection

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func connectionTemplate(t *testing.T) projecttemplates.Template {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "{{name}}.rpp"), []byte("template"), 0o600); err != nil {
		t.Fatal(err)
	}
	return projecttemplates.Template{
		ID: "plugin:neutral:project", Name: "Neutral Project", Path: root, HasSkeleton: true,
		ProjectEntry: &projecttemplates.ProjectEntry{RelativePath: "{{name}}.rpp"},
		StarterTasks: []projecttemplates.StarterTask{
			{Description: "Shared task"},
			{Description: "Existing task", ConnectionModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionExistingProject}},
			{Description: "New task", ConnectionModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionNewProject}},
		},
		ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{
			SchemaVersion: projecttemplates.ProjectConnectionSchemaVersion,
			SupportedModes: []projecttemplates.ProjectConnectionMode{
				projecttemplates.ProjectConnectionNewProject,
				projecttemplates.ProjectConnectionExistingProject,
			},
			AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}},
		},
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 2},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "project-guide",
			StationName: "Project Guide Home", DefaultPrimaryName: "Guide", HireTitle: "Hire guide",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "guide", Label: "Guide", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."},
				{ID: "reviewer", Label: "Reviewer", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Review."},
			},
			Stages:     []workspace.AssistantProgramStageSpec{{ID: "initial", Label: "Initial", AcceptedCompletionThreshold: 0}},
			Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 2, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 16, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Review patterns."},
		},
	}
}

func connectionService(t *testing.T) (*Service, *workspace.SyncStore, *pathselection.Store) {
	t.Helper()
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	selections := pathselection.NewStore()
	return NewService(store, selections), store, selections
}

// splitConnectionTemplate is a split blueprint: its project team names a Home
// contributed by a separate provider plugin instead of carrying a combined
// assistant_program.
func splitConnectionTemplate(t *testing.T) projecttemplates.Template {
	t.Helper()
	template := connectionTemplate(t)
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
	return template
}

// fakeHomeResolver stands in for the host's reciprocal two-provider join. err
// selects the provider state it reports; nil resolves the fixture's Home.
type fakeHomeResolver struct {
	err   error
	calls int
}

func (f *fakeHomeResolver) resolve(ownerUserID string, _ projecttemplates.Template, requireHome bool) (grouprequirements.IndependentHomeResolution, error) {
	f.calls++
	project := &workspace.AssistantProjectProviderOwner{
		PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 2,
		ProjectTeamID: "song-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1,
		ProjectTeamDigest: strings.Repeat("b", 64), PluginGeneration: 3, ComponentFingerprint: strings.Repeat("c", 64),
	}
	if !requireHome {
		return grouprequirements.IndependentHomeResolution{ProjectOwner: project}, nil
	}
	if f.err != nil {
		return grouprequirements.IndependentHomeResolution{}, f.err
	}
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "production", StationName: "Provider Station", DefaultPrimaryName: "Producer", HireTitle: "Hire producer",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "producer", Label: "Producer", Required: true, Primary: true, SystemPrompt: "Coordinate songs."}},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "initial", Label: "Initial"}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 2, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 16, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Review patterns."},
	}
	owner := &workspace.AssistantProgramHomeOwner{
		PluginID: "home-provider", PluginVersion: "0.1.0", ProgramID: "production", HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home), PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64),
	}
	return grouprequirements.IndependentHomeResolution{
		Key:         workspace.AssistantProgramKey{OwnerUserID: ownerUserID, PluginID: "home-provider", ProgramID: "production"}.Normalize(),
		Declaration: home.AssistantProgram(), Owner: owner, ProjectOwner: project,
	}, nil
}

func splitConnectionService(t *testing.T) (*Service, *workspace.SyncStore, *grouprequirements.Service, *fakeHomeResolver) {
	t.Helper()
	service, store, _ := connectionService(t)
	grouping := grouprequirements.NewService(store, grouprequirements.NewMemoryStore())
	resolver := &fakeHomeResolver{}
	grouping.SetIndependentHomeResolver(resolver.resolve)
	service.SetGroupRequirementService(grouping)
	return service, store, grouping, resolver
}

func TestAttachExistingPreviewsThenCommitsWithoutWritingExternalFolder(t *testing.T) {
	service, store, selections := connectionService(t)
	external := t.TempDir()
	entry := filepath.Join(external, "Existing Song.RPP")
	if err := os.WriteFile(entry, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := directoryFingerprint(t, external)
	token, err := selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-existing", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Existing Song"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Projection.SelectedFolder != external || preview.Projection.EntryName != "Existing Song.RPP" {
		t.Fatalf("preview = %+v", preview.Projection)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if after := directoryFingerprint(t, external); !reflect.DeepEqual(after, before) {
		t.Fatalf("external folder changed during setup: before=%#v after=%#v", before, after)
	}
	child, err := store.Get(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	locator, err := workspace.GetProjectEntryLocator(child.SharedData)
	if err != nil || locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference {
		t.Fatalf("child locator = %#v err=%v", locator, err)
	}
	folderRoot, _ := store.GetFolderPath(child.ID)
	resolved, err := workspace.ResolveProjectEntry(child, folderRoot)
	if err != nil || resolved.AbsolutePath != entry {
		t.Fatalf("resolved external entry = %#v err=%v", resolved, err)
	}
	home, err := store.Get(result.HomeWorkspaceID)
	if err != nil || home.Kind != "group" || child.ParentID != home.ID || child.GetAssistantProjectLink() == nil {
		t.Fatalf("Home/child topology = home %#v child %#v err=%v", home, child, err)
	}
	if got := taskDescriptions(child.Tasks); !reflect.DeepEqual(got, []string{"Shared task", "Existing task"}) {
		t.Fatalf("existing-mode starter tasks = %#v", got)
	}
	again, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || again != result {
		t.Fatalf("idempotent commit = %+v err=%v", again, err)
	}
	child, _ = store.Get(result.ProjectWorkspaceID)
	if len(child.Tasks) != 2 {
		t.Fatalf("replay duplicated starter tasks: %#v", taskDescriptions(child.Tasks))
	}
	otherScope := scope
	otherScope.RunID = "another-run"
	if _, err := service.Preview(context.Background(), otherScope, request); !errors.Is(err, ErrChanged) {
		t.Fatalf("duplicate external project ownership error = %v", err)
	}
	if len(child.AgentInstances) != 0 {
		t.Fatalf("project connection created agents: %#v", child.AgentInstances)
	}
	for _, task := range child.Tasks {
		if task.Status != workspace.TaskStatusPending {
			t.Fatalf("project connection started task: %+v", task)
		}
	}
}

func TestAttachExistingRevalidatesSelectionAndRequiresExactCandidate(t *testing.T) {
	service, _, selections := connectionService(t)
	external := t.TempDir()
	for _, name := range []string{"A.rpp", "B.RPP"} {
		if err := os.WriteFile(filepath.Join(external, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	token, _ := selections.Issue(external)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-choice", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Existing"}
	choice, err := service.Preview(context.Background(), scope, request)
	if err != nil || choice.Projection.EntryName != "" || !reflect.DeepEqual(choice.Projection.EntryCandidates, []string{"A.rpp", "B.RPP"}) {
		t.Fatalf("ambiguous preview = %+v err=%v", choice.Projection, err)
	}
	if _, err := service.Commit(context.Background(), scope, request, choice.InputDigest, choice.OwnerDigest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous commit error = %v", err)
	}
	request.EntryName = "A.rpp"
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "A.rpp"), []byte("changed material"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed selection commit error = %v", err)
	}
	request.EntryName = "../A.rpp"
	if _, err := service.Preview(context.Background(), scope, request); !errors.Is(err, ErrInvalid) {
		t.Fatalf("traversal choice error = %v", err)
	}
}

func TestAttachExistingRefusesAFolderImportFolderAlreadyAdopted(t *testing.T) {
	service, store, selections := connectionService(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "Existing Song.rpp"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	imported := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Imported Songs"})
	imported.SharedData = map[string]any{"folder_import": map[string]any{"enabled": true, "path": external}}
	if err := store.Save(imported); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(external)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-imported", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token, WorkspaceName: "Existing Song"}
	if _, err := service.Preview(context.Background(), scope, request); !errors.Is(err, ErrChanged) {
		t.Fatalf("imported folder preview error = %v", err)
	}

	owner, err := FindFolderOwner(store, external, "")
	if err != nil || owner == nil || owner.WorkspaceID != imported.ID || owner.Name != "Imported Songs" {
		t.Fatalf("owner = %#v err=%v", owner, err)
	}
	// A workspace's own folder is also taken, whatever it is called.
	folder, err := store.GetFolderPath(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := FindFolderOwner(store, folder, ""); err != nil || owner == nil || owner.WorkspaceID != imported.ID {
		t.Fatalf("own-folder owner = %#v err=%v", owner, err)
	}
	if owner, err := FindFolderOwner(store, external, imported.ID); err != nil || owner != nil {
		t.Fatalf("the ignored workspace still owns it: %#v err=%v", owner, err)
	}
	if _, err := FindFolderOwner(store, filepath.Join(external, "missing"), ""); !errors.Is(err, ErrFolderUnavailable) {
		t.Fatalf("missing folder error = %v", err)
	}
}

func TestAttachExistingRejectsSymlinkCandidateAndUntrustedToken(t *testing.T) {
	service, _, _ := connectionService(t)
	external := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.rpp")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(external, "linked.rpp")); err != nil {
		t.Fatal(err)
	}
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-symlink", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: external, WorkspaceName: "Existing"}
	if _, err := service.Preview(context.Background(), scope, request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("raw browser path token error = %v", err)
	}
}

func TestCreateNewProjectUsesSameHomeAndManagedLocator(t *testing.T) {
	service, store, _ := connectionService(t)
	template := connectionTemplate(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-new", Template: template}
	request := Request{ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "New Song", ProjectName: "First Idea"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Projection.HomeWillBeCreated || preview.Projection.ParentWorkspaceName != "Project Guide Home" ||
		!reflect.DeepEqual(preview.Projection.CreatedFiles, []string{"first-idea.rpp"}) || preview.Projection.DefaultsStatement == "" {
		t.Fatalf("new-project review is incomplete: %+v", preview.Projection)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := store.Get(result.ProjectWorkspaceID)
	locator, err := workspace.GetProjectEntryLocator(child.SharedData)
	if err != nil || locator.Kind != workspace.ProjectEntryManagedWorkspace || child.ProjectPath != "first-idea" {
		t.Fatalf("managed child = path %q locator %#v err=%v", child.ProjectPath, locator, err)
	}
	root, _ := store.GetFolderPath(child.ID)
	resolved, err := workspace.ResolveProjectEntry(child, root)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(resolved.AbsolutePath); string(got) != "template" {
		t.Fatalf("materialized project = %q", got)
	}
	if got := taskDescriptions(child.Tasks); !reflect.DeepEqual(got, []string{"Shared task", "New task"}) {
		t.Fatalf("new-mode starter tasks = %#v", got)
	}
}

func TestRecommendedStandaloneConnectionCreatesNoHomeOrMembership(t *testing.T) {
	service, store, _ := connectionService(t)
	service.SetGroupRequirementService(grouprequirements.NewService(store, grouprequirements.NewMemoryStore()))
	template := connectionTemplate(t)
	template.Revision = strings.Repeat("a", 64)
	template.GroupRequirement = &projecttemplates.GroupRequirement{
		SchemaVersion: projecttemplates.GroupRequirementSchemaVersion, Policy: projecttemplates.GroupPolicyRecommended,
		AssistantProgramID: template.AssistantProgram.ID, MissingHome: projecttemplates.MissingHomeOfferCreate,
		DefaultHomeName: template.AssistantProgram.StationName,
	}
	template.StandaloneComposition = &projecttemplates.StandaloneComposition{
		SchemaVersion: projecttemplates.StandaloneCompositionSchemaVersion,
		ProjectRoles:  []projecttemplates.StandaloneRole{{RoleID: "reviewer", SystemPrompt: "Review only this project."}},
	}
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-standalone", Template: template}
	request := Request{
		ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "Standalone Song", ProjectName: "Standalone Song",
		GroupComposition: grouprequirements.CompositionStandalone,
	}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Projection.GroupRequirementState != string(grouprequirements.StateReadyStandalone) ||
		preview.Projection.GroupComposition != grouprequirements.CompositionStandalone || preview.Projection.HomeWillBeCreated || preview.Projection.ParentWorkspaceName != "" {
		t.Fatalf("standalone projection = %#v", preview.Projection)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if result.HomeWorkspaceID != "" {
		t.Fatalf("standalone result created a Home: %#v", result)
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != result.ProjectWorkspaceID {
		t.Fatalf("standalone workspace state = %v", ids)
	}
	project, err := store.Get(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provenance := project.GetTemplateProvenance()
	if project.GetAssistantProjectLink() != nil || project.GetAssistantProgramState() != nil ||
		provenance == nil || provenance.GroupRequirement == nil || !provenance.GroupRequirement.StructurallyValid() ||
		provenance.GroupRequirement.SelectedComposition != workspace.GroupRequirementCompositionStandalone {
		t.Fatalf("standalone project = %#v provenance=%#v", project, provenance)
	}
	if observed, ok := service.ObservedResult(scope, "", ""); !ok || observed != result {
		t.Fatalf("standalone observation = %#v ok=%v", observed, ok)
	}
}

func TestManagedConnectionObservesCanonicalFilesWithSQLitePrimary(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	hybrid := session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() { _ = hybrid.Close() })
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewAgentSnapshotStore(workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(hybrid), folders), nil)
	service := NewService(store, pathselection.NewStore())
	scope := Scope{OwnerUserID: "local", RunID: "run-real-store", Template: connectionTemplate(t)}
	request := Request{ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "New Song", ProjectName: "First Idea"}
	preview, err := service.Preview(ctx, scope, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(ctx, scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if observed, ok := service.ObservedResult(scope, "", ""); !ok || observed != result {
		t.Fatalf("created project was not observed through production store: %+v ok=%v", observed, ok)
	}
	// A retry must recognize the existing project, not try to scaffold it again.
	again, err := service.Commit(ctx, scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil || again != result {
		t.Fatalf("retry = %+v err=%v", again, err)
	}
	// The read is still fail-closed: an actual loss of canonical provenance
	// must not be hidden by a cached/database version of the project.
	canonical, err := folders.Get(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	canonical.TemplateProvenance = nil
	if err := folders.Save(canonical); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.ObservedResult(scope, "", ""); ok {
		t.Fatal("missing canonical provenance reported connected")
	}
}

func taskDescriptions(tasks []workspace.Task) []string {
	result := make([]string, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, task.Description)
	}
	return result
}

func directoryFingerprint(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][32]byte, len(entries))
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = sha256.Sum256(content)
	}
	return result
}
