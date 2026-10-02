package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

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

// openCounter is the opener seam: every project file the pass opens.
type openCounter struct {
	mu     sync.Mutex
	opened []string
	before func(relative, file string)
}

func (c *openCounter) hook(relative, file string) {
	c.mu.Lock()
	c.opened = append(c.opened, filepath.Join(relative, file))
	before := c.before
	c.mu.Unlock()
	if before != nil {
		before(relative, file)
	}
}

func (c *openCounter) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	opened := c.opened
	c.opened = nil
	slices.Sort(opened)
	return opened
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
	r, scope, file, tree, root := connectedMusicRoot(t)
	saved := factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	published := &factsAtPublish{store: r.library, scope: scope}
	r.library.WithEventBus(published)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	before := treeHashes(t, tree)

	if scan := scanRoot(t, r, scope, root.ID, "facts-scan"); scan.Status != "complete" {
		t.Fatalf("scan: %+v", scan)
	}
	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Alternates", "Take B.RPP"), filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("opened %v, want only each song's dated REAPER file", got)
	}
	single := factsAt(t, r, scope, root.ID, "Single")
	if single == nil || single.SongFacts != (SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 2}) ||
		single.ReadFrom.File != "Song.rpp" || !single.ReadFrom.ModifiedAt.Equal(saved) || single.ReadFrom.Identity == "" {
		t.Fatalf("Single facts = %+v", single)
	}
	alternates := factsAt(t, r, scope, root.ID, "Alternates")
	if alternates == nil || alternates.SongFacts != (SongFacts{TempoBPM: 96, TempoVaries: true, LengthSeconds: 75, TrackCount: 3}) ||
		alternates.ReadFrom.File != "Take B.RPP" {
		t.Fatalf("Alternates facts = %+v", alternates)
	}
	for _, other := range []string{"Ableton", "Logic"} {
		if facts := factsAt(t, r, scope, root.ID, other); facts != nil {
			t.Fatalf("%s is not a REAPER song but has facts %+v", other, facts)
		}
	}
	if events := published.all(); len(events) != 1 || !events[0] {
		t.Fatalf("events %v: want one event, published after the facts were saved", events)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	keys := 0
	for _, op := range doc.Operations {
		if op.Action == "scan_facts" && op.Key == doc.Scans[len(doc.Scans)-1].ID+":facts" {
			keys++
		}
	}
	if keys != 1 {
		t.Fatalf("facts were not saved in their own fenced write: %+v", doc.Operations)
	}
	if after := treeHashes(t, tree); len(after) != len(before) {
		t.Fatalf("the scan added or removed files: %d → %d", len(before), len(after))
	} else {
		for path, hash := range before {
			if after[path] != hash {
				t.Fatalf("%s changed", path)
			}
		}
	}
}

func TestSongFacts_AnUnchangedRescanKeepsFactsAndOpensNothing(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	saved := factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "first")
	first := factsAt(t, r, scope, root.ID, "Single")
	opens.take()

	scanRoot(t, r, scope, root.ID, "unchanged")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("an unchanged rescan opened %v", got)
	}
	if kept := factsAt(t, r, scope, root.ID, "Single"); kept == nil || *kept != *first {
		t.Fatalf("facts after an unchanged rescan = %+v, want %+v", kept, first)
	}

	// A re-saved song is read again on the same scan, and only that song.
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"),
		rppProject("140", nil, [][][2]string{{{"0", "90"}}}), saved.Add(time.Hour))
	scanRoot(t, r, scope, root.ID, "resaved")
	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("after a re-save opened %v", got)
	}
	if updated := factsAt(t, r, scope, root.ID, "Single"); updated == nil ||
		updated.SongFacts != (SongFacts{TempoBPM: 140, LengthSeconds: 90, TrackCount: 1}) {
		t.Fatalf("facts after a re-save = %+v", updated)
	}
}

