package projectlibrary

import (
	"bytes"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SongNames are the names a person typed into a project: its track names and
// its markers and regions. They are read only under a consent that covers
// names, are kept apart from SongFacts (the numbers) so that nothing which
// shows numbers can show them by accident, and are data: stored and shown as
// text, never interpreted. A song read under numbers only has no SongNames.
type SongNames struct {
	Tracks []string `json:"tracks,omitempty"`
	// TracksLeftOut and MarkersLeftOut count the usable entries a cap cut off,
	// for "and K more".
	TracksLeftOut  int          `json:"tracks_left_out,omitempty"`
	Markers        []SongMarker `json:"markers,omitempty"`
	MarkersLeftOut int          `json:"markers_left_out,omitempty"`
}

// SongMarker is one marker or region, in project order. A region has an End
// after its At (both in seconds); a marker has none.
type SongMarker struct {
	Name string  `json:"name,omitempty"`
	At   float64 `json:"at"`
	End  float64 `json:"end,omitempty"`
}

// NotReadNoGrammar is shown for a song whose project format no reviewed app
// plugin reads at all: it is known from the format, so it is never stored. (The
// value keeps the name stored data and the page already use.) A format a
// reviewed plugin owns but that plugin has no reader for is not this: it is
// told as an update (ReaderUpdatePlugin), because installing a newer plugin
// would read it.
const NotReadNoGrammar = "no_grammar"

// SongLearned is what a song's own page shows under "Learned from the project
// file": the names read from the file that dates it, or why it was not read
// (NotReadTooLarge, NotReadUnreadable or NotReadNoGrammar). It is shown to the
// person only, on the song card and the shelf's detail; rows, the roster and
// everything the Manager is given carry numbers only.
type SongLearned struct {
	SongNames
	NotRead string `json:"not_read,omitempty"`
}

// learnedFrom is the learned section of one song, from the usable source that
// dates it (the newest save, the same rule that picks the facts a row shows). It
// is nil unless the Home's consent is active: names appear only under a consent
// that covers them, and a reason appears whenever the Home reads at all. A song
// the pass has not reached yet shows nothing.
func learnedFrom(entry Entry, roots map[string]Root, inactive map[string]bool, consent *workspace.SongDetailsConsent) *SongLearned {
	if !consent.Active() {
		return nil
	}
	var dating *Observation
	for i := range entry.Observations {
		observed := &entry.Observations[i]
		root := roots[observed.RootID]
		usable := root.RevokedAt == nil && !inactive[root.ID] &&
			(observed.Availability == "available" || observed.Availability == "ambiguous")
		if !usable || observed.FileModifiedAt.IsZero() {
			continue
		}
		if dating == nil || observed.FileModifiedAt.After(dating.FileModifiedAt) {
			dating = observed
		}
	}
	if dating == nil {
		return nil
	}
	switch facts := dating.Facts; {
	case facts == nil:
		if !formatHasReviewedPlugin(dating.Format) {
			return &SongLearned{NotRead: NotReadNoGrammar}
		}
		return nil
	case facts.NotRead != "":
		return &SongLearned{NotRead: facts.NotRead}
	case facts.Names != nil && consent.CoversNames():
		return &SongLearned{SongNames: *facts.Names.clone()}
	}
	return nil
}

// LearnedSong is one song of one folder as that folder's own agent may be given
// it for a turn: its name, the numbers, and, under a consent that covers names,
// the names read from its project file, or why it was not read (NotRead). It is
// read by no one but the folder agent's own turns; rows, the roster and the
// Manager's tools never carry it.
type LearnedSong struct {
	Name    string
	Facts   *SongFacts
	Names   *SongNames
	NotRead string
}

// maxLearnedSongs bounds how many songs one folder's reading returns.
const maxLearnedSongs = 500

// LearnedSongsInRoot lists the songs the library holds under one root, with what
// was learned from each: nothing at all unless the Home's consent is active, the
// numbers under any consent, and names only when it covers them. Songs are
// ordered by name. Only the usable source of each song in that root counts, by
// the same rule that dates a song anywhere else.
func (s *Store) LearnedSongsInRoot(scope Scope, rootID string) ([]LearnedSong, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return nil, err
	}
	consent := state.GetSongDetailsConsent()
	if !consent.Active() || rootID == "" {
		return nil, nil
	}
	views := newProjectionMaps(doc, state)
	var songs []LearnedSong
	for _, entry := range doc.Entries {
		inRoot := entry
		inRoot.Observations = nil
		for _, observed := range entry.Observations {
			if observed.RootID == rootID {
				inRoot.Observations = append(inRoot.Observations, observed)
			}
		}
		if len(inRoot.Observations) == 0 {
			continue
		}
		row := s.projectSearchRowInRoot(scope, inRoot, views.roots, views.linked, views.inactive, rootID, true)
		if row.Availability != "available" && row.Availability != "ambiguous" {
			continue
		}
		song := LearnedSong{Name: row.Name, Facts: row.Facts}
		if learned := learnedFrom(inRoot, views.roots, views.inactive, consent); learned != nil {
			song.NotRead = learned.NotRead
			if learned.NotRead == "" {
				names := learned.SongNames
				song.Names = &names
			}
		}
		songs = append(songs, song)
	}
	sort.SliceStable(songs, func(i, j int) bool {
		a, b := strings.ToLower(songs[i].Name), strings.ToLower(songs[j].Name)
		return a < b
	})
	if len(songs) > maxLearnedSongs {
		songs = songs[:maxLearnedSongs]
	}
	return songs, nil
}

