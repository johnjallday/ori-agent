package projectlibrary

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// namedTrack is one track of a test project: its attribute lines (a name, usually)
// and the items it holds.
type namedTrack struct {
	lines []string
	items [][2]string
}

func nameLine(name string) string { return `NAME "` + name + `"` }

// namedProject writes a project whose root holds the given marker lines, then
// tracks. Marker lines go in before the tracks, where a saved project keeps them.
func namedProject(tempo string, markers []string, tracks []namedTrack) string {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1 \"7.27/macOS-arm64\" 1727800000\n")
	b.WriteString("  <NOTES 0 2\n    |Mix notes the user typed\n  >\n")
	if tempo != "" {
		b.WriteString("  TEMPO " + tempo + " 4 4\n")
	}
	for _, line := range markers {
		b.WriteString("  " + line + "\n")
	}
	for _, track := range tracks {
		b.WriteString("  <TRACK {A982F44F-895F-4943-B6F7-299C7949DF39}\n")
		for _, line := range track.lines {
			b.WriteString("    " + line + "\n")
		}
		b.WriteString("    <FXCHAIN\n      NAME \"fx chain name\"\n    >\n")
		for _, item := range track.items {
			b.WriteString("    <ITEM\n      POSITION " + item[0] + "\n      LENGTH " + item[1] + "\n      NAME \"take name\"\n    >\n")
		}
		b.WriteString("  >\n")
	}
	b.WriteString("  <EXTENSIONS\n  >\n>\n")
	return b.String()
}

func marker(index int, at, name string, flags int) string {
	return fmt.Sprintf(`MARKER %d %s %s %d 0 1 R {C0FFEE00-0000-0000-0000-000000000000} 0`, index, at, name, flags)
}

func namesOf(t *testing.T, project string) (SongFacts, *SongNames) {
	t.Helper()
	facts, names, err := ReadSongDetails(strings.NewReader(project), factsGrammar("reaper"), DefaultSongFactsLimits)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return facts, names
}

func TestReadSongDetails_NamesAndMarkersComeBackInProjectOrder(t *testing.T) {
	project := namedProject("92", []string{
		marker(1, "0", `"Intro"`, 0),
		marker(2, "12.5", `"Verse 1"`, 0),
		marker(3, "30", `"Chorus"`, 1), // a region's start …
		marker(3, "45.25", `""`, 1),    // … and its end
		marker(4, "200.5", `Outro`, 0), // an unquoted name
	}, []namedTrack{
		{lines: []string{nameLine("Kick")}, items: [][2]string{{"0", "221"}}},
		{lines: []string{nameLine("Bass")}},
		{lines: []string{nameLine("Lead vocal")}},
	})
	facts, names := namesOf(t, project)
	if facts.TempoBPM != 92 || facts.TrackCount != 3 || facts.LengthSeconds != 221 {
		t.Fatalf("the numbers changed: %+v", facts)
	}
	want := &SongNames{
		Tracks: []string{"Kick", "Bass", "Lead vocal"},
		Markers: []SongMarker{
			{Name: "Intro", At: 0},
			{Name: "Verse 1", At: 12.5},
			{Name: "Chorus", At: 30, End: 45.25},
			{Name: "Outro", At: 200.5},
		},
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %+v\nwant   %+v", names, want)
	}
}

func TestReadSongDetails_ANumbersOnlyGrammarReadsNoNames(t *testing.T) {
	project := namedProject("92", []string{marker(1, "5", `"Intro"`, 0)}, []namedTrack{{lines: []string{nameLine("Kick")}}})
	full, _ := namesOf(t, project)

	grammar := factsGrammar("reaper").WithoutNames()
	facts, names, err := ReadSongDetails(strings.NewReader(project), grammar, DefaultSongFactsLimits)
	if err != nil || names != nil {
		t.Fatalf("a numbers-only grammar returned names %+v (err %v)", names, err)
	}
	if facts != full {
		t.Fatalf("numbers differ by grammar: %+v vs %+v", facts, full)
	}
	// The old entry point never reads names, whatever grammar it is handed.
	again, err := ReadSongFacts(strings.NewReader(project), factsGrammar("reaper"), DefaultSongFactsLimits)
	if err != nil || again != full {
		t.Fatalf("ReadSongFacts = %+v, %v", again, err)
	}
	// And the grammar the table holds was not changed by blanking a copy.
	if !factsGrammar("reaper").HasNames() {
		t.Fatal("blanking a copy changed the table's grammar")
	}
}

