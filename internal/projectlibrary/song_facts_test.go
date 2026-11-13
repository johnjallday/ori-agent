package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// What the library does with the song facts an installed app plugin's reader
// returns. The host reads no project file; these tests use fileReader, a stand-in
// for the plugin over the fixture projects (song_fixture_reader_test.go).

// grantSongDetails records the consent a folder card's Set up gives a new Home.
func grantSongDetails(t *testing.T, store workspace.Store, scope Scope) {
	t.Helper()
	if err := workspace.NewSongDetailsConsents(store).Grant(scope.HomeID, "offer-1"); err != nil {
		t.Fatal(err)
	}
}

// switchSongDetailsOff sets the consent's revoked_at, as the Home's switch does.
func switchSongDetailsOff(t *testing.T, store workspace.Store, scope Scope) {
	t.Helper()
	if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		now := time.Now().UTC()
		state.SongDetailsConsent.RevokedAt = &now
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func writeSongProject(t *testing.T, path, content string, saved time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	setSaved(t, path, saved)
}

// factsMusicTree replaces the fixture's REAPER files with real projects:
// Single is 92 BPM, 2 tracks, 3:41; Alternates is dated by Take B (96 BPM with
// tempo changes); Take A is older and must never be read.
func factsMusicTree(t *testing.T, tree musicTree) time.Time {
	t.Helper()
	saved := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"),
		rppProject("92", nil, [][][2]string{{{"0", "200"}, {"200", "21"}}, {}}), saved)
	writeSongProject(t, filepath.Join(tree.alternate, "Take A.rpp"),
		rppProject("120", nil, [][][2]string{{{"0", "30"}}}), saved.Add(-48*time.Hour))
	writeSongProject(t, filepath.Join(tree.alternate, "Take B.RPP"),
		rppProject("96", []string{"0 96 1", "30 100 1"}, [][][2]string{{{"0", "60"}}, {{"10", "65"}}, {}}), saved.Add(-time.Hour))
	return saved
}

// factsAtPublish records, for each published scan, whether the Home already
// held song facts when the event went out.
type factsAtPublish struct {
	store  *Store
	scope  Scope
	mu     sync.Mutex
	events []bool
}

func (p *factsAtPublish) Publish(workspace.Event) {
	doc, err := p.store.Read(p.scope)
	held := false
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			held = held || (observation.Facts != nil && !observation.Facts.empty())
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, err == nil && held)
}

func (p *factsAtPublish) all() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.events...)
}

func factsAt(t *testing.T, r *Roots, scope Scope, rootID, relative string) *ObservedFacts {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	_, observation, ok := observationAt(doc, rootID, relative)
	if !ok {
		t.Fatalf("no usable observation of %q", relative)
	}
	return observation.Facts
}

// storedFacts counts the observations whose stored record holds facts, from
// the Home's decoded library document.
func storedFacts(t *testing.T, store workspace.Store, scope Scope) int {
	t.Helper()
	home, err := store.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeDocument(home.GetAssistantProgramState().ProjectLibrary, scope)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range doc.Entries {
		for _, observation := range entry.Observations {
			if observation.Facts != nil {
				count++
			}
		}
	}
	return count
}

