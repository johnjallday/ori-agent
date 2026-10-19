package dailybrief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type coverageTestSource struct {
	read    backuprecovery.CoverageRead
	err     error
	folders []backuprecovery.AdoptedFolder
}

func (s *coverageTestSource) BriefCoverage(_ context.Context, _ string, _ WorkspaceScopeResult, folders []backuprecovery.AdoptedFolder) (backuprecovery.CoverageRead, error) {
	s.folders = folders
	return s.read, s.err
}

func coverageScopedWorkspace(id, name, folder string) ScopedWorkspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	ws.ID = id
	ws.SharedData = map[string]any{}
	if folder != "" {
		ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "ref-" + id, WorkspaceID: id, Name: name, Path: folder}}
		ws.SharedData["primary_directory_id"] = "ref-" + id
	}
	return ScopedWorkspace{ID: id, Slug: id, Name: name, Workspace: ws}
}

func TestScopedAdoptedFoldersCapIsExplicit(t *testing.T) {
	scope := WorkspaceScopeResult{Complete: true, Workspaces: []ScopedWorkspace{coverageScopedWorkspace("hq", "HQ", "")}}
	for i := 0; i < MaxAdoptedFolders+1; i++ {
		id := fmt.Sprintf("song-%02d", i)
		scope.Workspaces = append(scope.Workspaces, coverageScopedWorkspace(id, id, "/Invented/"+id))
	}
	folders, truncated := ScopedAdoptedFolders(scope)
	if !truncated || len(folders) != MaxAdoptedFolders || folders[0] != (backuprecovery.AdoptedFolder{WorkspaceID: "song-00", Path: "/Invented/song-00"}) {
		t.Fatal(truncated, len(folders), folders[0])
	}
	source := &coverageTestSource{read: backuprecovery.CoverageRead{Complete: true}}
	if got := ReadCoverage(context.Background(), source, "local", scope); got.Complete {
		t.Fatal("a truncated enumeration read as complete")
	}
}

func TestReadCoverageMapsScopeIdentityAndNeverPaths(t *testing.T) {
	at := time.Date(2026, 10, 18, 3, 0, 0, 0, time.UTC)
	scope := WorkspaceScopeResult{Complete: true, Workspaces: []ScopedWorkspace{
		coverageScopedWorkspace("backup", "My Backups", ""),
		coverageScopedWorkspace("song-a", "Song A", "/Invented/Songs/A"),
		coverageScopedWorkspace("song-b", "Song B", "/Invented/Songs/B"),
		coverageScopedWorkspace("song-c", "Song C", "/Invented/Songs/C"),
	}}
	source := &coverageTestSource{read: backuprecovery.CoverageRead{Complete: true,
		Consoles: []backuprecovery.CoverageConsole{{WorkspaceID: "outside", Configured: true}, {WorkspaceID: "backup", Configured: true}},
		Folders: []backuprecovery.FolderCoverage{
			{WorkspaceID: "song-a", State: backuprecovery.CoverageCovered, Reason: "completed_backup_observed", Provider: "restic", CoveringWorkspaceID: "backup", LastCompletedAt: &at},
			{WorkspaceID: "song-b", State: backuprecovery.CoverageNotCovered, Reason: "not_in_any_source"},
			{WorkspaceID: "song-c", State: backuprecovery.CoverageCovered, Reason: "completed_backup_observed", Provider: "restic", CoveringWorkspaceID: "outside", LastCompletedAt: &at},
		}}}
	got := ReadCoverage(context.Background(), source, "local", scope)
	if len(source.folders) != 3 || source.folders[1].Path != "/Invented/Songs/B" {
		t.Fatal("adopted folders did not reach the owning reader", source.folders)
	}
	if !got.Complete || !got.Configured || got.Console == nil || got.Console.ID != "backup" || got.Adopted() != 3 || got.NotCovered != 1 {
		t.Fatal(got)
	}
	rows := map[string]WorkspaceCoverage{}
	for _, row := range got.Workspaces {
		rows[row.Workspace.ID] = row
	}
	if a := rows["song-a"]; a.State != backuprecovery.CoverageCovered || a.Covering == nil || a.Covering.Name != "My Backups" || a.Workspace.Name != "Song A" || !a.LastCompletedAt.Equal(at) {
		t.Fatal(a)
	}
	if c := rows["song-c"]; c.State != backuprecovery.CoverageUnknown || c.Covering != nil {
		t.Fatal("evidence from outside the scope became a protection claim", c)
	}
	if route := CoverageConsoleRoute(got.Console); route != "/workspaces/backup?panel=backup-recovery&tab=overview&section=coverage" {
		t.Fatal(route)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "/Invented") {
		t.Fatal("a path crossed the coverage projection", string(encoded))
	}
}

