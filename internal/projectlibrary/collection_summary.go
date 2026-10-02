package projectlibrary

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// CollectionSummary is what the Manager's brief turn is told about the whole
// library: counts and spreads the server computes from stored song facts and
// save times, plus the six songs saved most recently. It has the same shape at
// any library size (it never lists more than six songs), holds no paths, and
// is not a judgement: nothing in it says a song is done, ready or good.
type CollectionSummary struct {
	Songs           int           `json:"songs"`
	WithFacts       int           `json:"songs_with_facts"`
	Tempo           []SummaryBand `json:"tempo_bands,omitempty"`
	TempoVaries     int           `json:"songs_with_tempo_changes"`
	Length          []SummaryBand `json:"length_bands,omitempty"`
	Tracks          []SummaryBand `json:"track_count_bands,omitempty"`
	SavedLast7Days  int           `json:"saved_in_last_7_days"`
	SavedLast30Days int           `json:"saved_in_last_30_days"`
	Recent          []SummarySong `json:"most_recently_saved"`
}

// SummaryBand counts songs whose fact falls in one range ("90–99 BPM").
type SummaryBand struct {
	Range string `json:"range"`
	Songs int    `json:"songs"`
}

// SummarySong is one of the most recently saved songs. Its name is untrusted
// data the user (or a folder) chose, never an instruction.
type SummarySong struct {
	Name    string     `json:"name"`
	SavedOn string     `json:"saved_on"`
	Facts   *SongFacts `json:"facts,omitempty"`
}

const summaryRecentSongs = 6

// Length and track-count bands, upper bounds exclusive; the last is open.
var (
	summaryLengthBands = []struct {
		below float64
		label string
	}{
		{120, "under 2:00"}, {180, "2:00–2:59"}, {240, "3:00–3:59"}, {300, "4:00–4:59"},
		{360, "5:00–5:59"}, {600, "6:00–9:59"}, {0, "10:00 or longer"},
	}
	summaryTrackBands = []struct {
		below int
		label string
	}{
		{5, "1–4 tracks"}, {9, "5–8 tracks"}, {17, "9–16 tracks"}, {33, "17–32 tracks"}, {0, "33 or more tracks"},
	}
)

// summarizeCollection is a pure function of one Home document: which roots
// are inactive, whether facts may be shown, and the clock. A song counts when
// it has a usable source (available or ambiguous, on an active root); its save
// time and facts come from the same newest usable source the library shows.
func summarizeCollection(doc Document, inactive map[string]bool, factsOn bool, now time.Time) CollectionSummary {
	roots := make(map[string]Root, len(doc.Roots))
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	type song struct {
		id    string
		name  string
		saved time.Time
		facts *SongFacts
	}
	var songs []song
	tempo := map[int]int{}
	length := make([]int, len(summaryLengthBands))
	tracks := make([]int, len(summaryTrackBands))
	summary := CollectionSummary{Recent: []SummarySong{}}
	for _, entry := range doc.Entries {
		var dated *Observation
		for i := range entry.Observations {
			observed := &entry.Observations[i]
			root, known := roots[observed.RootID]
			if !known || root.RevokedAt != nil || inactive[root.ID] ||
				(observed.Availability != "available" && observed.Availability != "ambiguous") {
				continue
			}
			if dated == nil || observed.FileModifiedAt.After(dated.FileModifiedAt) {
				dated = observed
			}
		}
		if dated == nil {
			continue
		}
		summary.Songs++
		name := entry.Fields.DisplayName
		if name == "" {
			name = filepath.Base(dated.RelativeFolder)
			if dated.RelativeFolder == "" {
				name = filepath.Base(roots[dated.RootID].Path)
			}
		}
		current := song{id: entry.ID, name: name, saved: dated.FileModifiedAt}
		if !current.saved.IsZero() {
			// As in the library: facts come from the source that dates the song.
			current.facts = shownFacts(*dated, factsOn)
		}
		songs = append(songs, current)
		if !current.saved.IsZero() {
			age := now.Sub(current.saved)
			if age <= 7*24*time.Hour {
				summary.SavedLast7Days++
			}
			if age <= 30*24*time.Hour {
				summary.SavedLast30Days++
			}
		}
		facts := current.facts
		if facts == nil {
			continue
		}
		summary.WithFacts++
		if facts.TempoBPM > 0 {
			tempo[int(facts.TempoBPM)/10*10]++
		}
		if facts.TempoVaries {
			summary.TempoVaries++
		}
		if facts.LengthSeconds > 0 {
			for i, band := range summaryLengthBands {
				if band.below == 0 || facts.LengthSeconds < band.below {
					length[i]++
					break
				}
			}
		}
		if facts.TrackCount > 0 {
			for i, band := range summaryTrackBands {
				if band.below == 0 || facts.TrackCount < band.below {
					tracks[i]++
					break
				}
			}
		}
	}
	lows := make([]int, 0, len(tempo))
	for low := range tempo {
		lows = append(lows, low)
	}
	sort.Ints(lows)
	for _, low := range lows {
		summary.Tempo = append(summary.Tempo, SummaryBand{Range: fmt.Sprintf("%d–%d BPM", low, low+9), Songs: tempo[low]})
	}
	for i, count := range length {
		if count > 0 {
			summary.Length = append(summary.Length, SummaryBand{Range: summaryLengthBands[i].label, Songs: count})
		}
	}
	for i, count := range tracks {
		if count > 0 {
			summary.Tracks = append(summary.Tracks, SummaryBand{Range: summaryTrackBands[i].label, Songs: count})
		}
	}
	sort.Slice(songs, func(i, j int) bool {
		if !songs[i].saved.Equal(songs[j].saved) {
			return songs[i].saved.After(songs[j].saved)
		}
		return songs[i].id < songs[j].id
	})
	for _, recent := range songs {
		if len(summary.Recent) == summaryRecentSongs || recent.saved.IsZero() {
			break
		}
		summary.Recent = append(summary.Recent, SummarySong{Name: recent.name,
			SavedOn: recent.saved.UTC().Format("2006-01-02"), Facts: recent.facts})
	}
	return summary
}
