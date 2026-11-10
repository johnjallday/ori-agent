package workspace

import "testing"

// stationTree builds Home > Albums > song plus an unrelated group outside the
// Home. Each test edits a copy of it to describe one shape.
func stationTree() map[string]*Workspace {
	link := &AssistantProjectLink{ID: "link-song", StationWorkspaceID: "home"}
	return map[string]*Workspace{
		"home":    {ID: "home", Kind: "group", Status: StatusActive, AssistantProgramState: &AssistantProgramState{}},
		"albums":  {ID: "albums", Kind: "group", Status: StatusActive, ParentID: "home"},
		"outside": {ID: "outside", Kind: "group", Status: StatusActive},
		"song":    {ID: "song", Status: StatusActive, ParentID: "albums", AssistantProjectLink: link},
	}
}

func TestWorkspaceInsideStation_Shapes(t *testing.T) {
	cases := []struct {
		name  string
		shape func(map[string]*Workspace)
		want  bool
	}{
		{"flat song under the station", func(m map[string]*Workspace) { m["song"].ParentID = "home" }, true},
		{"song nested in a group inside the station", func(map[string]*Workspace) {}, true},
		{"song two groups deep inside the station", func(m map[string]*Workspace) {
			m["label"] = &Workspace{ID: "label", Kind: "group", Status: StatusActive, ParentID: "albums"}
			m["song"].ParentID = "label"
		}, true},
		{"group outside the station", func(m map[string]*Workspace) { m["song"].ParentID = "outside" }, false},
		{"group whose chain leaves to the root", func(m map[string]*Workspace) { m["albums"].ParentID = "" }, false},
		{"top-level song", func(m map[string]*Workspace) { m["song"].ParentID = "" }, false},
		{"trashed group on the chain", func(m map[string]*Workspace) { m["albums"].Status = StatusTrashed }, false},
		{"missing group on the chain", func(m map[string]*Workspace) { m["albums"].Status = StatusMissing }, false},
		{"missing parent record", func(m map[string]*Workspace) { delete(m, "albums") }, false},
		{"parent is a leaf, not a group", func(m map[string]*Workspace) { m["albums"].Kind = "" }, false},
		{"parent is another station", func(m map[string]*Workspace) {
			m["albums"].AssistantProgramState = &AssistantProgramState{}
		}, false},
		{"parent is another linked project", func(m map[string]*Workspace) {
			m["albums"].AssistantProjectLink = &AssistantProjectLink{StationWorkspaceID: "home"}
		}, false},
		{"cycle between two groups", func(m map[string]*Workspace) {
			m["loop"] = &Workspace{ID: "loop", Kind: "group", Status: StatusActive, ParentID: "albums"}
			m["albums"].ParentID = "loop"
		}, false},
		{"group whose parent is the song itself", func(m map[string]*Workspace) { m["albums"].ParentID = "song" }, false},
		{"chain deeper than the nesting limit", func(m map[string]*Workspace) {
			parent := "home"
			for _, id := range []string{"g1", "g2", "g3", "g4", "g5", "g6"} {
				m[id] = &Workspace{ID: id, Kind: "group", Status: StatusActive, ParentID: parent}
				parent = id
			}
			m["song"].ParentID = parent
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := stationTree()
			tc.shape(tree)
			if got := WorkspaceInsideStation(LookupInWorkspaceMap(tree), tree["song"], "home"); got != tc.want {
				t.Fatalf("WorkspaceInsideStation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkspaceInsideStation_RefusesEmptyInputs(t *testing.T) {
	tree := stationTree()
	lookup := LookupInWorkspaceMap(tree)
	if WorkspaceInsideStation(lookup, nil, "home") {
		t.Fatal("a nil project is never inside a station")
	}
	if WorkspaceInsideStation(lookup, tree["song"], "") {
		t.Fatal("an empty station id is never satisfied")
	}
	if WorkspaceInsideStation(lookup, tree["home"], "home") {
		t.Fatal("the station is not inside itself")
	}
	if WorkspaceInsideStation(nil, tree["song"], "home") {
		t.Fatal("a nested parent can't be judged without a lookup")
	}
	tree["song"].ParentID = "home"
	if !WorkspaceInsideStation(nil, tree["song"], "home") {
		t.Fatal("a direct child of the station needs no lookup")
	}
}

func TestAssistantLinkHealingMove(t *testing.T) {
	tree := stationTree()
	lookup := LookupInWorkspaceMap(tree)
	song := tree["song"]
	cases := []struct {
		name          string
		current, dest string
		want          bool
	}{
		{"stray song into the station", "", "home", true},
		{"stray song into a group inside the station", "outside", "albums", true},
		{"nested song pulled out to the station", "albums", "home", false},
		{"flat song pushed into a group", "home", "albums", false},
		{"nested song moved out of the station", "albums", "outside", false},
		{"stray song moved to another stray place", "", "outside", false},
		{"flat song moved to the parent it has", "home", "home", true},
		{"nested song moved to the parent it has", "albums", "albums", true},
		{"stray song moved to the stray parent it has", "outside", "outside", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssistantLinkHealingMove(lookup, song, tc.current, tc.dest); got != tc.want {
				t.Fatalf("AssistantLinkHealingMove(%q -> %q) = %v, want %v", tc.current, tc.dest, got, tc.want)
			}
		})
	}
	if AssistantLinkHealingMove(lookup, tree["albums"], "", "home") {
		t.Fatal("a workspace without a link is never healed")
	}
}