func TestReadCoverageReportsAWorkspacesWeakestFolder(t *testing.T) {
	home := coverageScopedWorkspace("home", "Music Home", "")
	library, _ := json.Marshal(map[string]any{"roots": []map[string]any{{"id": "a", "path": "/Invented/Library/A"}, {"id": "b", "path": "/Invented/Library/B"}}})
	home.Workspace.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: 1, ProjectLibrary: library})
	scope := WorkspaceScopeResult{Complete: true, Workspaces: []ScopedWorkspace{home}}
	source := &coverageTestSource{read: backuprecovery.CoverageRead{Complete: true, Folders: []backuprecovery.FolderCoverage{
		{WorkspaceID: "home", State: backuprecovery.CoverageUnknown, Reason: "evidence_stale"},
		{WorkspaceID: "home", State: backuprecovery.CoverageNotCovered, Reason: "no_configuration"},
	}}}
	got := ReadCoverage(context.Background(), source, "local", scope)
	if len(source.folders) != 2 || got.Adopted() != 1 || got.NotCovered != 1 || got.Workspaces[0].State != backuprecovery.CoverageNotCovered || got.Configured || got.Console != nil {
		t.Fatal(got)
	}
}

// managedFolderSource is an in-memory store that, like the folder store,
// knows each workspace's own Ori-managed folder.
type managedFolderSource struct {
	*workspace.InMemoryStore
	folders map[string]string
}

func (s managedFolderSource) GetFolderPath(id string) (string, error) {
	if folder, ok := s.folders[id]; ok {
		return folder, nil
	}
	return "", errors.New("no folder")
}

// Demo 1.9 regression: a Personal HQ links its own managed folder as its
// primary directory; that is Ori's folder, not one the user adopted.
func TestScopedAdoptedFoldersSkipAWorkspacesOwnManagedFolder(t *testing.T) {
	store := managedFolderSource{InMemoryStore: workspace.NewInMemoryStore(), folders: map[string]string{
		"hq": "/Invented/Ori Workspaces/my-hq", "song": "/Invented/Ori Workspaces/night-drive",
	}}
	hq := coverageScopedWorkspace("hq", "My HQ", "/Invented/Ori Workspaces/my-hq")
	song := coverageScopedWorkspace("song", "Night Drive", "/Invented/Documents/Night Drive")
	song.Workspace.DirectoryReferences = append(song.Workspace.DirectoryReferences, workspace.DirectoryReference{ID: "inside", WorkspaceID: "song", Name: "Inside", Path: "/Invented/Ori Workspaces/night-drive/stems"})
	song.Workspace.SharedData["project_directory_id"] = "inside"
	for _, ws := range []*workspace.Workspace{hq.Workspace, song.Workspace} {
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	scope := ResolveWorkspaceScope(store, Config{Scope: ScopeAll, IncludeFutureWorkspaces: true}, "")
	folders, truncated := ScopedAdoptedFolders(scope)
	if truncated || len(folders) != 1 || folders[0] != (backuprecovery.AdoptedFolder{WorkspaceID: "song", Path: "/Invented/Documents/Night Drive"}) {
		t.Fatal("a managed folder was counted as adopted", folders)
	}
	if _, ok := WorkspaceProtection(context.Background(), &coverageTestSource{}, "local", WorkspaceScopeResult{}.Including(store, hq.Workspace), hq.Workspace, "/chooser", time.Now()); ok {
		t.Fatal("the HQ got a Protection fact for its own folder")
	}
}

func TestReadCoverageFailureIsIncompleteNotEmpty(t *testing.T) {
	scope := WorkspaceScopeResult{Complete: true, Workspaces: []ScopedWorkspace{coverageScopedWorkspace("song", "Song", "/Invented/Song")}}
	got := ReadCoverage(context.Background(), &coverageTestSource{err: errors.New("PRIVATE /Invented/Song")}, "local", scope)
	if got.Complete || got.NotCovered != 0 || len(got.Workspaces) != 0 {
		t.Fatal(got)
	}
	if got := ReadCoverage(context.Background(), nil, "local", scope); got.Complete {
		t.Fatal("a missing reader read as complete")
	}
}
