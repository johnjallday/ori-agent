package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type projectFallbackSource struct {
	workspace *Workspace
	root      string
}

func (s projectFallbackSource) GetFolderWorkspace(string) (*Workspace, error) {
	return s.workspace, nil
}
func (s projectFallbackSource) GetFolderPath(string) (string, error) { return s.root, nil }

func projectFallbackFixture(t *testing.T) (*ProjectFileFallbackPreparer, *Workspace, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "song")
	if err := os.Mkdir(project, 0o750); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(project, "song.rpp")
	if err := os.WriteFile(entry, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.SharedData = map[string]any{}
	ws.ProjectPath = "song"
	if err := SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	return NewProjectFileFallbackPreparer(projectFallbackSource{workspace: ws, root: root}), ws, entry
}

func genericFallbackTask(ws *Workspace) Task {
	return Task{
		ID: "task", WorkspaceID: ws.ID, Description: "Adjust project", Details: "Change one setting.",
		RequiredCapabilities: []string{"local_live_control"}, FileFallbackFor: []string{"local_live_control"},
	}
}

func TestProjectFileFallbackPromotesOnlyAuthoritativeProject(t *testing.T) {
	preparer, ws, entry := projectFallbackFixture(t)
	run, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, genericFallbackTask(ws), "local_live_control")
	if err != nil {
		t.Fatal(err)
	}
	defer run.Abort()
	prepared := run.PreparedTask()
	if prepared.RuntimeExecution == nil || !prepared.RuntimeExecution.FileOnly || !prepared.RuntimeExecution.DisableTools || len(prepared.RequiredCapabilities) != 0 {
		t.Fatalf("prepared task = %+v", prepared)
	}
	staged := filepath.Join(prepared.RuntimeExecution.WorkspaceRoot, prepared.RuntimeExecution.Filename)
	if err := os.WriteFile(staged, []byte("UPDATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(entry)
	if string(got) != "UPDATED" {
		t.Fatalf("source = %q", got)
	}
}

func TestProjectFileFallbackResolvesExactDirectoryReferenceEntry(t *testing.T) {
	workspaceRoot := t.TempDir()
	externalRoot := t.TempDir()
	entry := filepath.Join(externalRoot, "existing.rpp")
	if err := os.WriteFile(entry, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "External"})
	ws.SharedData = map[string]any{}
	if err := ws.AddDirectoryReference(DirectoryReference{ID: "external", Name: "External", Path: externalRoot}); err != nil {
		t.Fatal(err)
	}
	if err := SetProjectEntryLocator(ws.SharedData, ProjectEntryLocator{
		SchemaVersion: ProjectEntryLocatorSchemaVersion, Kind: ProjectEntryDirectoryReference,
		DirectoryReferenceID: "external", RelativePath: "existing.rpp",
	}); err != nil {
		t.Fatal(err)
	}
	resolved, err := authoritativeProjectEntry(projectFallbackSource{workspace: ws, root: workspaceRoot}, ws.ID)
	if err != nil || resolved != entry {
		t.Fatalf("external fallback entry = %q err=%v", resolved, err)
	}
}

func TestProjectFileFallbackRejectsUndeclaredCapabilityExtraFilesAndConflicts(t *testing.T) {
	preparer, ws, entry := projectFallbackFixture(t)
	if _, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, genericFallbackTask(ws), "other_control"); err == nil {
		t.Fatal("undeclared capability received a fallback")
	}

	run, err := preparer.PrepareTaskFileFallback(context.Background(), ws.ID, genericFallbackTask(ws), "local_live_control")
	if err != nil {
		t.Fatal(err)
	}
	defer run.Abort()
	if err := os.WriteFile(filepath.Join(run.PreparedTask().RuntimeExecution.WorkspaceRoot, "extra.txt"), []byte("not allowed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Commit(); err == nil {
		t.Fatal("extra staging file was promoted")
	}
	got, _ := os.ReadFile(entry)
	if string(got) != "ORIGINAL" {
		t.Fatal("failed fallback changed source")
	}

	preparer, ws, entry = projectFallbackFixture(t)
	run, err = preparer.PrepareTaskFileFallback(context.Background(), ws.ID, genericFallbackTask(ws), "local_live_control")
	if err != nil {
		t.Fatal(err)
	}
	defer run.Abort()
	staged := filepath.Join(run.PreparedTask().RuntimeExecution.WorkspaceRoot, run.PreparedTask().RuntimeExecution.Filename)
	if err := os.WriteFile(staged, []byte("FALLBACK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("USER CHANGE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Commit(); err == nil {
		t.Fatal("concurrent source change was overwritten")
	}
	got, _ = os.ReadFile(entry)
	if string(got) != "USER CHANGE" {
		t.Fatal("fallback overwrote concurrent source change")
	}
}
