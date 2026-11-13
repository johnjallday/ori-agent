//go:build darwin || linux || freebsd

package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
	"golang.org/x/sys/unix"
)

// assistant-reads-projects 4.6: the candidate plugin build stores exactly what
// Ori's own reader stores, for the same bytes. It runs the candidate's real
// binary through the real host call (plugin.SurfaceLifecycle.ProjectFacts: the
// declared input schema, timeout class, output limit and output schema), maps
// the answer with the host's own mapping, and compares it with what Ori's
// reader would have stored for the same file.
//
// Skipped unless a built candidate is named, so it never runs in CI:
//
//	scripts/project-facts-parity.sh <candidate worktree>
//
// This check and Ori's reader leave the branch together (task 7.1); the last
// green result is recorded in the findings log first.

const (
	parityBinEnv      = "ORI_PROJECT_FACTS_PARITY_BIN"
	parityManifestEnv = "ORI_PROJECT_FACTS_PARITY_MANIFEST"
	parityLimit       = int64(4 << 20)
	paritySmallLimit  = int64(2048)
)

type parityCase struct {
	name    string
	content []byte
	limit   int64 // 0 means parityLimit
}

func (c parityCase) maxBytes() int64 {
	if c.limit == 0 {
		return parityLimit
	}
	return c.limit
}

type parityRig struct {
	installed plugin.InstalledPlugin
	lifecycle *plugin.SurfaceLifecycle
	dir       string
	next      int
}

