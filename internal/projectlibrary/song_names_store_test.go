package projectlibrary

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxedNames is the largest set of names one song keeps: 128 track names and
// 128 markers (half of them regions), every name 80 characters long.
func maxedNames() *SongNames {
	names := &SongNames{}
	for i := 0; i < maxSongTracks; i++ {
		names.Tracks = append(names.Tracks, fmt.Sprintf("T%03d", i)+strings.Repeat("t", maxSongNameRunes-4))
	}
	for i := 0; i < maxSongMarkers; i++ {
		marker := SongMarker{Name: fmt.Sprintf("M%03d", i) + strings.Repeat("m", maxSongNameRunes-4), At: float64(i)*7.5 + 0.125}
		if i%2 == 0 {
			marker.End = marker.At + 4.25
		}
		names.Markers = append(names.Markers, marker)
	}
	names.TracksLeftOut, names.MarkersLeftOut = 17, 3
	return names
}

func smallNames() *SongNames {
	return &SongNames{
		Tracks:  []string{"Kick", "Bass"},
		Markers: []SongMarker{{Name: "Intro", At: 0}, {Name: "Chorus", At: 30, End: 45}},
	}
}

// setNames stores names on the song at relative, through the library's own
// fenced write (so the document is validated, size-checked and mirrored).
func setNames(t *testing.T, r *Roots, scope Scope, relative string, names *SongNames) error {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("fixture-names-%s-%d", relative, doc.Revision)
	_, _, err = r.library.mutate(scope, doc.Revision, operation{key: key, action: "fixture_names", digest: key}, func(current *Document) (string, error) {
		for i := range current.Entries {
			for j := range current.Entries[i].Observations {
				if o := &current.Entries[i].Observations[j]; o.RelativeFolder == relative && o.Facts != nil {
					o.Facts.Names = names.clone()
					return current.Entries[i].ID, nil
				}
			}
		}
		return "", errors.New("no song with facts at " + relative)
	})
	return err
}

func storedLibrary(t *testing.T, store workspace.Store, scope Scope) json.RawMessage {
	t.Helper()
	home, err := store.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	return home.GetAssistantProgramState().ProjectLibrary
}

// A song read under a names consent that holds the most names it ever keeps fits
// the document, its size check and the mirror, and reads back unchanged.
func TestStore_AMaxedSongFitsTheDocumentAndTheMirror(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "maxed")
	before := len(storedLibrary(t, file, scope))

	maxed := maxedNames()
	if err := setNames(t, r, scope, "Single", maxed); err != nil {
		t.Fatalf("a maxed song did not fit: %v", err)
	}
	perSong := len(storedLibrary(t, file, scope)) - before
	t.Logf("a maxed song adds %d bytes to the stored library (%d KiB); the %d MiB document holds about %d of them",
		perSong, perSong>>10, maxDocumentBytes>>20, maxDocumentBytes/perSong)
	// ~10 KiB of track names + ~10 KiB of marker names + ~7 KiB of times and keys,
	// well inside the ~30 KiB the contract budgets per song.
	if perSong < 20<<10 || perSong > 36<<10 {
		t.Fatalf("a maxed song costs %d bytes; expected about 20-35 KiB", perSong)
	}

	// Read back through the store (which compares the primary and the folder copy).
	doc, err := NewStore(file).Read(scope)
	if err != nil {
		t.Fatalf("the store cannot read a library with a maxed song: %v", err)
	}
	_, observation, ok := observationAt(doc, root.ID, "Single")
	if !ok || observation.Facts == nil || observation.Facts.Names == nil {
		t.Fatalf("names did not survive the write: %+v", observation.Facts)
	}
	if !reflect.DeepEqual(observation.Facts.Names, maxed) {
		t.Fatal("the names read back differ from what was written")
	}
	// The numbers beside them are unchanged.
	if observation.Facts.TempoBPM != 92 || observation.Facts.TrackCount != 2 {
		t.Fatalf("numbers changed beside the names: %+v", observation.Facts.SongFacts)
	}
	// A second maxed song in the same document also fits.
	if err := setNames(t, r, scope, "Alternates", maxedNames()); err != nil {
		t.Fatalf("a second maxed song did not fit: %v", err)
	}
	if _, err := NewStore(file).Read(scope); err != nil {
		t.Fatalf("two maxed songs: %v", err)
	}
}

// Names are additive: a record read under numbers only is byte-for-byte what it
// was before names existed.
func TestStore_ANumbersOnlyRecordStoresNoNamesKeys(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "numbers")
	stored := string(storedLibrary(t, file, scope))
	if !strings.Contains(stored, `"tempo_bpm":92`) {
		t.Fatalf("the fixture stored no facts: %.300s", stored)
	}
	for _, key := range []string{`"names"`, `"not_read"`, `"tracks"`, `"markers"`, `"tracks_left_out"`, `"markers_left_out"`} {
		if strings.Contains(stored, key) {
			t.Fatalf("a numbers-only library stores %s", key)
		}
	}
}

