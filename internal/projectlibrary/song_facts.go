package projectlibrary

import (
	"math"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// SongFacts are three numbers read from one project file: its base tempo, the
// end of its last item and its track count. They are facts, never a
// judgement of progress. The names a person typed into the project (track
// names, markers and regions) are not part of them: they are read only under a
// consent that covers names, and are kept apart as SongNames. Item names, notes
// and plugins are never read. A zero value means unknown and is left out
// wherever facts are shown.
//
// The host does not read project files. An app plugin's reader does, and the
// host keeps only what it returns after bounding it with the limits below
// (assistant-reads-projects).
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

// maxSongFileBytes is the largest project file a reader is asked to read. A
// file listed over it is recorded as too large without a call.
const maxSongFileBytes int64 = 32 << 20

// factsFile says file is a project file of format: a base name a project marker
// of that format matches. Whether anything reads that format is a different
// question, answered by the installed app plugins' readers.
func factsFile(format, file string) bool {
	if format == "" {
		return false
	}
	matched, isMarker := folderdigest.MatchMarker(file, false)
	return isMarker && matched.ProjectFormat == format
}

// formatHasReviewedPlugin says a reviewed app plugin owns the format, so a newer
// or newly installed copy of it could read the format's songs. A format no
// reviewed plugin owns can't be read at all.
func formatHasReviewedPlugin(format string) bool {
	_, ok := folderdigest.IntegrationKeyForProjectFormat(format)
	return ok
}

func (f SongFacts) empty() bool {
	return f.TempoBPM == 0 && !f.TempoVaries && f.LengthSeconds == 0 && f.TrackCount == 0
}

func (f SongFacts) valid() bool {
	return (f.TempoBPM == 0 || (f.TempoBPM >= minSongTempo && f.TempoBPM <= maxSongTempo)) &&
		(!f.TempoVaries || f.TempoBPM != 0) &&
		f.LengthSeconds >= 0 && f.LengthSeconds <= maxSongLength && !math.IsNaN(f.LengthSeconds) &&
		f.TrackCount >= 0 && f.TrackCount <= maxSongTrackCount
}
