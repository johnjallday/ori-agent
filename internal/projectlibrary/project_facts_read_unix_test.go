//go:build darwin || linux || freebsd

package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"golang.org/x/sys/unix"
)

// assistant-reads-projects 4.2 to 4.4: one batch of songs read through an app
// plugin's reader, with the host's identity checks and bounds.

// readerRoot is a root on disk with one song folder per name, each holding
// "<name>.zz", and the targets the listing would hand the pass.
func readerRoot(t *testing.T, names ...string) (Root, []songFactsTarget) {
	t.Helper()
	dir := t.TempDir()
	var rootStat unix.Stat_t
	if err := unix.Stat(dir, &rootStat); err != nil {
		t.Fatal(err)
	}
	root := Root{ID: "root", Path: dir, FileIdentity: unixIdentity(&rootStat)}
	var targets []songFactsTarget
	for _, name := range names {
		folder := filepath.Join(dir, name)
		if err := os.MkdirAll(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(folder, name+".zz")
		if err := os.WriteFile(file, []byte("<PROJECT "+name+"\n>\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, songTarget(t, root, name))
	}
	return root, targets
}

// songTarget is the target the listing would record for a song folder now.
func songTarget(t *testing.T, root Root, name string) songFactsTarget {
	t.Helper()
	var folderStat, fileStat unix.Stat_t
	if err := unix.Stat(filepath.Join(root.Path, name), &folderStat); err != nil {
		t.Fatal(err)
	}
	if err := unix.Stat(filepath.Join(root.Path, name, name+".zz"), &fileStat); err != nil {
		t.Fatal(err)
	}
	return songFactsTarget{Relative: name, FolderIdentity: unixIdentity(&folderStat), Format: "zzfmt",
		File: FactsSource{File: name + ".zz", Identity: unixIdentity(&fileStat), Size: fileStat.Size,
			ModifiedAt: time.Unix(fileStat.Mtim.Unix()).UTC()}}
}

// fakeReader answers each file with the result its song name chooses, and
// records what it was sent.
type fakeReader struct {
	results map[string]string // file base name -> a JSON object without "id"
	err     error
	inputs  []projectFactsInput
	during  func() // runs while the call is in flight
}

func (f *fakeReader) call(_ context.Context, _ plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
	var in projectFactsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	f.inputs = append(f.inputs, in)
	if f.during != nil {
		f.during()
	}
	if f.err != nil {
		return nil, f.err
	}
	var parts []string
	for _, file := range in.Files {
		body := f.results[filepath.Base(file.Path)]
		if body == "" {
			body = `"result":"read","tempo_bpm":120,"length_seconds":60,"track_count":2`
		}
		parts = append(parts, `{"id":`+itoa(file.ID)+`,`+body+`}`)
	}
	return json.RawMessage(`{"schema_version":1,"files":[` + strings.Join(parts, ",") + `]}`), nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var testReader = projectReader{batch: 8}

func TestProjectFactsBatch_SendsTheListedFilesAndStoresWhatCameBack(t *testing.T) {
	root, targets := readerRoot(t, "One", "Two", "Three")
	reader := &fakeReader{results: map[string]string{
		"Two.zz":   `"result":"too_large"`,
		"Three.zz": `"result":"unreadable"`,
	}}
	reads, counts, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, reader.call)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (projectFactsBatchCounts{Read: 1, TooLarge: 1, Unreadable: 1}) || len(reads) != 3 {
		t.Fatalf("counts = %+v reads = %+v", counts, reads)
	}
	if reads[0].Facts != (SongFacts{TempoBPM: 120, LengthSeconds: 60, TrackCount: 2}) || reads[0].Names != nil {
		t.Fatalf("first = %+v", reads[0])
	}
	if reads[1].NotRead != NotReadTooLarge || reads[2].NotRead != NotReadUnreadable {
		t.Fatalf("reasons = %q %q", reads[1].NotRead, reads[2].NotRead)
	}
	// The plugin was told the absolute paths the host resolved, the listed
	// sizes, the format, and that names are not allowed.
	in := reader.inputs[0]
	if in.Names || in.MaxFileBytes != DefaultSongFactsLimits.MaxBytes || len(in.Files) != 3 {
		t.Fatalf("input = %+v", in)
	}
	if in.Files[0].Path != filepath.Join(root.Path, "One", "One.zz") || in.Files[0].Size != targets[0].File.Size || in.Files[0].Format != "zzfmt" {
		t.Fatalf("file input = %+v", in.Files[0])
	}
}

func TestProjectFactsBatch_AFileOverTheCapIsNeverSent(t *testing.T) {
	root, targets := readerRoot(t, "Big", "Small")
	targets[0].File.Size = DefaultSongFactsLimits.MaxBytes + 1
	reader := &fakeReader{}
	reads, counts, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, reader.call)
	if err != nil {
		t.Fatal(err)
	}
	if counts.TooLarge != 1 || counts.Read != 1 || len(reader.inputs[0].Files) != 1 || reads[0].NotRead != NotReadTooLarge {
		t.Fatalf("counts = %+v inputs = %+v", counts, reader.inputs)
	}
}

// FR26: a file swapped, resized or replaced by a link while the plugin read it
// gives nothing and is not stored; the others in the batch are kept.
func TestProjectFactsBatch_AFileChangedDuringTheReadGivesNothing(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, root Root){
		"re-saved larger": func(t *testing.T, root Root) {
			if err := os.WriteFile(filepath.Join(root.Path, "Two", "Two.zz"), []byte("<PROJECT longer now\n>\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"replaced by another file": func(t *testing.T, root Root) {
			path := filepath.Join(root.Path, "Two", "Two.zz")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("<PROJECT Two\n>\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"replaced by a link": func(t *testing.T, root Root) {
			path := filepath.Join(root.Path, "Two", "Two.zz")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root.Path, "One", "One.zz"), path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, targets := readerRoot(t, "One", "Two")
			reader := &fakeReader{during: func() { change(t, root) }}
			reads, counts, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, reader.call)
			if err != nil {
				t.Fatal(err)
			}
			if counts.Changed != 1 || counts.Read != 1 || len(reads) != 1 || reads[0].Relative != "One" {
				t.Fatalf("counts = %+v reads = %+v", counts, reads)
			}
		})
	}
}

// FR27: a failed call, or an answer outside the contract, drops the batch.
func TestProjectFactsBatch_ACallErrorDropsTheBatch(t *testing.T) {
	root, targets := readerRoot(t, "One", "Two")
	failing := &fakeReader{err: errors.New("timed out")}
	if reads, _, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, failing.call); !errors.Is(err, errProjectFactsCall) || reads != nil {
		t.Fatalf("a failed call = %v %+v", err, reads)
	}
	for name, output := range map[string]string{
		"one answer for two files": `{"schema_version":1,"files":[{"id":0,"result":"read"}]}`,
		"a repeated id":            `{"schema_version":1,"files":[{"id":0,"result":"read"},{"id":0,"result":"read"}]}`,
		"an id out of range":       `{"schema_version":1,"files":[{"id":0,"result":"read"},{"id":5,"result":"read"}]}`,
		"another schema":           `{"schema_version":2,"files":[{"id":0,"result":"read"},{"id":1,"result":"read"}]}`,
	} {
		call := func(context.Context, plugin.InstalledPlugin, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(output), nil
		}
		if reads, _, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, call); !errors.Is(err, errProjectFactsCall) || reads != nil {
			t.Errorf("%s = %v %+v", name, err, reads)
		}
	}
	// A file the plugin could not read this time is not stored.
	reader := &fakeReader{results: map[string]string{"Two.zz": `"result":"failed"`}}
	reads, counts, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, testReader, reader.call)
	if err != nil || counts.Failed != 1 || len(reads) != 1 {
		t.Fatalf("failed file = %v %+v %+v", err, counts, reads)
	}
}

