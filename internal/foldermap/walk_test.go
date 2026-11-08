package foldermap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

var fixtureTime = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

// build writes files (relative, slash-separated) under a temp root; a path
// ending in "/" is an empty folder.
func build(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(full, 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, fixtureTime, fixtureTime); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// demoLayout is ~/Desktop/Ori Music Demo as John fed it: seven REAPER song
// folders (Album-5 holds two sessions), one Logic Pro bundle and an empty
// Filed folder, under Albums/ and Picker Songs/.
func demoLayout(t *testing.T) string {
	return build(t,
		"Albums/Album-1/Song.rpp",
		"Albums/Album-2/Song.rpp",
		"Albums/Album-3/Song.rpp",
		"Albums/Album-3/Song.rpp-bak",
		"Albums/Album-4/Song.rpp",
		"Albums/Album-5/Song.rpp",
		"Albums/Album-5/Alternate.rpp",
		"Albums/Logic Sketch/Sketch.logicx/ProjectData",
		"Albums/Logic Sketch/Sketch.logicx/Resources/info.plist",
		"Picker Songs/Track-1/Song.rpp",
		"Picker Songs/Track-2/Song.rpp",
		"Filed/",
	)
}

func walk(t *testing.T, root string) (*Map, Facts) {
	t.Helper()
	m, err := Walk(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m, FactsFrom(m)
}

func TestWalk_DemoLayoutFindsEveryNestedSong(t *testing.T) {
	m, facts := walk(t, demoLayout(t))
	if facts.RootProject != nil {
		t.Fatalf("the fed folder is not itself a project: %+v", facts.RootProject)
	}
	want := []ShapeCount{
		{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 7},
		{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 1},
	}
	if !reflect.DeepEqual(facts.Shapes, want) {
		t.Fatalf("shapes = %+v, want %+v", facts.Shapes, want)
	}
	if got := facts.ShapeTotals(); len(got) != 1 || got[0].Count != 8 {
		t.Fatalf("audio total = %+v, want 8", got)
	}
	if facts.Portfolio == nil || facts.Portfolio.Shape != folderdigest.ShapeAudio || facts.Portfolio.Count != 8 ||
		facts.Portfolio.HomeProviderKey != "music_project_management" || facts.Portfolio.HomeName != "Music Production Home" {
		t.Fatalf("portfolio = %+v", facts.Portfolio)
	}
	if !reflect.DeepEqual(facts.ProjectAreas, []string{"Albums", "Picker Songs"}) {
		t.Fatalf("areas = %v", facts.ProjectAreas)
	}
	if !reflect.DeepEqual(facts.EmptyFolders, []string{"Filed"}) {
		t.Fatalf("empty = %v", facts.EmptyFolders)
	}
	if facts.Stopped != "" || len(facts.Unopened) != 0 {
		t.Fatalf("walk should finish: stopped=%q unopened=%v", facts.Stopped, facts.Unopened)
	}
	album5, _ := m.Folder("Albums/Album-5")
	if album5.State != StateClosed || album5.MarkerFiles != 2 || album5.Marker != "*.rpp" {
		t.Fatalf("Album-5 = %+v", album5)
	}
	if !facts.Newest.Equal(fixtureTime) {
		t.Fatalf("newest = %v", facts.Newest)
	}
}

func TestWalk_LogicBundleIsClosedAsOneProject(t *testing.T) {
	m, facts := walk(t, demoLayout(t))
	sketch, ok := m.Folder("Albums/Logic Sketch")
	if !ok || sketch.State != StateClosed || sketch.Format != "logic" {
		t.Fatalf("Logic Sketch = %+v", sketch)
	}
	for _, f := range m.Folders {
		if strings.Contains(f.Path, ".logicx") {
			t.Fatalf("the bundle's insides were recorded: %q", f.Path)
		}
	}
	logic := 0
	for _, p := range facts.Members {
		if p.Format == "logic" {
			logic++
		}
	}
	if logic != 1 {
		t.Fatalf("logic projects = %d", logic)
	}
}

func TestWalk_DepthSixLayoutHasNoDepthLimit(t *testing.T) {
	root := build(t,
		"Label 2026/2025/Artist A/Album X/Disc 1/Song One/Song One.rpp",
		"Label 2026/2025/Artist A/Album X/Disc 1/Song Two/Song Two.rpp",
		"Label 2026/2026/Artist B/Album Y/Disc 2/Song Three/Song Three.rpp",
		"Label 2026/2026/Artist B/Album Y/Disc 2/Song Four/Song Four.rpp",
		"Label 2026/2026/Artist C/EP Z/Side A/Song Five/Song Five.rpp",
		"Label 2026/2026/Artist C/EP Z/Side A/Song Six/Song Six.als",
	)
	_, facts := walk(t, root)
	if len(facts.Members) != 6 {
		t.Fatalf("members = %d, want 6: %+v", len(facts.Members), facts.Members)
	}
	for _, p := range facts.Members {
		if p.Depth != 6 {
			t.Fatalf("%s depth = %d, want 6", p.Path, p.Depth)
		}
	}
	if facts.Portfolio == nil || facts.Portfolio.Count != 6 {
		t.Fatalf("portfolio = %+v", facts.Portfolio)
	}
}

func TestWalk_BudgetTripRecordsUnopenedFolders(t *testing.T) {
	var paths []string
	for i := range 30 {
		for j := range 10 {
			paths = append(paths, fmt.Sprintf("Box %02d/Item %02d/file.txt", i, j))
		}
	}
	root := build(t, paths...)
	m, err := Walk(root, Options{MaxEntries: 50})
	if err != nil {
		t.Fatal(err)
	}
	facts := FactsFrom(m)
	if facts.Stopped != StopEntries {
		t.Fatalf("stopped = %q", facts.Stopped)
	}
	if len(facts.Unopened) == 0 {
		t.Fatal("a tripped walk must record the folders it did not open")
	}
	if m.Entries < 50 || m.Entries > 50+10 {
		t.Fatalf("entries = %d: the walk may finish only the folder it was listing", m.Entries)
	}
	// Continue opens the rest under a fresh budget, user-named folders first.
	first := facts.Unopened[len(facts.Unopened)-1]
	if err := Continue(root, m, []string{first}, Options{MaxEntries: 5}); err != nil {
		t.Fatal(err)
	}
	if f, _ := m.Folder(first); f.State != StateWalked {
		t.Fatalf("named folder %q was not opened first: %+v", first, f)
	}
	if err := Continue(root, m, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := FactsFrom(m); got.Stopped != "" || len(got.Unopened) != 0 || got.Folders != 1+30+300 {
		t.Fatalf("after continuing: stopped=%q unopened=%d folders=%d", got.Stopped, len(got.Unopened), got.Folders)
	}
	if m.Walks != 3 {
		t.Fatalf("walks = %d", m.Walks)
	}
}

func TestWalk_TimeBudgetStopsTheWalk(t *testing.T) {
	root := demoLayout(t)
	clock := fixtureTime
	m, err := Walk(root, Options{Budget: time.Second, Now: func() time.Time {
		clock = clock.Add(600 * time.Millisecond)
		return clock
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Stopped != StopTime || len(m.Unopened()) == 0 {
		t.Fatalf("stopped=%q unopened=%v", m.Stopped, m.Unopened())
	}
}

func TestWalk_LargeFolderIsRecordedNotOpened(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 5,001 files")
	}
	root := t.TempDir()
	big := filepath.Join(root, "Exports")
	if err := os.MkdirAll(big, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= LargeFolderEntries; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f%05d.wav", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, facts := walk(t, root)
	if f, _ := m.Folder("Exports"); f.State != StateLarge || f.Files != 0 {
		t.Fatalf("Exports = %+v", f)
	}
	if !reflect.DeepEqual(facts.Large, []string{"Exports"}) || m.Entries != 1 {
		t.Fatalf("large=%v entries=%d", facts.Large, m.Entries)
	}
}

func TestWalk_UnreadableFilesGiveTheSameMap(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	root := demoLayout(t)
	readable, err := Walk(root, Options{Now: func() time.Time { return fixtureTime }})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	for _, p := range files {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, p := range files {
			_ = os.Chmod(p, 0o600)
		}
	})
	unreadable, err := Walk(root, Options{Now: func() time.Time { return fixtureTime }})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(readable)
	b, _ := json.Marshal(unreadable)
	if string(a) != string(b) {
		t.Fatalf("maps differ:\nreadable   %s\nunreadable %s", a, b)
	}
}

func TestWalk_NeverFollowsSymlinks(t *testing.T) {
	outside := build(t, "Secret Songs/A/Song.rpp", "Secret Songs/B/Song.rpp")
	root := build(t, "Mine/Song.rpp")
	if err := os.Symlink(filepath.Join(outside, "Secret Songs"), filepath.Join(root, "Linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "Secret Songs", "A", "Song.rpp"), filepath.Join(root, "Linked.rpp")); err != nil {
		t.Fatal(err)
	}
	m, facts := walk(t, root)
	if facts.RootProject != nil {
		t.Fatal("a linked marker must not make the fed folder a project")
	}
	if len(facts.Members) != 1 || facts.Members[0].Path != "Mine" {
		t.Fatalf("members = %+v", facts.Members)
	}
	if _, ok := m.Folder("Linked"); ok || m.SkippedLinks != 2 {
		t.Fatalf("links followed or not counted: skipped=%d", m.SkippedLinks)
	}
	// The fed folder itself may not be a link.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Walk(link, Options{}); err != ErrNotDirectory {
		t.Fatalf("walk of a linked root = %v", err)
	}
	// A folder replaced by a link after it was recorded is not opened either.
	m2, err := Walk(root, Options{MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "Mine")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "Secret Songs"), filepath.Join(root, "Mine")); err != nil {
		t.Fatal(err)
	}
	if err := Continue(root, m2, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if f, _ := m2.Folder("Mine"); f.State != StateUnreadable {
		t.Fatalf("swapped-in link opened: %+v", f)
	}
}

func TestWalk_SkipsHiddenToolingAndCloudPlaceholders(t *testing.T) {
	root := build(t,
		"node_modules/x/Song.rpp",
		"Library/Song.rpp",
		".hidden/Song.rpp",
		"Track.rpp.icloud",
		"Real/Song.rpp",
	)
	m, facts := walk(t, root)
	if len(facts.Members) != 1 || facts.Members[0].Path != "Real" {
		t.Fatalf("members = %+v", facts.Members)
	}
	if m.SkippedFolders != 2 || m.SkippedHidden != 2 {
		t.Fatalf("skipped folders=%d hidden=%d", m.SkippedFolders, m.SkippedHidden)
	}
}

func TestWalk_HiddenMarkerStillMarksAProject(t *testing.T) {
	root := build(t, "repo/.git/HEAD", "repo/src/main.go", "repo/src/deep/also.go")
	m, facts := walk(t, root)
	if len(facts.Members) != 1 || facts.Members[0].Shape != folderdigest.ShapeCode {
		t.Fatalf("members = %+v", facts.Members)
	}
	if _, ok := m.Folder("repo/src"); ok {
		t.Fatal("a closed project's subfolders are not walked")
	}
}

func TestWalk_ProjectAtTheFedFolderIsTheRootProject(t *testing.T) {
	root := build(t, "Song.rpp", "Audio/kick.wav", "Audio/Old/Other.rpp")
	m, facts := walk(t, root)
	if facts.RootProject == nil || facts.RootProject.Format != "reaper" || len(facts.Members) != 0 {
		t.Fatalf("root=%+v members=%+v", facts.RootProject, facts.Members)
	}
	if len(m.Folders) != 1 {
		t.Fatalf("the closed root's subfolders were recorded: %+v", m.Folders)
	}
}

func TestFacts_DumpUsesTheDigestThresholds(t *testing.T) {
	var paths []string
	kinds := []string{"pdf", "jpg", "zip", "docx", "dmg"}
	for i := range DumpMinLooseFiles {
		paths = append(paths, fmt.Sprintf("file%02d.%s", i, kinds[i%len(kinds)]))
	}
	_, facts := walk(t, build(t, paths...))
	if !facts.Dump || facts.RootLooseFiles != DumpMinLooseFiles || facts.RootLooseKinds != DumpMinKinds {
		t.Fatalf("dump=%v files=%d kinds=%d", facts.Dump, facts.RootLooseFiles, facts.RootLooseKinds)
	}
	if facts.Recognized() {
		t.Fatal("a dump holds no project")
	}
	_, few := walk(t, build(t, paths[:DumpMinLooseFiles-1]...))
	if few.Dump {
		t.Fatal("one file short of the threshold is not a dump")
	}
}

func TestFacts_PortfolioNeedsAHomeProviderShape(t *testing.T) {
	var paths []string
	for i := range PortfolioMinProjects + 1 {
		paths = append(paths, fmt.Sprintf("Repos/r%d/go.mod", i))
	}
	_, facts := walk(t, build(t, paths...))
	if facts.Portfolio != nil {
		t.Fatalf("code has no Home provider: %+v", facts.Portfolio)
	}
	var songs []string
	for i := range PortfolioMinProjects - 1 {
		songs = append(songs, fmt.Sprintf("s%d/Song.rpp", i))
	}
	if _, f := walk(t, build(t, songs...)); f.Portfolio != nil {
		t.Fatal("four songs are not a portfolio")
	}
}

func TestCleanRel_RefusesEscapes(t *testing.T) {
	for _, bad := range []string{"/etc", "..", "../x", "a/../../b", "a//b", "a/./b", `a\b`, "./a"} {
		if _, err := CleanRel(bad); err == nil {
			t.Errorf("CleanRel(%q) accepted", bad)
		}
	}
	for in, want := range map[string]string{"": "", ".": "", "Albums": "Albums", "Albums/Album-1/": "Albums/Album-1"} {
		if got, err := CleanRel(in); err != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v", in, got, err)
		}
	}
}

func TestMap_ValidateAndRoundTrip(t *testing.T) {
	m, _ := walk(t, demoLayout(t))
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), os.TempDir()) || strings.Contains(string(raw), "\"/") {
		t.Fatalf("the map holds an absolute path: %s", raw)
	}
	var back Map
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	if f, ok := back.Folder("Albums/Album-1"); !ok || f.State != StateClosed {
		t.Fatalf("index after round trip: %+v %v", f, ok)
	}
	back.Folders = append(back.Folders, Folder{Path: "../escape"})
	if err := back.Validate(); err == nil {
		t.Fatal("an escaping path must not validate")
	}
}
