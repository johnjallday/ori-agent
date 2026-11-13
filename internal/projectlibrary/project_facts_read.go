package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

// One batch of song files read through an app plugin's reader
// (assistant-reads-projects FR22 to FR28). The host resolves the files from the
// scan it holds, opens each through the pinned no-follow walk and keeps it open
// while the plugin reads it by path, then checks the held file and a fresh open
// against what the listing saw. Every value the plugin returns is bounded again
// with the stored facts' own limits, and names are kept only when the Home's
// consent covers them, whatever came back.

// ProjectFactsCaller calls one plugin's declared reader with the host's input
// and returns its output after the host's schema, size and time checks
// (plugin.SurfaceLifecycle.ProjectFacts in production).
type ProjectFactsCaller func(ctx context.Context, installed plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error)

// errProjectFactsCall means a reader call failed or answered outside the
// contract: the whole batch is dropped and the read counts as stopped, never as
// "no facts" (FR27).
var errProjectFactsCall = errors.New("project reader call failed")

type projectFactsFileInput struct {
	ID     int    `json:"id"`
	Format string `json:"format"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}

type projectFactsInput struct {
	SchemaVersion int                     `json:"schema_version"`
	Names         bool                    `json:"names"`
	MaxFileBytes  int64                   `json:"max_file_bytes"`
	Files         []projectFactsFileInput `json:"files"`
}

// projectFactsMarker is one marker or region as a reader returns it.
type projectFactsMarker struct {
	Name string  `json:"name"`
	At   float64 `json:"at"`
	End  float64 `json:"end"`
}

// projectFactsResult is one file's answer from a reader.
type projectFactsResult struct {
	ID             int                  `json:"id"`
	Result         string               `json:"result"`
	TempoBPM       float64              `json:"tempo_bpm"`
	TempoVaries    bool                 `json:"tempo_varies"`
	LengthSeconds  float64              `json:"length_seconds"`
	TrackCount     int                  `json:"track_count"`
	Tracks         []string             `json:"tracks"`
	TracksLeftOut  int                  `json:"tracks_left_out"`
	Markers        []projectFactsMarker `json:"markers"`
	MarkersLeftOut int                  `json:"markers_left_out"`
}

type projectFactsOutput struct {
	SchemaVersion int                  `json:"schema_version"`
	Files         []projectFactsResult `json:"files"`
}

// Reader results.
const (
	projectFactsRead       = "read"
	projectFactsTooLarge   = "too_large"
	projectFactsUnreadable = "unreadable"
	projectFactsFailed     = "failed"
)

// projectFactsBatchCounts is what one batch did, counts only.
type projectFactsBatchCounts struct {
	// Read gave facts; TooLarge and Unreadable gave nothing because of their
	// content (both stored); Changed were no longer the file the listing saw;
	// Failed could not be read this time (not stored, read again later).
	Read, TooLarge, Unreadable, Changed, Failed int
}

// readProjectFactsBatch reads up to reader.batch targets through the plugin. A
// file listed over the per-file cap is too large without a call. The caller
// holds the root gate and checks the root and the consent before and after.
// A call error drops the whole batch (errProjectFactsCall).
func readProjectFactsBatch(ctx context.Context, root Root, targets []songFactsTarget, grant songFactsGrant,
	reader projectReader, call ProjectFactsCaller) ([]songFactsRead, projectFactsBatchCounts, error) {
	var counts projectFactsBatchCounts
	var reads []songFactsRead
	type held struct {
		target songFactsTarget
		file   *os.File
	}
	var open []held
	defer func() {
		for _, h := range open {
			_ = h.file.Close()
		}
	}()
	input := projectFactsInput{SchemaVersion: 1, Names: grant.names, MaxFileBytes: DefaultSongFactsLimits.MaxBytes}
	for _, target := range targets {
		if len(open) >= reader.batch || len(open) >= plugin.ProjectFactsMaxFilesPerCall {
			break
		}
		if target.File.Size > DefaultSongFactsLimits.MaxBytes {
			counts.TooLarge++
			reads = append(reads, songFactsRead{songFactsTarget: target, NotRead: NotReadTooLarge})
			continue
		}
		file, err := openPinnedProjectFile(root, target.Relative, target.FolderIdentity, target.File)
		if err != nil {
			// Gone or replaced since the listing: read on a later scan.
			counts.Changed++
			continue
		}
		input.Files = append(input.Files, projectFactsFileInput{
			ID: len(open), Format: target.Format, Size: target.File.Size,
			// Built only from the root, the song's folder and its dated file.
			Path: filepath.Join(root.Path, filepath.FromSlash(target.Relative), target.File.File),
		})
		open = append(open, held{target: target, file: file})
	}
	if len(open) == 0 {
		return reads, counts, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, projectFactsBatchCounts{}, errProjectFactsCall
	}
	raw, err := call(ctx, reader.plugin, encoded)
	if err != nil {
		return nil, projectFactsBatchCounts{}, errors.Join(errProjectFactsCall, err)
	}
	var output projectFactsOutput
	if json.Unmarshal(raw, &output) != nil || output.SchemaVersion != 1 || len(output.Files) != len(open) {
		return nil, projectFactsBatchCounts{}, errProjectFactsCall
	}
	byID := make(map[int]projectFactsResult, len(output.Files))
	for _, result := range output.Files {
		if _, duplicate := byID[result.ID]; duplicate || result.ID < 0 || result.ID >= len(open) {
			return nil, projectFactsBatchCounts{}, errProjectFactsCall
		}
		byID[result.ID] = result
	}
	for id, h := range open {
		// FR26: the held file and a fresh open must still be the file the
		// listing saw, after the plugin read it by path.
		if !pinnedFileStillMatches(h.file, h.target.File) || !freshOpenMatches(root, h.target) {
			counts.Changed++
			continue
		}
		read, keep := mapProjectFacts(h.target, byID[id], grant)
		if !keep {
			counts.Failed++
			continue
		}
		switch read.NotRead {
		case NotReadTooLarge:
			counts.TooLarge++
		case NotReadUnreadable:
			counts.Unreadable++
		default:
			counts.Read++
		}
		reads = append(reads, read)
	}
	return reads, counts, nil
}

// freshOpenMatches opens the target again through the pinned walk and checks
// it is still the listed file.
func freshOpenMatches(root Root, target songFactsTarget) bool {
	file, err := openPinnedProjectFile(root, target.Relative, target.FolderIdentity, target.File)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	return pinnedFileStillMatches(file, target.File)
}

// mapProjectFacts turns one reader result into what is stored (FR24, FR25). A
// file the reader could not read this time is not kept. A value outside the
// stored facts' bounds is dropped for that song; names pass the same cleaning
// and caps as names read by the host, and are kept only under a grant that
// covers them.
func mapProjectFacts(target songFactsTarget, result projectFactsResult, grant songFactsGrant) (songFactsRead, bool) {
	read := songFactsRead{songFactsTarget: target}
	switch result.Result {
	case projectFactsTooLarge:
		read.NotRead = NotReadTooLarge
		return read, true
	case projectFactsUnreadable:
		read.NotRead = NotReadUnreadable
		return read, true
	case projectFactsRead:
	default: // failed, or anything else
		return songFactsRead{}, false
	}
	facts := SongFacts{TempoVaries: result.TempoVaries}
	if tempo := result.TempoBPM; tempo >= minSongTempo && tempo <= maxSongTempo && !math.IsNaN(tempo) {
		facts.TempoBPM = math.Round(tempo*10) / 10
	}
	if facts.TempoBPM == 0 {
		facts.TempoVaries = false
	}
	if length := result.LengthSeconds; length > 0 && length <= maxSongLength && !math.IsNaN(length) {
		facts.LengthSeconds = math.Round(length*1000) / 1000
	}
	if result.TrackCount > 0 && result.TrackCount <= maxSongTrackCount {
		facts.TrackCount = result.TrackCount
	}
	if !facts.valid() {
		facts = SongFacts{}
	}
	read.Facts = facts
	if grant.names {
		read.Names = projectFactsNames(result)
	}
	return read, true
}

// projectFactsNames bounds a reader's names exactly as the host's own reader
// does: each name cleaned (songName), path-shaped ones dropped, at most 128 of
// each kind within its byte budget, the rest counted as left out, project order
// kept. A result that still fails the loader's check keeps no names, but is
// marked as read under names.
func projectFactsNames(result projectFactsResult) *SongNames {
	collector := newNameCollector(nil)
	for _, raw := range result.Tracks {
		collector.noteTrack([]byte(raw))
	}
	names := &SongNames{TracksLeftOut: collector.tracksLeftOut}
	if len(collector.tracks) > 0 {
		names.Tracks = collector.tracks
	}
	var scratch songNameScratch
	bytes := 0
	for _, marker := range result.Markers {
		at := math.Round(marker.At*1000) / 1000
		if math.IsNaN(at) || at < 0 || at > maxSongLength {
			continue
		}
		name, _ := songName([]byte(marker.Name), &scratch)
		cost := songNameCost(name)
		if len(names.Markers) >= maxSongMarkers || bytes+cost > maxSongNameBytes {
			names.MarkersLeftOut = addLeftOut(names.MarkersLeftOut)
			continue
		}
		entry := SongMarker{Name: name, At: at}
		if end := math.Round(marker.End*1000) / 1000; end > at && end <= maxSongLength {
			entry.End = end
		}
		names.Markers = append(names.Markers, entry)
		bytes += cost
	}
	names.TracksLeftOut = min(names.TracksLeftOut+max(result.TracksLeftOut, 0), maxSongLeftOut)
	names.MarkersLeftOut = min(names.MarkersLeftOut+max(result.MarkersLeftOut, 0), maxSongLeftOut)
	if !names.valid() {
		return &SongNames{}
	}
	return names
}