func TestReadSongDetails_OnlyATracksOwnFirstNameCounts(t *testing.T) {
	project := namedProject("", nil, []namedTrack{
		{lines: []string{nameLine("First"), nameLine("A second name line")}, items: [][2]string{{"0", "1"}}},
		{lines: []string{"PEAKCOL 1", nameLine("Second")}},
		{lines: []string{`NAME ""`}}, // an unnamed track
		{lines: []string{nameLine("Fourth")}},
	})
	facts, names := namesOf(t, project)
	if facts.TrackCount != 4 {
		t.Fatalf("track count = %d", facts.TrackCount)
	}
	// The effects chain's and the item's NAME lines are never read, and an
	// unnamed track adds no name (and is not "left out").
	if want := []string{"First", "Second", "Fourth"}; !reflect.DeepEqual(names.Tracks, want) || names.TracksLeftOut != 0 {
		t.Fatalf("tracks = %+v", names)
	}
}

func TestReadSongDetails_ThreeQuoteStylesAndAnUnclosedOne(t *testing.T) {
	project := namedProject("", []string{
		marker(1, "1", `"Say "hi"`, 0),        // a double quote inside a double-quoted name: unclosed -> skipped
		marker(2, "2", `'Don"t stop'`, 0),     // single quotes hold a double quote
		marker(3, "3", "`it's here`", 0),      // backticks hold a single quote
		marker(4, "4", `"Spaces  inside"`, 0), // quoted with spaces
		marker(5, "5", `"never closed`, 0),    // unterminated: skipped
		marker(6, "6", `"closed"junk`, 0),     // a closing quote must end the token: skipped
		marker(7, "7", `Plain`, 0),            // unquoted
		`MARKER 8 oops "Bad time" 0 0 1`,      // a time that is not a number: skipped
		`MARKER`,                              // nothing after the key
		`MARKER 9 9`,                          // too few values
		marker(10, "10", `"After the bad ones"`, 0),
	}, []namedTrack{
		{lines: []string{`NAME 'it''s'`}}, // a quote the format does not escape: unreadable -> no name, still a track
		{lines: []string{"NAME `back tick`"}},
		{lines: []string{`NAME Unquoted`}},
		{lines: []string{`NAME "unterminated`}},
		{lines: []string{nameLine("Last")}},
	})
	facts, names := namesOf(t, project)
	if facts.TrackCount != 5 {
		t.Fatalf("a malformed name changed the track count: %d", facts.TrackCount)
	}
	wantMarkers := []SongMarker{
		{Name: `Don"t stop`, At: 2},
		{Name: "it's here", At: 3},
		{Name: "Spaces  inside", At: 4},
		{Name: "Plain", At: 7},
		{Name: "After the bad ones", At: 10},
	}
	if !reflect.DeepEqual(names.Markers, wantMarkers) || names.MarkersLeftOut != 0 {
		t.Fatalf("markers = %+v (left out %d)", names.Markers, names.MarkersLeftOut)
	}
	if want := []string{"back tick", "Unquoted", "Last"}; !reflect.DeepEqual(names.Tracks, want) || names.TracksLeftOut != 0 {
		t.Fatalf("tracks = %+v (left out %d)", names.Tracks, names.TracksLeftOut)
	}
}

func TestReadSongDetails_CapsKeepTheFirstAndCountWhatWasLeftOut(t *testing.T) {
	var tracks []namedTrack
	var markers []string
	for i := 0; i < 200; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(fmt.Sprintf("Track %03d", i))}})
		markers = append(markers, marker(i+1, fmt.Sprintf("%d", i), fmt.Sprintf(`"Mark %03d"`, i), 0))
	}
	facts, names := namesOf(t, namedProject("120", markers, tracks))
	if facts.TrackCount != 200 {
		t.Fatalf("the numbers were capped too: %d tracks", facts.TrackCount)
	}
	if len(names.Tracks) != 128 || names.TracksLeftOut != 72 || names.Tracks[0] != "Track 000" || names.Tracks[127] != "Track 127" {
		t.Fatalf("tracks: kept %d, left out %d, first %q last %q", len(names.Tracks), names.TracksLeftOut, names.Tracks[0], names.Tracks[len(names.Tracks)-1])
	}
	if len(names.Markers) != 128 || names.MarkersLeftOut != 72 || names.Markers[127].Name != "Mark 127" || names.Markers[127].At != 127 {
		t.Fatalf("markers: kept %d, left out %d", len(names.Markers), names.MarkersLeftOut)
	}
}

