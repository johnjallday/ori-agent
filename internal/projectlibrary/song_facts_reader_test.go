package projectlibrary

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// readRPP reads with the grammar the host table declares for REAPER projects.
func readRPP(r io.Reader, limits SongFactsLimits) (SongFacts, error) {
	return ReadSongFacts(r, factsGrammar("reaper"), limits)
}

func TestReadSongFacts_TheTableDeclaresOnlyTheREAPERGrammar(t *testing.T) {
	if factsGrammar("reaper") == nil {
		t.Fatal("the REAPER marker declares no facts grammar")
	}
	for _, format := range []string{"ableton", "logic", "", "unknown"} {
		if factsGrammar(format) != nil {
			t.Fatalf("%q declares facts", format)
		}
	}
	if !factsFile("reaper", "Song.RPP") || factsFile("reaper", "Song.rpp-bak") || factsFile("reaper", "Song.als") ||
		factsFile("ableton", "Song.als") {
		t.Fatal("factsFile matched the wrong files")
	}
	project := rppProject("92", nil, nil)
	for _, grammar := range []*folderdigest.ProjectFacts{nil, {}, {Root: "REAPER_PROJECT"}} {
		if _, err := ReadSongFacts(strings.NewReader(project), grammar, DefaultSongFactsLimits); !errors.Is(err, ErrSongFactsUnreadable) {
			t.Fatalf("grammar %+v read facts: %v", grammar, err)
		}
	}
}

// rppProject builds a minimal project shaped like one REAPER saves: a header,
// the TEMPO line, optional tempo-envelope points, then tracks with items. A
// SOURCE section inside each item carries its own LENGTH, which must not count.
func rppProject(tempo string, points []string, tracks [][][2]string) string {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1 \"7.27/macOS-arm64\" 1727800000\n")
	b.WriteString("  <NOTES 0 2\n    |Mix notes the user typed\n  >\n")
	if tempo != "" {
		b.WriteString("  TEMPO " + tempo + " 4 4\n")
	}
	if points != nil {
		b.WriteString("  <TEMPOENVEX\n    EGUID {BCDBD6D5-9DE6-AE4C-AF1F-047643CB4BD0}\n    ACT 0 -1\n")
		for _, point := range points {
			b.WriteString("    PT " + point + "\n")
		}
		b.WriteString("  >\n")
	}
	for _, items := range tracks {
		b.WriteString("  <TRACK {A982F44F-895F-4943-B6F7-299C7949DF39}\n    NAME \"Lead vocal\"\n")
		b.WriteString("    <FXCHAIN\n      <VST \"VST: ReaEQ\" reaeq.vst 0 \"\" 1919247729<56535472656571>\n        cmVlcWVkAAAAAAAAAAAAAAAAAA==\n      >\n    >\n")
		for _, item := range items {
			b.WriteString("    <ITEM\n      POSITION " + item[0] + "\n      SNAPOFFS 0\n      LENGTH " + item[1] + "\n")
			b.WriteString("      <SOURCE SECTION\n        LENGTH 9999\n        <SOURCE WAVE\n          FILE \"Media/take.wav\"\n        >\n      >\n    >\n")
		}
		b.WriteString("  >\n")
	}
	b.WriteString("  <EXTENSIONS\n  >\n>\n")
	return b.String()
}