func treeHashes(t *testing.T, tree musicTree) map[string][32]byte {
	t.Helper()
	hashes := map[string][32]byte{}
	if err := filepath.WalkDir(tree.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		hashes[path] = fileDigest(t, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return hashes
}

func TestSongFacts_AConsentingHomeReadsFactsBeforeTheScanIsPublished(t *testing.T) {
	f, reader := newFileReadFixture(t)
	before := treeHashes(t, f.tree)

	if scan := f.scan(t, "facts-scan"); scan.Status != "complete" {
		t.Fatalf("scan: %+v", scan)
	}
	if got := reader.take(); !slices.Equal(got, []string{filepath.Join("Alternates", "Take B.RPP"), filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("the plugin was asked to read %v, want only each song's dated REAPER file", got)
	}
	single := factsAt(t, f.r, f.scope, f.root.ID, "Single")
	if single == nil || single.SongFacts != (SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 2}) ||
		single.ReadFrom.File != "Song.rpp" || !single.ReadFrom.ModifiedAt.Equal(f.saved) || single.ReadFrom.Identity == "" {
		t.Fatalf("Single facts = %+v", single)
	}
	alternates := factsAt(t, f.r, f.scope, f.root.ID, "Alternates")
	if alternates == nil || alternates.SongFacts != (SongFacts{TempoBPM: 96, TempoVaries: true, LengthSeconds: 75, TrackCount: 3}) ||
		alternates.ReadFrom.File != "Take B.RPP" {
		t.Fatalf("Alternates facts = %+v", alternates)
	}
	for _, other := range []string{"Ableton", "Logic"} {
		if facts := factsAt(t, f.r, f.scope, f.root.ID, other); facts != nil {
			t.Fatalf("%s is not a REAPER song but has facts %+v", other, facts)
		}
	}
	if events := f.published.all(); len(events) != 1 || !events[0] {
		t.Fatalf("events %v: want one event, published after the facts were saved", events)
	}
	doc, err := f.r.library.Read(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	keys := 0
	for _, op := range doc.Operations {
		if op.Action == "scan_facts" && strings.HasPrefix(op.Key, doc.Scans[len(doc.Scans)-1].ID+":facts") {
			keys++
		}
	}
	if keys != 1 {
		t.Fatalf("facts were not saved in their own fenced write: %+v", doc.Operations)
	}
	if after := treeHashes(t, f.tree); len(after) != len(before) {
		t.Fatalf("the scan added or removed files: %d → %d", len(before), len(after))
	} else {
		for path, hash := range before {
			if after[path] != hash {
				t.Fatalf("%s changed", path)
			}
		}
	}
}

func TestSongFacts_AnUnchangedRescanKeepsFactsAndAsksForNothing(t *testing.T) {
	f, reader := newFileReadFixture(t)
	f.scan(t, "first")
	first := factsAt(t, f.r, f.scope, f.root.ID, "Single")
	reader.take()

	f.scan(t, "unchanged")
	if got := reader.take(); len(got) != 0 {
		t.Fatalf("an unchanged rescan asked the plugin to read %v", got)
	}
	if kept := factsAt(t, f.r, f.scope, f.root.ID, "Single"); kept == nil || *kept != *first {
		t.Fatalf("facts after an unchanged rescan = %+v, want %+v", kept, first)
	}

	// A re-saved song is read again on the same scan, and only that song.
	writeSongProject(t, filepath.Join(f.tree.single, "Song.rpp"),
		rppProject("140", nil, [][][2]string{{{"0", "90"}}}), f.saved.Add(time.Hour))
	f.scan(t, "resaved")
	if got := reader.take(); !slices.Equal(got, []string{filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("after a re-save the plugin was asked to read %v", got)
	}
	if updated := factsAt(t, f.r, f.scope, f.root.ID, "Single"); updated == nil ||
		updated.SongFacts != (SongFacts{TempoBPM: 140, LengthSeconds: 90, TrackCount: 1}) {
		t.Fatalf("facts after a re-save = %+v", updated)
	}
}

// finishWithoutPass records one scan through finishScan, which reconciles but
// starts no read, so carry-over is visible on its own.
func finishWithoutPass(t *testing.T, r *Roots, scope Scope, root Root, key string) {
	t.Helper()
	started := runningScanRecord(t, r, scope, root, key)
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished, err := r.finishScan(scope, started, observed, "complete", ""); err != nil || finished.Status != "complete" {
		t.Fatalf("finish: %+v %v", finished, err)
	}
}

func TestReconcile_FactsStayOnlyWhileTheDatedFileIsUnchanged(t *testing.T) {
	f, _ := newFileReadFixture(t)
	r, scope, root, tree := f.r, f.scope, f.root, f.tree
	f.scan(t, "facts")
	if factsAt(t, r, scope, root.ID, "Single") == nil || factsAt(t, r, scope, root.ID, "Alternates") == nil {
		t.Fatal("fixture has no facts")
	}

	// Unchanged: carried over by reconciliation alone.
	finishWithoutPass(t, r, scope, root, "carry")
	if factsAt(t, r, scope, root.ID, "Single") == nil || factsAt(t, r, scope, root.ID, "Alternates") == nil {
		t.Fatal("unchanged songs lost their facts")
	}

	// Re-saved: dropped until the next read picks up the new file.
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), rppProject("92", nil, nil), f.saved.Add(time.Hour))
	// The dated file is gone: the older take dates the song now, and its facts
	// were never read.
	if err := os.Remove(filepath.Join(tree.alternate, "Take B.RPP")); err != nil {
		t.Fatal(err)
	}
	finishWithoutPass(t, r, scope, root, "changed")
	if facts := factsAt(t, r, scope, root.ID, "Single"); facts != nil {
		t.Fatalf("a re-saved song kept old facts: %+v", facts)
	}
	if facts := factsAt(t, r, scope, root.ID, "Alternates"); facts != nil {
		t.Fatalf("a song dated by another file kept old facts: %+v", facts)
	}

	// An appended observation (a second root over the same folder) starts
	// without facts.
	f.scan(t, "reread")
	picker := r.picker.(*testRootPicker)
	var err error
	if picker.path, err = filepath.EvalSymlinks(tree.single); err != nil {
		t.Fatal(err)
	}
	token, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, token, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	inner, _, err := r.Commit(scope, review.Token, "inner-root")
	if err != nil {
		t.Fatal(err)
	}
	finishWithoutPass(t, r, scope, inner, "inner")
	doc, err = r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	shared, appended, ok := observationAt(doc, inner.ID, "")
	if !ok || len(shared.Observations) != 2 || appended.Facts != nil {
		t.Fatalf("appended observation: %+v %+v", shared, appended)
	}
	if outer := observationFor(shared, root.ID); outer == nil || outer.Facts == nil {
		t.Fatalf("the first root's observation lost its facts: %+v", shared)
	}
}

func TestSongFacts_AHomeWithoutConsentAsksForNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, store workspace.Store, scope Scope)
	}{
		{"no consent record", func(*testing.T, workspace.Store, Scope) {}},
		{"switched off", func(t *testing.T, store workspace.Store, scope Scope) {
			grantSongDetails(t, store, scope)
			switchSongDetailsOff(t, store, scope)
		}},
		{"invalid record", func(t *testing.T, store workspace.Store, scope Scope) {
			if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
				state := home.GetAssistantProgramState()
				state.SongDetailsConsent = &workspace.SongDetailsConsent{Source: "forged"}
				home.SetAssistantProgramState(state)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, reader := newFileReadFixtureWithoutConsent(t)
			tc.setup(t, f.file, f.scope)
			f.scan(t, "scan")
			f.scan(t, "rescan")
			if got := reader.take(); len(got) != 0 {
				t.Fatalf("the plugin was asked to read %v", got)
			}
			if len(f.chat.requests) != 0 {
				t.Fatalf("the assistant was called %d times for a Home that did not consent", len(f.chat.requests))
			}
			if events := f.published.all(); len(events) != 2 {
				t.Fatalf("events = %d, want one per scan", len(events))
			}
			if count := storedFacts(t, f.file, f.scope); count != 0 {
				t.Fatalf("a Home without consent stored %d facts", count)
			}
		})
	}
}