// finishWithoutPass records one scan through finishScan, which reconciles but
// runs no fact pass, so carry-over is visible on its own.
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
	r, scope, file, tree, root := connectedMusicRoot(t)
	saved := factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "facts")
	if factsAt(t, r, scope, root.ID, "Single") == nil || factsAt(t, r, scope, root.ID, "Alternates") == nil {
		t.Fatal("fixture has no facts")
	}

	// Unchanged: carried over by reconciliation alone.
	finishWithoutPass(t, r, scope, root, "carry")
	if factsAt(t, r, scope, root.ID, "Single") == nil || factsAt(t, r, scope, root.ID, "Alternates") == nil {
		t.Fatal("unchanged songs lost their facts")
	}

	// Re-saved: dropped until the next pass reads the new file.
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), rppProject("92", nil, nil), saved.Add(time.Hour))
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
	scanRoot(t, r, scope, root.ID, "reread")
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

func TestSongFacts_AHomeWithoutConsentOpensNoFile(t *testing.T) {
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
			r, scope, file, tree, root := connectedMusicRoot(t)
			factsMusicTree(t, tree)
			tc.setup(t, file, scope)
			events := &eventRecorder{}
			r.library.WithEventBus(events)
			opens := &openCounter{}
			r.factsOpened = opens.hook
			scanRoot(t, r, scope, root.ID, "scan")
			scanRoot(t, r, scope, root.ID, "rescan")
			if got := opens.take(); len(got) != 0 {
				t.Fatalf("opened %v", got)
			}
			if len(events.all()) != 2 {
				t.Fatalf("events = %d, want one per scan", len(events.all()))
			}
			if count := storedFacts(t, file, scope); count != 0 {
				t.Fatalf("a Home without consent stored %d facts", count)
			}
		})
	}
}

func TestSongFacts_AReplayedCommitRunsNoPassAndPublishesNothing(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	events := &eventRecorder{}
	r.library.WithEventBus(events)
	opens := &openCounter{}
	r.factsOpened = opens.hook
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
	if len(opens.take()) != 2 || len(events.all()) != 1 {
		t.Fatal("first commit did not read and publish")
	}
	if _, replay, err := r.CommitScan(context.Background(), scope, root.ID, review.Token, "once"); err != nil || !replay {
		t.Fatalf("retry: %v %v", replay, err)
	}
	if got := opens.take(); len(got) != 0 || len(events.all()) != 1 {
		t.Fatalf("a replay opened %v and published %d events", got, len(events.all()))
	}
}

func TestSongFacts_AStoppedPassKeepsTheListingAndStillPublishes(t *testing.T) {
	t.Run("out of time", func(t *testing.T) {
		r, scope, file, tree, root := connectedMusicRoot(t)
		factsMusicTree(t, tree)
		grantSongDetails(t, file, scope)
		events := &eventRecorder{}
		r.library.WithEventBus(events)
		r.factsBudget = songFactsBudget{Duration: time.Nanosecond, Bytes: 1 << 20}
		scan := scanRoot(t, r, scope, root.ID, "late")
		if scan.Status != "complete" || len(events.all()) != 1 {
			t.Fatalf("scan %+v, events %d", scan, len(events.all()))
		}
		doc, err := r.library.Read(scope)
		if err != nil || len(doc.Entries) != 4 {
			t.Fatalf("listing: %d entries %v", len(doc.Entries), err)
		}
		if facts := factsAt(t, r, scope, root.ID, "Single"); facts != nil {
			t.Fatalf("a pass out of time saved %+v", facts)
		}
	})
	t.Run("switched off during the pass", func(t *testing.T) {
		r, scope, file, tree, root := connectedMusicRoot(t)
		factsMusicTree(t, tree)
		grantSongDetails(t, file, scope)
		scanRoot(t, r, scope, root.ID, "facts")
		older := factsAt(t, r, scope, root.ID, "Alternates")
		writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), rppProject("70", nil, nil), time.Now().UTC().Add(time.Hour).Truncate(time.Second))
		events := &eventRecorder{}
		r.library.WithEventBus(events)
		opens := &openCounter{before: func(string, string) { switchSongDetailsOff(t, file, scope) }}
		r.factsOpened = opens.hook
		scanRoot(t, r, scope, root.ID, "off-mid-pass")
		if got := opens.take(); len(got) != 1 || len(events.all()) != 1 {
			t.Fatalf("opened %v, events %d", got, len(events.all()))
		}
		if facts := factsAt(t, r, scope, root.ID, "Single"); facts != nil {
			t.Fatalf("a batch read while the switch went off was saved: %+v", facts)
		}
		if kept := factsAt(t, r, scope, root.ID, "Alternates"); kept == nil || *kept != *older {
			t.Fatalf("older facts were not left intact: %+v", kept)
		}
	})
	t.Run("cancelled request", func(t *testing.T) {
		r, scope, file, tree, root := connectedMusicRoot(t)
		factsMusicTree(t, tree)
		grantSongDetails(t, file, scope)
		events := &eventRecorder{}
		r.library.WithEventBus(events)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := runningScanRecord(t, r, scope, root, "cancel-start")
		observed, err := r.Discover(ctx, scope, root.ID)
		if err != nil {
			t.Fatal(err)
		}
		finished, completed, err := r.recordScanFinish(scope, started, observed, "complete", "")
		if err != nil || completed == nil {
			t.Fatalf("finish: %+v %v", finished, err)
		}
		cancel()
		if outcome := r.readSongFactsAfterScan(ctx, scope, finished, observed, root); outcome.Stopped != "cancelled" || outcome.Opened != 0 {
			t.Fatalf("a cancelled pass: %+v", outcome)
		}
	})
}

