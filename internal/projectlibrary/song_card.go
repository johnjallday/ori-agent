package projectlibrary

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Facts states a song card reports. "off": the Home's project-details switch
// is off (or was never turned on). "absent": the switch is on but this song's
// stored observation holds no facts. "on": facts are shown.
const (
	FactsStateOn     = "on"
	FactsStateOff    = "off"
	FactsStateAbsent = "absent"
)

// maxSessionSentence bounds the recap excerpt a card or roster row shows.
const maxSessionSentence = 120

// SongSession is the most recent accepted studio session of a song that has a
// recap: when it was accepted and the recap's first sentence. User-reviewed
// text only; never a model summary, a Ticket or evidence of DAW work.
type SongSession struct {
	AcceptedAt    time.Time `json:"accepted_at"`
	FirstSentence string    `json:"first_sentence"`
}

// SongCard is the read-only projection of one linked song for its own pages
// (Workspace Details, the map's selected context). Every value is already
// stored: the Home library's entry and observation, the child's link, its
// entry agent's name and its own Ticket counts. Building it reads no project
// file, starts no scan and calls no model.
type SongCard struct {
	HomeID string `json:"home_id"`
	// EntryID is the Home library entry whose reciprocal link to this child is
	// connected now. Empty when the link is broken, diverged or not yet
	// adopted by the library; the card then shows no facts and no entry link.
	EntryID         string                 `json:"entry_id,omitempty"`
	Title           string                 `json:"title"`
	Facts           *SongFacts             `json:"facts,omitempty"`
	FactsState      string                 `json:"facts_state,omitempty"`
	LastSavedAt     *time.Time             `json:"last_saved_at,omitempty"`
	KeeperAgentName string                 `json:"keeper_agent_name,omitempty"`
	LastSession     *SongSession           `json:"last_session,omitempty"`
	OpenWork        workspace.TicketCounts `json:"open_work_counts"`
}

// RosterRow is one connected song in the Home's "Songs and assistants" list.
// The keeper's name is the only thing read from the child's roster; no agent
// record, prompt or memory crosses the link.
type RosterRow struct {
	WorkspaceID     string       `json:"workspace_id"`
	EntryID         string       `json:"entry_id"`
	Title           string       `json:"title"`
	KeeperAgentName string       `json:"keeper_agent_name,omitempty"`
	LastSession     *SongSession `json:"last_session"`
	LastSavedAt     *time.Time   `json:"last_saved_at,omitempty"`
	// Facts are for the map tile's meta line; Home Details does not repeat
	// them (the shelf shows them). Nil when unknown or the switch is off.
	Facts    *SongFacts             `json:"facts,omitempty"`
	OpenWork workspace.TicketCounts `json:"open_work_counts"`
	// FolderSlug lets the host build the child's page route; never sent.
	FolderSlug string `json:"-"`
}

// songChild returns the child when it is the owner's live, non-resource song
// whose own link names this Home and program. Library membership and the
// reciprocal entry are checked separately: a song with a broken library link
// still has a card, just without facts.
func (s *Store) songChild(scope Scope, childID string) (*workspace.Workspace, error) {
	if s == nil || s.workspaces == nil || !scope.valid() || childID == "" || childID == scope.HomeID || !validText(childID, 160) {
		return nil, ErrUnavailable
	}
	child, err := s.workspaces.Get(childID)
	if err != nil || child == nil || child.ID != childID || child.OwnerUserID != scope.OwnerUserID ||
		child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing {
		return nil, ErrConflict
	}
	link := child.GetAssistantProjectLink()
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	if link == nil || link.StationWorkspaceID != scope.HomeID || link.Key.Normalize() != key.Normalize() ||
		workspace.AssistantProjectLinkIsResource(link) {
		return nil, ErrConflict
	}
	return child, nil
}

// SongCard projects one linked child. A broken or diverged link, or a Home
// whose library is not set up, returns a card with the child's own name and
// keeper but no entry, facts or save time (PRD FR4).
func (s *Store) SongCard(scope Scope, childID string) (SongCard, error) {
	child, err := s.songChild(scope, childID)
	if err != nil {
		return SongCard{}, err
	}
	card := SongCard{HomeID: scope.HomeID, Title: child.Name, KeeperAgentName: child.EntryAgentName(),
		OpenWork: child.TicketCounts()}
	if !workspace.AssistantProjectLinkMirrorsAgree(s.workspaces, child) {
		return card, nil
	}
	doc, state, err := s.readSnapshot(scope)
	if errors.Is(err, ErrNotInitialized) || errors.Is(err, ErrMirrorDiverged) {
		return card, nil
	}
	if err != nil {
		return SongCard{}, err
	}
	factsOn := state.GetSongDetailsConsent().Active()
	views := newProjectionMaps(doc, state)
	for _, entry := range doc.Entries {
		if entry.Link == nil || entry.Link.WorkspaceID != childID {
			continue
		}
		row := s.projectSearchRow(scope, entry, views.roots, views.linked, views.inactive, factsOn)
		if row.Connection != "connected" {
			return card, nil
		}
		card.EntryID, card.Title, card.LastSavedAt, card.Facts = entry.ID, row.Name, row.LastSavedAt, row.Facts
		switch {
		case !factsOn:
			card.FactsState = FactsStateOff
		case row.Facts == nil:
			card.FactsState = FactsStateAbsent
		default:
			card.FactsState = FactsStateOn
		}
		card.LastSession = lastAcceptedRecap(doc.Sessions, entry.ID)
		return card, nil
	}
	return card, nil
}