func TestReadSongDetails_ARegionCutByTheCapIsCountedOnceNotTwice(t *testing.T) {
	var markers []string
	// 130 regions, each a start and an end line: the last two do not fit.
	for i := 0; i < 130; i++ {
		markers = append(markers, marker(i+1, fmt.Sprintf("%d", i*10), fmt.Sprintf(`"Region %d"`, i), 1))
		markers = append(markers, marker(i+1, fmt.Sprintf("%d", i*10+5), `""`, 1))
	}
	_, names := namesOf(t, namedProject("120", markers, nil))
	if len(names.Markers) != 128 || names.MarkersLeftOut != 2 {
		t.Fatalf("kept %d, left out %d: the two cut regions must count as 2, not 4", len(names.Markers), names.MarkersLeftOut)
	}
	last := names.Markers[127]
	if last.Name != "Region 127" || last.At != 1270 || last.End != 1275 {
		t.Fatalf("the last kept region = %+v", last)
	}
}

func TestReadSongDetails_RegionPairingEdges(t *testing.T) {
	_, names := namesOf(t, namedProject("120", []string{
		marker(1, "10", `"Backwards"`, 1), // an end at or before the start leaves a plain marker
		marker(1, "5", `""`, 1),
		marker(2, "20", `"Never ended"`, 1), // still open at the end of the file: a plain marker
		marker(3, "30", `"Marker three"`, 0),
		marker(3, "40", `"Region three"`, 1), // a marker #3 beside a region #3 does not pair
		marker(3, "50", `""`, 1),
		marker(4, "60", `"Equal"`, 1),
		marker(4, "60", `""`, 1),
	}, nil))
	want := []SongMarker{
		{Name: "Backwards", At: 10},
		{Name: "Never ended", At: 20},
		{Name: "Marker three", At: 30},
		{Name: "Region three", At: 40, End: 50},
		{Name: "Equal", At: 60},
	}
	if !reflect.DeepEqual(names.Markers, want) || names.MarkersLeftOut != 0 {
		t.Fatalf("markers = %+v (left out %d)\nwant      %+v", names.Markers, names.MarkersLeftOut, want)
	}
}

func TestReadSongDetails_TheByteBudgetCutsNamesThatEscapeToMoreBytes(t *testing.T) {
	// 80 characters that JSON escapes to six bytes each cost 480: 25 fit in 12 KiB.
	heavy := strings.Repeat("<", maxSongNameRunes)
	var tracks []namedTrack
	for i := 0; i < 40; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(heavy)}})
	}
	_, names := namesOf(t, namedProject("", nil, tracks))
	if len(names.Tracks) != maxSongNameBytes/480 || names.TracksLeftOut != 40-maxSongNameBytes/480 {
		t.Fatalf("kept %d, left out %d", len(names.Tracks), names.TracksLeftOut)
	}
	// Ordinary 80-character names are far under the budget: all 128 fit.
	plain := strings.Repeat("n", maxSongNameRunes)
	tracks = tracks[:0]
	for i := 0; i < 128; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(plain)}})
	}
	_, names = namesOf(t, namedProject("", nil, tracks))
	if len(names.Tracks) != 128 || names.TracksLeftOut != 0 {
		t.Fatalf("128 plain names: kept %d, left out %d", len(names.Tracks), names.TracksLeftOut)
	}
}

