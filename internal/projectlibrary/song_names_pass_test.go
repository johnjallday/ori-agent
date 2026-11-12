package projectlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func grantNumbers(t *testing.T, store workspace.Store, scope Scope) {
	t.Helper()
	if err := workspace.NewSongDetailsConsents(store).GrantScope(scope.HomeID, workspace.SongDetailsConsentLearnSongs, "offer-numbers", workspace.SongDetailsScopeNumbers); err != nil {
		t.Fatal(err)
	}
}

func grantNames(t *testing.T, store workspace.Store, scope Scope) {
	t.Helper()
	if err := workspace.NewSongDetailsConsents(store).GrantScope(scope.HomeID, workspace.SongDetailsConsentLearnSongs, "offer-names", workspace.SongDetailsScopeNames); err != nil {
		t.Fatal(err)
	}
}

// namedMusicTree gives the two REAPER songs of the fixture content with names:
// Single is 92 BPM with tracks Kick, Bass and a Chorus region; Alternates is
// dated by Take B (96 BPM), whose names are different from Take A's, which is
// older and must never be read.
func namedMusicTree(t *testing.T, tree musicTree) {
	t.Helper()
	saved := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), namedProject("92", []string{
		marker(1, "0", `"Intro"`, 0),
		marker(2, "30", `"Chorus"`, 1),
		marker(2, "45", `""`, 1),
	}, []namedTrack{
		{lines: []string{nameLine("Kick")}, items: [][2]string{{"0", "221"}}},
		{lines: []string{nameLine("Bass")}},
	}), saved)
	writeSongProject(t, filepath.Join(tree.alternate, "Take A.rpp"), namedProject("120", nil, []namedTrack{
		{lines: []string{nameLine("Older take, never read")}},
	}), saved.Add(-48*time.Hour))
	writeSongProject(t, filepath.Join(tree.alternate, "Take B.RPP"), namedProject("96", []string{marker(1, "10", `"Bridge"`, 0)}, []namedTrack{
		{lines: []string{nameLine("Lead vocal")}, items: [][2]string{{"0", "75"}}},
	}), saved.Add(-time.Hour))
}

func storedFactsAt(t *testing.T, r *Roots, scope Scope, rootID, relative string) *ObservedFacts {
	t.Helper()
	return factsAt(t, r, scope, rootID, relative)
}

func TestSongFactsPass_ANumbersOnlyConsentReadsNoNames(t *testing.T) {
	for name, grant := range map[string]func(*testing.T, workspace.Store, Scope){
		"the older card's consent":    func(t *testing.T, s workspace.Store, sc Scope) { grantSongDetails(t, s, sc) },
		"the folder agent's, numbers": grantNumbers,
	} {
		t.Run(name, func(t *testing.T) {
			r, scope, file, tree, root := connectedMusicRoot(t)
			namedMusicTree(t, tree)
			grant(t, file, scope)
			scanRoot(t, r, scope, root.ID, "numbers-only")

			single := storedFactsAt(t, r, scope, root.ID, "Single")
			if single == nil || single.TempoBPM != 92 || single.TrackCount != 2 || single.LengthSeconds != 221 {
				t.Fatalf("the numbers were not read: %+v", single)
			}
			if single.Names != nil {
				t.Fatalf("a numbers-only consent stored names: %+v", single.Names)
			}
			// No name appears anywhere in the stored library.
			stored := string(storedLibrary(t, file, scope))
			for _, leaked := range []string{"Kick", "Bass", "Intro", "Chorus", "Bridge", "Lead vocal", `"names"`} {
				if strings.Contains(stored, leaked) {
					t.Fatalf("a numbers-only consent stored %q", leaked)
				}
			}
		})
	}
}

