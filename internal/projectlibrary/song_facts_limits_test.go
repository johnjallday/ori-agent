package projectlibrary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// connectedRootAt connects a library root at dir (its folders already written)
// on a fresh Home that agreed to song details.
func connectedRootAt(t *testing.T, dir string, consent bool) (*Roots, Scope, *workspace.FileStore, Root) {
	t.Helper()
	r, scope, file, picker := rootTestService(t)
	picker.path, _ = filepath.EvalSymlinks(dir)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "connect-"+filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	if consent {
		grantSongDetails(t, file, scope)
	}
	return r, scope, file, root
}

// writeSong makes one song folder holding one project file.
func writeSong(t *testing.T, dir, name, file string, content []byte, saved time.Time) string {
	t.Helper()
	folder := filepath.Join(dir, name)
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, file)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	setSaved(t, path, saved)
	return path
}

func TestSongFacts_UnreadableFilesGiveNoFactsAndNoError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	writeSong(t, dir, "Good", "Good.rpp", []byte(rppProject("92", nil, [][][2]string{{{"0", "60"}}})), saved)
	writeSong(t, dir, "Not REAPER", "Song.rpp", []byte("<Ableton MajorVersion=\"5\">\n"), saved)
	writeSong(t, dir, "Binary", "Song.rpp", []byte("<REAPER_PROJECT 0.1\n\x00\x01\x02\n>\n"), saved)
	writeSong(t, dir, "Long Line", "Song.rpp",
		[]byte("<REAPER_PROJECT 0.1\n  NOTES "+strings.Repeat("x", DefaultSongFactsLimits.MaxLineBytes+1)+"\n>\n"), saved)
	writeSong(t, dir, "Empty", "Song.rpp", nil, saved)
	huge := writeSong(t, dir, "Huge", "Song.rpp", []byte(rppProject("92", nil, nil)), saved)
	if err := os.Truncate(huge, DefaultSongFactsLimits.MaxBytes+1); err != nil { // sparse: no disk used
		t.Fatal(err)
	}
	setSaved(t, huge, saved)
	r, scope, _, root := connectedRootAt(t, dir, true)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	if scan := scanRoot(t, r, scope, root.ID, "unreadable"); scan.Status != "complete" {
		t.Fatalf("an unreadable file failed the scan: %+v", scan)
	}
	if got := opens.take(); slices.Contains(got, filepath.Join("Huge", "Song.rpp")) {
		t.Fatalf("a file over the cap was opened: %v", got)
	}
	page, err := r.library.Query(scope, Search{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		switch {
		case row.Name == "Good" && row.Facts == nil:
			t.Fatal("the readable song lost its facts")
		case row.Name != "Good" && row.Facts != nil:
			t.Fatalf("%s shows facts %+v", row.Name, row.Facts)
		case row.LastSavedAt == nil:
			t.Fatalf("%s lost its save time", row.Name)
		}
	}
	// The refused files are remembered; only the oversized one is skipped
	// unopened again, so a rescan opens nothing.
	scanRoot(t, r, scope, root.ID, "unreadable-again")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("a rescan opened unchanged files: %v", got)
	}
}

func TestSongFacts_FilesThatMoveUnderThePassAreReadNextScan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{"re-saved", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(rppProject("140", nil, nil)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced", func(t *testing.T, path string) {
			next := path + ".next"
			if err := os.WriteFile(next, []byte(rppProject("92", nil, [][][2]string{{{"0", "60"}}})), 0o600); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			setSaved(t, next, info.ModTime())
			if err := os.Rename(next, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"deleted", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"swapped for a symlink", func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(filepath.Dir(path)), "elsewhere.rpp")
			if err := os.WriteFile(target, []byte(rppProject("92", nil, nil)), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "Songs")
			saved := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
			path := writeSong(t, dir, "Song", "Song.rpp", []byte(rppProject("92", nil, [][][2]string{{{"0", "60"}}})), saved)
			r, scope, _, root := connectedRootAt(t, dir, true)
			changed := false
			opens := &openCounter{before: func(string, string) {
				if !changed {
					changed = true
					tc.change(t, path)
				}
			}}
			r.factsOpened = opens.hook
			if scan := scanRoot(t, r, scope, root.ID, "moving"); scan.Status != "complete" {
				t.Fatalf("scan: %+v", scan)
			}
			if facts := factsAt(t, r, scope, root.ID, "Song"); facts != nil {
				t.Fatalf("facts from a file that moved under the read: %+v", facts)
			}
			if tc.name == "deleted" || tc.name == "swapped for a symlink" {
				return // the next scan lists no such project file
			}
			scanRoot(t, r, scope, root.ID, "after-move")
			if facts := factsAt(t, r, scope, root.ID, "Song"); facts == nil {
				t.Fatal("the next scan did not read the changed file")
			}
		})
	}
}

