package projectlibrary

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The host reads no project file: an app plugin's reader does, and the host
// bounds what it returns. These tests cover the bounds the host keeps (a name's
// cleaning, its stored cost), and this file holds the fixture builders the
// library's other tests use for the project files they save.

// rppProject writes a REAPER project the way the application saves one: the
// TEMPO line, optional tempo-envelope points, then tracks with items. A SOURCE
// section inside each item carries its own LENGTH, which must not count.
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
				t.Fatalf("the host kept %q but the loader would reject it", got)
			}
		})
	}
}

// Every name the host keeps must be one the document accepts when it is
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
			t.Errorf("validSongName(%q) accepted a name nothing would keep", name)
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