// LearnedSongForWorkspace is what the Home saved about the one song linked to
// workspaceID, as the Home's app assistant may be given it inside that song:
// its name, the numbers, and under a consent that covers them the names read
// from its project file, or why it was not read. It gives nothing unless the
// Home's consent is active, so the next task or turn after the switch goes off
// has none (assistant-reads-projects FR43). Only that one song: never another
// song's facts. ok is false when no song of this Home is linked to the workspace.
func (s *Store) LearnedSongForWorkspace(scope Scope, workspaceID string) (LearnedSong, bool, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return LearnedSong{}, false, err
	}
	consent := state.GetSongDetailsConsent()
	if !consent.Active() || strings.TrimSpace(workspaceID) == "" {
		return LearnedSong{}, false, nil
	}
	views := newProjectionMaps(doc, state)
	for _, entry := range doc.Entries {
		if entry.Link == nil || entry.Link.WorkspaceID != workspaceID {
			continue
		}
		// The song's usable source that dates it, the rule every other reader uses.
		rootID := ""
		var newest time.Time
		for _, observed := range entry.Observations {
			root := views.roots[observed.RootID]
			usable := root.RevokedAt == nil && !views.inactive[root.ID] &&
				(observed.Availability == "available" || observed.Availability == "ambiguous")
			if usable && !observed.FileModifiedAt.IsZero() && (rootID == "" || observed.FileModifiedAt.After(newest)) {
				rootID, newest = observed.RootID, observed.FileModifiedAt
			}
		}
		if rootID == "" {
			return LearnedSong{}, false, nil
		}
		inRoot := entry
		inRoot.Observations = nil
		for _, observed := range entry.Observations {
			if observed.RootID == rootID {
				inRoot.Observations = append(inRoot.Observations, observed)
			}
		}
		row := s.projectSearchRowInRoot(scope, inRoot, views.roots, views.linked, views.inactive, rootID, true)
		song := LearnedSong{Name: row.Name, Facts: row.Facts}
		if learned := learnedFrom(inRoot, views.roots, views.inactive, consent); learned != nil {
			song.NotRead = learned.NotRead
			if learned.NotRead == "" {
				names := learned.SongNames
				song.Names = &names
			}
		}
		return song, true, nil
	}
	return LearnedSong{}, false, nil
}

// clone copies the names so a copy shares no slice with the original.
func (n *SongNames) clone() *SongNames {
	if n == nil {
		return nil
	}
	copied := *n
	copied.Tracks = slices.Clone(n.Tracks)
	copied.Markers = slices.Clone(n.Markers)
	return &copied
}

// valid is what the loader accepts of stored names: within every cap, every name
// one the reader could have kept, and times that make sense. A nil value is valid
// (the song was read under numbers only). The size budget is not checked here:
// it is a policy of the write that stores them, so that a document never turns
// unreadable because of how many names it holds.
func (n *SongNames) valid() bool {
	if n == nil {
		return true
	}
	if len(n.Tracks) > maxSongTracks || len(n.Markers) > maxSongMarkers ||
		n.TracksLeftOut < 0 || n.TracksLeftOut > maxSongLeftOut || n.MarkersLeftOut < 0 || n.MarkersLeftOut > maxSongLeftOut {
		return false
	}
	for _, name := range n.Tracks {
		if !validSongName(name) {
			return false
		}
	}
	for _, m := range n.Markers {
		if m.Name != "" && !validSongName(m.Name) {
			return false
		}
		if math.IsNaN(m.At) || math.IsInf(m.At, 0) || m.At < 0 || m.At > maxSongLength {
			return false
		}
		if m.End != 0 && (math.IsNaN(m.End) || math.IsInf(m.End, 0) || m.End <= m.At || m.End > maxSongLength) {
			return false
		}
	}
	return true
}