func TestSongName_CleansWhatIsKeptAndDropsWhatIsNot(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"plain", "Lead vocal", "Lead vocal", true},
		{"trimmed", "   Kick   ", "Kick", true},
		{"control characters removed", "Ki\x00c\x1bk\x7f\u0085", "Kick", true},
		{"line separators removed", "A\u2028B\u2029C", "ABC", true},
		{"bidi controls removed", "A\u202eB\u2066C\u2069D", "ABCD", true},
		{"invalid utf-8 dropped", "Ba\xffss\xc3", "Bass", true},
		{"cut at 80 runes", strings.Repeat("é", 100), strings.Repeat("é", 80), true},
		{"exactly 80 runes", strings.Repeat("x", 80), strings.Repeat("x", 80), true},
		{"emoji kept", "Drums 🥁", "Drums 🥁", true},
		{"markup kept as text", `<b>"Night" & Day</b>`, `<b>"Night" & Day</b>`, true},
		{"a slash inside a word", "Vox L/R", "Vox L/R", true},
		{"a band", "AC/DC", "AC/DC", true},
		{"empty", "", "", false},
		{"only controls", "\x00\x01\x02", "", false},
		{"only spaces", "    ", "", false},
		{"absolute path", "/Users/me/Music/kick.wav", "", false},
		{"home path", "~/Music/kick.wav", "", false},
		{"home shorthand", "~", "", false},
		{"relative path", "../samples/kick.wav", "", false},
		{"dot path", "./kick.wav", "", false},
		{"dots", "..", "", false},
		{"windows path", `C:\Samples\kick.wav`, "", false},
		{"windows path with a slash", "D:/Samples/kick.wav", "", false},
		{"a backslash anywhere", `Kick\Snare`, "", false},
		{"a path after a word", "Bounce /Users/me/out.wav", "", false},
		{"a path after a bracket", "Kick (/Users/me/kick.wav)", "", false},
	}
	var scratch songNameScratch
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := songName([]byte(tc.raw), &scratch)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("songName(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
			}
			if ok && !validSongName(got) {
				t.Fatalf("the reader kept %q but the loader would reject it", got)
			}
		})
	}
}

// Every name the reader keeps must be one the document accepts when it is
// loaded, or a saved library would turn corrupt.
func TestSongName_EveryKeptNameIsOneTheLoaderAccepts(t *testing.T) {
	inputs := []string{
		`<&>`, `<<<<<>>>>>&&&&&`, "🥁🎸🎹", "e\u0301\u0301\u0301", "\u0080\u0090\u009f", "tab\tinside",
		strings.Repeat("🥁", 80), strings.Repeat("🥁", 200), strings.Repeat("a", 80) + " ", " " + strings.Repeat("b", 79),
		"a\u202eb", "mixed \x00 \xff 🥁 end", `quote " and \ backslash`, "\u00a0nbsp\u00a0", "\u3000wide space\u3000",
	}
	var scratch songNameScratch
	for _, raw := range inputs {
		name, ok := songName([]byte(raw), &scratch)
		if !ok {
			continue
		}
		if !validSongName(name) {
			t.Errorf("songName(%q) = %q: not a valid stored name", raw, name)
		}
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxSongNameRunes {
			t.Errorf("songName(%q) = %q: not valid UTF-8 or too long", raw, name)
		}
	}
	for _, name := range []string{"", " lead", "trail ", "a\x00b", "a\u202eb", "/x", strings.Repeat("x", 81), "\xff"} {
		if validSongName(name) {
			t.Errorf("validSongName(%q) accepted a name no reader would keep", name)
		}
	}
}