func TestSongFacts_AReplayedCommitStartsNoReadAndPublishesNothing(t *testing.T) {
	f, reader := newFileReadFixture(t)
	r, scope, root := f.r, f.scope, f.root
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScan(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "once"); err != nil || replay {
		t.Fatalf("first commit: %v %v", replay, err)
	}
	f.waitEvents(t, 1)
	if got := reader.take(); len(got) != 2 {
		t.Fatalf("first commit read %v", got)
	}
	requests := len(f.chat.requests)
	if _, replay, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "once"); err != nil || !replay {
		t.Fatalf("retry: %v %v", replay, err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := reader.take(); len(got) != 0 || len(f.published.all()) != 1 || len(f.chat.requests) != requests {
		t.Fatalf("a replay read %v, published %d events and made %d calls", got, len(f.published.all()), len(f.chat.requests)-requests)
	}
}

func TestSongFacts_ContentWithoutFactsIsRememberedUntilTheFileChanges(t *testing.T) {
	f, reader := newFileReadFixture(t)
	// The fixture's REAPER files are plain text here, not projects.
	writeSongProject(t, filepath.Join(f.tree.single, "Song.rpp"), "not a project\n", f.saved)
	writeSongProject(t, filepath.Join(f.tree.alternate, "Take B.RPP"), "not a project either\n", f.saved.Add(-time.Hour))
	f.scan(t, "first")
	if got := reader.take(); len(got) != 2 {
		t.Fatalf("the plugin was asked to read %v", got)
	}
	if facts := factsAt(t, f.r, f.scope, f.root.ID, "Single"); facts == nil || !facts.empty() || facts.NotRead != NotReadUnreadable {
		t.Fatalf("unreadable content facts = %+v, want an empty record that says why", facts)
	}
	f.scan(t, "again")
	if got := reader.take(); len(got) != 0 {
		t.Fatalf("an unchanged unreadable file was read again: %v", got)
	}
	page, err := f.r.library.Query(f.scope, Search{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Facts != nil {
			t.Fatalf("an empty record shows facts: %+v", row)
		}
	}
}

func TestStore_ForgedFactsMakeTheDocumentInvalid(t *testing.T) {
	f, _ := newFileReadFixture(t)
	scope, file := f.scope, f.file
	f.scan(t, "facts")
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	stored := home.GetAssistantProgramState().ProjectLibrary
	if _, err := decodeDocument(stored, scope); err != nil {
		t.Fatalf("the stored document does not decode: %v", err)
	}
	forge := func(edit func(facts map[string]any, observation map[string]any)) json.RawMessage {
		decoder := json.NewDecoder(bytes.NewReader(stored))
		decoder.UseNumber()
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, entry := range raw["entries"].([]any) {
			for _, observation := range entry.(map[string]any)["observations"].([]any) {
				o := observation.(map[string]any)
				if facts, ok := o["facts"].(map[string]any); ok && o["relative_folder"] == "Single" {
					edit(facts, o)
				}
			}
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	for name, edit := range map[string]func(map[string]any, map[string]any){
		"tempo out of range":     func(f, _ map[string]any) { f["tempo_bpm"] = 5000 },
		"negative length":        func(f, _ map[string]any) { f["length_seconds"] = -1 },
		"too many tracks":        func(f, _ map[string]any) { f["track_count"] = maxSongTrackCount + 1 },
		"varies without a tempo": func(f, _ map[string]any) { delete(f, "tempo_bpm"); f["tempo_varies"] = true },
		"a file not listed":      func(f, _ map[string]any) { f["read_from"].(map[string]any)["file"] = "Other.rpp" },
		"a path":                 func(f, _ map[string]any) { f["read_from"].(map[string]any)["file"] = "../Song.rpp" },
		"not a REAPER file": func(f, o map[string]any) {
			o["alternates"] = []any{"Song.als"}
			f["read_from"].(map[string]any)["file"] = "Song.als"
		},
		"another format": func(_, o map[string]any) { o["format"] = "ableton" },
		"over the size cap": func(f, _ map[string]any) {
			f["read_from"].(map[string]any)["size"] = maxSongFileBytes + 1
		},
		"no save time":    func(f, _ map[string]any) { delete(f["read_from"].(map[string]any), "modified_at") },
		"no identity":     func(f, _ map[string]any) { f["read_from"].(map[string]any)["identity"] = "" },
		"an unknown fact": func(f, _ map[string]any) { f["key"] = "C minor" },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeDocument(forge(edit), scope); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("forged facts decoded: %v", err)
			}
		})
	}
}

func TestStore_AHomeWithoutFactsStillLoads(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	scanRoot(t, r, scope, root.ID, "no-consent")
	if count := storedFacts(t, file, scope); count != 0 {
		t.Fatalf("a Home without consent wrote %d facts", count)
	}
	doc, err := NewStore(file).Read(scope)
	if err != nil || len(doc.Entries) != 4 {
		t.Fatalf("a Home without facts: %d entries %v", len(doc.Entries), err)
	}
}

func TestDigest_FactsAreNotAChange(t *testing.T) {
	before := Observation{RootID: "root", RelativeFolder: "Song", FileIdentity: "1:2", Format: "reaper",
		Alternates: []string{"Song.rpp"}, Availability: "available"}
	after := before
	after.Facts = &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92}, ReadFrom: FactsSource{File: "Song.rpp", Identity: "3:4",
		ModifiedAt: time.Now()}}
	if observationChanged(before, after) || observationChanged(after, before) {
		t.Fatal("facts alone counted as a changed song")
	}

	f, _ := newFileReadFixture(t)
	f.scan(t, "facts")
	writeSongProject(t, filepath.Join(f.tree.single, "Song.rpp"), rppProject("150", nil, nil), f.saved.Add(time.Hour))
	f.scan(t, "new-facts")
	doc, err := f.r.library.Read(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Digest == nil || doc.Digest.Updated != 0 || doc.Digest.New != 0 {
		t.Fatalf("new facts counted as an updated song: %+v", doc.Digest)
	}
}