func TestReadSongFacts_ReadsTheThreeFacts(t *testing.T) {
	cases := []struct {
		name    string
		project string
		want    SongFacts
	}{
		{
			name:    "tempo, tracks and the end of the last item",
			project: rppProject("92", nil, [][][2]string{{{"0", "24"}, {"200", "21.4"}}, {{"10", "5"}}, {}}),
			want:    SongFacts{TempoBPM: 92, LengthSeconds: 221.4, TrackCount: 3},
		},
		{
			name:    "tempo to one decimal",
			project: rppProject("128.04999", nil, [][][2]string{{{"0", "1"}}}),
			want:    SongFacts{TempoBPM: 128, LengthSeconds: 1, TrackCount: 1},
		},
		{
			name:    "a tempo envelope with changes varies",
			project: rppProject("92", []string{"0 92 1", "60 96 1", "120 92 1"}, [][][2]string{{{"0", "10"}}}),
			want:    SongFacts{TempoBPM: 92, TempoVaries: true, LengthSeconds: 10, TrackCount: 1},
		},
		{
			name:    "an envelope point at the base tempo does not vary",
			project: rppProject("120", []string{"0 120 0"}, [][][2]string{{{"0", "10"}}}),
			want:    SongFacts{TempoBPM: 120, LengthSeconds: 10, TrackCount: 1},
		},
		{
			name:    "meter-only envelope points do not vary",
			project: rppProject("100", []string{"0 100 1 262148", "30 100 1 196612"}, [][][2]string{{{"0", "10"}}}),
			want:    SongFacts{TempoBPM: 100, LengthSeconds: 10, TrackCount: 1},
		},
		{
			name:    "an empty envelope does not vary",
			project: rppProject("160", []string{}, [][][2]string{{{"0", "24"}}}),
			want:    SongFacts{TempoBPM: 160, LengthSeconds: 24, TrackCount: 1},
		},
		{
			name:    "no items has no length",
			project: rppProject("92", nil, [][][2]string{{}, {}}),
			want:    SongFacts{TempoBPM: 92, TrackCount: 2},
		},
		{
			name:    "no tracks",
			project: rppProject("92", nil, nil),
			want:    SongFacts{TempoBPM: 92},
		},
		{
			name:    "no tempo line",
			project: rppProject("", nil, [][][2]string{{{"0", "3"}}}),
			want:    SongFacts{LengthSeconds: 3, TrackCount: 1},
		},
		{
			name:    "a tempo out of REAPER's range is unknown",
			project: rppProject("4000", nil, [][][2]string{{{"0", "3"}}}),
			want:    SongFacts{LengthSeconds: 3, TrackCount: 1},
		},
		{
			name:    "an item with an unreadable position does not count",
			project: rppProject("92", nil, [][][2]string{{{"x", "3"}, {"1", "2"}}}),
			want:    SongFacts{TempoBPM: 92, LengthSeconds: 3, TrackCount: 1},
		},
		{
			name:    "negative and infinite values do not count",
			project: rppProject("92", nil, [][][2]string{{{"-5", "100"}, {"1", "Inf"}, {"2", "NaN"}, {"1", "1"}}}),
			want:    SongFacts{TempoBPM: 92, LengthSeconds: 2, TrackCount: 1},
		},
		{
			name:    "an hour-long project",
			project: rppProject("60", nil, [][][2]string{{{"3600", "125.5"}}}),
			want:    SongFacts{TempoBPM: 60, LengthSeconds: 3725.5, TrackCount: 1},
		},
		{
			name: "windows line endings and tabs",
			project: strings.ReplaceAll(strings.ReplaceAll(
				rppProject("92", nil, [][][2]string{{{"0", "4"}}}), "\n", "\r\n"), "  ", "\t"),
			want: SongFacts{TempoBPM: 92, LengthSeconds: 4, TrackCount: 1},
		},
		{
			name: "a nested project-like block is not counted",
			project: "<REAPER_PROJECT 0.1 \"7.0\" 1\n  TEMPO 90 4 4\n  <TRACK\n    <ITEM\n      POSITION 0\n      LENGTH 2\n" +
				"      <SOURCE RPP_PROJECT\n        <RPP_PROJECT\n          <TRACK\n          >\n        >\n      >\n    >\n  >\n>\n",
			want: SongFacts{TempoBPM: 90, LengthSeconds: 2, TrackCount: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readRPP(strings.NewReader(tc.project), DefaultSongFactsLimits)
			if err != nil {
				t.Fatalf("ReadSongFacts: %v", err)
			}
			if got != tc.want {
				t.Fatalf("facts = %+v, want %+v", got, tc.want)
			}
			if !got.valid() {
				t.Fatalf("facts %+v are not valid", got)
			}
		})
	}
}