func TestSongFacts_SymlinksAndBackupsAreNeverOpened(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	older := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	real := writeSong(t, dir, "Song", "Take A.rpp", []byte(rppProject("92", nil, nil)), older)
	if err := os.WriteFile(filepath.Join(dir, "Song", "Take A.rpp-bak"), []byte(rppProject("150", nil, nil)), 0o600); err != nil {
		t.Fatal(err)
	}
	setSaved(t, filepath.Join(dir, "Song", "Take A.rpp-bak"), newer)
	if err := os.Symlink(real, filepath.Join(dir, "Song", "Link.rpp")); err != nil {
		t.Fatal(err)
	}
	r, scope, _, root := connectedRootAt(t, dir, true)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "links")
	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Song", "Take A.rpp")}) {
		t.Fatalf("opened %v, want only the real project file", got)
	}
	if facts := factsAt(t, r, scope, root.ID, "Song"); facts == nil || facts.TempoBPM != 92 {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestSongFacts_BudgetStopsThePassAndTheNextScanReadsTheRest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	content := []byte(rppProject("92", nil, [][][2]string{{{"0", "60"}}}))
	for i := 0; i < 5; i++ {
		writeSong(t, dir, fmt.Sprintf("Song %d", i), "Song.rpp", content, base.Add(time.Duration(i)*time.Hour))
	}
	r, scope, _, root := connectedRootAt(t, dir, true)
	r.factsBudget = songFactsBudget{Duration: time.Minute, Bytes: int64(2 * len(content))}
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "budget-1")
	first := opens.take()
	if !slices.Equal(first, []string{filepath.Join("Song 3", "Song.rpp"), filepath.Join("Song 4", "Song.rpp")}) {
		t.Fatalf("first pass opened %v, want the two most recently saved", first)
	}
	scanRoot(t, r, scope, root.ID, "budget-2")
	if second := opens.take(); len(second) != 2 || slices.Contains(second, first[0]) || slices.Contains(second, first[1]) {
		t.Fatalf("second pass opened %v, want two songs it had not read", second)
	}
	scanRoot(t, r, scope, root.ID, "budget-3")
	if third := opens.take(); !slices.Equal(third, []string{filepath.Join("Song 0", "Song.rpp")}) {
		t.Fatalf("third pass opened %v, want the last one", third)
	}
	scanRoot(t, r, scope, root.ID, "budget-4")
	if fourth := opens.take(); len(fourth) != 0 {
		t.Fatalf("every song read, yet a pass opened %v", fourth)
	}
}

// A realistic project: plugin state as wrapped base64 lines, so the file is
// about size bytes.
func sizedProject(i, size int) []byte {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<REAPER_PROJECT 0.1 \"7.27/macOS-arm64\" 1727800000\n  TEMPO %d 4 4\n", 80+i%90))
	for track := 0; b.Len() < size; track++ {
		b.WriteString("  <TRACK\n    <FXCHAIN\n      <VST \"VST: ReaComp\" reacomp.vst 0 \"\" 1\n")
		for line := 0; line < 40; line++ {
			b.WriteString("        " + strings.Repeat("QUJDREVGR0hJSktMTU5PUA", 6) + "\n")
		}
		b.WriteString("      >\n    >\n    <ITEM\n      POSITION 0\n      LENGTH 180\n    >\n  >\n")
	}
	b.WriteString(">\n")
	return []byte(b.String())
}

func TestSongFacts_TwoHundredSongsFitTheBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("measures a 200-song pass")
	}
	dir := filepath.Join(t.TempDir(), "Songs")
	base := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		writeSong(t, dir, fmt.Sprintf("Song %03d", i), "Song.rpp", sizedProject(i, 256<<10), base.Add(time.Duration(i)*time.Minute))
	}
	r, scope, _, root := connectedRootAt(t, dir, true)
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	scan, _, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "two-hundred")
	elapsed := time.Since(started)
	if err != nil || scan.Status != "complete" {
		t.Fatalf("scan: %+v %v", scan, err)
	}
	doc, err = r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	withFacts := 0
	for _, entry := range doc.Entries {
		if entry.Observations[0].Facts != nil && !entry.Observations[0].Facts.empty() {
			withFacts++
		}
	}
	t.Logf("200 songs × 256 KiB: scan with fact pass %v, %d with facts", elapsed, withFacts)
	if withFacts < 190 {
		t.Fatalf("only %d of 200 songs got facts in one pass", withFacts)
	}
}