// Roster lists the Home's connected songs: the same rows the shelf counts as
// connected, ordered by most recent session, then most recent save, then name.
func (s *Store) Roster(scope Scope) ([]RosterRow, error) {
	doc, state, err := s.readSnapshot(scope)
	if errors.Is(err, ErrNotInitialized) {
		return []RosterRow{}, nil
	}
	if err != nil {
		return nil, err
	}
	views := newProjectionMaps(doc, state)
	factsOn := state.GetSongDetailsConsent().Active()
	rows := []RosterRow{}
	for _, entry := range doc.Entries {
		if entry.Link == nil {
			continue
		}
		row := s.projectSearchRow(scope, entry, views.roots, views.linked, views.inactive, factsOn)
		if row.Connection != "connected" {
			continue
		}
		child, getErr := s.workspaces.Get(entry.Link.WorkspaceID)
		if getErr != nil || child == nil {
			continue
		}
		rows = append(rows, RosterRow{WorkspaceID: child.ID, EntryID: entry.ID, Title: row.Name,
			KeeperAgentName: child.EntryAgentName(), LastSession: lastAcceptedRecap(doc.Sessions, entry.ID),
			LastSavedAt: row.LastSavedAt, Facts: row.Facts, OpenWork: child.TicketCounts(), FolderSlug: child.FolderSlug})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rosterLess(rows[i], rows[j]) })
	return rows, nil
}

func rosterLess(a, b RosterRow) bool {
	if (a.LastSession == nil) != (b.LastSession == nil) {
		return a.LastSession != nil
	}
	if a.LastSession != nil && !a.LastSession.AcceptedAt.Equal(b.LastSession.AcceptedAt) {
		return a.LastSession.AcceptedAt.After(b.LastSession.AcceptedAt)
	}
	if (a.LastSavedAt == nil) != (b.LastSavedAt == nil) {
		return a.LastSavedAt != nil
	}
	if a.LastSavedAt != nil && !a.LastSavedAt.Equal(*b.LastSavedAt) {
		return a.LastSavedAt.After(*b.LastSavedAt)
	}
	if !strings.EqualFold(a.Title, b.Title) {
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	}
	return a.WorkspaceID < b.WorkspaceID
}

type projectionMaps struct {
	roots            map[string]Root
	linked, inactive map[string]bool
}

func newProjectionMaps(doc Document, state *workspace.AssistantProgramState) projectionMaps {
	views := projectionMaps{roots: make(map[string]Root, len(doc.Roots)),
		linked: make(map[string]bool, len(state.LinkedProjectIDs)), inactive: make(map[string]bool, len(state.ProjectLibraryInactiveRoots))}
	for _, root := range doc.Roots {
		views.roots[root.ID] = root
	}
	for _, id := range state.LinkedProjectIDs {
		views.linked[id] = true
	}
	for _, id := range state.ProjectLibraryInactiveRoots {
		views.inactive[id] = true
	}
	return views
}

// lastAcceptedRecap is the newest accepted session (ListSessions' order) that
// has a recap a page may show. A recap naming a file, path or link is skipped
// rather than echoed (PRD FR6).
func lastAcceptedRecap(records []StudioSession, entryID string) *SongSession {
	var best *StudioSession
	for i := range records {
		record := &records[i]
		if record.EntryID != entryID || record.State != "accepted" || firstSentence(record.Recap) == "" {
			continue
		}
		if best == nil || record.UpdatedAt.After(best.UpdatedAt) ||
			(record.UpdatedAt.Equal(best.UpdatedAt) && record.ID < best.ID) {
			best = record
		}
	}
	if best == nil {
		return nil
	}
	at := best.UpdatedAt
	if best.AcceptedAt != nil {
		at = *best.AcceptedAt
	}
	return &SongSession{AcceptedAt: at.UTC(), FirstSentence: firstSentence(best.Recap)}
}

var (
	recapLink     = regexp.MustCompile(`(?i)(://|www\.|mailto:)`)
	recapPath     = regexp.MustCompile(`(^|[\s(])(~|\.{1,2})?[/\\][^\s]|[A-Za-z]:\\|\w[/\\]\w`)
	recapFileName = regexp.MustCompile(`\w\.[A-Za-z][A-Za-z0-9]{1,4}\b`)
)

