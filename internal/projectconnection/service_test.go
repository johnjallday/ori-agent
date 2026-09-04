package projectconnection

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
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