func TestSongFacts_ContentWithoutFactsIsRememberedUntilTheFileChanges(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	grantSongDetails(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	// The fixture's REAPER files are plain text, not projects.
	scanRoot(t, r, scope, root.ID, "first")
	if got := opens.take(); len(got) != 2 {
		t.Fatalf("opened %v", got)
	}
	if facts := factsAt(t, r, scope, root.ID, "Single"); facts == nil || !facts.empty() {
		t.Fatalf("unreadable content facts = %+v, want an empty record", facts)
	}
	scanRoot(t, r, scope, root.ID, "again")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("an unchanged unreadable file was opened again: %v", got)
	}
	page, err := r.library.Query(scope, Search{})
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
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "facts")
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
			f["read_from"].(map[string]any)["size"] = DefaultSongFactsLimits.MaxBytes + 1
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

	r, scope, file, tree, root := connectedMusicRoot(t)
	saved := factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "facts")
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), rppProject("150", nil, nil), saved.Add(time.Hour))
	scanRoot(t, r, scope, root.ID, "new-facts")
	doc, err := r.library.Read(scope)
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
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	store := r.library
	for _, enabled := range []bool{true, false} {
		if _, err := store.SetSongDetails(scope, enabled); !errors.Is(err, ErrSongDetailsNotGranted) {
			t.Fatalf("a Home without consent, enabled=%v: %v", enabled, err)
		}
	}
	grantSongDetails(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "facts")
	opens.take()
	before, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}

	// Off: the switch and every stored fact go in one Home write.
	if state, err := store.SetSongDetails(scope, false); err != nil || state != workspace.SongDetailsOff {
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
	if state, err := store.SetSongDetails(scope, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off again: %q %v", state, err)
	}
	if again, _ := store.Read(scope); again.Revision != after.Revision {
		t.Fatalf("off again moved the revision %d→%d", after.Revision, again.Revision)
	}
	// Off means off: the next scan opens nothing.
	scanRoot(t, r, scope, root.ID, "while-off")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("a scan with the switch off opened %v", got)
	}

	// On: no scan starts, no facts until the next scan, then they come back.
	if state, err := store.SetSongDetails(scope, true); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on: %q %v", state, err)
	}
	if got := opens.take(); len(got) != 0 || factsAt(t, r, scope, root.ID, "Single") != nil {
		t.Fatalf("turning on read files (%v) or showed facts", got)
	}
	if state, err := store.SetSongDetails(scope, true); err != nil || state != workspace.SongDetailsOn {
		t.Fatalf("on again: %q %v", state, err)
	}
	scanRoot(t, r, scope, root.ID, "back-on")
	if got := opens.take(); len(got) != 2 || factsAt(t, r, scope, root.ID, "Single") == nil {
		t.Fatalf("after turning on, the next scan opened %v", got)
	}
}

func TestSetSongDetails_AReadOnlyHomeCanTurnOffButNotOn(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "facts")
	readOnly := NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return false })
	if state, err := readOnly.SetSongDetails(scope, false); err != nil || state != workspace.SongDetailsOff {
		t.Fatalf("off on a read-only Home: %q %v", state, err)
	}
	if _, err := readOnly.SetSongDetails(scope, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("on on a read-only Home: %v", err)
	}
	if state, err := readOnly.SongDetailsState(scope); err != nil || state != workspace.SongDetailsOff {
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