// Bounds on what is kept per song. The reader never holds more than these, so
// its memory does not grow with the file.
const (
	maxSongTracks    = 128
	maxSongMarkers   = 128
	maxSongNameRunes = 80
	// maxSongNameBytes bounds the JSON-escaped size of all track names, and of
	// all marker names, of one song.
	maxSongNameBytes = 12 << 10
	// maxSongLeftOut is where a left-out counter stops counting.
	maxSongLeftOut = 1 << 20
)

// songNameScratch holds one cleaned name: at most 80 runes of at most 4 bytes.
type songNameScratch [maxSongNameRunes * utf8.UTFMax]byte

// songName cleans one name a project file holds: invalid UTF-8 and every
// control, line-separator and bidirectional-control rune is dropped, leading and
// trailing space is trimmed, and at most 80 runes are kept. It returns false for
// a name that is empty afterwards or shaped like a path, which is dropped.
func songName(raw []byte, scratch *songNameScratch) (string, bool) {
	out := scratch[:0]
	runes := 0
	for len(raw) > 0 && runes < maxSongNameRunes {
		r, size := utf8.DecodeRune(raw)
		raw = raw[size:]
		if r == utf8.RuneError && size <= 1 {
			continue
		}
		if droppedNameRune(r) {
			continue
		}
		if runes == 0 && unicode.IsSpace(r) {
			continue
		}
		out = utf8.AppendRune(out, r)
		runes++
	}
	out = bytes.TrimRightFunc(out, unicode.IsSpace)
	if len(out) == 0 || pathShapedName(string(out)) {
		return "", false
	}
	return string(out), true
}

