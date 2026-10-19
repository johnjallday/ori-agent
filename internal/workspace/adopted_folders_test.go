package workspace

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func adoptedFoldersFixture() *Workspace {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	ws.ID = "song"
	ws.DirectoryReferences = []DirectoryReference{
		{ID: "entry", WorkspaceID: "song", Name: "Song", Path: "/Invented/Songs/Song"},
		{ID: "loose", WorkspaceID: "song", Name: "Loose", Path: "/Invented/Loose"},
		{ID: "samples", WorkspaceID: "song", Name: "Samples", Path: "/Invented/Samples", Purpose: "sample_library"},
		{ID: "downloads", WorkspaceID: "song", Name: "Downloads", Path: "/Invented/Downloads"},
	}
	ws.SharedData = map[string]any{}
	return ws
}

func TestAdoptedFoldersNamesOnlyTheProjectEntryReference(t *testing.T) {
	ws := adoptedFoldersFixture()
	if got := ws.AdoptedFolders(); len(got) != 0 {
		t.Fatal("a reference no project entry names was adopted", got)
	}
	if err := SetProjectEntryLocator(ws.SharedData, ProjectEntryLocator{SchemaVersion: ProjectEntryLocatorSchemaVersion, Kind: ProjectEntryDirectoryReference, DirectoryReferenceID: "entry", RelativePath: "Song.rpp"}); err != nil {
		t.Fatal(err)
	}
	want := []AdoptedFolder{{Kind: AdoptedProjectFolder, Path: "/Invented/Songs/Song"}}
	if got := ws.AdoptedFolders(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestAdoptedFoldersIncludesThePrimaryLinkedDirectoryOnce(t *testing.T) {
	ws := adoptedFoldersFixture()
	ws.SharedData["primary_directory_id"] = "entry"
	ws.SharedData["project_directory_id"] = "entry"
	want := []AdoptedFolder{{Kind: AdoptedProjectFolder, Path: "/Invented/Songs/Song"}}
	if got := ws.AdoptedFolders(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestAdoptedFoldersExcludesCapabilityAndJanitorReferences(t *testing.T) {
	ws := adoptedFoldersFixture()
	ws.SharedData["primary_directory_id"] = "samples"
	if got := ws.AdoptedFolders(); len(got) != 0 {
		t.Fatal("a capability-owned reference was adopted", got)
	}
	ws.SharedData["primary_directory_id"] = "downloads"
	ws.InstalledCapabilities = []InstalledCapability{{ID: CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(),
		OwnedResources: []CapabilityResource{{Kind: ResourceDirectoryReference, ID: "downloads", Shared: true}}}}
	if got := ws.AdoptedFolders(); len(got) != 0 {
		t.Fatal("a File Janitor root was adopted", got)
	}
}

func TestAdoptedFoldersRejectsRelativeRootAndForeignReferences(t *testing.T) {
	for _, ref := range []DirectoryReference{
		{ID: "x", WorkspaceID: "song", Name: "x", Path: "relative/song"},
		{ID: "x", WorkspaceID: "song", Name: "x", Path: "/"},
		{ID: "x", WorkspaceID: "song", Name: "x", Path: "/Invented/../Escape"},
		{ID: "x", WorkspaceID: "another", Name: "x", Path: "/Invented/Other"},
	} {
		ws := adoptedFoldersFixture()
		ws.DirectoryReferences = []DirectoryReference{ref}
		ws.SharedData["primary_directory_id"] = "x"
		if got := ws.AdoptedFolders(); len(got) != 0 {
			t.Fatal("unsafe reference adopted", ref.Path, got)
		}
	}
}

func homeLibraryFixture(t *testing.T, roots []map[string]any, inactive []string) *Workspace {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"schema_version": 1, "roots": roots})
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
	ws.ID = "home"
	ws.SetAssistantProgramState(&AssistantProgramState{SchemaVersion: 1, ProjectLibrary: encoded, ProjectLibraryInactiveRoots: inactive})
	return ws
}

func TestAdoptedFoldersListsOnlyActiveHomeLibraryRoots(t *testing.T) {
	revoked := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ws := homeLibraryFixture(t, []map[string]any{
		{"id": "b", "path": "/Invented/Library/B"},
		{"id": "a", "path": "/Invented/Library/A"},
		{"id": "gone", "path": "/Invented/Library/Gone", "revoked_at": revoked},
		{"id": "frozen", "path": "/Invented/Library/Frozen"},
	}, []string{"frozen"})
	want := []AdoptedFolder{{Kind: AdoptedLibraryRoot, Path: "/Invented/Library/A"}, {Kind: AdoptedLibraryRoot, Path: "/Invented/Library/B"}}
	if got := ws.AdoptedFolders(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	// A linked project's copy of the program state is not a Home.
	ws.SetAssistantProjectLink(&AssistantProjectLink{SchemaVersion: 1, StationWorkspaceID: "station"})
	if got := ws.AdoptedFolders(); len(got) != 0 {
		t.Fatal("a linked project reported library roots", got)
	}
}

func TestAdoptedFoldersIgnoresMalformedLibraryDocuments(t *testing.T) {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
	ws.SetAssistantProgramState(&AssistantProgramState{SchemaVersion: 1, ProjectLibrary: json.RawMessage(`{"roots":"not a list"}`)})
	if got := ws.AdoptedFolders(); len(got) != 0 {
		t.Fatal(got)
	}
	many := make([]map[string]any, maxAdoptedLibraryRoots+1)
	for i := range many {
		many[i] = map[string]any{"id": string(rune('a' + i%26)), "path": "/Invented/Library"}
	}
	if got := homeLibraryFixture(t, many, nil).AdoptedFolders(); len(got) != 0 {
		t.Fatal("an oversized root list was trusted", got)
	}
}