func TestSongFactsPass_ANamesConsentReadsNamesAndMarkersFromTheDatingFileOnly(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNames(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "names")

	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Alternates", "Take B.RPP"), filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("opened %v, want each song's dated file only", got)
	}
	single := storedFactsAt(t, r, scope, root.ID, "Single")
	want := &SongNames{
		Tracks:  []string{"Kick", "Bass"},
		Markers: []SongMarker{{Name: "Intro", At: 0}, {Name: "Chorus", At: 30, End: 45}},
	}
	if single == nil || !reflect.DeepEqual(single.Names, want) || single.TempoBPM != 92 || single.TrackCount != 2 {
		t.Fatalf("Single = %+v names %+v", single, single.Names)
	}
	alternates := storedFactsAt(t, r, scope, root.ID, "Alternates")
	if alternates == nil || alternates.Names == nil || !reflect.DeepEqual(alternates.Names.Tracks, []string{"Lead vocal"}) ||
		!reflect.DeepEqual(alternates.Names.Markers, []SongMarker{{Name: "Bridge", At: 10}}) {
		t.Fatalf("Alternates = %+v", alternates)
	}
	if strings.Contains(string(storedLibrary(t, file, scope)), "Older take, never read") {
		t.Fatal("the older take was read")
	}
	// Songs in a format with no reading grammar are never opened or recorded.
	for _, other := range []string{"Ableton", "Logic"} {
		if facts := storedFactsAt(t, r, scope, root.ID, other); facts != nil {
			t.Fatalf("%s has facts %+v", other, facts)
		}
	}
	// A rescan of unchanged files opens nothing: the names stay.
	scanRoot(t, r, scope, root.ID, "names-again")
	if got := opens.take(); len(got) != 0 {
		t.Fatalf("an unchanged rescan opened %v", got)
	}
	if again := storedFactsAt(t, r, scope, root.ID, "Single"); again == nil || !reflect.DeepEqual(again.Names, want) {
		t.Fatalf("an unchanged rescan lost the names: %+v", again)
	}
}

func TestSongFactsPass_UpgradingToNamesReadsEachSongOnceMoreAndNeverAgain(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNumbers(t, file, scope)
	opens := &openCounter{}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "before-upgrade")
	opens.take()
	before := storedFactsAt(t, r, scope, root.ID, "Single")
	if before == nil || before.Names != nil {
		t.Fatalf("setup: %+v", before)
	}

	// The owner agrees to names: a numbers record is read once more, each song once.
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "after-upgrade")
	if got := opens.take(); !slices.Equal(got, []string{filepath.Join("Alternates", "Take B.RPP"), filepath.Join("Single", "Song.rpp")}) {
		t.Fatalf("the upgrade opened %v, want the two REAPER songs once", got)
	}
	after := storedFactsAt(t, r, scope, root.ID, "Single")
	if after == nil || after.Names == nil || !reflect.DeepEqual(after.Names.Tracks, []string{"Kick", "Bass"}) {
		t.Fatalf("the upgrade stored no names: %+v", after)
	}
	if after.SongFacts != before.SongFacts {
		t.Fatalf("the upgrade changed the numbers: %+v -> %+v", before.SongFacts, after.SongFacts)
	}
	// And then it is done: two more scans open nothing (no upgrade loop), including
	// the formats that have no grammar and so never had a record to upgrade.
	for i := 0; i < 2; i++ {
		scanRoot(t, r, scope, root.ID, fmt.Sprintf("settled-%d", i))
		if got := opens.take(); len(got) != 0 {
			t.Fatalf("scan %d after the upgrade opened %v", i, got)
		}
	}
}

func TestSongFactsPass_ASongWhoseFileChangedIsReadAgainUnderTheGrantItHas(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "first")

	// The person saves the project again with a new track.
	saved := time.Date(2026, time.October, 5, 10, 0, 0, 0, time.UTC)
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), namedProject("100", nil, []namedTrack{
		{lines: []string{nameLine("Kick")}}, {lines: []string{nameLine("Bass")}}, {lines: []string{nameLine("Synth")}},
	}), saved)
	scanRoot(t, r, scope, root.ID, "second")
	single := storedFactsAt(t, r, scope, root.ID, "Single")
	if single == nil || single.TempoBPM != 100 || single.Names == nil || !reflect.DeepEqual(single.Names.Tracks, []string{"Kick", "Bass", "Synth"}) ||
		len(single.Names.Markers) != 0 {
		t.Fatalf("a re-saved project: %+v", single)
	}
}

// The consent is checked again during the pass: switched off while a names read
// is under way, nothing it read is kept, numbers or names.
func TestSongFactsPass_ASwitchOffDuringANamesPassStoresNothing(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	grantNames(t, file, scope)
	var once sync.Once
	opens := &openCounter{before: func(string, string) {
		once.Do(func() { switchSongDetailsOff(t, file, scope) })
	}}
	r.factsOpened = opens.hook
	scanRoot(t, r, scope, root.ID, "switch-off")

	if count := storedFacts(t, file, scope); count != 0 {
		t.Fatalf("a pass whose consent was switched off stored %d records", count)
	}
	stored := string(storedLibrary(t, file, scope))
	for _, leaked := range []string{"Kick", "Bass", "Intro", "Lead vocal", `"names"`} {
		if strings.Contains(stored, leaked) {
			t.Fatalf("a switched-off pass stored %q", leaked)
		}
	}
}