// seedFactsLibrary is seedLastSavedLibrary with facts on every observation:
// each source's tempo is its day offset + 100, so a row shows which source its
// facts came from.
func seedFactsLibrary(t *testing.T) (*Store, Scope) {
	t.Helper()
	s, scope, _ := seedLastSavedLibrary(t)
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.mutate(scope, doc.Revision, operation{key: "facts-fixture", action: "seed", digest: "fixture"},
		func(current *Document) (string, error) {
			for i := range current.Entries {
				for j := range current.Entries[i].Observations {
					o := &current.Entries[i].Observations[j]
					saved := o.FileModifiedAt
					if saved.IsZero() {
						saved = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
					}
					o.Facts = &ObservedFacts{SongFacts: SongFacts{TempoBPM: float64(100 + saved.Day()), TrackCount: 4},
						ReadFrom: FactsSource{File: "Song.rpp", Identity: "file:" + o.RootID, Size: 10, ModifiedAt: saved}}
				}
			}
			return "facts-fixture", nil
		}); err != nil {
		t.Fatal(err)
	}
	grantSongDetails(t, s.workspaces, scope)
	return s, scope
}

func TestQuery_FactsComeFromTheSourceThatDatesTheSong(t *testing.T) {
	s, scope := seedFactsLibrary(t)
	page, err := s.Query(scope, Search{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		switch {
		case row.LastSavedAt == nil && row.Facts != nil:
			t.Fatalf("%s has no usable dated source but shows facts %+v", row.ID, row.Facts)
		case row.LastSavedAt != nil && (row.Facts == nil || row.Facts.TempoBPM != float64(100+row.LastSavedAt.Day())):
			t.Fatalf("%s facts %+v did not come from the source saved %v", row.ID, row.Facts, row.LastSavedAt)
		}
	}
	detail, err := s.Detail(scope, "revoked-newer")
	if err != nil {
		t.Fatal(err)
	}
	usable := 0
	for _, source := range detail.Sources {
		switch source.Availability {
		case "available", "ambiguous":
			usable++
			if source.Facts == nil {
				t.Fatalf("a usable source has no facts: %+v", source)
			}
		default:
			if source.Facts != nil {
				t.Fatalf("a %s source shows facts: %+v", source.Availability, source)
			}
		}
	}
	for _, id := range []string{"unavailable-newer", "paused-newer"} {
		detail, err := s.Detail(scope, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range detail.Sources {
			if (source.Availability == "available") != (source.Facts != nil) {
				t.Fatalf("%s: a %s source shows facts %+v", id, source.Availability, source.Facts)
			}
		}
	}
	if usable != 1 {
		t.Fatalf("revoked-newer has %d usable sources", usable)
	}
	encoded, err := json.Marshal(page.Rows[0])
	if err != nil || !bytes.Contains(encoded, []byte(`"facts":{"tempo_bpm":`)) || bytes.Contains(encoded, []byte("read_from")) {
		t.Fatalf("a row's facts on the wire: %s %v", encoded, err)
	}

	// The switch off hides facts everywhere, even before they are cleared.
	switchSongDetailsOff(t, s.workspaces, scope)
	page, err = s.Query(scope, Search{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Facts != nil {
			t.Fatalf("switched off, %s shows %+v", row.ID, row.Facts)
		}
	}
	if detail, err := s.Detail(scope, "two-sources"); err != nil || detail.Row.Facts != nil || detail.Sources[0].Facts != nil {
		t.Fatalf("switched off, detail shows facts: %+v %v", detail, err)
	}
}

func TestSetSongDetails_OffClearsFactsAndOnReadsThemOnTheNextScan(t *testing.T) {
	f, reader := newFileReadFixtureWithoutConsent(t)
	r, scope, file, root := f.r, f.scope, f.file, f.root
	store := r.library
	for _, enabled := range []bool{true, false} {
		if _, err := store.SetSongDetails(scope, enabled, false); !errors.Is(err, ErrSongDetailsNotGranted) {
			t.Fatalf("a Home without consent, enabled=%v: %v", enabled, err)
		}
	}
	grantSongDetails(t, file, scope)
	f.scan(t, "facts")
	reader.take()
	before, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}

	// Off: the switch and every stored fact go in one Home write.
	if state, err := store.SetSongDetails(scope, false, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off: %q %v", state, err)
	}
	if count := storedFacts(t, file, scope); count != 0 {
		t.Fatalf("switching off left %d facts stored", count)
	}
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if consent := home.GetAssistantProgramState().GetSongDetailsConsent(); consent.RevokedAt == nil {
		t.Fatalf("switching off kept the consent on: %+v", consent)
	}
	after, err := store.Read(scope)
	if err != nil || after.Revision != before.Revision+1 || len(after.Entries) != len(before.Entries) {
		t.Fatalf("off: revision %d→%d, %d entries %v", before.Revision, after.Revision, len(after.Entries), err)
	}
	// Off again (a retried request) writes nothing.
	if state, err := store.SetSongDetails(scope, false, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off again: %q %v", state, err)
	}
	if again, _ := store.Read(scope); again.Revision != after.Revision {
		t.Fatalf("off again moved the revision %d→%d", after.Revision, again.Revision)
	}
	// Off means off: the next scan asks for nothing.
	f.scan(t, "while-off")
	if got := reader.take(); len(got) != 0 {
		t.Fatalf("a scan with the switch off asked the plugin to read %v", got)
	}

	// On: no scan starts, no facts until the next scan, then they come back.
	if state, err := store.SetSongDetails(scope, true, false); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on: %q %v", state, err)
	}
	if got := reader.take(); len(got) != 0 || factsAt(t, r, scope, root.ID, "Single") != nil {
		t.Fatalf("turning on read files (%v) or showed facts", got)
	}
	if state, err := store.SetSongDetails(scope, true, false); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on again: %q %v", state, err)
	}
	f.scan(t, "back-on")
	if got := reader.take(); len(got) != 2 || factsAt(t, r, scope, root.ID, "Single") == nil {
		t.Fatalf("after turning on, the next scan asked the plugin to read %v", got)
	}
}

