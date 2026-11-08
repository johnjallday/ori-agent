package folderagent

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestProjectParents_NeedOnlyFoldersTwoOrMoreDown(t *testing.T) {
	facts := foldermap.Facts{Members: []foldermap.Project{
		{Path: "Top Song", Depth: 1},
		{Path: "Albums/Album-1", Depth: 2},
		{Path: "Albums/Album-2", Depth: 2},
		{Path: "Label/2025/Artist/Album/Disc/Song", Depth: 6},
	}}
	got := ProjectParents(facts)
	want := []string{"Albums", "Label/2025/Artist/Album/Disc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parents = %v, want %v", got, want)
	}
}

func TestBuildPlan_ExistingHomeIsJoinedNotCreated(t *testing.T) {
	members := []foldermap.Project{}
	for _, p := range []string{"2025/A/X/D1/One", "2025/A/X/D1/Two", "2026/B/Y/D2/Three", "2026/B/Y/D2/Four", "2026/C/Z/S/Five"} {
		members = append(members, foldermap.Project{Path: p, Depth: 5, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
	}
	rec := Record{FolderName: "Label 2026", WorkspaceName: "Label 2026", AgentName: "Label 2026 Manager", Stage: foldermap.StageHome,
		Facts: foldermap.Facts{Members: members, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 5, HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}},
		Candidates: []folderlook.Candidate{{ID: "home:music_project_management", Kind: folderlook.KindHomeProvider, Name: "Music Production Home",
			Action: folderlook.ActionAlreadyHere, Required: true, HomeProviderKey: "music_project_management"}}}
	plan := BuildPlan(rec, nil)
	var names []string
	for _, l := range plan.Lines {
		names = append(names, l.Name)
	}
	want := []string{"Adds Label 2026 to your Music Production Home", "Lists 5 songs on its shelf",
		"Looks into 3 folders for songs", "Places Label 2026 inside your Music Production Home", "Nothing is moved, copied or read"}
	if !plan.Runnable || !reflect.DeepEqual(names, want) {
		t.Fatalf("plan runnable=%v lines=%v", plan.Runnable, names)
	}
	if got := HomeReceiptLine(5, 1, rec.Facts); got != "5 songs listed on the shelf · 1 folder not reached yet" {
		t.Fatalf("receipt = %q", got)
	}
}

func TestBuildPlan_DumpAddsFileJanitorInPlaceWithFileCurator(t *testing.T) {
	rec := Record{FolderName: "Messy", WorkspaceName: "Messy", AgentName: "Messy Manager", Stage: foldermap.StageTools,
		Facts:      foldermap.Facts{Dump: true, RootLooseFiles: 40, RootLooseKinds: 6},
		Candidates: folderlook.RequiredCandidates(foldermap.Facts{Dump: true}, folderlook.Readiness{})}
	plan := BuildPlan(rec, nil)
	var names []string
	for _, l := range plan.Lines {
		names = append(names, l.Name)
	}
	want := []string{"Adds File Janitor to Messy", "Adds File Curator", "Nothing is moved, copied or read"}
	if !plan.Runnable || plan.Unavailable != "" || !reflect.DeepEqual(names, want) {
		t.Fatalf("plan runnable=%v unavailable=%q lines=%v", plan.Runnable, plan.Unavailable, names)
	}
	if plan.Lines[0].Kind != FolderPlanCapability {
		t.Fatalf("first line kind = %q", plan.Lines[0].Kind)
	}
}

func TestNextScopes_WalksDownTheIssuedScopes(t *testing.T) {
	needed := []string{"Albums", "Label/2025/Artist/Album/Disc"}
	scanned := map[string]bool{}
	// After the root listing the library issued its children and
	// grandchildren as scopes.
	scopes := []LibraryScope{{ID: "s1", RelativeFolder: "Albums"}, {ID: "s2", RelativeFolder: "Label"}, {ID: "s3", RelativeFolder: "Label/2025"}}
	first := NextScopes(needed, scopes, scanned)
	if !reflect.DeepEqual(first, []LibraryScope{{ID: "s1", RelativeFolder: "Albums"}, {ID: "s3", RelativeFolder: "Label/2025"}}) {
		t.Fatalf("first = %+v", first)
	}
	for _, s := range first {
		scanned[s.RelativeFolder] = true
	}
	// Scanning Label/2025 issued deeper scopes; the walk goes on down.
	scopes = append(scopes, LibraryScope{ID: "s4", RelativeFolder: "Label/2025/Artist/Album"}, LibraryScope{ID: "s5", RelativeFolder: "Label/2025/Artist"})
	second := NextScopes(needed, scopes, scanned)
	if !reflect.DeepEqual(second, []LibraryScope{{ID: "s4", RelativeFolder: "Label/2025/Artist/Album"}}) {
		t.Fatalf("second = %+v", second)
	}
	scanned["Label/2025/Artist/Album"] = true
	scopes = append(scopes, LibraryScope{ID: "s6", RelativeFolder: "Label/2025/Artist/Album/Disc"})
	third := NextScopes(needed, scopes, scanned)
	if len(third) != 1 || third[0].ID != "s6" {
		t.Fatalf("third = %+v", third)
	}
	scanned["Label/2025/Artist/Album/Disc"] = true
	if got := NextScopes(needed, scopes, scanned); len(got) != 0 {
		t.Fatalf("nothing left = %+v", got)
	}
	if got := Unreached(needed, scanned); len(got) != 0 {
		t.Fatalf("unreached = %v", got)
	}
	// A folder no scope ever covers stays unreached.
	if got := Unreached([]string{"Elsewhere/X"}, scanned); !reflect.DeepEqual(got, []string{"Elsewhere/X"}) {
		t.Fatalf("unreached = %v", got)
	}
	if got := NextScopes([]string{"Elsewhere/X"}, scopes, scanned); len(got) != 0 {
		t.Fatalf("no issued scope above it = %+v", got)
	}
}