// firstSentence is a recap's first sentence on one line, at most 120
// characters, without its closing period. "" when the recap is empty or the
// sentence names a file, path or link.
func firstSentence(recap string) string {
	text := strings.TrimSpace(recap)
	if line := strings.IndexAny(text, "\r\n"); line >= 0 {
		text = text[:line]
	}
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := text[i+utf8.RuneLen(r):]
		if next == "" || next[0] == ' ' {
			if r == '.' {
				text = text[:i]
			} else {
				text = text[:i+utf8.RuneLen(r)]
			}
			break
		}
	}
	text = strings.TrimSpace(text)
	if text == "" || recapLink.MatchString(text) || recapPath.MatchString(text) || recapFileName.MatchString(text) {
		return ""
	}
	return cutRunes(text, maxSessionSentence)
}

// cutRunes keeps at most max characters, ending on a word with "…" when cut.
func cutRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)[:max-1]
	cut := string(runes)
	if space := strings.LastIndex(cut, " "); space > max/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// StatusLine is the song's one plain sentence, from stored data only, in this
// order and skipping what is unknown: facts, "saved <relative>", "last
// session: <first sentence>", open work. It never names a file, path or link
// and makes no claim about how far along the song is.
func StatusLine(card SongCard, now time.Time) string {
	parts := []string{}
	if card.FactsState == FactsStateOn {
		if facts := FactsLabel(card.Facts); facts != "" {
			parts = append(parts, facts)
		}
	}
	if card.LastSavedAt != nil {
		if saved := SavedLabel(*card.LastSavedAt, now); saved != "" {
			parts = append(parts, "s"+strings.TrimPrefix(saved, "S"))
		}
	}
	if card.LastSession != nil && card.LastSession.FirstSentence != "" {
		parts = append(parts, "last session: "+card.LastSession.FirstSentence)
	}
	open := OpenWorkLabel(card.OpenWork)
	if len(parts) > 0 {
		open = strings.Replace(open, "No open work", "no open work", 1)
	}
	parts = append(parts, open)
	line := strings.Join(parts, "; ")
	if r, size := utf8.DecodeRuneInString(line); size > 0 {
		line = string(unicode.ToUpper(r)) + line[size:]
	}
	return line
}

// FactsLabel words the facts exactly as the shelf's songFactsLabel does
// (library-open.js): "14 tracks · 92 BPM · 3:41", "92 BPM, varies". An unknown
// part is left out; no facts is "".
func FactsLabel(facts *SongFacts) string {
	if facts == nil {
		return ""
	}
	parts := []string{}
	if facts.TrackCount > 0 {
		parts = append(parts, pluralWord(facts.TrackCount, "track"))
	}
	if facts.TempoBPM > 0 {
		tempo := strings.TrimSuffix(fmt.Sprintf("%.1f", math.Round(facts.TempoBPM*10)/10), ".0") + " BPM"
		if facts.TempoVaries {
			tempo += ", varies"
		}
		parts = append(parts, tempo)
	}
	if total := int(math.Round(facts.LengthSeconds)); facts.LengthSeconds > 0 && total > 0 {
		hours, minutes, seconds := total/3600, (total%3600)/60, total%60
		if hours > 0 {
			parts = append(parts, fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%02d", minutes, seconds))
		}
	}
	return strings.Join(parts, " · ")
}

// SavedLabel is the shelf's lastSavedLabel in the server's calendar days:
// "Saved today", "Saved 3 days ago", "Saved 2 weeks ago", "Saved 4 months
// ago", "Saved Mar 2024". A save time is never evidence of work.
func SavedLabel(saved, now time.Time) string {
	if saved.IsZero() || saved.Year() <= 1 {
		return ""
	}
	local, today := saved.In(now.Location()), now
	days := int(math.Round(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).
		Sub(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24))
	switch {
	case days <= 0:
		return "Saved today"
	case days == 1:
		return "Saved yesterday"
	case days < 14:
		return fmt.Sprintf("Saved %d days ago", days)
	case days < 45:
		return fmt.Sprintf("Saved %d weeks ago", days/7)
	case days < 335:
		return "Saved " + pluralWord(max(1, int(math.Round(float64(days)/30.44))), "month") + " ago"
	}
	return fmt.Sprintf("Saved %s %d", local.Month().String()[:3], local.Year())
}

// OpenWorkLabel is ticket-count-line.js's format(): "1 in review · 2 in
// progress · 3 ready", or "No open work". Backlog, Done and Cancelled are
// never part of it.
func OpenWorkLabel(counts workspace.TicketCounts) string {
	parts := []string{}
	for _, part := range []struct {
		count  int
		phrase string
	}{{counts.Review, "in review"}, {counts.InProgress, "in progress"}, {counts.Ready, "ready"}} {
		if part.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.count, part.phrase))
		}
	}
	if len(parts) == 0 {
		return "No open work"
	}
	return strings.Join(parts, " · ")
}

func pluralWord(count int, word string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, word)
	}
	return fmt.Sprintf("%d %ss", count, word)
}
