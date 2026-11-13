package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The host reads no project file: an installed app plugin's reader does. These
// tests are about what the library does with the facts a read returns (store,
// reconcile, query, clear, bound), so they need facts to arrive. fileReader
// stands in for the plugin: it parses the fixture projects the builders in
// song_names_clean_test.go write, and answers in the reader's own output shape.
//
// It is not a general parser, and it is not the reader's specification. That the
// real plugin build stores what Ori's own reader stored was proved against the
// plugin itself (assistant-reads-projects 4.6, recorded in the findings log).

// seedMusicTreeFacts saves the facts factsMusicTree's projects hold, through the
// same fenced write the read turn uses, for a fixture whose scan was made without
// a read turn and that needs the facts present: Single is 92 BPM, 2 tracks, 3:41;
// Alternates is dated by Take B (96 BPM with tempo changes, 3 tracks, 1:15).
func seedMusicTreeFacts(t *testing.T, r *Roots, scope Scope, scan Scan, root Root) {
	t.Helper()
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	targets, grant, ok := r.songFactsTargets(scope, scan, observed)
	if !ok {
		t.Fatal("the Home gives no grant to read under")
	}
	facts := map[string]SongFacts{
		"Single":     {TempoBPM: 92, LengthSeconds: 221, TrackCount: 2},
		"Alternates": {TempoBPM: 96, TempoVaries: true, LengthSeconds: 75, TrackCount: 3},
	}
	var reads []songFactsRead
	for _, target := range targets {
		if want, ok := facts[target.Relative]; ok && target.Format == "reaper" {
			reads = append(reads, songFactsRead{songFactsTarget: target, Facts: want})
		}
	}
	if saved, _, err := r.saveSongFacts(scope, scan, reads, grant); err != nil || saved != 2 {
		t.Fatalf("seeding the fixture's facts: saved %d, %v", saved, err)
	}
}

// fileReader is a plugin reader over real fixture files that remembers which
// files it was asked to read.
type fileReader struct {
	mu     sync.Mutex
	opened []string
	// before, when set, runs for each file just before it is read.
	before func(path string)
}

// take returns the files read since the last call, as "<folder>/<file>", sorted.
func (p *fileReader) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	opened := p.opened
	p.opened = nil
	slices.Sort(opened)
	return opened
}

func (p *fileReader) call(_ context.Context, _ plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
	var in projectFactsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	out := projectFactsOutput{SchemaVersion: 1}
	for _, file := range in.Files {
		p.mu.Lock()
		before := p.before
		p.opened = append(p.opened, filepath.Join(filepath.Base(filepath.Dir(file.Path)), filepath.Base(file.Path)))
		p.mu.Unlock()
		if before != nil {
			before(file.Path)
		}
		result := projectFactsResult{ID: file.ID, Result: projectFactsFailed}
		if content, err := os.ReadFile(file.Path); err == nil && int64(len(content)) == file.Size {
			result = parseFixtureProject(content, in.Names, in.MaxFileBytes)
			result.ID = file.ID
		}
		out.Files = append(out.Files, result)
	}
	return json.Marshal(out)
}

// newFileReadFixture is a read fixture whose plugin reads the fixture's real
// files, and whose assistant calls the tool whenever it is asked to read.
func newFileReadFixture(t *testing.T) (*readFixture, *fileReader) {
	t.Helper()
	reader := &fileReader{}
	f := newReadFixtureWith(t, fixtureOptions{reader: reader.call})
	f.chat.alwaysCalls = true
	return f, reader
}

// newFileReadFixtureAt gives a root the caller connected (connectedRootAt, which
// also grants the consent) the same fake read turn and file reader.
func newFileReadFixtureAt(t *testing.T, r *Roots, scope Scope, file workspace.Store, root Root) (*readFixture, *fileReader) {
	t.Helper()
	reader := &fileReader{}
	f := wireReadFixture(t, r, scope, file, root, fixtureOptions{reader: reader.call, noConsent: true})
	f.chat.alwaysCalls = true
	return f, reader
}

// newFileReadFixtureWithoutConsent is newFileReadFixture for a Home that holds
// no song-details consent.
func newFileReadFixtureWithoutConsent(t *testing.T) (*readFixture, *fileReader) {
	t.Helper()
	reader := &fileReader{}
	f := newReadFixtureWith(t, fixtureOptions{reader: reader.call, noConsent: true})
	f.chat.alwaysCalls = true
	return f, reader
}

// fixtureMaxLine is the longest line a project file of the format keeps; a
// longer one is not a file the application saved, and the reader answers "too
// large" for it, as the plugin's reader does.
const fixtureMaxLine = 256 << 10