// The names are an addition to the listing's own budget, never a threat to it:
// all songs together stay within the library's names budget, and what was cut is
// counted so the card can say "and K more".
func TestSongFactsPass_NamesAreCutToTheLibraryBudget(t *testing.T) {
	budget := 400
	was := maxLibraryNamesBytes
	maxLibraryNamesBytes = budget
	t.Cleanup(func() { maxLibraryNamesBytes = was })

	var tracks []namedTrack
	for i := 0; i < 20; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(fmt.Sprintf("Track %02d", i))}})
	}
	var markers []string
	for i := 0; i < 5; i++ {
		markers = append(markers, marker(i+1, fmt.Sprintf("%d", i*10), fmt.Sprintf(`"Mark %d"`, i), 0))
	}
	r, scope, file, tree, root := connectedMusicRoot(t)
	saved := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	writeSongProject(t, filepath.Join(tree.single, "Song.rpp"), namedProject("92", markers, tracks), saved)
	// Take A is older than Take B, so Take B dates the song and is the one read.
	writeSongProject(t, filepath.Join(tree.alternate, "Take A.rpp"), namedProject("120", nil, nil), saved.Add(-48*time.Hour))
	writeSongProject(t, filepath.Join(tree.alternate, "Take B.RPP"), namedProject("96", markers, tracks), saved.Add(-time.Hour))
	grantNames(t, file, scope)
	scanRoot(t, r, scope, root.ID, "budget")

	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if used := libraryNamesSize(&doc); used > budget {
		t.Fatalf("the library's names cost %d bytes, over its %d budget", used, budget)
	}
	for _, relative := range []string{"Single", "Alternates"} {
		facts := storedFactsAt(t, r, scope, root.ID, relative)
		if facts == nil || facts.Names == nil || facts.TrackCount != 20 {
			t.Fatalf("%s: numbers or names lost: %+v", relative, facts)
		}
		if got := len(facts.Names.Tracks) + facts.Names.TracksLeftOut; got != 20 {
			t.Fatalf("%s: %d tracks kept + %d left out = %d, want 20", relative, len(facts.Names.Tracks), facts.Names.TracksLeftOut, got)
		}
		if got := len(facts.Names.Markers) + facts.Names.MarkersLeftOut; got != 5 {
			t.Fatalf("%s: %d markers kept + %d left out = %d, want 5", relative, len(facts.Names.Markers), facts.Names.MarkersLeftOut, got)
		}
		if facts.Names.TracksLeftOut+facts.Names.MarkersLeftOut == 0 {
			t.Fatalf("%s: nothing was cut under a %d byte budget", relative, budget)
		}
	}
}