func TestSongFacts_AnOlderHomeOpensNothingAndKeepsTodaysTurn(t *testing.T) {
	f := newRunFixture(t) // a Home built before song details: no consent record
	f.roots.factsOpened = (&openCounter{before: func(relative, file string) {
		t.Errorf("an older Home opened %s/%s", relative, file)
	}}).hook
	factsMusicTree(t, f.tree)
	doc := f.doc(t)
	review, err := f.roots.ReviewScan(f.scope, doc.Roots[0].ID, doc.Revision)
	if err != nil || review.ReadsSongFacts || review.Scope != ScanScopeNamesOnly {
		t.Fatalf("an older Home's scan review: %+v %v", review, err)
	}
	rescan, _, err := f.roots.CommitScan(context.Background(), f.scope, doc.Roots[0].ID, review.Token, "older-rescan")
	if err != nil {
		t.Fatal(err)
	}
	f.runner = f.newBriefRunner(f.library)
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, rescan.ID)
	if err != nil || run.Mode != "" || f.chat.requests[0].SystemPrompt != managerRunSystemPrompt {
		t.Fatalf("an older Home's turn changed: %+v %v", run, err)
	}
	if state, err := f.library.SongDetailsState(f.scope); err != nil || state != workspace.SongDetailsNone {
		t.Fatalf("an older Home has a switch: %q %v", state, err)
	}
	if storedFacts(t, f.file, f.scope) != 0 || f.doc(t).CollectionBrief != nil {
		t.Fatal("an older Home stored facts or a brief")
	}
}

func TestSongFacts_AnAbletonCollectionGetsNoFactsButABrief(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Songs")
	base := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		writeSong(t, dir, fmt.Sprintf("Set %d", i), "Set.als", []byte("ableton"), base.Add(time.Duration(i)*time.Hour))
	}
	r, scope, file, root := connectedRootAt(t, dir, true)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scan := scanRoot(t, r, scope, root.ID, "ableton")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("an Ableton set was opened: %v", got)
	}
	if storedFacts(t, file, scope) != 0 {
		t.Fatal("Ableton sets got facts")
	}
	bindTestManager(t, file, scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole},
		[]workspace.AssistantRoleBinding{managerBinding})
	f := &runFixture{scope: scope, file: file, library: r.library, chat: &scriptedChat{}}
	f.forbidden = &atomic.Bool{}
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		saveBrief("Three Ableton sets were saved this week."),
	}
	f.library = NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	run, err := f.newBriefRunner(f.library).Run(context.Background(), scope.HomeID, scan.ID)
	if err != nil || run.Status != runFinished || run.Mode != RunModeBrief {
		t.Fatalf("run: %+v %v", run, err)
	}
	user := f.chat.requests[0].Messages[0].Content
	if !strings.Contains(user, `"songs":3`) || !strings.Contains(user, `"songs_with_facts":0`) ||
		!strings.Contains(user, `"saved_in_last_30_days":3`) {
		t.Fatalf("the brief prompt lacks counts and save dates: %s", user)
	}
	if brief := f.doc(t).CollectionBrief; brief == nil {
		t.Fatal("an Ableton collection got no brief")
	}
}

func TestSongFacts_SourceFilesAreUnchangedAcrossScanAndRescan(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	before := treeHashes(t, tree)
	scanRoot(t, r, scope, root.ID, "hash-1")
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), rppProject("100", nil, nil), time.Now().UTC().Truncate(time.Second))
	before[filepath.Join(tree.single, "Song.rpp")] = fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	scanRoot(t, r, scope, root.ID, "hash-2")
	scanRoot(t, r, scope, root.ID, "hash-3")
	after := treeHashes(t, tree)
	if len(after) != len(before) {
		t.Fatalf("files added or removed: %d → %d", len(before), len(after))
	}
	for path, hash := range before {
		if after[path] != hash {
			t.Fatalf("%s changed", path)
		}
	}
}