// parseFixtureProject reads the numbers, and when names is set the track names,
// markers and regions, of a fixture project.
func parseFixtureProject(content []byte, names bool, maxBytes int64) projectFactsResult {
	unreadable := projectFactsResult{Result: projectFactsUnreadable}
	if int64(len(content)) > maxBytes {
		return projectFactsResult{Result: projectFactsTooLarge}
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return unreadable
	}
	for _, raw := range bytes.Split(content, []byte("\n")) {
		if len(raw) > fixtureMaxLine {
			return projectFactsResult{Result: projectFactsTooLarge}
		}
	}
	res := projectFactsResult{Result: projectFactsRead}
	var (
		stack                                       []string
		points                                      []float64
		itemStart, itemLength, length               float64
		haveStart, haveLength, tempoSet, trackNamed bool
		started, closed                             bool
		openRegions                                 []int // indexes into res.Markers whose end has not come
		regionNumbers                               = map[int]uint64{}
	)
	number := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
	}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !started {
			if !strings.HasPrefix(line, "<REAPER_PROJECT") {
				return unreadable
			}
			started = true
		} else if closed {
			return unreadable
		}
		switch {
		case strings.HasPrefix(line, "<"):
			name := ""
			if words := strings.Fields(line[1:]); len(words) > 0 {
				name = words[0]
			}
			depth := len(stack)
			stack = append(stack, name)
			if depth == 1 && name == "TRACK" {
				res.TrackCount++
				trackNamed = false
			}
			if depth == 2 && name == "ITEM" && stack[1] == "TRACK" {
				haveStart, haveLength = false, false
			}
		case line == ">":
			if len(stack) == 0 {
				return unreadable
			}
			depth := len(stack) - 1
			if stack[depth] == "ITEM" && depth == 2 && stack[1] == "TRACK" && haveStart && haveLength {
				length = max(length, itemStart+itemLength)
			}
			stack = stack[:depth]
			if depth == 0 {
				closed = true
			}
		default:
			if len(stack) == 0 {
				return unreadable
			}
			fields := strings.Fields(line)
			depth, top := len(stack), stack[len(stack)-1]
			switch {
			case depth == 1 && fields[0] == "TEMPO" && len(fields) > 1 && !tempoSet:
				if v, ok := number(fields[1]); ok && v >= 1 && v <= 960 {
					res.TempoBPM, tempoSet = math.Round(v*10)/10, true
				}
			case depth == 1 && fields[0] == "MARKER" && names:
				values := quotedValues(line)
				if len(values) < 5 {
					break
				}
				index, err1 := strconv.ParseUint(values[1], 10, 32)
				at, ok := number(values[2])
				flags, err2 := strconv.ParseUint(values[4], 10, 64)
				if err1 != nil || err2 != nil || !ok || at > 360000 {
					break
				}
				at = math.Round(at*1000) / 1000
				if flags&1 != 0 {
					closedRegion := false
					for i := len(openRegions) - 1; i >= 0; i-- {
						if m := &res.Markers[openRegions[i]]; regionNumbers[openRegions[i]] == index {
							if at > m.At {
								m.End = at
							}
							openRegions = append(openRegions[:i], openRegions[i+1:]...)
							closedRegion = true
							break
						}
					}
					if closedRegion {
						break
					}
					regionNumbers[len(res.Markers)] = index
					openRegions = append(openRegions, len(res.Markers))
				}
				res.Markers = append(res.Markers, projectFactsMarker{Name: values[3], At: at})
			case depth == 2 && top == "TEMPOENVEX" && fields[0] == "PT" && len(fields) > 2:
				if v, ok := number(fields[2]); ok && v >= 1 && v <= 960 {
					points = append(points, v)
				}
			case depth == 2 && top == "TRACK" && fields[0] == "NAME" && !trackNamed:
				trackNamed = true
				if values := quotedValues(line); names && len(values) > 1 {
					res.Tracks = append(res.Tracks, values[1])
				}
			case depth == 3 && top == "ITEM" && stack[1] == "TRACK" && len(fields) > 1:
				if v, ok := number(fields[1]); ok {
					switch fields[0] {
					case "POSITION":
						itemStart, haveStart = v, true
					case "LENGTH":
						if !haveLength {
							itemLength, haveLength = v, true
						}
					}
				}
			}
		}
	}
	if !started || !closed {
		return unreadable
	}
	if res.TempoBPM != 0 && len(points) > 0 {
		low, high := math.Round(res.TempoBPM*10), math.Round(res.TempoBPM*10)
		for _, p := range points {
			low, high = math.Min(low, math.Round(p*10)), math.Max(high, math.Round(p*10))
		}
		res.TempoVaries = high != low
	}
	if rounded := math.Round(length*1000) / 1000; rounded <= 360000 {
		res.LengthSeconds = rounded
	}
	if names {
		if res.Tracks == nil {
			res.Tracks = []string{}
		}
		if res.Markers == nil {
			res.Markers = []projectFactsMarker{}
		}
	}
	return res
}

// quotedValues splits a line on spaces and tabs, except that a value opening
// with a double quote, a single quote or a backtick runs to the next copy of it.
// A quote that never closes drops the line.
func quotedValues(line string) []string {
	var values []string
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		switch quote := line[i]; quote {
		case '"', '\'', '`':
			end := strings.IndexByte(line[i+1:], quote)
			if end < 0 {
				return nil
			}
			values = append(values, line[i+1:i+1+end])
			i += end + 2
		default:
			start := i
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			values = append(values, line[start:i])
		}
	}
	return values
}