func TestSongNameCost_CountsTheEscapedBytes(t *testing.T) {
	for name, want := range map[string]int{"": 0, "abc": 3, `a"b`: 4, `a\b`: 4, "<>&": 18, "é": 2, "🥁": 4, `"<`: 8} {
		if got := songNameCost(name); got != want {
			t.Errorf("songNameCost(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestLineValues(t *testing.T) {
	var out [4][]byte
	cases := []struct {
		line    string
		key     string
		want    []string
		matched bool
		ok      bool
	}{
		{`NAME "Lead vocal"`, "NAME", []string{"Lead vocal"}, true, true},
		{"NAME\tKick", "NAME", []string{"Kick"}, true, true},
		{`MARKER 1 2.5 "A b" 0`, "MARKER", []string{"1", "2.5", "A b", "0"}, true, true},
		{`MARKER 1 2 'x' 4 5 6`, "MARKER", []string{"1", "2", "x", "4"}, true, true}, // stops at len(out)
		{`NAME ""`, "NAME", []string{""}, true, true},
		{`NAME`, "NAME", nil, false, false},
		{`NAMED "x"`, "NAME", nil, false, false},
		{`NAME "open`, "NAME", nil, true, false},
		{`NAME "a"b`, "NAME", nil, true, false},
		{`NAME`, "", nil, false, false},
		{`OTHER "x"`, "NAME", nil, false, false},
	}
	for _, tc := range cases {
		n, matched, ok := lineValues([]byte(tc.line), tc.key, out[:])
		if matched != tc.matched || ok != tc.ok {
			t.Errorf("lineValues(%q, %q) matched=%v ok=%v, want %v %v", tc.line, tc.key, matched, ok, tc.matched, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		var got []string
		for _, v := range out[:n] {
			got = append(got, string(v))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("lineValues(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestReadSongDetails_OverCapAndMalformedAreToldApart(t *testing.T) {
	small := SongFactsLimits{MaxBytes: 4 << 10, MaxLineBytes: 256}
	grammar := factsGrammar("reaper")
	cases := []struct {
		name    string
		project string
		limits  SongFactsLimits
		overCap bool
	}{
		{"a file over the size cap", namedProject("", nil, []namedTrack{{lines: []string{nameLine(strings.Repeat("n", 60))}, items: make([][2]string, 80)}}), small, true},
		{"a line over the line cap", "<REAPER_PROJECT 0.1\n  NOTE " + strings.Repeat("x", 4000) + "\n>\n", SongFactsLimits{MaxBytes: 1 << 20, MaxLineBytes: 1024}, true},
		{"a project cut off", "<REAPER_PROJECT 0.1\n  TEMPO 100 4 4\n  <TRACK\n    NAME \"x\"\n", DefaultSongFactsLimits, false},
		{"not a project", "this is not a project\n", DefaultSongFactsLimits, false},
		{"binary", "<REAPER_PROJECT 0.1\n\x00\x01\n>\n", DefaultSongFactsLimits, false},
		{"empty", "", DefaultSongFactsLimits, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts, names, err := ReadSongDetails(strings.NewReader(tc.project), grammar, tc.limits)
			if !errors.Is(err, ErrSongFactsUnreadable) {
				t.Fatalf("err = %v, want an unreadable refusal", err)
			}
			if got := errors.Is(err, errSongFactsOverCap); got != tc.overCap {
				t.Fatalf("over a cap = %v, want %v (%v)", got, tc.overCap, err)
			}
			if facts != (SongFacts{}) || names != nil {
				t.Fatalf("a refused file returned details: %+v %+v", facts, names)
			}
		})
	}
	// A read failure is not a verdict on the content: it stays a plain error.
	_, names, err := ReadSongDetails(io.MultiReader(strings.NewReader("<REAPER_PROJECT 0.1\n"), errReader{}), grammar, DefaultSongFactsLimits)
	if err == nil || errors.Is(err, ErrSongFactsUnreadable) || names != nil {
		t.Fatalf("an I/O failure = %v, %+v", err, names)
	}
}

// FuzzReadSongDetails: whatever the file holds, a read either refuses or returns
// names within every cap and that the loader would accept.
func FuzzReadSongDetails(f *testing.F) {
	f.Add(namedProject("92", []string{marker(1, "1", `"Intro"`, 0), marker(2, "2", `"R"`, 1), marker(2, "9", `""`, 1)},
		[]namedTrack{{lines: []string{nameLine("Kick")}}, {lines: []string{`NAME 'x'`}}}))
	f.Add("<REAPER_PROJECT 1\n  MARKER 1 2 \"a\" 1\n  <TRACK\n    NAME \"\xff" + string(rune(0x202e)) + "/etc/passwd\"\n  >\n>\n")
	f.Fuzz(func(t *testing.T, project string) {
		facts, names, err := ReadSongDetails(strings.NewReader(project), factsGrammar("reaper"), SongFactsLimits{MaxBytes: 1 << 20, MaxLineBytes: 4096})
		if err != nil {
			if names != nil || facts != (SongFacts{}) {
				t.Fatalf("a refusal returned details: %+v %+v", facts, names)
			}
			return
		}
		if !facts.valid() {
			t.Fatalf("invalid facts %+v", facts)
		}
		if names == nil {
			t.Fatal("a grammar that gives names returned none")
		}
		if len(names.Tracks) > maxSongTracks || len(names.Markers) > maxSongMarkers ||
			names.TracksLeftOut < 0 || names.TracksLeftOut > maxSongLeftOut || names.MarkersLeftOut < 0 || names.MarkersLeftOut > maxSongLeftOut {
			t.Fatalf("a cap was passed: %d tracks, %d markers, left out %d/%d", len(names.Tracks), len(names.Markers), names.TracksLeftOut, names.MarkersLeftOut)
		}
		for _, name := range names.Tracks {
			if !validSongName(name) {
				t.Fatalf("kept track name %q is not valid", name)
			}
		}
		for _, m := range names.Markers {
			if m.Name != "" && !validSongName(m.Name) {
				t.Fatalf("kept marker name %q is not valid", m.Name)
			}
			if m.At < 0 || m.At > maxSongLength || m.End != 0 && (m.End <= m.At || m.End > maxSongLength) {
				t.Fatalf("marker times out of range: %+v", m)
			}
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("disk went away") }

// streamReader makes a very large project without holding it: header, then the
// same track-and-marker unit until target bytes were served, then the closing
// line. onChunk sees the bytes served so far, so a test can sample the heap while
// the reader is in the middle of the file.
type streamReader struct {
	target, served int64
	pending        []byte
	stage          int
	onChunk        func(served int64)
}

const streamUnit = "  MARKER 7 12.5 \"Verse\" 0 0 1 R {C0FFEE00-0000-0000-0000-000000000000} 0\n" +
	"  <TRACK\n    NAME \"Track\"\n    <ITEM\n      POSITION 1\n      LENGTH 2\n    >\n  >\n"

func (s *streamReader) Read(p []byte) (int, error) {
	if len(s.pending) == 0 {
		switch {
		case s.stage == 0:
			s.pending, s.stage = []byte("<REAPER_PROJECT 0.1 \"7.27\" 1\n  TEMPO 100 4 4\n"), 1
		case s.stage == 1 && s.served < s.target:
			s.pending = []byte(strings.Repeat(streamUnit, 64))
		case s.stage == 1:
			s.pending, s.stage = []byte(">\n"), 2
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	s.served += int64(n)
	if s.onChunk != nil {
		s.onChunk(s.served)
	}
	return n, nil
}

// The memory a read holds does not grow with the file: a file hundreds of times
// larger leaves the live heap about where a small one does.
func TestReadSongDetails_MemoryDoesNotGrowWithTheFile(t *testing.T) {
	live := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	measure := func(target int64) (peak uint64, facts SongFacts, names *SongNames) {
		base := live()
		nextSample := int64(1 << 20)
		stream := &streamReader{target: target}
		stream.onChunk = func(served int64) {
			if served >= nextSample {
				nextSample += 4 << 20
				if now := live(); now > base && now-base > peak {
					peak = now - base
				}
			}
		}
		var err error
		facts, names, err = ReadSongDetails(stream, factsGrammar("reaper"), DefaultSongFactsLimits)
		if err != nil {
			t.Fatalf("read of %d bytes: %v", target, err)
		}
		return peak, facts, names
	}
	smallPeak, smallFacts, smallNames := measure(256 << 10)
	bigPeak, bigFacts, bigNames := measure(24 << 20)
	t.Logf("live heap above baseline: %d KiB for a 256 KiB file, %d KiB for a 24 MiB file", smallPeak>>10, bigPeak>>10)

	// What was kept is capped, however large the file.
	for label, names := range map[string]*SongNames{"small": smallNames, "big": bigNames} {
		if len(names.Tracks) != 128 || len(names.Markers) != 128 {
			t.Fatalf("%s: kept %d track names and %d markers", label, len(names.Tracks), len(names.Markers))
		}
	}
	if bigNames.TracksLeftOut <= smallNames.TracksLeftOut || bigNames.MarkersLeftOut <= smallNames.MarkersLeftOut {
		t.Fatalf("the big file should have more left out: %+v vs %+v", bigNames, smallNames)
	}
	if smallFacts.TrackCount == 0 || smallFacts.TempoBPM != 100 || bigFacts.TempoBPM != 100 {
		t.Fatalf("numbers lost: %+v %+v", smallFacts, bigFacts)
	}
	// More tracks than the count's own bound is an unknown count, as before.
	if bigFacts.TrackCount != 0 {
		t.Fatalf("a file past the track bound reported %d tracks", bigFacts.TrackCount)
	}
	// A 24 MiB file is ~96x the small one; anything that grew with it would be
	// megabytes. The reader holds one line, the stack and the capped names.
	const limit = 2 << 20
	if bigPeak > limit {
		t.Fatalf("live heap grew by %d KiB while reading a 24 MiB file (limit %d KiB)", bigPeak>>10, limit>>10)
	}
}