func newParityRig(t *testing.T) *parityRig {
	t.Helper()
	bin, manifest := os.Getenv(parityBinEnv), os.Getenv(parityManifestEnv)
	if bin == "" || manifest == "" {
		t.Skipf("set %s and %s to a built candidate (scripts/project-facts-parity.sh does)", parityBinEnv, parityManifestEnv)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := plugin.ParseSurfaceContribution(data)
	if err != nil {
		t.Fatalf("Ori refuses the candidate's manifest: %v", err)
	}
	// The service starts under a throwaway home, never the real one.
	t.Setenv("HOME", t.TempDir())
	manager := workspacesurface.NewServiceManager(nil)
	t.Cleanup(func() { _ = manager.Shutdown() })
	installed := plugin.InstalledPlugin{
		Name: "reaper-plugin", Version: "candidate", WorkspaceSurfaces: contribution, Generation: 1, Enabled: true,
		ResolvedArtifacts: []plugin.ResolvedArtifact{{
			ServiceID: "reaper-service", ArtifactID: "reaper-plugin-darwin-arm64", OS: runtime.GOOS, Arch: runtime.GOARCH,
			ManagedPath: bin, Available: true,
		}},
	}
	return &parityRig{installed: installed, lifecycle: plugin.NewSurfaceLifecycle(nil, manager), dir: t.TempDir()}
}

func (r *parityRig) write(t *testing.T, content []byte) string {
	t.Helper()
	r.next++
	path := filepath.Join(r.dir, fmt.Sprintf("f%05d.rpp", r.next))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// read sends one batch (at most 8 files, one limit) through the host call and
// checks the answer's own shape: one entry per id, nothing but id and result
// for a file that was not read, name fields exactly when names were asked for.
func (r *parityRig) read(t *testing.T, batch []parityCase, names bool) []projectFactsResult {
	t.Helper()
	input := projectFactsInput{SchemaVersion: 1, Names: names, MaxFileBytes: batch[0].maxBytes()}
	for id, c := range batch {
		input.Files = append(input.Files, projectFactsFileInput{
			ID: id, Format: "reaper", Path: r.write(t, c.content), Size: int64(len(c.content)),
		})
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := r.lifecycle.ProjectFacts(ctx, r.installed, encoded)
	if err != nil {
		t.Fatalf("the host call failed for a batch starting at %q (names=%v): %v", batch[0].name, names, err)
	}
	var shape struct {
		SchemaVersion int              `json:"schema_version"`
		Files         []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil || shape.SchemaVersion != 1 || len(shape.Files) != len(batch) {
		t.Fatalf("answer for a batch starting at %q is not one entry per file: %v %s", batch[0].name, err, truncate(string(raw), 300))
	}
	for id, file := range shape.Files {
		if file["id"] != float64(id) {
			t.Errorf("%q: entry %d carries id %v", batch[id].name, id, file["id"])
		}
		if file["result"] != "read" {
			if len(file) != 2 {
				t.Errorf("%q: a file that was not read carries more than id and result: %v", batch[id].name, file)
			}
			continue
		}
		for _, key := range []string{"tempo_bpm", "tempo_varies", "length_seconds", "track_count"} {
			if _, ok := file[key]; !ok {
				t.Errorf("%q: a read file lacks %s", batch[id].name, key)
			}
		}
		for _, key := range []string{"tracks", "tracks_left_out", "markers", "markers_left_out"} {
			if _, has := file[key]; has != names {
				t.Errorf("%q: names=%v but %s present=%v", batch[id].name, names, key, has)
			}
		}
	}
	var output projectFactsOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	return output.Files
}

// batches groups cases by limit and cuts each group into calls of at most 8.
func parityBatches(cases []parityCase) [][]parityCase {
	var out [][]parityCase
	for _, limit := range []int64{parityLimit, paritySmallLimit} {
		var group []parityCase
		for _, c := range cases {
			if c.maxBytes() != limit {
				continue
			}
			group = append(group, c)
			if len(group) == 8 {
				out = append(out, group)
				group = nil
			}
		}
		if len(group) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// oriStored is what Ori's own reader stores for these bytes, the way the scan's
// fact pass does (readOneSongFacts) minus the file identity checks.
func oriStored(content []byte, names bool, limit int64) (songFactsRead, bool) {
	limits := SongFactsLimits{MaxBytes: limit, MaxLineBytes: DefaultSongFactsLimits.MaxLineBytes}
	facts, details, err := ReadSongDetails(bytes.NewReader(content), factsGrammarFor("reaper", names), limits)
	if err != nil && !errors.Is(err, ErrSongFactsUnreadable) {
		return songFactsRead{}, false
	}
	if err != nil {
		reason := NotReadUnreadable
		if errors.Is(err, errSongFactsOverCap) {
			reason = NotReadTooLarge
		}
		return songFactsRead{NotRead: reason}, true
	}
	return songFactsRead{Facts: facts, Names: details}, true
}

func normalizedNames(n *SongNames) SongNames {
	if n == nil {
		return SongNames{}
	}
	c := *n.clone()
	if len(c.Tracks) == 0 {
		c.Tracks = nil
	}
	if len(c.Markers) == 0 {
		c.Markers = nil
	}
	return c
}

// storedDifference is "" when both stored the same, else what differs.
func storedDifference(plug songFactsRead, plugKeep bool, ori songFactsRead, oriKeep bool) string {
	show := func(r songFactsRead, keep bool) string {
		if !keep {
			return "(not kept)"
		}
		encoded, _ := json.Marshal(r)
		return truncate(string(encoded), 700)
	}
	switch {
	case plugKeep != oriKeep:
	case !plugKeep:
		return ""
	case plug.NotRead != ori.NotRead, plug.Facts != ori.Facts:
	case (plug.Names == nil) != (ori.Names == nil):
	case !reflect.DeepEqual(normalizedNames(plug.Names), normalizedNames(ori.Names)):
	default:
		return ""
	}
	return fmt.Sprintf("plugin stores %s\n\tOri stores    %s", show(plug, plugKeep), show(ori, oriKeep))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type parityTally struct{ read, tooLarge, unreadable, compared, withNames int }

func TestProjectFactsParity_PluginStoresWhatOrisReaderStores(t *testing.T) {
	rig := newParityRig(t)
	cases := parityGolden()
	golden := len(cases)
	random := rand.New(rand.NewSource(20261113))
	for i := 0; i < 600; i++ {
		cases = append(cases, parityCase{name: fmt.Sprintf("random %03d", i), content: parityRandomProject(random)})
	}
	var tally parityTally
	mismatches := 0
	for _, names := range []bool{true, false} {
		for _, batch := range parityBatches(cases) {
			results := rig.read(t, batch, names)
			for i, c := range batch {
				plug, plugKeep := mapProjectFacts(songFactsTarget{}, results[i], songFactsGrant{names: names})
				ori, oriKeep := oriStored(c.content, names, c.maxBytes())
				tally.compared++
				switch {
				case !oriKeep:
				case ori.NotRead == NotReadTooLarge:
					tally.tooLarge++
				case ori.NotRead == NotReadUnreadable:
					tally.unreadable++
				default:
					tally.read++
					if ori.Names != nil && (len(ori.Names.Tracks) > 0 || len(ori.Names.Markers) > 0) {
						tally.withNames++
					}
				}
				if diff := storedDifference(plug, plugKeep, ori, oriKeep); diff != "" {
					mismatches++
					if mismatches <= 12 {
						t.Errorf("%s (names=%v, %d bytes, starts %q):\n\t%s", c.name, names, len(c.content), truncate(string(c.content), 80), diff)
					}
				}
			}
		}
	}
	t.Logf("PARITY: %d comparisons (%d golden + %d random files, names on and off): %d read (%d with names), %d too large, %d unreadable; %d differences",
		tally.compared, golden, len(cases)-golden, tally.read, tally.withNames, tally.tooLarge, tally.unreadable, mismatches)
	if mismatches > 0 {
		t.Fatalf("%d of %d comparisons differ", mismatches, tally.compared)
	}
	if tally.read < 400 || tally.tooLarge < 4 || tally.unreadable < 100 {
		t.Fatalf("the corpus no longer exercises every outcome: %+v", tally)
	}
}

func TestProjectFactsParity_TheManifestDeclaresTheContractsReader(t *testing.T) {
	rig := newParityRig(t)
	if !plugin.DeclaresProjectFacts(rig.installed) {
		t.Fatal("the candidate declares no usable project reader")
	}
	if formats := plugin.ProjectFactsFormats(rig.installed); !reflect.DeepEqual(formats, []string{"reaper"}) {
		t.Fatalf("formats = %v", formats)
	}
	if batch := plugin.ProjectFactsBatchFiles(rig.installed); batch != 8 {
		t.Fatalf("batch files = %d, want 8", batch)
	}
	ref := rig.installed.WorkspaceSurfaces.ProjectFacts
	if ref.ServiceID != "reaper-service" || ref.Operation != "project.facts.read" {
		t.Fatalf("reader ref = %+v", ref)
	}
}

func TestProjectFactsParity_EightFilesAtTheCapsFitTheOutputLimit(t *testing.T) {
	rig := newParityRig(t)
	var tracks []namedTrack
	for i := 0; i < 40; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(strings.Repeat("a", 79) + fmt.Sprint(i%10))}})
	}
	for i := 0; i < 20; i++ {
		tracks = append(tracks, namedTrack{lines: []string{nameLine(strings.Repeat("<", 80))}})
	}
	var markers []string
	for i := 0; i < 140; i++ {
		name := `"` + strings.Repeat("m", 79) + fmt.Sprint(i%10) + `"`
		markers = append(markers, marker(i+1, fmt.Sprintf("%d.123", 350000+i), name, 1), marker(i+1, fmt.Sprintf("%d.987", 359000+i), `""`, 1))
	}
	content := []byte(namedProject("123.4", markers, tracks))
	batch := make([]parityCase, 8)
	for i := range batch {
		batch[i] = parityCase{name: fmt.Sprintf("worst case %d", i), content: content}
	}
	results := rig.read(t, batch, true)
	ori, _ := oriStored(content, true, parityLimit)
	// Markers stop at the 128-entry cap, tracks at the 12288-byte budget.
	if ori.Names == nil || len(ori.Names.Markers) != 128 || ori.Names.MarkersLeftOut != 12 || len(ori.Names.Tracks) != 58 || ori.Names.TracksLeftOut != 2 {
		t.Fatalf("the worst case no longer reaches the caps in Ori's reader: %d tracks (%d left out), %d markers (%d left out)",
			len(ori.Names.Tracks), ori.Names.TracksLeftOut, len(ori.Names.Markers), ori.Names.MarkersLeftOut)
	}
	for i := range batch {
		plug, keep := mapProjectFacts(songFactsTarget{}, results[i], songFactsGrant{names: true})
		if diff := storedDifference(plug, keep, ori, true); diff != "" {
			t.Fatalf("file %d: %s", i, diff)
		}
	}
}

// rppTarget is the target the listing would record for "<name>/<name>.rpp".
func rppTarget(t *testing.T, root Root, name string) songFactsTarget {
	t.Helper()
	var folderStat, fileStat unix.Stat_t
	if err := unix.Stat(filepath.Join(root.Path, name), &folderStat); err != nil {
		t.Fatal(err)
	}
	if err := unix.Stat(filepath.Join(root.Path, name, name+".rpp"), &fileStat); err != nil {
		t.Fatal(err)
	}
	return songFactsTarget{Relative: name, FolderIdentity: unixIdentity(&folderStat), Format: "reaper",
		File: FactsSource{File: name + ".rpp", Identity: unixIdentity(&fileStat), Size: fileStat.Size,
			ModifiedAt: time.Unix(fileStat.Mtim.Unix()).UTC()}}
}

func TestProjectFactsParity_ThroughTheHostsOwnBatch(t *testing.T) {
	rig := newParityRig(t)
	root, _ := readerRoot(t)
	songs := []struct {
		name    string
		content string
	}{
		{"Real", realSavedProject},
		{"Plain", rppProject("100", []string{"0 100 0", "9 80 0"}, [][][2]string{{{"0", "30"}}, {{"1", "60"}}})},
		{"NotAProject", "hello"},
		{"Named", namedProject("90", []string{marker(1, "5", `"Intro"`, 0)}, []namedTrack{{lines: []string{nameLine("Kick")}}})},
	}
	var targets []songFactsTarget
	for _, song := range songs {
		folder := filepath.Join(root.Path, song.name)
		if err := os.MkdirAll(folder, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, song.name+".rpp"), []byte(song.content), 0o600); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, rppTarget(t, root, song.name))
	}
	// Listed over the per-file cap: never sent, too large without a call.
	huge := targets[0]
	huge.Relative = "Huge"
	huge.File.Size = DefaultSongFactsLimits.MaxBytes + 1
	targets = append(targets, huge)

	for _, names := range []bool{true, false} {
		reads, counts, err := readProjectFactsBatch(context.Background(), root, targets, songFactsGrant{names: names},
			projectReader{plugin: rig.installed, batch: 8}, rig.lifecycle.ProjectFacts)
		if err != nil {
			t.Fatalf("names=%v: %v", names, err)
		}
		if counts.Read != 3 || counts.Unreadable != 1 || counts.TooLarge != 1 || counts.Changed != 0 || counts.Failed != 0 || len(reads) != 5 {
			t.Fatalf("names=%v: counts = %+v, %d reads", names, counts, len(reads))
		}
		byFolder := map[string]songFactsRead{}
		for _, read := range reads {
			byFolder[read.Relative] = read
		}
		if byFolder["Huge"].NotRead != NotReadTooLarge {
			t.Errorf("names=%v: a file listed over the cap was %+v, want too large", names, byFolder["Huge"])
		}
		for _, song := range songs {
			ori, _ := oriStored([]byte(song.content), names, DefaultSongFactsLimits.MaxBytes)
			read, ok := byFolder[song.name]
			if !ok {
				t.Errorf("%s names=%v: nothing was stored", song.name, names)
				continue
			}
			if diff := storedDifference(read, true, ori, true); diff != "" {
				t.Errorf("%s names=%v: %s", song.name, names, diff)
			}
		}
	}
}

// Task 5.7: a folder of songs read through the host's own batch path and the
// real plugin service, eight at a time as the read turn does, and compared with
// what Ori's reader stores for each file. It times the read itself: no model
// turn and no scan (the scan's own 3 s listing budget lists fewer than 500 songs
// of the fixture on a slow disk). Needs the songs fixture's Bulk folder:
// ORI_PROJECT_FACTS_PARITY_BULK_DIR=<sandbox>/Bulk/REAPER Projects.
func TestProjectFactsParity_AFolderOfSongsThroughTheHostsBatchPath(t *testing.T) {
	dir := os.Getenv("ORI_PROJECT_FACTS_PARITY_BULK_DIR")
	if dir == "" {
		t.Skip("set ORI_PROJECT_FACTS_PARITY_BULK_DIR to the songs fixture's Bulk/REAPER Projects folder")
	}
	rig := newParityRig(t)
	var rootStat unix.Stat_t
	if err := unix.Stat(dir, &rootStat); err != nil {
		t.Fatal(err)
	}
	root := Root{ID: "root", Path: dir, FileIdentity: unixIdentity(&rootStat)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var targets []songFactsTarget
	for _, entry := range entries {
		if entry.IsDir() {
			targets = append(targets, rppTarget(t, root, entry.Name()))
		}
	}
	if len(targets) < 100 {
		t.Fatalf("only %d songs in %s", len(targets), dir)
	}
	reader := projectReader{plugin: rig.installed, batch: 8}
	started := time.Now()
	var reads []songFactsRead
	for start := 0; start < len(targets); start += 8 {
		batch, counts, err := readProjectFactsBatch(context.Background(), root, targets[start:min(start+8, len(targets))],
			songFactsGrant{names: true}, reader, rig.lifecycle.ProjectFacts)
		if err != nil || counts.Read != len(batch) {
			t.Fatalf("batch at %d: %v %+v", start, err, counts)
		}
		reads = append(reads, batch...)
	}
	elapsed := time.Since(started)
	differences := 0
	for _, read := range reads {
		content, err := os.ReadFile(filepath.Join(dir, read.Relative, read.File.File))
		if err != nil {
			t.Fatal(err)
		}
		ori, _ := oriStored(content, true, DefaultSongFactsLimits.MaxBytes)
		if diff := storedDifference(read, true, ori, true); diff != "" {
			differences++
			if differences <= 5 {
				t.Errorf("%s: %s", read.Relative, diff)
			}
		}
	}
	t.Logf("SCALE: %d songs read through the host's batch path in %s (%.1f ms per song, %d plugin calls); %d differences from Ori's reader",
		len(reads), elapsed.Round(time.Millisecond), float64(elapsed.Milliseconds())/float64(len(reads)), (len(targets)+7)/8, differences)
}

func TestProjectFactsParity_WhatTheReaderWillNotOpen(t *testing.T) {
	rig := newParityRig(t)
	real := []byte(realSavedProject)
	good := rig.write(t, real)
	link := filepath.Join(rig.dir, "link.rpp")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(rig.dir, "folder.rpp")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	size := int64(len(real))
	refusals := []struct {
		name string
		file projectFactsFileInput
	}{
		{"a symbolic link", projectFactsFileInput{Format: "reaper", Path: link, Size: size}},
		{"a directory", projectFactsFileInput{Format: "reaper", Path: folder, Size: 0}},
		{"a missing file", projectFactsFileInput{Format: "reaper", Path: filepath.Join(rig.dir, "gone.rpp"), Size: size}},
		{"a relative path", projectFactsFileInput{Format: "reaper", Path: "f00001.rpp", Size: size}},
		{"a size that is not the listed size", projectFactsFileInput{Format: "reaper", Path: good, Size: size + 1}},
		{"a format it does not cover", projectFactsFileInput{Format: "logic", Path: good, Size: size}},
	}
	input := projectFactsInput{SchemaVersion: 1, Names: true, MaxFileBytes: parityLimit}
	for id, refusal := range refusals {
		refusal.file.ID = id
		input.Files = append(input.Files, refusal.file)
	}
	encoded, _ := json.Marshal(input)
	raw, err := rig.lifecycle.ProjectFacts(context.Background(), rig.installed, encoded)
	if err != nil {
		t.Fatal(err)
	}
	var output projectFactsOutput
	if err := json.Unmarshal(raw, &output); err != nil || len(output.Files) != len(refusals) {
		t.Fatalf("answer = %v %s", err, raw)
	}
	for id, refusal := range refusals {
		if output.Files[id].Result != projectFactsFailed {
			t.Errorf("%s: result = %q, want failed", refusal.name, output.Files[id].Result)
		}
	}
	// The same real file in the same batch shape is read: the refusals above are
	// about the paths, not the service.
	again, _ := json.Marshal(projectFactsInput{SchemaVersion: 1, Names: false, MaxFileBytes: parityLimit,
		Files: []projectFactsFileInput{{ID: 0, Format: "reaper", Path: good, Size: size}}})
	raw, err = rig.lifecycle.ProjectFacts(context.Background(), rig.installed, again)
	if err != nil || !strings.Contains(string(raw), `"result":"read"`) {
		t.Fatalf("the real file was not read: %v %s", err, raw)
	}
}

// A named pipe where a file was listed must not stall the reader: opening one
// for reading waits for a writer unless the open itself refuses it.
func TestProjectFactsParity_ANamedPipeDoesNotStallTheReader(t *testing.T) {
	rig := newParityRig(t)
	pipe := filepath.Join(rig.dir, "pipe.rpp")
	if err := unix.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(projectFactsInput{SchemaVersion: 1, Names: false, MaxFileBytes: parityLimit,
		Files: []projectFactsFileInput{{ID: 0, Format: "reaper", Path: pipe, Size: 0}}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := time.Now()
	raw, err := rig.lifecycle.ProjectFacts(ctx, rig.installed, input)
	if err != nil {
		t.Fatalf("a named pipe stalled or broke the reader after %s: %v", time.Since(started).Round(time.Millisecond), err)
	}
	if !strings.Contains(string(raw), `"result":"failed"`) {
		t.Fatalf("a named pipe was not refused: %s", raw)
	}
}

// --- the corpus ---------------------------------------------------------

func parityTracks(n int, named bool) string {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1\n  TEMPO 100 4 4\n")
	for i := 0; i < n; i++ {
		if named {
			fmt.Fprintf(&b, "  <TRACK\n    NAME \"Track %d\"\n  >\n", i)
		} else {
			b.WriteString("  <TRACK\n  >\n")
		}
	}
	b.WriteString(">\n")
	return b.String()
}

func parityNested(depth int) string {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1\n")
	for i := 1; i < depth; i++ {
		b.WriteString("<A\n")
	}
	for i := 0; i < depth; i++ {
		b.WriteString(">\n")
	}
	return b.String()
}

// parityPad grows a project to exactly size bytes with spaces after its last
// line, which the reader trims away.
func parityPad(project string, size int) []byte {
	trimmed := strings.TrimSuffix(project, ">\n")
	return []byte(trimmed + ">" + strings.Repeat(" ", size-len(trimmed)-2) + "\n")
}

func parityGolden() []parityCase {
	var cases []parityCase
	add := func(name, content string) { cases = append(cases, parityCase{name: name, content: []byte(content)}) }
	addSmall := func(name string, content []byte) {
		cases = append(cases, parityCase{name: name, content: content, limit: paritySmallLimit})
	}
	oneTrack := [][][2]string{{{"0", "10"}}}

	add("a real saved project", realSavedProject)
	add("no tempo, no tracks", rppProject("", nil, nil))
	for _, tempo := range []string{"120", "0", "1", "0.99", "960", "960.5", "961", "120.25", "119.96", "120.04", "abc", "-5", "nan", "Inf", "1e2", "0x78", "120abc", strings.Repeat("1", 33)} {
		add("tempo "+tempo, rppProject(tempo, nil, oneTrack))
	}
	add("a bad tempo line then two good ones", "<REAPER_PROJECT 0.1\n  TEMPO abc 4 4\n  TEMPO 100 4 4\n  TEMPO 140 4 4\n>\n")
	add("tempo line inside a track", "<REAPER_PROJECT 0.1\n  <TRACK\n    TEMPO 150 4 4\n  >\n>\n")
	for _, points := range []struct {
		name string
		base string
		pts  []string
	}{
		{"points equal to the base", "120", []string{"0 120 0", "10 120 0"}},
		{"points that vary", "120", []string{"0 120 0", "10 90 0"}},
		{"points out of range are ignored", "120", []string{"0 120 0", "10 961 0", "20 0.5 0"}},
		{"points that are not numbers", "120", []string{"0 x 0", "5 y"}},
		{"one point", "120", []string{"3 130 0"}},
		{"points that round to the base", "120", []string{"0 120.04 0", "1 119.96 0"}},
		{"points with no base tempo", "", []string{"0 100 0", "5 140 0"}},
		{"points with a zero base tempo", "0", []string{"0 100 0", "5 140 0"}},
	} {
		add(points.name, rppProject(points.base, points.pts, oneTrack))
	}
	for _, item := range []struct {
		name  string
		items [][2]string
	}{
		{"one item", [][2]string{{"0", "10"}}},
		{"overlapping items", [][2]string{{"0", "10"}, {"5", "12.5"}}},
		{"a negative position", [][2]string{{"-1", "5"}}},
		{"a negative length", [][2]string{{"2", "-5"}}},
		{"a NaN length", [][2]string{{"0", "NaN"}}},
		{"an exponent length", [][2]string{{"0", "1e5"}}},
		{"a length at the cap", [][2]string{{"0", "360000"}}},
		{"a length over the cap", [][2]string{{"0", "360001"}}},
		{"lengths that round to three decimals", [][2]string{{"0.0004", "0.0004"}}},
		{"a tiny length", [][2]string{{"0", "0.0001"}}},
		{"a huge item", [][2]string{{"1e300", "1e300"}}},
	} {
		add(item.name, rppProject("120", nil, [][][2]string{item.items}))
	}
	add("an item with the length first", "<REAPER_PROJECT 0.1\n  <TRACK\n    <ITEM\n      LENGTH 9\n      POSITION 3\n    >\n  >\n>\n")
	add("an item with no length", "<REAPER_PROJECT 0.1\n  <TRACK\n    <ITEM\n      POSITION 3\n    >\n  >\n>\n")
	add("an item with no position", "<REAPER_PROJECT 0.1\n  <TRACK\n    <ITEM\n      LENGTH 3\n    >\n  >\n>\n")
	add("an item outside any track", "<REAPER_PROJECT 0.1\n  <ITEM\n    POSITION 0\n    LENGTH 99\n  >\n>\n")
	add("an item inside another block in a track", "<REAPER_PROJECT 0.1\n  <TRACK\n    <FOLDER\n      <ITEM\n        POSITION 0\n        LENGTH 99\n      >\n    >\n  >\n>\n")
	add("a track inside another block", "<REAPER_PROJECT 0.1\n  <GROUP\n    <TRACK\n    >\n  >\n>\n")
	for _, n := range []int{0, 1, 128, 129, 10000, 10001} {
		add(fmt.Sprintf("%d tracks", n), parityTracks(n, false))
	}
	add("129 named tracks", parityTracks(129, true))

	for _, form := range []string{
		`NAME "double"`, `NAME 'single'`, "NAME `back`", `NAME bare`, `NAME "unclosed`, `NAME "closed"junk`, `NAME ""`, `NAME`,
		`NAME "two words"`, `NAME 'it"s'`, "NAME \"tab\tinside\"", `NAME "  padded  "`, "NAME \"ctrl\x01char\"", `NAME 'a' b`, `NAME "a" "b"`, `NAME "a"	`,
	} {
		add("track name line "+fmt.Sprintf("%q", form), namedProject("100", nil, []namedTrack{{lines: []string{form}}}))
	}
	add("the first name line wins", namedProject("100", nil, []namedTrack{{lines: []string{nameLine("first"), nameLine("second")}}}))
	add("a bad first name line still wins", namedProject("100", nil, []namedTrack{{lines: []string{`NAME "bad`, nameLine("second")}}}))
	add("a track with no name line", namedProject("100", nil, []namedTrack{{}, {lines: []string{nameLine("only")}}}))

	for _, raw := range []string{
		"Kick", "  lead", "trail  ", "Vox L/R", "AC/DC", ".", "..", "~", "/abs", "~/x", "./x", "../x", `a\b`, "x /y", "x ~/y", "x (/y)",
		`C:\x`, `C:/x`, `c:x`, "a\u202Eb", "a\u2066b", "a\u2028b", "a\u0007b", "bad\xffutf8", strings.Repeat("x", 80), strings.Repeat("x", 81),
		strings.Repeat("é", 81), "<<<<", `\\\\`, "日本語", "🎵 emoji", "a\u0085b", "\u00a0nbsp lead", "tab\there", "x\u200bzero width", "   ",
	} {
		add("track name "+fmt.Sprintf("%q", raw), namedProject("100", nil, []namedTrack{{lines: []string{nameLine(raw)}}}))
		add("marker name "+fmt.Sprintf("%q", raw), namedProject("100", []string{marker(1, "1.5", `"`+raw+`"`, 0)}, nil))
	}

	for _, set := range []struct {
		name    string
		markers []string
	}{
		{"plain markers", []string{marker(1, "5", `"A"`, 0), marker(2, "10", `"B"`, 0)}},
		{"a region pair", []string{marker(3, "4", `"R"`, 1), marker(3, "9", `""`, 1)}},
		{"a region that ends before it starts", []string{marker(3, "9", `"R"`, 1), marker(3, "4", `""`, 1)}},
		{"a region that ends where it starts", []string{marker(3, "4", `"R"`, 1), marker(3, "4", `""`, 1)}},
		{"a region with no end", []string{marker(3, "4", `"R"`, 1)}},
		{"two regions interleaved", []string{marker(1, "1", `"A"`, 1), marker(2, "2", `"B"`, 1), marker(1, "8", `""`, 1), marker(2, "9", `""`, 1)}},
		{"a number reused after its region closed", []string{marker(1, "1", `"A"`, 1), marker(1, "5", `""`, 1), marker(1, "7", `"B"`, 1)}},
		{"duplicate plain numbers", []string{marker(1, "1", `"A"`, 0), marker(1, "2", `"B"`, 0)}},
		{"a plain marker sharing a region's number", []string{marker(4, "1", `"A"`, 1), marker(4, "2", `"B"`, 0)}},
		{"unnamed markers", []string{marker(1, "1", `""`, 0), marker(2, "2", `""`, 1)}},
		{"a number over 32 bits", []string{`MARKER 4294967296 5 "x" 0`}},
		{"the largest 32-bit number", []string{`MARKER 4294967295 5 "x" 0`}},
		{"a negative number", []string{`MARKER -1 5 "x" 0`}},
		{"a time over the cap", []string{`MARKER 1 360001 "x" 0`}},
		{"a time at the cap", []string{`MARKER 1 360000 "x" 0`}},
		{"a negative time", []string{`MARKER 1 -5 "x" 0`}},
		{"a time that is not a number", []string{`MARKER 1 abc "x" 0`}},
		{"flags that are not a number", []string{`MARKER 1 5 "x" abc`}},
		{"three values only", []string{`MARKER 1 5 "x"`}},
		{"two values only", []string{`MARKER 1 5`}},
		{"the largest 64-bit flags", []string{`MARKER 1 5 "x" 18446744073709551615`, `MARKER 1 9 "" 18446744073709551615`}},
		{"flags over 64 bits", []string{`MARKER 1 5 "x" 18446744073709551616`}},
		{"flags with other bits set", []string{`MARKER 1 5 "x" 3`, `MARKER 1 6 "x" 2`, `MARKER 2 6 "y" 4`}},
		{"an unclosed marker name", []string{`MARKER 1 5 "x 0 0`}},
		{"a quoted name with spaces", []string{`MARKER 1 5 "Big  Drop" 0`}},
		{"a marker line with a tab separator", []string{"MARKER\t1\t5\t\"Tabbed\"\t0"}},
		{"a time rounded to three decimals", []string{`MARKER 1 5.0004 "x" 0`, `MARKER 2 5.0005 "y" 0`, `MARKER 3 59.9996 "z" 0`}},
	} {
		add(set.name, namedProject("100", set.markers, []namedTrack{{lines: []string{nameLine("Kick")}}}))
	}
	add("a marker inside a track", "<REAPER_PROJECT 0.1\n  <TRACK\n    MARKER 1 5 \"nested\" 0\n  >\n>\n")

	var many []string
	for i := 1; i <= 130; i++ {
		many = append(many, marker(i, fmt.Sprint(i), fmt.Sprintf(`"M%d"`, i), 0))
	}
	add("130 markers", namedProject("100", many, nil))
	cut := append([]string{}, many[:128]...)
	cut = append(cut, marker(900, "500", `"Late"`, 1), marker(900, "600", `""`, 1), marker(901, "700", `"Later"`, 1))
	add("a region cut by the cap is counted once", namedProject("100", cut, nil))
	var wide []string
	for i := 1; i <= 30; i++ {
		wide = append(wide, marker(i, fmt.Sprint(i), `'`+strings.Repeat(`"`, 80)+`'`, 0))
	}
	add("markers whose names escape to more bytes", namedProject("100", wide, nil))
	var wideTracks []namedTrack
	for i := 0; i < 40; i++ {
		wideTracks = append(wideTracks, namedTrack{lines: []string{nameLine(strings.Repeat("<", 80))}})
	}
	add("tracks whose names escape to more bytes", namedProject("100", nil, wideTracks))
	var hundredTracks []namedTrack
	for i := 0; i < 130; i++ {
		hundredTracks = append(hundredTracks, namedTrack{lines: []string{nameLine(fmt.Sprintf("T%03d", i))}})
	}
	add("130 named tracks", namedProject("100", nil, hundredTracks))

	for _, refusal := range []struct{ name, content string }{
		{"empty", ""},
		{"blank lines only", "\n\n  \n"},
		{"binary", "\x00\x01\x02"},
		{"a NUL after the first line", "<REAPER_PROJECT 0.1\n\x00\n>\n"},
		{"a NUL inside a name", "<REAPER_PROJECT 0.1\n  <TRACK\n    NAME \"a\x00b\"\n  >\n>\n"},
		{"the wrong first line", "<NOT_A_PROJECT\n>\n"},
		{"the root word with more letters", "<REAPER_PROJECTX 1\n>\n"},
		{"the root word then a tab", "<REAPER_PROJECT\t0.1\n>\n"},
		{"the root word alone", "<REAPER_PROJECT\n>\n"},
		{"the root closed on one line", "<REAPER_PROJECT>\n"},
		{"not closed", "<REAPER_PROJECT 0.1\n  TEMPO 100 4 4\n"},
		{"content after the close", "<REAPER_PROJECT 0.1\n>\nTEMPO 90 4 4\n"},
		{"a second root", "<REAPER_PROJECT 0.1\n>\n<REAPER_PROJECT 0.1\n>\n"},
		{"an extra close", "<REAPER_PROJECT 0.1\n>\n>\n"},
		{"leading blank lines", "\n\n<REAPER_PROJECT 0.1\n  TEMPO 100 4 4\n>\n"},
		{"CRLF line endings", strings.ReplaceAll(rppProject("111", nil, oneTrack), "\n", "\r\n")},
		{"a byte order mark", "\xef\xbb\xbf<REAPER_PROJECT 0.1\n>\n"},
		{"63 blocks deep", parityNested(63)},
		{"64 blocks deep", parityNested(64)},
		{"65 blocks deep", parityNested(65)},
		{"an inner block never closed", "<REAPER_PROJECT 0.1\n  <TRACK\n>\n"},
		{"spaces and blank lines after the close", "<REAPER_PROJECT 0.1\n>   \n\n  \n"},
		{"no newline at the end", "<REAPER_PROJECT 0.1\n  TEMPO 100 4 4\n>"},
		{"a close with text", ">x"},
		{"a line that opens with a bracket inside the root", "<REAPER_PROJECT 0.1\n  <\n  >\n>\n"},
		{"a block with no name and a space", "<REAPER_PROJECT 0.1\n  < TRACK\n  >\n>\n"},
		{"a close with trailing spaces", "<REAPER_PROJECT 0.1\n  <TRACK   \n  >  \n>\n"},
	} {
		add(refusal.name, refusal.content)
	}

	small := rppProject("120", nil, oneTrack)
	addSmall("exactly at the byte limit", parityPad(small, int(paritySmallLimit)))
	addSmall("one byte under the byte limit", parityPad(small, int(paritySmallLimit)-1))
	addSmall("one byte over the byte limit", parityPad(small, int(paritySmallLimit)+1))
	addSmall("over the byte limit with a wrong first line", append([]byte("<NOPE\n"), bytes.Repeat([]byte("x\n"), 1500)...))
	addSmall("over the byte limit and never closed", []byte("<REAPER_PROJECT 0.1\n"+strings.Repeat("  TEMPO 100 4 4\n", 200)))
	addSmall("over the byte limit with a NUL early", append([]byte("<REAPER_PROJECT 0.1\n\x00\n"), bytes.Repeat([]byte("x\n"), 1500)...))
	addSmall("over the byte limit and closed", []byte("<REAPER_PROJECT 0.1\n"+strings.Repeat("  TEMPO 100 4 4\n", 200)+">\n"))

	line := func(n int) string { return strings.Repeat("a", n) }
	for _, n := range []int{262143, 262144, 262145} {
		add(fmt.Sprintf("a line of %d bytes", n), "<REAPER_PROJECT 0.1\n"+line(n)+"\n>\n")
	}
	add("a line of the cap with no newline before the end", "<REAPER_PROJECT 0.1\n"+line(262144))
	add("a long line then a NUL", "<REAPER_PROJECT 0.1\n"+line(262145)+"\n\x00\n>\n")
	add("a NUL then a long line", "<REAPER_PROJECT 0.1\n\x00\n"+line(262145)+"\n>\n")
	add("a wrong first line then a long line", "<NOPE\n"+line(262145)+"\n")
	add("content after the close then a long line", "<REAPER_PROJECT 0.1\n>\ntrailing\n"+line(262145)+"\n")
	add("a long line then content after the close", "<REAPER_PROJECT 0.1\n"+line(262145)+"\n>\ntrailing\n")
	add("nesting too deep then a long line", parityNested(65)+line(262145)+"\n")
	return cases
}

var parityNamePool = []string{
	"Kick", "Snare Top", "Vox L/R", "AC/DC", "Ünïcødé 🎵", "bad\xffutf8", "a\u202Ereverse", "tab\there", "  leading", "trailing  ",
	strings.Repeat("x", 90), "/abs/path", "~/x", "../x", `C:\x`, `C:/x`, `a\b`, "<<>>&&", "back`tick", "it's", `quote"inside`, "", "日本語 トラック",
	"Track 001", "Marker 7", "Lead Vocal", "Bass DI", "Pad (wide)", "a (/b)", ".", "..", "~", "zero\u200bwidth", "nbsp\u00a0here",
}

var (
	parityTempoPool  = []string{"120", "90.5", "60", "200", "1", "960", "0", "961", "abc", "-3", "nan", "1e1", "120.04", "119.96", "33.33", "150.55", "0.5"}
	parityNumberPool = []string{"0", "1", "2.5", "10", "30.123", "59.9996", "100", "0.0004", "-1", "abc", "nan", "1e3", "360000", "360001", "1e400", "0x10", "215.5"}
	parityFlagsPool  = []string{"0", "0", "1", "1", "3", "2", "abc", "18446744073709551615", "18446744073709551616", "0", "1"}
)

func parityQuote(r *rand.Rand, name string) string {
	switch r.Intn(12) {
	case 0:
		return `'` + name + `'`
	case 1:
		return "`" + name + "`"
	case 2:
		return name
	case 3:
		return `"` + name
	case 4:
		return `"` + name + `"junk`
	default:
		return `"` + name + `"`
	}
}

func parityPick(r *rand.Rand, pool []string) string { return pool[r.Intn(len(pool))] }

func parityRandomProject(r *rand.Rand) []byte {
	var b strings.Builder
	b.WriteString("<REAPER_PROJECT 0.1 \"7.27/macOS-arm64\" 1727800000\n")
	if r.Intn(3) > 0 {
		b.WriteString("  <NOTES 0 2\n    |Mix notes " + parityPick(r, parityNamePool) + "\n  >\n")
	}
	for n := r.Intn(3); n > 0; n-- {
		b.WriteString("  TEMPO " + parityPick(r, parityTempoPool) + " 4 4\n")
	}
	if r.Intn(3) == 0 {
		b.WriteString("  <TEMPOENVEX\n    ACT 0 -1\n")
		for n := r.Intn(6); n > 0; n-- {
			b.WriteString("    PT " + parityPick(r, parityNumberPool) + " " + parityPick(r, parityTempoPool) + " 0\n")
		}
		b.WriteString("  >\n")
	}
	markers := []int{0, 0, 3, 10, 140}[r.Intn(5)]
	for i := 0; i < markers; i++ {
		index := r.Intn(markers/2+1) + r.Intn(2)
		fmt.Fprintf(&b, "  MARKER %d %s %s %s 0 1 R {C0FFEE00-0000-0000-0000-000000000000} 0\n",
			index, parityPick(r, parityNumberPool), parityQuote(r, parityPick(r, parityNamePool)), parityPick(r, parityFlagsPool))
	}
	tracks := []int{0, 1, 4, 12, 130, 200}[r.Intn(6)]
	for i := 0; i < tracks; i++ {
		b.WriteString("  <TRACK {A982F44F-895F-4943-B6F7-299C7949DF39}\n")
		for n := []int{0, 1, 1, 1, 2}[r.Intn(5)]; n > 0; n-- {
			b.WriteString("    NAME " + parityQuote(r, parityPick(r, parityNamePool)) + "\n")
		}
		if r.Intn(3) == 0 {
			b.WriteString("    <FXCHAIN\n      NAME \"fx\"\n    >\n")
		}
		for n := r.Intn(3); n > 0; n-- {
			b.WriteString("    <ITEM\n")
			if r.Intn(8) > 0 {
				b.WriteString("      POSITION " + parityPick(r, parityNumberPool) + "\n")
			}
			if r.Intn(8) > 0 {
				b.WriteString("      LENGTH " + parityPick(r, parityNumberPool) + "\n")
			}
			b.WriteString("      NAME " + parityQuote(r, "take") + "\n    >\n")
		}
		b.WriteString("  >\n")
	}
	b.WriteString("  <EXTENSIONS\n  >\n>\n")
	return parityMutate(r, []byte(b.String()))
}

func parityMutate(r *rand.Rand, content []byte) []byte {
	if r.Intn(100) >= 40 {
		return content
	}
	lines := bytes.SplitAfter(content, []byte("\n"))
	switch r.Intn(10) {
	case 0: // truncate
		return content[:r.Intn(len(content)+1)]
	case 1: // flip one byte
		out := append([]byte{}, content...)
		out[r.Intn(len(out))] = byte(r.Intn(256))
		return out
	case 2: // insert a NUL
		at := r.Intn(len(content) + 1)
		return append(append(append([]byte{}, content[:at]...), 0), content[at:]...)
	case 3: // drop a line
		at := r.Intn(len(lines))
		return bytes.Join(append(append([][]byte{}, lines[:at]...), lines[at+1:]...), nil)
	case 4: // repeat a line
		at := r.Intn(len(lines))
		return bytes.Join(append(append(append([][]byte{}, lines[:at+1]...), lines[at]), lines[at+1:]...), nil)
	case 5: // an extra close
		at := r.Intn(len(lines))
		return bytes.Join(append(append(append([][]byte{}, lines[:at]...), []byte(">\n")), lines[at:]...), nil)
	case 6: // an extra open
		at := r.Intn(len(lines))
		return bytes.Join(append(append(append([][]byte{}, lines[:at]...), []byte("<FOO\n")), lines[at:]...), nil)
	case 7: // text after the close
		return append(append([]byte{}, content...), []byte("trailing\n")...)
	case 8: // CRLF
		return bytes.ReplaceAll(content, []byte("\n"), []byte("\r\n"))
	default: // blank lines and trailing spaces
		return bytes.ReplaceAll(content, []byte("\n"), []byte("  \n\n"))
	}
}