func TestSetSongDetails_AReadOnlyHomeCanTurnOffButNotOn(t *testing.T) {
	f, _ := newFileReadFixture(t)
	f.scan(t, "facts")
	readOnly := NewStore(f.file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return false })
	if state, err := readOnly.SetSongDetails(f.scope, false, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off on a read-only Home: %q %v", state, err)
	}
	if _, err := readOnly.SetSongDetails(f.scope, true, false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("on on a read-only Home: %v", err)
	}
	if state, err := readOnly.SongDetailsState(f.scope); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("state: %q %v", state, err)
	}
}

func TestReviewScan_SaysWhenTheScanReadsSongFacts(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	review := func() ScanReview {
		t.Helper()
		doc, err := r.library.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		disclosed, err := r.ReviewScan(scope, root.ID, doc.Revision)
		if err != nil {
			t.Fatal(err)
		}
		return disclosed
	}
	if names := review(); names.ReadsSongFacts || names.Scope != ScanScopeNamesOnly {
		t.Fatalf("a Home without consent: %+v", names)
	}
	grantSongDetails(t, file, scope)
	if facts := review(); !facts.ReadsSongFacts || facts.Scope != ScanScopeSongDetails {
		t.Fatalf("a consenting Home: %+v", facts)
	}
	// The review says what the consent reaches: once it covers names, so does the
	// review, in the same sentence the scan's disclosure is held to.
	if _, err := r.library.SetSongDetails(scope, true, true); err != nil {
		t.Fatal(err)
	}
	if names := review(); !names.ReadsSongFacts || names.Scope != ScanScopeSongNames || !ScanScopeKnown(names.Scope, true) ||
		!strings.Contains(names.Scope, "track names, markers and regions") {
		t.Fatalf("a Home that agreed to names: %+v", names)
	}
	if ScanScopeKnown(ScanScopeSongNames, false) || ScanScopeKnown(ScanScopeNamesOnly, true) || ScanScopeKnown("something else", true) {
		t.Fatal("a disclosure that does not match what the scan reads was accepted")
	}
	switchSongDetailsOff(t, file, scope)
	if off := review(); off.ReadsSongFacts || off.Scope != ScanScopeNamesOnly {
		t.Fatalf("switched off: %+v", off)
	}
}