func TestProjectFactsBatch_CarriesAtMostTheReadersBatch(t *testing.T) {
	root, targets := readerRoot(t, "A", "B", "C")
	reader := &fakeReader{}
	reads, _, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{}, projectReader{batch: 2}, reader.call)
	if err != nil || len(reader.inputs[0].Files) != 2 || len(reads) != 2 {
		t.Fatalf("batch of 2 = %v, sent %d, reads %d", err, len(reader.inputs[0].Files), len(reads))
	}
}

// FR24, FR25: every value is bounded again; names only under a names grant,
// cleaned and capped exactly as the host's own reader keeps them.
func TestMapProjectFacts(t *testing.T) {
	target := songFactsTarget{Relative: "One", Format: "zzfmt"}
	read := func(result projectFactsResult, names bool) songFactsRead {
		t.Helper()
		got, keep := mapProjectFacts(target, result, songFactsGrant{names: names})
		if !keep {
			t.Fatalf("dropped %+v", result)
		}
		return got
	}
	base := projectFactsResult{Result: "read", TempoBPM: 92.04, TempoVaries: true, LengthSeconds: 221.12345, TrackCount: 14,
		Tracks: []string{"Kick", "Vox L/R", "/Users/me/Audio", "  Bass  "}, Markers: []projectFactsMarker{{Name: "Verse", At: 10}, {Name: "Chorus", At: 40, End: 72.25}}}
	got := read(base, true)
	if got.Facts != (SongFacts{TempoBPM: 92, TempoVaries: true, LengthSeconds: 221.123, TrackCount: 14}) {
		t.Fatalf("facts = %+v", got.Facts)
	}
	if strings.Join(got.Names.Tracks, "|") != "Kick|Vox L/R|Bass" || len(got.Names.Markers) != 2 || got.Names.Markers[1].End != 72.25 {
		t.Fatalf("names = %+v", got.Names)
	}
	// Names are never kept without the grant, whatever the plugin returned.
	if read(base, false).Names != nil {
		t.Fatal("names kept under a numbers-only grant")
	}
	// Out-of-bounds values are dropped one by one.
	for name, tc := range map[string]struct {
		in   projectFactsResult
		want SongFacts
	}{
		"tempo too high":           {projectFactsResult{Result: "read", TempoBPM: 2000, TempoVaries: true, TrackCount: 3}, SongFacts{TrackCount: 3}},
		"tempo zero with varies":   {projectFactsResult{Result: "read", TempoVaries: true}, SongFacts{}},
		"length over 100 hours":    {projectFactsResult{Result: "read", LengthSeconds: 360001, TrackCount: 1}, SongFacts{TrackCount: 1}},
		"negative length":          {projectFactsResult{Result: "read", LengthSeconds: -1, TempoBPM: 100}, SongFacts{TempoBPM: 100}},
		"too many tracks":          {projectFactsResult{Result: "read", TrackCount: 10001, TempoBPM: 100}, SongFacts{TempoBPM: 100}},
		"tempo rounded to a tenth": {projectFactsResult{Result: "read", TempoBPM: 119.96}, SongFacts{TempoBPM: 120}},
	} {
		if got := read(tc.in, false).Facts; got != tc.want {
			t.Errorf("%s = %+v, want %+v", name, got, tc.want)
		}
	}
	// 129 names keep 128 and count the rest; the plugin's own left-out adds up.
	many := projectFactsResult{Result: "read", TracksLeftOut: 5}
	for i := 0; i < 129; i++ {
		many.Tracks = append(many.Tracks, "Track "+itoa(i))
	}
	if names := read(many, true).Names; len(names.Tracks) != 128 || names.TracksLeftOut != 6 {
		t.Fatalf("caps = %d tracks, %d left out", len(names.Tracks), names.TracksLeftOut)
	}
	// A region whose end is not after its start is a plain marker; a marker
	// whose name is dropped keeps its time.
	odd := projectFactsResult{Result: "read", Markers: []projectFactsMarker{{Name: "Back", At: 50, End: 20}, {Name: "../x", At: 5}}}
	if names := read(odd, true).Names; names.Markers[0].End != 0 || names.Markers[1].Name != "" || names.Markers[1].At != 5 {
		t.Fatalf("markers = %+v", names.Markers)
	}
	// A failed file, or an unknown result, is not kept.
	for _, result := range []string{"failed", "maybe", ""} {
		if _, keep := mapProjectFacts(target, projectFactsResult{Result: result}, songFactsGrant{}); keep {
			t.Errorf("%q was kept", result)
		}
	}
}