// fillLibrary adds filler entries until the stored library is within leave bytes
// of its 8 MiB limit, through the library's own fenced writes, so the final
// document is exactly what the product validated and stored.
//
// Every write decodes, validates, encodes and stores the whole document, so what
// costs time is the number of writes made once the document is large, not the
// number of entries. fillLibrary therefore measures what a write and an entry
// cost while the document is still small, adds the bulk in a single write that
// stops one to two entries short of the margin, and lands the rest with the
// small final writes. (It used to halve the remaining gap each round, which is
// about fifteen full-size writes.) The limit, the margin and the entries'
// contents are unchanged.
func fillLibrary(t *testing.T, r *Roots, scope Scope, store workspace.Store, leave int) {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	revision := doc.Revision
	stored := func() int { return len(storedLibrary(t, store, scope)) }
	// add writes n filler entries, each holding the given number of long
	// alternate names, and follows the revision through the write's receipt
	// instead of decoding the document again.
	add := func(n, alternates int, tag string) {
		t.Helper()
		key := fmt.Sprintf("filler-%s-%d", tag, revision)
		written, _, err := r.library.mutate(scope, revision, operation{key: key, action: "fixture_filler", digest: key}, func(current *Document) (string, error) {
			seed := current.Entries[0]
			for i := 0; i < n; i++ {
				entry := seed
				entry.ID = fmt.Sprintf("filler-%s-%04d-%d", tag, i, current.Revision)
				observation := seed.Observations[0]
				observation.Facts = nil
				observation.RelativeFolder = fmt.Sprintf("Filler %s %04d %d", tag, i, current.Revision)
				observation.FileIdentity = fmt.Sprintf("filler-identity-%s-%04d-%d", tag, i, current.Revision)
				observation.Alternates = make([]string, alternates)
				for a := range observation.Alternates {
					observation.Alternates[a] = fillerAlternate(a)
				}
				entry.Observations = []Observation{observation}
				current.Entries = append(current.Entries, entry)
			}
			return "filler", nil
		})
		if err != nil {
			t.Fatalf("filler %s: %v", tag, err)
		}
		revision = written.Revision
	}

	// What a write and an entry cost, measured on the small document: one write
	// of one entry, then of two, so the per-write receipt and the per-entry bytes
	// separate. The tags have the bulk tag's length because each entry embeds its
	// tag, so the measured entries are the same size as the bulk ones.
	start := stored()
	add(1, 64, "cal1")
	afterOne := stored()
	add(2, 64, "cal2")
	afterTwo := stored()
	entry := (afterTwo - afterOne) - (afterOne - start) // one entry holding 64 long alternates
	receipt := (afterOne - start) - entry               // what a write adds besides its entries
	if entry < 4<<10 || entry > 64<<10 || receipt < 0 {
		t.Fatalf("filler cost is not what the fixture assumes: entry %d bytes, write overhead %d bytes", entry, receipt)
	}
	// What one alternate name costs in the stored document (its encoded string and
	// the comma), and so what an entry costs besides its alternates.
	encodedAlternate, err := json.Marshal(fillerAlternate(0))
	if err != nil {
		t.Fatal(err)
	}
	perAlternate := len(encodedAlternate) + 1
	fixed := entry - 64*perAlternate
	if perAlternate < 100 || fixed < 0 {
		t.Fatalf("filler cost is not what the fixture assumes: %d bytes per alternate, %d bytes per entry besides them", perAlternate, fixed)
	}
	// The bulk: as many entries as leave one to two entries of room under the
	// margin, in one write.
	bulk := (maxDocumentBytes-leave-afterTwo-receipt)/entry - 1
	if bulk < 1 {
		t.Fatalf("the library is already within %d bytes of its limit before the fill", leave+2*entry)
	}
	add(bulk, 64, "bulk")

	for round := 0; ; round++ {
		gap := maxDocumentBytes - stored() - leave
		if gap < 2*entry {
			break
		}
		add(1, 64, fmt.Sprintf("big%d", round))
	}
	// Land within leave: one entry whose alternates take what is left, sized from
	// the measured costs so the write never crosses the margin. The slack covers
	// the few bytes a receipt's timestamp varies in width between platforms.
	const slack = 64
	for round := 0; round < 4; round++ {
		gap := maxDocumentBytes - stored() - leave
		fit := (gap - receipt - fixed - slack) / perAlternate
		if fit < 1 {
			break
		}
		add(1, min(63, fit), fmt.Sprintf("fine%d", round))
	}
}

// fillerAlternate is the long alternate file name a filler entry carries: 207
// characters, so each costs the stored document a predictable amount.
func fillerAlternate(index int) string {
	return fmt.Sprintf("%s-%02d.rpp", strings.Repeat("a", 200), index)
}

