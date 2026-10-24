package folderdigest

import (
	"testing"
	"time"
)

func scanTree(t *testing.T, name string) Result {
	t.Helper()
	result, err := Scan(materializeTree(t, name), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIsInbox_ByVerdictBelowCollectionsAndKnownProjects(t *testing.T) {
	cases := []struct {
		tree string
		want bool
	}{
		// What a Downloads, Desktop or Documents folder usually is.
		{tree: "dump", want: true},
		{tree: "ambiguous", want: true},
		{tree: "empty", want: true},
		// Thirty PDFs are not a project by density: a records folder is an inbox.
		{tree: "records", want: true},
		// Several projects side by side are a collection, set up as a group.
		{tree: "mixed", want: false},
		// One project of a kind Ori knows keeps its own card.
		{tree: "code", want: false},
		{tree: "reaper", want: false},
		{tree: "manuscript", want: false},
		{tree: "corpus", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.tree, func(t *testing.T) {
			if got := IsInbox(scanTree(t, tc.tree), fixtureNow(), nil); got != tc.want {
				t.Fatalf("IsInbox = %t, want %t (verdict %s)", got, tc.want, Decide(scanTree(t, tc.tree), fixtureNow()).Kind)
			}
		})
	}
}

func TestIsInbox_ProjectByDensityOfNoKnownKind(t *testing.T) {
	now := time.Now()
	root := Candidate{
		Name: "Exports", IsRoot: true, FileCount: 12, LooseFiles: 12,
		DominantExtension: ".mov", DominantShare: 1, NewestModTime: now.Add(-time.Hour),
		LooseExtensions: map[string]int{".mov": 12},
	}
	result := Result{Candidates: []Candidate{root}}
	if v := Decide(result, now); v.Kind != KindProject {
		t.Fatalf("fixture is not a density project: %s", v.Kind)
	}
	if !IsInbox(result, now, nil) {
		t.Fatal("a project only by the density of files of no known kind is an inbox folder")
	}
	// The same density in a kind Ori names is a project, not an inbox.
	root.DominantExtension, root.LooseExtensions = ".tex", map[string]int{".tex": 12}
	if IsInbox(Result{Candidates: []Candidate{root}}, now, nil) {
		t.Fatal("a manuscript by density is a recognized project")
	}
}

func TestIsInbox_DeclinedRootIsNeverAnInbox(t *testing.T) {
	result := scanTree(t, "dump")
	declined := func(c Candidate) bool { return c.IsRoot }
	if IsInbox(result, fixtureNow(), declined) {
		t.Fatal("a folder the user said never to ask about again stays unrecognized")
	}
}

func TestInboxCapability_IsKeyedByVerdictNotMarker(t *testing.T) {
	row, ok := InboxCapability()
	if !ok || row.Shape != ShapeInbox || row.Builtin == nil || row.Builtin.Recipe != BuiltinInboxFolder || row.Builtin.Version != 1 {
		t.Fatalf("inbox row = %+v ok=%t", row, ok)
	}
	b := row.Builtin
	if b.DisplayName != "inbox folder" || b.Workspace != "File Janitor workspace" ||
		b.Question != "Want File Curator to keep this folder tidy?" || b.AcceptLabel != "Review setup" || b.DeclineLabel != "No thanks" {
		t.Fatalf("inbox copy = %+v", b)
	}
	if row.Blueprint.BlueprintID != "file-janitor" || row.Blueprint.Label != "File Janitor" || row.Offer != nil {
		t.Fatalf("inbox blueprint = %+v offer=%v", row.Blueprint, row.Offer)
	}
	if len(row.Markers) != 0 || len(row.Tools) != 0 {
		t.Fatal("no file name may select the inbox row")
	}
	if _, ok := BuiltinCapabilityFor(ShapeInbox, ""); ok {
		t.Fatal("the marker-keyed lookup must not find the inbox row")
	}
	if blueprint, ok := BlueprintForShape(ShapeInbox); !ok || blueprint.BlueprintID != "file-janitor" {
		t.Fatalf("inbox blueprint by shape = %+v ok=%t", blueprint, ok)
	}
	row.Builtin.Recipe = "forged"
	if again, _ := InboxCapability(); again.Builtin.Recipe != BuiltinInboxFolder {
		t.Fatal("caller mutated the host table")
	}
}
