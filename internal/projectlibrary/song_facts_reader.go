package projectlibrary

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// SongFacts are three numbers read from one project file: its base tempo, the
// end of its last item and its track count. They are facts, never a
// judgement of progress, and nothing the user typed into the project (track,
// marker or item names, notes, plugins) is read or kept. A zero value means
// unknown and is left out wherever facts are shown.
type SongFacts struct {
	TempoBPM      float64 `json:"tempo_bpm,omitempty"`
	TempoVaries   bool    `json:"tempo_varies,omitempty"`
	LengthSeconds float64 `json:"length_seconds,omitempty"`
	TrackCount    int     `json:"track_count,omitempty"`
}

// Bounds on a stored fact. A tempo outside 1–960 BPM is unknown; the length
// and track bounds only refuse values no real project has.
const (
	minSongTempo      = 1
	maxSongTempo      = 960
	maxSongLength     = 100 * 60 * 60 // seconds
	maxSongTrackCount = 10000
)

func (f SongFacts) empty() bool {
	return f.TempoBPM == 0 && !f.TempoVaries && f.LengthSeconds == 0 && f.TrackCount == 0
}

func (f SongFacts) valid() bool {
	return (f.TempoBPM == 0 || (f.TempoBPM >= minSongTempo && f.TempoBPM <= maxSongTempo)) &&
		(!f.TempoVaries || f.TempoBPM != 0) &&
		f.LengthSeconds >= 0 && f.LengthSeconds <= maxSongLength && !math.IsNaN(f.LengthSeconds) &&
		f.TrackCount >= 0 && f.TrackCount <= maxSongTrackCount
}

// SongFactsLimits bound one read. A file over either cap gives no facts.
type SongFactsLimits struct {
	MaxBytes     int64
	MaxLineBytes int
}

// DefaultSongFactsLimits are the caps the scan's fact pass uses. Project files
// of a block format keep their lines short (binary state is wrapped), so a
// quarter-mebibyte line is not a file the application saved.
var DefaultSongFactsLimits = SongFactsLimits{MaxBytes: 32 << 20, MaxLineBytes: 256 << 10}

// ErrSongFactsUnreadable means the file's content gives no facts: it is over
// a cap, is not a project of the declared format, or is malformed. It is not
// a scan failure, and the same unchanged file is not read again. Any other
// error is an I/O failure, tried again on the next scan.
var ErrSongFactsUnreadable = errors.New("project file holds no readable song facts")

func unreadableFacts(why string) error { return fmt.Errorf("%w: %s", ErrSongFactsUnreadable, why) }

// maxSongFactsDepth bounds block nesting. Real projects nest about six deep
// (project > track > item > source > …).
const maxSongFactsDepth = 64

// Block kinds the reader cares about; everything else is blockOther.
const (
	blockOther byte = iota
	blockRoot
	blockTrack
	blockItem
	blockTempoChanges
)