// A library so full that names cannot be written keeps the numbers and says how
// many names there were: a full library never costs a song its numbers.
//
// The listing scan and the discovery run BEFORE the library is filled. A scan of
// an almost-full library is slow (every directory read re-reads the document) and
// runs under a time budget, so on a loaded machine it can honestly come back
// partial, and a partial scan leaves the songs it did not reach alone. That is
// not what is under test: this is the save, which has no such budget, so it is
// called with what the pass would have read.
func TestSongFactsPass_ALibraryTooFullKeepsTheNumbersAndCountsTheNames(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	namedMusicTree(t, tree)
	scan := scanRoot(t, r, scope, root.ID, "listing") // no consent yet: the listing only
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Status != "complete" || observed.PartialReason != "" {
		t.Fatalf("setup needs a complete listing: scan %q, discovery %q", scan.Status, observed.PartialReason)
	}
	grantNames(t, file, scope)
	targets, grant, ok := r.songFactsTargets(scope, scan, observed)
	if !ok || !grant.names || len(targets) != 2 {
		t.Fatalf("targets = %d under grant %+v (ok %v), want the two REAPER songs under names", len(targets), grant, ok)
	}
	// What the pass would have read: each song holds 100 long track names, more
	// than the room the library will have left.
	var reads []songFactsRead
	for _, target := range targets {
		names := &SongNames{}
		for i := 0; i < 100; i++ {
			names.Tracks = append(names.Tracks, fmt.Sprintf("Track %03d ", i)+strings.Repeat("n", 60))
		}
		reads = append(reads, songFactsRead{songFactsTarget: target,
			Facts: SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 100}, Names: names})
	}

	const leave = 6 << 10
	filling := time.Now()
	fillLibrary(t, r, scope, file, leave)
	filled := time.Since(filling)
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore := len(doc.Entries)
	stored := len(storedLibrary(t, file, scope))
	t.Logf("library filled to %d of %d bytes with %d entries in %s", stored, maxDocumentBytes, entriesBefore, filled)

	// What the rest of the test stands on, so a fixture that stopped being full
	// cannot pass silently: the library is within a few KiB of the real 8 MiB
	// limit, the names these songs carry do not fit in the room left, and their
	// numbers do.
	jsonSize := func(v any) int {
		t.Helper()
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return len(encoded)
	}
	room := maxDocumentBytes - stored
	var namesBytes, numbersBytes int
	for _, read := range reads {
		namesBytes += jsonSize(read.Names)
		numbersBytes += jsonSize(read.Facts)
	}
	if room < leave || room > leave+4<<10 {
		t.Fatalf("the fixture must leave about %d bytes of the %d byte limit, left %d", leave, maxDocumentBytes, room)
	}
	if namesBytes <= room {
		t.Fatalf("the names (%d bytes) would fit in the %d bytes left, so nothing is under test", namesBytes, room)
	}
	if numbersBytes >= room {
		t.Fatalf("the numbers (%d bytes) cannot fit in the %d bytes left", numbersBytes, room)
	}

	saving := time.Now()
	saved, readable, err := r.saveSongFacts(scope, scan, reads, grant)
	if err != nil || saved != 2 || readable != 2 {
		t.Fatalf("saveSongFacts = saved %d, readable %d, err %v; want both songs' numbers kept", saved, readable, err)
	}
	t.Logf("the save under test took %s", time.Since(saving))

	doc, err = NewStore(file).Read(scope)
	if err != nil {
		t.Fatalf("the full library no longer reads: %v", err)
	}
	for _, relative := range []string{"Single", "Alternates"} {
		_, observation, ok := observationAt(doc, root.ID, relative)
		if !ok || observation.Facts == nil {
			t.Fatalf("%s lost its facts in a full library", relative)
		}
		facts := observation.Facts
		if facts.TrackCount != 100 || facts.TempoBPM != 92 || facts.LengthSeconds != 221 {
			t.Fatalf("%s: the numbers were not kept: %+v", relative, facts.SongFacts)
		}
		if facts.Names == nil || len(facts.Names.Tracks) != 0 || facts.Names.TracksLeftOut != 100 {
			t.Fatalf("%s: names should be counts only (100 left out): %+v", relative, facts.Names)
		}
	}
	// The listing itself is untouched by the failed larger write.
	if got := len(doc.Entries); got < entriesBefore {
		t.Fatalf("entries went from %d to %d", entriesBefore, got)
	}
}

func TestSongFactsGrant_Rules(t *testing.T) {
	numbers, names := songFactsGrant{}, songFactsGrant{names: true}
	if !numbers.covers(numbers) || !names.covers(names) || !names.covers(numbers) {
		t.Fatal("a grant must cover itself and a narrower one")
	}
	if numbers.covers(names) {
		t.Fatal("a numbers grant covers a names read")
	}
}