func TestReadSongFacts_RefusesWhatIsNotAProjectREAPERSaved(t *testing.T) {
	small := SongFactsLimits{MaxBytes: 1024, MaxLineBytes: 128}
	project := rppProject("92", nil, [][][2]string{{{"0", "4"}}})
	cases := []struct {
		name    string
		content string
		limits  SongFactsLimits
	}{
		{"empty file", "", DefaultSongFactsLimits},
		{"only blank lines", "\n\n   \n", DefaultSongFactsLimits},
		{"another format", "<Ableton MajorVersion=\"5\">\n", DefaultSongFactsLimits},
		{"a longer first word", "<REAPER_PROJECTX 0.1\n  TEMPO 92 4 4\n>\n", DefaultSongFactsLimits},
		{"a header later in the file", "hello\n" + project, DefaultSongFactsLimits},
		{"binary content", "<REAPER_PROJECT 0.1 \"7.0\" 1\n  TEMPO 92\x00 4 4\n>\n", DefaultSongFactsLimits},
		{"cut off before its end", strings.TrimSuffix(project, ">\n"), DefaultSongFactsLimits},
		{"more closes than opens", project + ">\n", DefaultSongFactsLimits},
		{"content after the project", project + "TEMPO 120 4 4\n", DefaultSongFactsLimits},
		{"over the size cap", project + strings.Repeat("\n", 1024), small},
		{"a very long line", "<REAPER_PROJECT 0.1\n  NAME " + strings.Repeat("a", 200) + "\n>\n", small},
		{"nested too deep", "<REAPER_PROJECT 0.1\n" + strings.Repeat("<X\n", maxSongFactsDepth) + strings.Repeat(">\n", maxSongFactsDepth) + ">\n", DefaultSongFactsLimits},
		{"no limits", project, SongFactsLimits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := readRPP(strings.NewReader(tc.content), tc.limits)
			if !errors.Is(err, ErrSongFactsUnreadable) {
				t.Fatalf("err = %v, want ErrSongFactsUnreadable", err)
			}
			if facts != (SongFacts{}) {
				t.Fatalf("facts = %+v, want none", facts)
			}
		})
	}
}

func TestReadSongFacts_ReadErrorIsNotARefusal(t *testing.T) {
	project := rppProject("92", nil, [][][2]string{{{"0", "4"}}})
	facts, err := readRPP(iotest.TimeoutReader(iotest.HalfReader(strings.NewReader(project))), DefaultSongFactsLimits)
	if err == nil || errors.Is(err, ErrSongFactsUnreadable) || facts != (SongFacts{}) {
		t.Fatalf("an I/O failure gave %+v, %v; want no facts and an error that is not a refusal", facts, err)
	}
}

func TestReadSongFacts_CountsAtMostTheTrackBound(t *testing.T) {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1\n  TEMPO 92 4 4\n")
	for i := 0; i <= maxSongTrackCount; i++ {
		b.WriteString("  <TRACK\n  >\n")
	}
	b.WriteString(">\n")
	facts, err := readRPP(strings.NewReader(b.String()), DefaultSongFactsLimits)
	if err != nil {
		t.Fatal(err)
	}
	if facts.TrackCount != 0 || facts.TempoBPM != 92 {
		t.Fatalf("facts = %+v, want tempo only (track count over the bound is unknown)", facts)
	}
}

func FuzzReadSongFacts(f *testing.F) {
	f.Add(rppProject("92", []string{"0 92 1", "60 96 1"}, [][][2]string{{{"0", "24"}, {"200", "21.4"}}, {}}))
	f.Add("<REAPER_PROJECT 0.1\n  TEMPO 120 4 4\n>\n")
	f.Add("<REAPER_PROJECT\n<TRACK\n<ITEM\nPOSITION 1e308\nLENGTH 1e308\n>\n>\n>\n")
	f.Add("<REAPER_PROJECT 0.1\n>\n>\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, content string) {
		facts, err := readRPP(strings.NewReader(content), SongFactsLimits{MaxBytes: 1 << 16, MaxLineBytes: 1 << 10})
		if err != nil {
			if !errors.Is(err, ErrSongFactsUnreadable) || facts != (SongFacts{}) {
				t.Fatalf("refusal returned %+v, %v", facts, err)
			}
			return
		}
		if !facts.valid() {
			t.Fatalf("facts %+v are out of bounds", facts)
		}
	})
}
