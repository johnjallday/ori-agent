package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeepestCoveringAnchor(t *testing.T) {
	anchors := map[string]string{"albums": "/m/Albums", "label": "/m/Albums/Label", "other": "/m/Other"}
	cases := []struct{ folder, want string }{
		{"/m/Albums/Album-1", "albums"},
		{"/m/Albums/Label/Song", "label"},
		{"/m/Albums/Label/Deep/Song", "label"},
		{"/m/Albums", ""},             // the folder workspace's own folder (FR7)
		{"/m/Albums/Label", "albums"}, // a folder workspace's own folder is covered by its parent's
		{"/m/AlbumsExtra/Song", ""},   // a shared name prefix is not containment
		{"/elsewhere/Song", ""},
	}
	for _, tc := range cases {
		if got := deepestCoveringAnchor(tc.folder, anchors); got != tc.want {
			t.Errorf("deepestCoveringAnchor(%q) = %q, want %q", tc.folder, got, tc.want)
		}
	}
	if got := deepestCoveringAnchor("/m/x/Song", map[string]string{"b": "/m/x", "a": "/m/x"}); got != "a" {
		t.Errorf("equal anchors must resolve deterministically, got %q", got)
	}
}

// coveringTree is a station with an anchored Albums folder workspace (a
// group) and an anchored Label folder workspace (a leaf) inside Albums.
func coveringTree(t *testing.T) (map[string]*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Albums/Album-1", "Albums/Label/Song", "Outside/Song"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	anchored := func(id, kind, parent, path string) *Workspace {
		ws := &Workspace{ID: id, Kind: kind, Status: StatusActive, ParentID: parent, SharedData: map[string]any{AnchorDirectoryReferenceIDKey: id + "-anchor"}}
		if err := ws.AddDirectoryReference(DirectoryReference{ID: id + "-anchor", Name: id, Path: path}); err != nil {
			t.Fatal(err)
		}
		return ws
	}
	tree := map[string]*Workspace{
		"home":    {ID: "home", Kind: "group", Status: StatusActive, AssistantProgramState: &AssistantProgramState{}},
		"albums":  anchored("albums", "group", "home", filepath.Join(root, "Albums")),
		"label":   anchored("label", "", "albums", filepath.Join(root, "Albums", "Label")),
		"outside": anchored("outside", "group", "", filepath.Join(root, "Outside")),
	}
	return tree, root
}

func coveringID(tree map[string]*Workspace, folder string) string {
	list := make([]*Workspace, 0, len(tree))
	for _, ws := range tree {
		list = append(list, ws)
	}
	if ws := CoveringFolderWorkspace(list, LookupInWorkspaceMap(tree), "home", folder); ws != nil {
		return ws.ID
	}
	return ""
}

func TestCoveringFolderWorkspace(t *testing.T) {
	tree, root := coveringTree(t)
	cases := []struct {
		name, folder, want string
	}{
		{"song inside a folder workspace", filepath.Join(root, "Albums", "Album-1"), "albums"},
		{"deepest anchor wins, even a leaf", filepath.Join(root, "Albums", "Label", "Song"), "label"},
		{"the folder workspace's own folder maps to the station", filepath.Join(root, "Albums"), ""},
		{"a folder workspace outside the station never covers", filepath.Join(root, "Outside", "Song"), ""},
		{"an empty folder maps to the station", "", ""},
		{"a relative folder maps to the station", "Albums/Album-1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coveringID(tree, tc.folder); got != tc.want {
				t.Fatalf("covering = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoveringFolderWorkspace_SkipsWhatCannotHoldSongs(t *testing.T) {
	song := func(root string) string { return filepath.Join(root, "Albums", "Album-1") }
	for name, change := range map[string]func(map[string]*Workspace){
		"trashed": func(m map[string]*Workspace) { m["albums"].Status = StatusTrashed },
		"a linked song": func(m map[string]*Workspace) {
			m["albums"].AssistantProjectLink = &AssistantProjectLink{StationWorkspaceID: "home"}
		},
		"another station": func(m map[string]*Workspace) { m["albums"].AssistantProgramState = &AssistantProgramState{} },
		"not anchored":    func(m map[string]*Workspace) { delete(m["albums"].SharedData, AnchorDirectoryReferenceIDKey) },
		"moved out":       func(m map[string]*Workspace) { m["albums"].ParentID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			tree, root := coveringTree(t)
			change(tree)
			if got := coveringID(tree, song(root)); got != "" {
				t.Fatalf("covering = %q, want the station", got)
			}
		})
	}
}

func TestCoveringFolderWorkspace_ResolvesSymlinkedFolders(t *testing.T) {
	tree, root := coveringTree(t)
	link := filepath.Join(t.TempDir(), "music-link")
	if err := os.Symlink(filepath.Join(root, "Albums"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := coveringID(tree, filepath.Join(link, "Album-1")); got != "albums" {
		t.Fatalf("a song reached through a symlink = %q, want albums", got)
	}
}