func forgeStored(t *testing.T, stored json.RawMessage, relative string, edit func(facts, observation map[string]any)) json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(stored))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	forged := false
	for _, entry := range raw["entries"].([]any) {
		for _, observation := range entry.(map[string]any)["observations"].([]any) {
			o := observation.(map[string]any)
			if facts, ok := o["facts"].(map[string]any); ok && o["relative_folder"] == relative {
				edit(facts, o)
				forged = true
			}
		}
	}
	if !forged {
		t.Fatalf("no stored facts at %q to forge", relative)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestStore_ForgedNamesAndNotReadMakeTheDocumentInvalid(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	scanRoot(t, r, scope, root.ID, "forge")
	if err := setNames(t, r, scope, "Single", smallNames()); err != nil {
		t.Fatal(err)
	}
	stored := storedLibrary(t, file, scope)
	if _, err := decodeDocument(stored, scope); err != nil {
		t.Fatalf("the stored document does not decode: %v", err)
	}
	names := func(f map[string]any) map[string]any { return f["names"].(map[string]any) }
	marker := func(f map[string]any, i int) map[string]any { return names(f)["markers"].([]any)[i].(map[string]any) }
	clearNumbers := func(f map[string]any) {
		delete(f, "tempo_bpm")
		delete(f, "length_seconds")
		delete(f, "track_count")
	}
	many := func(n int, name string) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = name
		}
		return out
	}

	bad := map[string]func(f, o map[string]any){
		"too many track names":                func(f, _ map[string]any) { names(f)["tracks"] = many(maxSongTracks+1, "Kick") },
		"too many markers":                    func(f, _ map[string]any) { names(f)["markers"] = manyMarkers(maxSongMarkers + 1) },
		"a path as a track name":              func(f, _ map[string]any) { names(f)["tracks"] = []any{"/etc/passwd"} },
		"a windows path":                      func(f, _ map[string]any) { names(f)["tracks"] = []any{`C:\Users\me\kick.wav`} },
		"a control character":                 func(f, _ map[string]any) { names(f)["tracks"] = []any{"a\u0001b"} },
		"a bidi control":                      func(f, _ map[string]any) { names(f)["tracks"] = []any{"a\u202eb"} },
		"a name over 80 runes":                func(f, _ map[string]any) { names(f)["tracks"] = []any{strings.Repeat("x", maxSongNameRunes+1)} },
		"a name with a leading space":         func(f, _ map[string]any) { names(f)["tracks"] = []any{" Kick"} },
		"a name with a trailing space":        func(f, _ map[string]any) { names(f)["tracks"] = []any{"Kick "} },
		"an empty track name":                 func(f, _ map[string]any) { names(f)["tracks"] = []any{""} },
		"a path as a marker name":             func(f, _ map[string]any) { marker(f, 0)["name"] = "/Users/me/x.wav" },
		"a marker before zero":                func(f, _ map[string]any) { marker(f, 0)["at"] = -1 },
		"a marker past the maximum":           func(f, _ map[string]any) { marker(f, 0)["at"] = maxSongLength + 1 },
		"a region ending before it starts":    func(f, _ map[string]any) { marker(f, 1)["end"] = 5 },
		"a region ending where it starts":     func(f, _ map[string]any) { marker(f, 1)["end"] = 30 },
		"a region past the maximum":           func(f, _ map[string]any) { marker(f, 1)["end"] = maxSongLength + 1 },
		"a negative tracks left out":          func(f, _ map[string]any) { names(f)["tracks_left_out"] = -1 },
		"a markers left out past the maximum": func(f, _ map[string]any) { names(f)["markers_left_out"] = maxSongLeftOut + 1 },
		"an unknown key in the names":         func(f, _ map[string]any) { names(f)["key"] = "C minor" },
		"an unknown key in a marker":          func(f, _ map[string]any) { marker(f, 0)["colour"] = "red" },
		"an unknown reason":                   func(f, _ map[string]any) { clearNumbers(f); delete(f, "names"); f["not_read"] = "tired" },
		"not read, with numbers":              func(f, _ map[string]any) { delete(f, "names"); f["not_read"] = NotReadUnreadable },
		"not read, with names":                func(f, _ map[string]any) { clearNumbers(f); f["not_read"] = NotReadUnreadable },
		"unreadable over the size cap": func(f, _ map[string]any) {
			clearNumbers(f)
			delete(f, "names")
			f["not_read"] = NotReadUnreadable
			f["read_from"].(map[string]any)["size"] = DefaultSongFactsLimits.MaxBytes + 1
		},
		"names over the size cap": func(f, _ map[string]any) {
			f["read_from"].(map[string]any)["size"] = DefaultSongFactsLimits.MaxBytes + 1
		},
	}
	for name, edit := range bad {
		t.Run(name, func(t *testing.T) {
			forged := forgeStored(t, stored, "Single", func(f, o map[string]any) { edit(f, o) })
			if _, err := decodeDocument(forged, scope); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("forged record decoded: %v", err)
			}
		})
	}

	// What is allowed still decodes.
	good := map[string]func(f, o map[string]any){
		"names as stored": func(_, _ map[string]any) {},
		"empty names (read under names, none found)": func(f, _ map[string]any) { f["names"] = map[string]any{} },
		"names with no numbers":                      func(f, _ map[string]any) { clearNumbers(f) },
		"a name of exactly 80 runes":                 func(f, _ map[string]any) { names(f)["tracks"] = []any{strings.Repeat("é", maxSongNameRunes)} },
		"a slash inside a name":                      func(f, _ map[string]any) { names(f)["tracks"] = []any{"Vox L/R", "AC/DC"} },
		"an unnamed marker":                          func(f, _ map[string]any) { delete(marker(f, 0), "name") },
		"a marker at the maximum":                    func(f, _ map[string]any) { marker(f, 0)["at"] = maxSongLength },
		"left out counts at the maximum": func(f, _ map[string]any) {
			names(f)["tracks_left_out"], names(f)["markers_left_out"] = maxSongLeftOut, maxSongLeftOut
		},
		"too large, over the read cap": func(f, _ map[string]any) {
			clearNumbers(f)
			delete(f, "names")
			f["not_read"] = NotReadTooLarge
			f["read_from"].(map[string]any)["size"] = DefaultSongFactsLimits.MaxBytes + 1
		},
		"unreadable, within the cap": func(f, _ map[string]any) {
			clearNumbers(f)
			delete(f, "names")
			f["not_read"] = NotReadUnreadable
		},
	}
	for name, edit := range good {
		t.Run("allowed: "+name, func(t *testing.T) {
			forged := forgeStored(t, stored, "Single", func(f, o map[string]any) { edit(f, o) })
			if _, err := decodeDocument(forged, scope); err != nil {
				t.Fatalf("a valid record was refused: %v", err)
			}
		})
	}
}