func TestFactsWanted(t *testing.T) {
	source := FactsSource{File: "Song.rpp"}
	numbersOnly := &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92}, ReadFrom: source}
	withNames := &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92}, Names: &SongNames{}, ReadFrom: source}
	notRead := &ObservedFacts{NotRead: NotReadUnreadable, ReadFrom: source}
	cases := []struct {
		name   string
		facts  *ObservedFacts
		names  bool
		format string
		want   bool
	}{
		{"nothing stored, numbers", nil, false, "reaper", true},
		{"nothing stored, names", nil, true, "reaper", true},
		{"numbers stored, numbers asked", numbersOnly, false, "reaper", false},
		{"numbers stored, names asked: the upgrade", numbersOnly, true, "reaper", true},
		{"names stored, names asked", withNames, true, "reaper", false},
		{"names stored, numbers asked", withNames, false, "reaper", false},
		{"not read, names asked", notRead, true, "reaper", false},
		{"numbers stored, names asked, a format with no names to give", numbersOnly, true, "logic", false},
	}
	for _, tc := range cases {
		if got := factsWanted(tc.facts, tc.names, tc.format); got != tc.want {
			t.Errorf("%s: factsWanted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFactsGrammarFor(t *testing.T) {
	if factsGrammarFor("reaper", true) != factsGrammar("reaper") {
		t.Fatal("under a names grant the reader gets the format's own grammar")
	}
	numbers := factsGrammarFor("reaper", false)
	if numbers == nil || numbers.HasNames() || numbers == factsGrammar("reaper") {
		t.Fatalf("under numbers only the reader must get a blanked copy: %+v", numbers)
	}
	if numbers.Tempo == "" || numbers.Track == "" {
		t.Fatalf("the blanked copy lost the numbers' keys: %+v", numbers)
	}
	if factsGrammarFor("logic", true) != nil || factsGrammarFor("logic", false) != nil || factsGrammarFor("", true) != nil {
		t.Fatal("a format with no grammar gave one")
	}
}

func TestCutSongNames(t *testing.T) {
	names := &SongNames{
		Tracks:  []string{"Kick", "Snare", "Hat", "Bass"},
		Markers: []SongMarker{{Name: "Intro", At: 0}, {Name: "Verse", At: 10}, {Name: "Chorus", At: 20, End: 30}},
	}
	full := songNamesSize(names)
	if got := cutSongNames(names, full); !reflect.DeepEqual(got, names) {
		t.Fatalf("room enough: %+v", got)
	}
	cut := cutSongNames(names, full-1)
	if songNamesSize(cut) > full-1 {
		t.Fatalf("still %d over %d", songNamesSize(cut), full-1)
	}
	if len(cut.Tracks)+cut.TracksLeftOut != 4 || len(cut.Markers)+cut.MarkersLeftOut != 3 {
		t.Fatalf("what was cut is not counted: %+v", cut)
	}
	// The argument is never changed: a write that runs again cuts afresh.
	if len(names.Tracks) != 4 || len(names.Markers) != 3 || names.TracksLeftOut != 0 || names.MarkersLeftOut != 0 {
		t.Fatalf("the argument changed: %+v", names)
	}
	// Room for nothing leaves only counts; negative room is the same.
	for _, room := range []int{0, -5} {
		none := cutSongNames(names, max(0, room))
		if len(none.Tracks) != 0 || len(none.Markers) != 0 || none.TracksLeftOut != 4 || none.MarkersLeftOut != 3 {
			t.Fatalf("room %d: %+v", room, none)
		}
	}
	// Entries already counted as left out stay counted.
	prior := &SongNames{Tracks: []string{"Kick"}, TracksLeftOut: 5}
	if got := cutSongNames(prior, 0); got.TracksLeftOut != 6 {
		t.Fatalf("an earlier count was lost: %+v", got)
	}
	if cutSongNames(nil, 10) != nil {
		t.Fatal("cutting nothing gave something")
	}
}

func TestNamesAsCountsKeepsTheNumbersAndMarksTheSongReadUnderNames(t *testing.T) {
	reads := []songFactsRead{
		{Facts: SongFacts{TempoBPM: 92, TrackCount: 3}, Names: &SongNames{Tracks: []string{"a", "b"}, TracksLeftOut: 1, Markers: []SongMarker{{Name: "m"}}, MarkersLeftOut: 2}},
		{Facts: SongFacts{TempoBPM: 100}}, // a numbers-only read has no names to reduce
	}
	if !readsHoldNames(reads) || readsHoldNames(reads[1:]) || readsHoldNames(nil) {
		t.Fatal("readsHoldNames")
	}
	counted := namesAsCounts(reads)
	if counted[0].Facts != reads[0].Facts || counted[1].Names != nil {
		t.Fatalf("the numbers or a numbers-only read changed: %+v", counted)
	}
	if got := counted[0].Names; got == nil || len(got.Tracks) != 0 || len(got.Markers) != 0 || got.TracksLeftOut != 3 || got.MarkersLeftOut != 3 {
		t.Fatalf("names as counts = %+v", got)
	}
	if len(reads[0].Names.Tracks) != 2 {
		t.Fatal("the original reads were changed")
	}
}
