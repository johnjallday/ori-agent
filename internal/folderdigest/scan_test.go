package folderdigest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScan_RootStatsAndCandidates(t *testing.T) {
	root := materializeTree(t, "mixed")
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rootCand := result.RootCandidate()
	if !rootCand.IsRoot || rootCand.Name != "mixed" {
		t.Fatalf("root candidate = %+v", rootCand)
	}
	if rootCand.LooseFiles != 40 {
		t.Errorf("loose files = %d, want 40", rootCand.LooseFiles)
	}
	if rootCand.LooseKinds() != 6 {
		t.Errorf("loose kinds = %d, want 6", rootCand.LooseKinds())
	}
	// 40 loose + 6 thesis + 5 website + 4 album + 5 scans.
	if rootCand.FileCount != 60 {
		t.Errorf("root file count = %d, want 60", rootCand.FileCount)
	}
	if rootCand.HasMarker() {
		t.Errorf("root should carry no marker, got %+v", rootCand.Marker)
	}

	subs := result.Subfolders()
	names := make([]string, 0, len(subs))
	for _, c := range subs {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "Album,Scans,Thesis,website" {
		t.Fatalf("subfolders = %s", got)
	}
	byName := map[string]Candidate{}
	for _, c := range subs {
		byName[c.Name] = c
	}
	thesis := byName["Thesis"]
	if thesis.Marker == nil || thesis.Marker.Shape != ShapeManuscript {
		t.Errorf("Thesis marker = %+v, want manuscript", thesis.Marker)
	}
	if thesis.FileCount != 6 || thesis.DominantExtension != ".tex" {
		t.Errorf("Thesis stats = %+v", thesis)
	}
	if thesis.RelPath != "Thesis" || thesis.Path != filepath.Join(root, "Thesis") {
		t.Errorf("Thesis paths = %q / %q", thesis.RelPath, thesis.Path)
	}
	if byName["website"].Marker == nil || byName["website"].Marker.Shape != ShapeCode {
		t.Errorf("website marker = %+v", byName["website"].Marker)
	}
	if byName["Album"].Marker == nil || byName["Album"].Marker.Shape != ShapeAudio {
		t.Errorf("Album marker = %+v", byName["Album"].Marker)
	}
	if byName["Scans"].HasMarker() {
		t.Errorf("Scans should carry no marker")
	}
	if result.Partial {
		t.Errorf("unexpected partial result: %s", result.PartialReason)
	}
}

func TestScan_HiddenMarkersCountButHiddenFilesDoNot(t *testing.T) {
	root := materializeTree(t, "code")
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := result.RootCandidate()
	if c.Marker == nil || c.Marker.Name != ".git" {
		t.Fatalf("marker = %+v, want .git", c.Marker)
	}
	// .git/HEAD is never counted: the directory is hidden and not descended.
	if c.FileCount != 12 {
		t.Errorf("file count = %d, want 12", c.FileCount)
	}
	if c.DominantExtension != ".go" || c.DominantCount != 10 {
		t.Errorf("dominant = %s x%d", c.DominantExtension, c.DominantCount)
	}
}

func TestScan_DoesNotFollowSymlinks(t *testing.T) {
	root := materializeTree(t, "symlink")
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 30 {
		if err := os.WriteFile(filepath.Join(outside, "leak"+string(rune('a'+i%6))+".bin"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedLinks != 1 {
		t.Errorf("skipped links = %d, want 1", result.SkippedLinks)
	}
	if got := result.RootCandidate().FileCount; got != 3 {
		t.Errorf("file count = %d, want 3 (link not followed)", got)
	}
	for _, c := range result.Subfolders() {
		if c.Name == "link" {
			t.Errorf("symlink became a candidate")
		}
	}
	v := Decide(result, fixtureNow())
	if !strings.HasSuffix(v.Reason, ", 1 skipped link") {
		t.Errorf("reason = %q, want skipped-link suffix", v.Reason)
	}
}

func TestScan_SkipsCloudPlaceholders(t *testing.T) {
	root := materializeTree(t, "icloud")
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.RootCandidate().FileCount; got != 2 {
		t.Errorf("file count = %d, want 2", got)
	}
}

func TestScan_StopsAtDepthThree(t *testing.T) {
	root := materializeTree(t, "depth")
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.RootCandidate().FileCount; got != 3 {
		t.Errorf("file count = %d, want 3", got)
	}
	if result.Partial {
		t.Errorf("depth limit must not mark the scan partial")
	}
}

func TestScan_SkipsToolingAndSystemFolders(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node_modules/pkg", "Library/Caches", "Trash", "src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"node_modules/pkg/index.js", "Library/Caches/blob", "Trash/old.txt", "src/main.js", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.RootCandidate().FileCount; got != 2 {
		t.Errorf("file count = %d, want 2 (only src/main.js and README.md)", got)
	}
	for _, c := range result.Subfolders() {
		if skippedFolders[c.Name] {
			t.Errorf("%s became a candidate", c.Name)
		}
	}
}

func TestScan_EntryCapMarksPartial(t *testing.T) {
	root := makeOverflowTree(t, DefaultMaxEntries+1500)
	result, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || result.PartialReason != PartialEntries {
		t.Fatalf("partial = %v (%s), want entries cap", result.Partial, result.PartialReason)
	}
	if result.Entries > DefaultMaxEntries+1 {
		t.Errorf("examined %d entries, cap is %d", result.Entries, DefaultMaxEntries)
	}
	v := Decide(result, fixtureNow())
	if !strings.HasSuffix(v.Reason, "(partial look)") {
		t.Errorf("reason = %q, want partial suffix", v.Reason)
	}
}

func TestScan_TimeBudgetMarksPartial(t *testing.T) {
	root := materializeTree(t, "mixed")
	base := time.Now()
	calls := 0
	clock := func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * 2 * time.Second)
	}
	result, err := Scan(root, Options{Budget: 3 * time.Second, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || result.PartialReason != PartialTime {
		t.Fatalf("partial = %v (%s), want time budget", result.Partial, result.PartialReason)
	}
}

func TestScan_RefusesNonDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(file, Options{}); err != ErrNotDirectory {
		t.Errorf("err = %v, want ErrNotDirectory", err)
	}
	if _, err := Scan(filepath.Join(t.TempDir(), "missing"), Options{}); err == nil {
		t.Errorf("missing root should fail")
	}
}

// TestScan_NeverOpensFiles is the FR47 proof: with every file unreadable the
// verdict and its reason are unchanged, so the scan can only have looked at
// metadata.
func TestScan_NeverOpensFiles(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores file modes")
	}
	trees := []string{"manuscript", "corpus", "records", "code", "reaper", "dump", "mixed", "empty", "ambiguous", "icloud"}
	now := fixtureNow()
	for _, name := range trees {
		t.Run(name, func(t *testing.T) {
			root := materializeTree(t, name)
			before, err := Scan(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			want := Decide(before, now)

			makeUnreadable(t, root)
			after, err := Scan(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := Decide(after, now)
			if got.Kind != want.Kind || got.Reason != want.Reason {
				t.Errorf("unreadable tree changed the verdict: %s %q -> %s %q", want.Kind, want.Reason, got.Kind, got.Reason)
			}
			if after.RootCandidate().FileCount != before.RootCandidate().FileCount {
				t.Errorf("file count changed: %d -> %d", before.RootCandidate().FileCount, after.RootCandidate().FileCount)
			}
		})
	}
}
