package projectlibrary

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// setSongNames stores names (or a refusal) on song-a's observation through the
// library's own fenced write.
func setSongRecord(t *testing.T, s *Store, scope Scope, edit func(*ObservedFacts)) {
	t.Helper()
	const entryID = "song-a"
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	key := "learned-" + entryID + "-" + time.Now().Format("150405.000000000")
	if _, _, err := s.mutate(scope, doc.Revision, operation{key: key, action: "fixture", digest: key}, func(current *Document) (string, error) {
		for i := range current.Entries {
			if current.Entries[i].ID != entryID {
				continue
			}
			for j := range current.Entries[i].Observations {
				if o := &current.Entries[i].Observations[j]; o.Facts != nil {
					edit(o.Facts)
					return entryID, nil
				}
			}
		}
		t.Fatalf("no observation with facts on %s", entryID)
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
}

// What was learned shows to the person, on the card and on the shelf's detail,
// and nowhere else: not on a row, the roster, or the detail the Manager's tools
// read.
func TestSongLearned_NamesReachOnlyThePersonsOwnCardAndShelf(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	grantNames(t, file, scope) // widens the fixture's older numbers-only consent
	setSongRecord(t, s, scope, func(f *ObservedFacts) { f.Names = smallNames() })

	card, err := s.SongCard(scope, "child-a", musicWordsForTest())
	if err != nil {
		t.Fatal(err)
	}
	if card.Learned == nil || card.Learned.NotRead != "" ||
		strings.Join(card.Learned.Tracks, ",") != "Kick,Bass" || len(card.Learned.Markers) != 2 ||
		card.Learned.Markers[1] != (SongMarker{Name: "Chorus", At: 30, End: 45}) {
		t.Fatalf("card.Learned = %+v", card.Learned)
	}
	if card.Facts == nil || card.Facts.TempoBPM != 92 || card.Facts.TrackCount != 14 {
		t.Fatalf("the numbers beside the names changed: %+v", card.Facts)
	}

	shelf, err := s.DetailWithWords(scope, "song-a", musicWordsForTest())
	if err != nil {
		t.Fatal(err)
	}
	if shelf.Learned == nil || strings.Join(shelf.Learned.Tracks, ",") != "Kick,Bass" {
		t.Fatalf("the shelf's detail shows no names: %+v", shelf.Learned)
	}

	// Everything an agent reads, and every projection of numbers, is names-free.
	plain, err := s.Detail(scope, "song-a")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Learned != nil {
		t.Fatalf("Detail (the Manager's read) carries names: %+v", plain.Learned)
	}
	page, err := s.Query(scope, Search{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	roster, err := s.Roster(scope)
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]any{"Detail": plain, "Query": page, "Roster": roster} {
		encoded, _ := json.Marshal(value)
		for _, leaked := range []string{"Kick", "Bass", "Intro", "Chorus", `"learned"`, `"tracks"`, `"markers"`} {
			if strings.Contains(string(encoded), leaked) {
				t.Fatalf("%s carries %q: %.300s", label, leaked, encoded)
			}
		}
	}
}

func TestSongLearned_NamesShowOnlyUnderAConsentThatCoversThem(t *testing.T) {
	s, scope, file, _ := songCardFixture(t) // the fixture's consent covers numbers only
	setSongRecord(t, s, scope, func(f *ObservedFacts) { f.Names = smallNames() })
	card, err := s.SongCard(scope, "child-a", musicWordsForTest())
	if err != nil {
		t.Fatal(err)
	}
	if card.Learned != nil {
		t.Fatalf("names shown under a numbers-only consent: %+v", card.Learned)
	}
	shelf, _ := s.DetailWithWords(scope, "song-a", musicWordsForTest())
	if shelf.Learned != nil {
		t.Fatalf("the shelf shows names under a numbers-only consent: %+v", shelf.Learned)
	}

	// Covered, then switched off: nothing.
	grantNames(t, file, scope)
	if card, _ = s.SongCard(scope, "child-a", musicWordsForTest()); card.Learned == nil {
		t.Fatal("names not shown under a names consent")
	}
	switchSongDetailsOff(t, file, scope)
	card, _ = s.SongCard(scope, "child-a", musicWordsForTest())
	shelf, _ = s.DetailWithWords(scope, "song-a", musicWordsForTest())
	if card.Learned != nil || shelf.Learned != nil {
		t.Fatalf("a switched-off Home shows what was learned: %+v %+v", card.Learned, shelf.Learned)
	}
}

func TestSongLearned_ASongThatWasNotReadSaysWhy(t *testing.T) {
	for _, reason := range []string{NotReadUnreadable, NotReadTooLarge} {
		t.Run(reason, func(t *testing.T) {
			s, scope, _, _ := songCardFixture(t)
			setSongRecord(t, s, scope, func(f *ObservedFacts) {
				f.SongFacts, f.Names, f.NotRead = SongFacts{}, nil, reason
				if reason == NotReadTooLarge {
					f.ReadFrom.Size = maxSongFileBytes + 1
				}
			})
			card, err := s.SongCard(scope, "child-a", musicWordsForTest())
			if err != nil {
				t.Fatal(err)
			}
			if card.Learned == nil || card.Learned.NotRead != reason || len(card.Learned.Tracks) != 0 || len(card.Learned.Markers) != 0 {
				t.Fatalf("card.Learned = %+v, want only the reason %q", card.Learned, reason)
			}
			// A not-read song shows no numbers: the card says it plainly instead.
			if card.Facts != nil {
				t.Fatalf("a not-read song shows facts %+v", card.Facts)
			}
		})
	}
}

func TestSongLearned_AFormatWithNoGrammarSaysSoAndASongNotYetReachedSaysNothing(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	grantNames(t, file, scope)
	// song-b is a REAPER-format song the pass has not reached: nothing to say.
	if card, _ := s.SongCard(scope, "child-b", musicWordsForTest()); card.Learned != nil {
		t.Fatalf("a song not yet reached shows %+v", card.Learned)
	}
	// "catalog" holds a project whose format has no reading grammar.
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.mutate(scope, doc.Revision, operation{key: "format-logic", action: "fixture", digest: "format-logic"}, func(current *Document) (string, error) {
		for i := range current.Entries {
			if current.Entries[i].ID == "catalog" {
				current.Entries[i].Observations[0].Format = "logic"
				current.Entries[i].Observations[0].Alternates = nil
			}
		}
		return "logic", nil
	}); err != nil {
		t.Fatal(err)
	}
	shelf, err := s.DetailWithWords(scope, "catalog", musicWordsForTest())
	if err != nil {
		t.Fatal(err)
	}
	if shelf.Learned == nil || shelf.Learned.NotRead != NotReadNoGrammar {
		t.Fatalf("a format with no grammar: %+v", shelf.Learned)
	}
	// Under no consent at all the Home reads nothing, so nothing is said.
	switchSongDetailsOff(t, file, scope)
	if shelf, _ = s.DetailWithWords(scope, "catalog", musicWordsForTest()); shelf.Learned != nil {
		t.Fatalf("a switched-off Home says why it did not read: %+v", shelf.Learned)
	}
}

// learnedFrom follows the rule that picks the facts a row shows: the usable source
// with the newest save, never a revoked, inactive or unavailable one.
func TestLearnedFrom_UsesTheSourceThatDatesTheSong(t *testing.T) {
	older := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(48 * time.Hour)
	names := func(track string) *ObservedFacts {
		return &ObservedFacts{Names: &SongNames{Tracks: []string{track}}}
	}
	observation := func(root string, saved time.Time, availability string, facts *ObservedFacts) Observation {
		return Observation{RootID: root, Format: "reaper", FileModifiedAt: saved, Availability: availability, Facts: facts}
	}
	roots := map[string]Root{"a": {ID: "a"}, "b": {ID: "b"}, "gone": {ID: "gone", RevokedAt: ptrTime(older)}}
	consent := &workspace.SongDetailsConsent{GrantedAt: older, Source: workspace.SongDetailsConsentLearnSongs, Scope: workspace.SongDetailsScopeNames}

	entry := Entry{Observations: []Observation{
		observation("a", older, "available", names("Older")),
		observation("b", newer, "available", names("Newer")),
	}}
	if got := learnedFrom(entry, roots, nil, consent); got == nil || got.Tracks[0] != "Newer" {
		t.Fatalf("learned = %+v, want the newer source", got)
	}
	// A newer source that is revoked, inactive or unavailable does not date it.
	for label, hidden := range map[string]Observation{
		"revoked root":  observation("gone", newer.Add(time.Hour), "available", names("Revoked")),
		"unavailable":   observation("b", newer.Add(time.Hour), "unavailable", names("Unavailable")),
		"inactive root": observation("a", newer.Add(time.Hour), "available", names("Inactive")),
	} {
		inactive := map[string]bool{}
		if label == "inactive root" {
			inactive["a"] = true
		}
		with := Entry{Observations: []Observation{observation("b", older, "available", names("Real")), hidden}}
		if got := learnedFrom(with, roots, inactive, consent); got == nil || got.Tracks[0] != "Real" {
			t.Fatalf("%s: learned = %+v, want the usable source", label, got)
		}
	}
	// No usable source, no save time, or no consent: nothing.
	if learnedFrom(Entry{Observations: []Observation{observation("gone", newer, "available", names("X"))}}, roots, nil, consent) != nil {
		t.Fatal("a revoked source alone shows names")
	}
	if learnedFrom(Entry{Observations: []Observation{observation("a", time.Time{}, "available", names("X"))}}, roots, nil, consent) != nil {
		t.Fatal("a source with no save time shows names")
	}
	if learnedFrom(entry, roots, nil, nil) != nil {
		t.Fatal("a Home with no consent shows names")
	}
	// The result is a copy: changing it leaves the stored names alone.
	got := learnedFrom(entry, roots, nil, consent)
	got.Tracks[0] = "Changed"
	if entry.Observations[1].Facts.Names.Tracks[0] != "Newer" {
		t.Fatal("learnedFrom shares storage with the document")
	}
}

// The folder agent's own turns read one folder's songs with what was learned:
// nothing without a consent, numbers under any consent, names only under one that
// covers them.
func TestLearnedSongsInRoot_ReadsOneFoldersSongsUnderTheConsent(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	setSongRecord(t, s, scope, func(f *ObservedFacts) { f.Names = smallNames() })
	names := func(songs []LearnedSong) []string {
		var out []string
		for _, song := range songs {
			out = append(out, song.Name)
		}
		return out
	}

	// The fixture's consent covers numbers only: numbers, no names.
	songs, err := s.LearnedSongsInRoot(scope, "home-root")
	if err != nil {
		t.Fatal(err)
	}
	// Ordered by name; a linked song is named after its workspace; song-c has no
	// observation under this root.
	if got := strings.Join(names(songs), "|"); got != "Catalog only|Song A|Song B" {
		t.Fatalf("songs = %s", got)
	}
	songA := songs[1]
	if songA.Facts == nil || songA.Facts.TempoBPM != 92 || songA.Facts.TrackCount != 14 || songA.Names != nil || songA.NotRead != "" {
		t.Fatalf("under a numbers consent Song A = %+v", songA)
	}
	if songs[2].Facts != nil || songs[2].Names != nil {
		t.Fatalf("a song the pass has not reached shows %+v", songs[2])
	}

	// A consent that covers names: the names come with the numbers.
	grantNames(t, file, scope)
	songs, _ = s.LearnedSongsInRoot(scope, "home-root")
	if songs[1].Names == nil || strings.Join(songs[1].Names.Tracks, ",") != "Kick,Bass" || songs[1].Facts == nil {
		t.Fatalf("under a names consent Song A = %+v", songs[1])
	}
	// The returned names are a copy.
	songs[1].Names.Tracks[0] = "Changed"
	if again, _ := s.LearnedSongsInRoot(scope, "home-root"); again[1].Names.Tracks[0] != "Kick" {
		t.Fatal("the reader shares storage with the document")
	}

	// A song that was not read says why, with no numbers.
	setSongRecord(t, s, scope, func(f *ObservedFacts) {
		f.SongFacts, f.Names, f.NotRead = SongFacts{}, nil, NotReadUnreadable
	})
	songs, _ = s.LearnedSongsInRoot(scope, "home-root")
	if songs[1].NotRead != NotReadUnreadable || songs[1].Facts != nil || songs[1].Names != nil {
		t.Fatalf("a not-read song = %+v", songs[1])
	}

	// Another root, an empty root id, and a switched-off Home give nothing.
	if other, _ := s.LearnedSongsInRoot(scope, "some-other-root"); len(other) != 0 {
		t.Fatalf("another root returned %d songs", len(other))
	}
	if none, _ := s.LearnedSongsInRoot(scope, ""); len(none) != 0 {
		t.Fatalf("an empty root id returned %d songs", len(none))
	}
	switchSongDetailsOff(t, file, scope)
	if off, _ := s.LearnedSongsInRoot(scope, "home-root"); len(off) != 0 {
		t.Fatalf("a switched-off Home returned %d songs", len(off))
	}
}
