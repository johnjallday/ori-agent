package folderdigest

import (
	"strings"
	"testing"
	"time"
)

func decideTree(t *testing.T, name string) Verdict {
	t.Helper()
	root := materializeTree(t, name)
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return Decide(result, fixtureNow())
}

func TestDecide_OneTreePerVerdict(t *testing.T) {
	cases := []struct {
		tree    string
		kind    Kind
		reason  string
		project string
		shape   Shape
	}{
		{tree: "manuscript", kind: KindProject, reason: "6 LaTeX files, edited yesterday", project: "manuscript", shape: ShapeManuscript},
		{tree: "corpus", kind: KindProject, reason: "12 PDF files, edited 5 days ago", project: "corpus", shape: ShapeCorpus},
		{tree: "code", kind: KindProject, reason: "10 Go files, edited yesterday", project: "code", shape: ShapeCode},
		{tree: "reaper", kind: KindProject, reason: "6 WAV files, edited yesterday", project: "reaper", shape: ShapeAudio},
		{tree: "dump", kind: KindDump, reason: "25 loose files of 6 kinds"},
		{tree: "mixed", kind: KindMixed, reason: "3 projects and 40 loose files", project: "Thesis", shape: ShapeManuscript},
		{tree: "empty", kind: KindEmpty, reason: "2 files"},
		{tree: "ambiguous", kind: KindAmbiguous, reason: "10 files of 4 kinds, edited 2 days ago"},
	}
	for _, tc := range cases {
		t.Run(tc.tree, func(t *testing.T) {
			v := decideTree(t, tc.tree)
			if v.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s (reason %q)", v.Kind, tc.kind, v.Reason)
			}
			if v.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", v.Reason, tc.reason)
			}
			if tc.project == "" {
				if v.Project != nil {
					t.Errorf("unexpected project %s", v.Project.Name)
				}
				return
			}
			if v.Project == nil || v.Project.Name != tc.project {
				t.Fatalf("project = %+v, want %s", v.Project, tc.project)
			}
			if got := ShapeFor(*v.Project); got != tc.shape {
				t.Errorf("shape = %q, want %q", got, tc.shape)
			}
		})
	}
}

func TestDecide_RecordsAreAmbiguousNotProject(t *testing.T) {
	v := decideTree(t, "records")
	if v.Kind != KindAmbiguous {
		t.Fatalf("kind = %s, want ambiguous", v.Kind)
	}
	if !strings.HasPrefix(v.Reason, "30 PDF files, last edited in ") {
		t.Errorf("reason = %q", v.Reason)
	}
	// After "It's a project" the document kind picks the corpus blueprint.
	if got := ShapeFor(v.Root); got != ShapeCorpus {
		t.Errorf("shape = %q, want corpus", got)
	}
}

func TestDecide_ReasonAlwaysPresent(t *testing.T) {
	for _, name := range []string{"manuscript", "corpus", "records", "code", "reaper", "dump", "mixed", "empty", "ambiguous", "icloud", "depth"} {
		if v := decideTree(t, name); strings.TrimSpace(v.Reason) == "" {
			t.Errorf("%s: empty reason", name)
		}
	}
}

func TestDecide_MixedRanksProjects(t *testing.T) {
	v := decideTree(t, "mixed")
	if len(v.Projects) != 3 {
		t.Fatalf("projects = %d", len(v.Projects))
	}
	order := []string{v.Projects[0].Name, v.Projects[1].Name, v.Projects[2].Name}
	if strings.Join(order, ",") != "Thesis,website,Album" {
		t.Errorf("rank order = %v", order)
	}
	if v.LooseFiles != 40 || v.LooseKinds != 6 {
		t.Errorf("loose = %d of %d kinds", v.LooseFiles, v.LooseKinds)
	}
}

func TestRank_MarkerThenRecencyThenCount(t *testing.T) {
	now := time.Now()
	marker := Markers[0]
	cands := []Candidate{
		{Name: "big-old", FileCount: 500, NewestModTime: now.Add(-72 * time.Hour)},
		{Name: "small-new", FileCount: 5, NewestModTime: now.Add(-time.Hour)},
		{Name: "marked-old", FileCount: 3, NewestModTime: now.Add(-240 * time.Hour), Marker: &marker},
		{Name: "tie-b", FileCount: 10, NewestModTime: now.Add(-48 * time.Hour)},
		{Name: "tie-a", FileCount: 10, NewestModTime: now.Add(-48 * time.Hour)},
	}
	ranked := Rank(cands)
	var names []string
	for _, c := range ranked {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "marked-old,small-new,tie-a,tie-b,big-old" {
		t.Errorf("rank = %s", got)
	}
}

func TestProjectSignal_DensityRuleAndDocumentException(t *testing.T) {
	now := time.Now()
	dense := Candidate{FileCount: 8, DominantExtension: ".go", DominantShare: 0.6, NewestModTime: now.Add(-24 * time.Hour)}
	if !projectSignal(dense, now) {
		t.Errorf("dense recent code folder should be a project signal")
	}
	stale := dense
	stale.NewestModTime = now.Add(-31 * 24 * time.Hour)
	if projectSignal(stale, now) {
		t.Errorf("folder untouched for 31 days should not be a project signal")
	}
	sparse := dense
	sparse.DominantShare = 0.5
	if projectSignal(sparse, now) {
		t.Errorf("dominant share below 60%% should not be a project signal")
	}
	docs := dense
	docs.DominantExtension = ".pdf"
	if projectSignal(docs, now) {
		t.Errorf("PDF-heavy folder must not become a project by density")
	}
	docs.Marker = &Markers[6]
	if !projectSignal(docs, now) {
		t.Errorf("a marker still makes a document folder a project")
	}
}

func TestDumpSignal_RequiresLooseFilesKindsAndNoMarker(t *testing.T) {
	root := Candidate{IsRoot: true, LooseFiles: 20, LooseExtensions: map[string]int{".a": 1, ".b": 1, ".c": 1, ".d": 1, ".e": 16}}
	if !dumpSignal(root) {
		t.Errorf("20 loose files of 5 kinds should be a dump signal")
	}
	fewKinds := root
	fewKinds.LooseExtensions = map[string]int{".a": 10, ".b": 10}
	if dumpSignal(fewKinds) {
		t.Errorf("two kinds are a collection, not a dump")
	}
	marked := root
	marked.Marker = &Markers[0]
	if dumpSignal(marked) {
		t.Errorf("a marked root is never a dump")
	}
}

func TestDescribeWhen(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"edited today":               now.Add(-2 * time.Hour),
		"edited yesterday":           now.Add(-26 * time.Hour),
		"edited 12 days ago":         now.AddDate(0, 0, -12),
		"last edited in March":       time.Date(2026, time.March, 3, 0, 0, 0, 0, time.UTC),
		"last edited in August 2024": time.Date(2024, time.August, 3, 0, 0, 0, 0, time.UTC),
	}
	for want, at := range cases {
		if got := describeWhen(at, now); got != want {
			t.Errorf("describeWhen(%s) = %q, want %q", at, got, want)
		}
	}
}

func TestKindName(t *testing.T) {
	if KindName(".tex") != "LaTeX" || KindName(".docx") != "Word" || KindName(".mov") != "video" {
		t.Errorf("known kinds mis-named")
	}
	if got := KindName(".xyz"); got != "XYZ" {
		t.Errorf("fallback = %q", got)
	}
	if got := KindName(""); got != "untyped" {
		t.Errorf("no-extension = %q", got)
	}
}