func TestRecognizedProject_RecordsTheDatedFile(t *testing.T) {
	older := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	newer := older.Add(time.Hour)
	candidate, ok := recognizedProject("Song", "folder", []DirectoryRow{
		{Name: "Take A.rpp", Identity: "1:10", Size: 100, ModifiedAt: older},
		{Name: "Take B.rpp", Identity: "1:11", Size: 200, ModifiedAt: newer},
		{Name: "Take C.rpp-bak", Identity: "1:12", Size: 300, ModifiedAt: newer.Add(time.Hour)},
		{Name: "Link.rpp", IsLink: true, Identity: "1:13", ModifiedAt: newer.Add(time.Hour)},
	})
	want := FactsSource{File: "Take B.rpp", Identity: "1:11", Size: 200, ModifiedAt: newer}
	if !ok || candidate.DatedFile == nil || *candidate.DatedFile != want {
		t.Fatalf("dated file = %+v, want %+v", candidate.DatedFile, want)
	}
	tied, _ := recognizedProject("Song", "folder", []DirectoryRow{
		{Name: "Z.rpp", Identity: "1:20", ModifiedAt: older}, {Name: "A.rpp", Identity: "1:21", ModifiedAt: older}})
	if tied.DatedFile == nil || tied.DatedFile.File != "A.rpp" {
		t.Fatalf("an equal save time picked %+v, want the first name", tied.DatedFile)
	}
	bundle, _ := recognizedProject("Song", "folder", []DirectoryRow{
		{Name: "Song.logicx", IsDir: true, Identity: "1:30", ModifiedAt: older}})
	if bundle.DatedFile != nil {
		t.Fatalf("a bundle recorded a dated file: %+v", bundle.DatedFile)
	}
}