// ReadSongFacts streams one project file line by line, guided only by the
// format's inert grammar from the host's marker table. It holds a fixed-size
// block stack and at most one line, never recurses, and allocates nothing that
// grows with the file. It reads exactly three things: the root's tempo
// attribute and tempo-change points, the track blocks directly in the root,
// and the start and length directly inside each item of a track.
func ReadSongFacts(r io.Reader, grammar *folderdigest.ProjectFacts, limits SongFactsLimits) (SongFacts, error) {
	if r == nil || limits.MaxBytes < 1 || limits.MaxLineBytes < 64 {
		return SongFacts{}, unreadableFacts("no limits")
	}
	if grammar == nil || len(grammar.Root) < 2 || grammar.Root[0] != '<' {
		return SongFacts{}, unreadableFacts("no grammar for this format")
	}
	root := []byte(grammar.Root)
	counted := &io.LimitedReader{R: r, N: limits.MaxBytes + 1}
	scanner := bufio.NewScanner(counted)
	scanner.Buffer(make([]byte, 0, min(64<<10, limits.MaxLineBytes)), limits.MaxLineBytes)

	var stack [maxSongFactsDepth]byte
	depth, started, closed := 0, false, false
	var facts SongFacts
	var tempoSeen bool
	var tempoMin, tempoMax float64
	var itemStart, itemLength float64
	var itemHasStart, itemHasLength bool
	var tracks int
	tracksOverflow := false

	noteTempo := func(bpm float64) {
		if !tempoSeen {
			tempoMin, tempoMax, tempoSeen = bpm, bpm, true
			return
		}
		tempoMin, tempoMax = math.Min(tempoMin, bpm), math.Max(tempoMax, bpm)
	}

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if bytes.IndexByte(line, 0) >= 0 {
			return SongFacts{}, unreadableFacts("binary content")
		}
		if len(line) == 0 {
			continue
		}
		if !started {
			if !bytes.HasPrefix(line, root) || (len(line) > len(root) && line[len(root)] != ' ' && line[len(root)] != '\t') {
				return SongFacts{}, unreadableFacts("not a project of this format")
			}
			started, depth, stack[0] = true, 1, blockRoot
			continue
		}
		if closed {
			return SongFacts{}, unreadableFacts("content after the project")
		}
		if line[0] == '<' {
			if depth >= maxSongFactsDepth {
				return SongFacts{}, unreadableFacts("nested too deep")
			}
			parent := stack[depth-1]
			kind := blockOther
			switch name := blockName(line); {
			case parent == blockRoot && string(name) == grammar.Track:
				kind = blockTrack
				if tracks < maxSongTrackCount {
					tracks++
				} else {
					tracksOverflow = true
				}
			case parent == blockTrack && string(name) == grammar.Item:
				kind = blockItem
				itemHasStart, itemHasLength = false, false
			case parent == blockRoot && string(name) == grammar.TempoChanges:
				kind = blockTempoChanges
			}
			stack[depth] = kind
			depth++
			continue
		}
		if len(line) == 1 && line[0] == '>' {
			depth--
			if stack[depth] == blockItem && itemHasStart && itemHasLength {
				if end := itemStart + itemLength; end > facts.LengthSeconds {
					facts.LengthSeconds = end
				}
			}
			if depth == 0 {
				closed = true
			}
			continue
		}
		switch stack[depth-1] {
		case blockRoot:
			if value, ok := field(line, grammar.Tempo, 1); ok && facts.TempoBPM == 0 {
				if bpm, ok := songNumber(value); ok && bpm >= minSongTempo && bpm <= maxSongTempo {
					facts.TempoBPM = bpm
				}
			}
		case blockTempoChanges:
			if value, ok := field(line, grammar.TempoPoint, 2); ok {
				if bpm, ok := songNumber(value); ok && bpm >= minSongTempo && bpm <= maxSongTempo {
					noteTempo(bpm)
				}
			}
		case blockItem:
			if value, ok := field(line, grammar.ItemStart, 1); ok {
				itemStart, itemHasStart = songNumber(value)
			} else if value, ok := field(line, grammar.ItemLength, 1); ok {
				itemLength, itemHasLength = songNumber(value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return SongFacts{}, unreadableFacts("line too long")
		}
		// An I/O failure says nothing about the file's content: not a refusal.
		return SongFacts{}, fmt.Errorf("read project file: %w", err)
	}
	if counted.N <= 0 {
		return SongFacts{}, unreadableFacts("file too large")
	}
	if !started {
		return SongFacts{}, unreadableFacts("empty file")
	}
	if !closed {
		// A project the application finished saving ends with its closing '>';
		// a cut-off file is being written or is damaged, and its facts would be
		// partial.
		return SongFacts{}, unreadableFacts("project not closed")
	}
	if !tracksOverflow {
		facts.TrackCount = tracks
	}
	facts.TempoBPM = math.Round(facts.TempoBPM*10) / 10
	if facts.TempoBPM != 0 && tempoSeen {
		// Points at the base tempo (meter-only changes) are not tempo changes.
		noteTempo(facts.TempoBPM)
		facts.TempoVaries = math.Round(tempoMax*10) != math.Round(tempoMin*10)
	}
	facts.LengthSeconds = math.Round(facts.LengthSeconds*1000) / 1000
	if facts.LengthSeconds > maxSongLength {
		facts.LengthSeconds = 0
	}
	return facts, nil
}

// blockName is the word after '<' on a block's opening line. Comparing
// string(name) with a string does not allocate.
func blockName(line []byte) []byte {
	rest := line[1:]
	if end := bytes.IndexAny(rest, " \t"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// field returns the index-th space-separated value of a line whose first word
// is key (index 1 is the first value after the key). An empty key never matches.
func field(line []byte, key string, index int) ([]byte, bool) {
	if key == "" || !bytes.HasPrefix(line, []byte(key)) || len(line) == len(key) ||
		(line[len(key)] != ' ' && line[len(key)] != '\t') {
		return nil, false
	}
	parts := bytes.Fields(line[len(key):])
	if index < 1 || index > len(parts) {
		return nil, false
	}
	return parts[index-1], true
}

// songNumber parses a finite, non-negative decimal no longer than any value a
// project file writes.
func songNumber(value []byte) (float64, bool) {
	if len(value) == 0 || len(value) > 32 {
		return 0, false
	}
	number, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, false
	}
	return number, true
}

// factsGrammar is the grammar of a project format that declares facts, or nil.
func factsGrammar(format string) *folderdigest.ProjectFacts {
	marker, ok := folderdigest.FactsMarker(format)
	if !ok {
		return nil
	}
	return marker.Facts
}

// factsFile says file is a project file of format whose facts can be read: a
// base name its facts-declaring marker matches.
func factsFile(format, file string) bool {
	marker, ok := folderdigest.FactsMarker(format)
	if !ok {
		return false
	}
	matched, isMarker := folderdigest.MatchMarker(file, false)
	return isMarker && matched.ProjectFormat == marker.ProjectFormat
}
