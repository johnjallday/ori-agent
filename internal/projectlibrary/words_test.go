package projectlibrary

import (
	"regexp"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// musicWordsForTest are the words the Music Project Management 0.5.0
// candidate declares: the #791 card's wording, now supplied by the package.
func musicWordsForTest() Words {
	return WordsFromDeclaration(&projecttemplates.ProjectCardDeclaration{
		SchemaVersion: 1, Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs and assistants",
		Facts: []projecttemplates.ProjectCardFact{
			{Kind: "track_count", Unit: "tracks", UnitOne: "track"},
			{Kind: "tempo", Unit: "BPM"},
			{Kind: "length"},
		},
		Fields: []projecttemplates.ProjectCardField{{ID: "key", Label: "Key", Kind: "text"}},
	})
}

func TestWordsFromDeclaration(t *testing.T) {
	words := musicWordsForTest()
	if words.Source != WordsSourcePackage || words.Noun != "Song" || words.RosterTitle != "Songs and assistants" ||
		len(words.Facts) != 3 || len(words.Fields) != 1 {
		t.Fatalf("words = %+v", words)
	}
	if field, ok := words.Field("key"); !ok || field.Label != "Key" {
		t.Fatalf("field = %+v %v", field, ok)
	}
	neutral := WordsFromDeclaration(nil)
	if neutral.Source != WordsSourceNeutral || neutral.Noun != "Project" || neutral.NounPlural != "Projects" ||
		neutral.RosterTitle != "Projects and assistants" || neutral.Facts == nil || neutral.Fields == nil {
		t.Fatalf("neutral = %+v", neutral)
	}
}

func TestFactsLabel_PackageWords(t *testing.T) {
	words := musicWordsForTest()
	for _, tc := range []struct {
		facts *SongFacts
		want  string
	}{
		{nil, ""},
		{&SongFacts{}, ""},
		{&SongFacts{TrackCount: 1}, "1 track"},
		{&SongFacts{TempoBPM: 96.25, TempoVaries: true}, "96.3 BPM, varies"},
		{&SongFacts{TempoBPM: 120, LengthSeconds: 3725.4, TrackCount: 24}, "24 tracks · 120 BPM · 1:02:05"},
		{&SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14}, "14 tracks · 92 BPM · 3:41"},
		{&SongFacts{LengthSeconds: 0.4}, ""},
	} {
		if got := FactsLabel(tc.facts, words); got != tc.want {
			t.Errorf("FactsLabel(%+v) = %q, want %q", tc.facts, got, tc.want)
		}
	}
	// Declared order wins, a kind not declared is left out, and a unit
	// without unit_one is used for one too.
	reordered := Words{Source: WordsSourcePackage, Noun: "Episode", NounPlural: "Episodes", RosterTitle: "Episodes",
		Facts: []WordsFact{{Kind: "length"}, {Kind: "tempo", Unit: "bpm"}}}
	if got := FactsLabel(&SongFacts{TempoBPM: 1, LengthSeconds: 61, TrackCount: 3}, reordered); got != "1:01 · 1 bpm" {
		t.Errorf("reordered = %q", got)
	}
}

func TestFactsLabel_NeutralWords(t *testing.T) {
	for _, tc := range []struct {
		facts *SongFacts
		want  string
	}{
		{&SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14}, "Tracks 14 · Tempo 92 · 3:41"},
		{&SongFacts{TempoBPM: 92, TempoVaries: true}, "Tempo 92, varies"},
		{&SongFacts{TrackCount: 1}, "Tracks 1"},
		{&SongFacts{LengthSeconds: 3725}, "1:02:05"},
	} {
		if got := FactsLabel(tc.facts, NeutralWords()); got != tc.want {
			t.Errorf("FactsLabel(%+v) = %q, want %q", tc.facts, got, tc.want)
		}
		// A caller that forgets the words gets the neutral ones, never a blank.
		if got := FactsLabel(tc.facts, Words{}); got != tc.want {
			t.Errorf("FactsLabel(%+v, zero) = %q, want %q", tc.facts, got, tc.want)
		}
	}
	// A package that declares the words but no facts gets the neutral facts.
	wordsOnly := Words{Source: WordsSourcePackage, Noun: "Song", NounPlural: "Songs", RosterTitle: "Songs"}
	if got := FactsLabel(&SongFacts{TrackCount: 2, TempoBPM: 80}, wordsOnly); got != "Tracks 2 · Tempo 80" {
		t.Errorf("words-only = %q", got)
	}
}

// FR11: under neutral words the server's facts label and status line carry
// no domain word. "Tracks" is the host's own label for the track_count kind
// (PRD FR6), so the lower-case unit words are what is checked for tracks.
var (
	domainWordAnyCase   = regexp.MustCompile(`(?i)\b(songs?|bpm)\b`)
	domainUnitLowerCase = regexp.MustCompile(`\btracks?\b`)
)

func neutralTextHasDomainWord(text string) bool {
	return domainWordAnyCase.MatchString(text) || domainUnitLowerCase.MatchString(text)
}

func TestNeutralWords_RenderNoDomainWord(t *testing.T) {
	now := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	saved := now.AddDate(0, 0, -2)
	for _, facts := range []*SongFacts{
		{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14},
		{TempoBPM: 92, TempoVaries: true, TrackCount: 1},
	} {
		label := FactsLabel(facts, NeutralWords())
		line := StatusLine(SongCard{FactsState: FactsStateOn, Facts: facts, LastSavedAt: &saved,
			OpenWork: workspace.TicketCounts{Ready: 1}}, NeutralWords(), now)
		for _, text := range []string{label, line} {
			if neutralTextHasDomainWord(text) {
				t.Errorf("neutral text carries a domain word: %q", text)
			}
		}
	}
	// The check itself catches the package words.
	if !neutralTextHasDomainWord(FactsLabel(&SongFacts{TempoBPM: 92, TrackCount: 14}, musicWordsForTest())) {
		t.Fatal("the domain-word check misses the package words")
	}
}