// manyMarkers is n valid markers, as the loader's JSON holds them.
func manyMarkers(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"name": "Mark", "at": i}
	}
	return out
}

func TestObservedFacts_CloneSharesNoSliceAndKeepFactsCarriesNames(t *testing.T) {
	saved := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	source := FactsSource{File: "Song.rpp", Identity: "1:2", Size: 100, ModifiedAt: saved}
	original := &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92, TrackCount: 2}, Names: smallNames(), ReadFrom: source}

	copied := original.clone()
	if !reflect.DeepEqual(copied, original) {
		t.Fatalf("clone differs: %+v vs %+v", copied, original)
	}
	original.Names.Tracks[0] = "Changed"
	original.Names.Markers[1].End = 99
	if copied.Names.Tracks[0] != "Kick" || copied.Names.Markers[1].End != 45 {
		t.Fatalf("the clone shares storage with the original: %+v", copied.Names)
	}
	if (*ObservedFacts)(nil).clone() != nil || (*SongNames)(nil).clone() != nil {
		t.Fatal("cloning nothing gave something")
	}
	// A record with no names clones with none.
	if numbers := (&ObservedFacts{SongFacts: SongFacts{TempoBPM: 100}, ReadFrom: source}).clone(); numbers.Names != nil {
		t.Fatalf("a numbers-only clone gained names: %+v", numbers.Names)
	}

	// Rescanning an unchanged file keeps the names (a copy); a changed file drops them.
	prior := Observation{Facts: &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92}, Names: smallNames(), ReadFrom: source}}
	candidate := Candidate{Format: "reaper", DatedFile: &source}
	var kept Observation
	keepFacts(&kept, prior, candidate)
	if kept.Facts == nil || kept.Facts.Names == nil || !reflect.DeepEqual(kept.Facts.Names, smallNames()) {
		t.Fatalf("an unchanged file lost its names: %+v", kept.Facts)
	}
	kept.Facts.Names.Tracks[0] = "Changed"
	if prior.Facts.Names.Tracks[0] != "Kick" {
		t.Fatal("the kept record shares names with the prior one")
	}
	moved := FactsSource{File: "Song.rpp", Identity: "1:2", Size: 101, ModifiedAt: saved}
	var dropped Observation
	keepFacts(&dropped, prior, Candidate{Format: "reaper", DatedFile: &moved})
	if dropped.Facts != nil {
		t.Fatalf("a changed file kept its old names: %+v", dropped.Facts)
	}
}

func TestSongNames_ValidCases(t *testing.T) {
	if !(*SongNames)(nil).valid() || !(&SongNames{}).valid() || !smallNames().valid() || !maxedNames().valid() {
		t.Fatal("a valid set of names was refused")
	}
	over := maxedNames()
	over.Tracks = append(over.Tracks, "one more")
	if over.valid() {
		t.Fatal("129 track names were accepted")
	}
}