// droppedNameRune is a rune no stored name keeps: controls, the line and
// paragraph separators, and the bidirectional controls that can reorder the text
// around a name.
func droppedNameRune(r rune) bool {
	switch {
	case unicode.IsControl(r):
		return true
	case r == 0x2028 || r == 0x2029:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// validSongName says a stored name is one songName could have produced: not
// empty, valid UTF-8, at most 80 runes, nothing dropped, already trimmed, and
// not shaped like a path. The reader and the loader share it, so a name the
// reader keeps is a name the document accepts.
func validSongName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxSongNameRunes {
		return false
	}
	for _, r := range name {
		if droppedNameRune(r) {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last) && !pathShapedName(name)
}

// pathShapedName says a name reads as a location on a disk, which a project's
// own name for something is not: ".", "..", a name starting with a separator, a
// home shorthand, "./", "../" or a drive letter, or one holding a backslash or a
// separator that follows a space or an opening bracket. A separator in the middle
// of a word is allowed: "Vox L/R" and "AC/DC" are names.
func pathShapedName(s string) bool {
	switch {
	case s == "." || s == ".." || s == "~":
		return true
	case len(s) > 0 && s[0] == '/', startsWith(s, "~/"), startsWith(s, "./"), startsWith(s, "../"):
		return true
	case containsAny(s, `\`, " /", " ~/", "(/"):
		return true
	case len(s) >= 3 && isASCIILetter(s[0]) && s[1] == ':' && (s[2] == '/' || s[2] == '\\'):
		return true
	}
	return false
}

func startsWith(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

func containsAny(s string, parts ...string) bool {
	for _, part := range parts {
		if bytes.Contains([]byte(s), []byte(part)) {
			return true
		}
	}
	return false
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// maxLibraryNamesBytes bounds the stored names of every song in one library
// together, so a library full of long projects cannot crowd out its own listing
// (the document as a whole is capped at 8 MiB). 2 MiB holds about 80 songs with
// every name at its longest, or some two thousand ordinary ones. It is a
// variable so a test can lower it.
var maxLibraryNamesBytes = 2 << 20

// What a stored track or marker costs beyond its name: its JSON punctuation, and
// for a marker its two times and keys.
const (
	trackNameOverhead  = 4
	markerNameOverhead = 48
)

// songNamesSize is what a song's names cost against the library's names budget.
func songNamesSize(n *SongNames) int {
	if n == nil {
		return 0
	}
	size := 0
	for _, name := range n.Tracks {
		size += songNameCost(name) + trackNameOverhead
	}
	for _, m := range n.Markers {
		size += songNameCost(m.Name) + markerNameOverhead
	}
	return size
}

// libraryNamesSize is what every song's stored names cost together.
func libraryNamesSize(doc *Document) int {
	size := 0
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			if facts := doc.Entries[i].Observations[j].Facts; facts != nil {
				size += songNamesSize(facts.Names)
			}
		}
	}
	return size
}

// cutSongNames returns a copy of names that costs at most room, dropping entries
// from the end of whichever list is larger and counting each as left out, so the
// reader shows "and K more". It never changes its argument: a write that runs
// again after a conflict cuts afresh.
func cutSongNames(names *SongNames, room int) *SongNames {
	if names == nil {
		return nil
	}
	cut := names.clone()
	trackBytes, markerBytes := 0, 0
	for _, name := range cut.Tracks {
		trackBytes += songNameCost(name) + trackNameOverhead
	}
	for _, m := range cut.Markers {
		markerBytes += songNameCost(m.Name) + markerNameOverhead
	}
	for trackBytes+markerBytes > room {
		switch {
		case len(cut.Markers) > 0 && (markerBytes >= trackBytes || len(cut.Tracks) == 0):
			last := cut.Markers[len(cut.Markers)-1]
			markerBytes -= songNameCost(last.Name) + markerNameOverhead
			cut.Markers = cut.Markers[:len(cut.Markers)-1]
			cut.MarkersLeftOut = addLeftOut(cut.MarkersLeftOut)
		case len(cut.Tracks) > 0:
			last := cut.Tracks[len(cut.Tracks)-1]
			trackBytes -= songNameCost(last) + trackNameOverhead
			cut.Tracks = cut.Tracks[:len(cut.Tracks)-1]
			cut.TracksLeftOut = addLeftOut(cut.TracksLeftOut)
		default:
			return cut
		}
	}
	if len(cut.Tracks) == 0 {
		cut.Tracks = nil
	}
	if len(cut.Markers) == 0 {
		cut.Markers = nil
	}
	return cut
}

// readsHoldNames says some read brought track names or markers.
func readsHoldNames(reads []songFactsRead) bool {
	for _, read := range reads {
		if read.Names != nil && (len(read.Names.Tracks) > 0 || len(read.Names.Markers) > 0) {
			return true
		}
	}
	return false
}

// namesAsCounts is the reads with each song's names reduced to how many there
// were: the song is still marked as read under names, and the card can say how
// many names there are without listing them. The numbers are untouched.
func namesAsCounts(reads []songFactsRead) []songFactsRead {
	out := make([]songFactsRead, len(reads))
	for i, read := range reads {
		out[i] = read
		if read.Names != nil {
			out[i].Names = &SongNames{
				TracksLeftOut:  min(len(read.Names.Tracks)+read.Names.TracksLeftOut, maxSongLeftOut),
				MarkersLeftOut: min(len(read.Names.Markers)+read.Names.MarkersLeftOut, maxSongLeftOut),
			}
		}
	}
	return out
}

// songNameCost is what a name costs against the stored-size budget: its bytes
// as they will be written in JSON, where a quote or backslash takes two and the
// characters the encoder escapes for HTML take six.
func songNameCost(name string) int {
	cost := 0
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '"', '\\':
			cost += 2
		case '<', '>', '&':
			cost += 6
		default:
			cost++
		}
	}
	return cost
}

// nameCollector bounds the track names a reader returned: each cleaned with
// songName, at most 128 within the byte budget, the rest counted as left out.
// Markers and regions are bounded the same way by projectFactsNames. Everything
// in it is bounded and it never grows with the input.
type nameCollector struct {
	tracks        []string
	trackBytes    int
	tracksLeftOut int

	scratch songNameScratch
}

func newNameCollector() *nameCollector {
	return &nameCollector{tracks: make([]string, 0, maxSongTracks)}
}

func addLeftOut(n int) int { return min(n+1, maxSongLeftOut) }

// noteTrack takes the name of one track.
func (c *nameCollector) noteTrack(raw []byte) {
	name, ok := songName(raw, &c.scratch)
	if !ok {
		return
	}
	cost := songNameCost(name)
	if len(c.tracks) >= maxSongTracks || c.trackBytes+cost > maxSongNameBytes {
		c.tracksLeftOut = addLeftOut(c.tracksLeftOut)
		return
	}
	c.tracks = append(c.tracks, name)
	c.trackBytes += cost
}
