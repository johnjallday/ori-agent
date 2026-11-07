package projectlibrary

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// songCardFixture is a set-up library with three linked children:
//   - song-a (child-a): facts, two accepted recaps and a newer draft, open work;
//   - song-b (child-b): saved later than song-a, no facts, no sessions;
//   - song-c (child-c): links to the Home but the Home does not list it, so
//     the library calls it needs_review.
//
// Plus a catalog-only entry with no link.
func songCardFixture(t *testing.T) (*Store, Scope, *workspace.FileStore, time.Time) {
	t.Helper()
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	day := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	links := map[string]*workspace.AssistantProjectLink{}
	for _, child := range []struct {
		id, name, keeper string
		tasks            []workspace.Task
	}{
		{"child-a", "Child A folder name", "Studio Keeper", []workspace.Task{
			{ID: "t1", TicketState: workspace.TicketStateReview},
			{ID: "t2", TicketState: workspace.TicketStateReady},
			{ID: "t3", TicketState: workspace.TicketStateReady},
			{ID: "t4", TicketState: workspace.TicketStateDone},
		}},
		{"child-b", "Song B", "", nil},
		{"child-c", "Song C", "", nil},
	} {
		ws := &workspace.Workspace{ID: child.id, Name: child.name, OwnerUserID: scope.OwnerUserID,
			Status: workspace.StatusActive, CreatedAt: day, UpdatedAt: day, Tasks: child.tasks}
		link := &workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.id),
			SchemaVersion: workspace.AssistantProjectLinkSchemaVersion, StationWorkspaceID: scope.HomeID,
			Key: key, StateRevision: 1, LinkedAt: day}
		ws.SetAssistantProjectLink(link)
		if child.keeper != "" {
			ws.AgentInstances = []workspace.AgentInstance{{Name: child.keeper}}
			if err := ws.SetEntryAgentName(child.keeper); err != nil {
				t.Fatal(err)
			}
		}
		if err := file.Save(ws); err != nil {
			t.Fatal(err)
		}
		links[child.id] = link
	}
	base := t.TempDir()
	if _, _, err := s.mutate(scope, 1, operation{key: "song-card-fixture", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			doc.Roots = append(doc.Roots, Root{ID: "home-root", Path: filepath.Join(base, "home"), FileIdentity: "home",
				Revision: 1, ApprovedAt: day.AddDate(0, 0, -30)})
			finished := day.AddDate(0, 0, -30)
			doc.Scans = append(doc.Scans, Scan{ID: "scan-home", RootID: "home-root", RootRevision: 1,
				RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
				Status: "complete", StartedAt: finished, FinishedAt: &finished})
			observed := func(id string, saved time.Time, facts *ObservedFacts) Observation {
				return Observation{RootID: "home-root", RelativeFolder: id, FileIdentity: "fixture:" + id, Format: "reaper",
					Alternates: []string{"Song.rpp"}, ScanID: "scan-home", ScannedAt: finished,
					FileModifiedAt: saved, Availability: "available", Facts: facts}
			}
			savedA := day.AddDate(0, 0, -3)
			factsA := &ObservedFacts{SongFacts: SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14},
				ReadFrom: FactsSource{File: "Song.rpp", Identity: "fixture:song-a", Size: 10, ModifiedAt: savedA}}
			for _, entry := range []Entry{
				{ID: "song-a", Revision: 1, Fields: Fields{DisplayName: "Song A"},
					Observations: []Observation{observed("song-a", savedA, factsA)}},
				{ID: "song-b", Revision: 1, Observations: []Observation{observed("song-b", day.AddDate(0, 0, -1), nil)}},
				{ID: "song-c", Revision: 1, Fields: Fields{DisplayName: "Song C"}},
				{ID: "catalog", Revision: 1, Fields: Fields{DisplayName: "Catalog only"},
					Observations: []Observation{observed("catalog", day, nil)}},
			} {
				if child := "child-" + strings.TrimPrefix(entry.ID, "song-"); links[child] != nil {
					entry.Link = &ExactLink{WorkspaceID: child, LinkID: links[child].ID, Revision: 1}
				}
				doc.Entries = append(doc.Entries, entry)
			}
			session := func(id, state, recap string, at time.Time) StudioSession {
				record := StudioSession{ID: id, EntryID: "song-a", Revision: 1, Goal: "Work on the song", Recap: recap,
					Author: "local", State: state, CreatedAt: at, UpdatedAt: at}
				if state == "accepted" {
					record.AcceptedAt = &at
				}
				return record
			}
			doc.Sessions = append(doc.Sessions,
				session("s-old", "accepted", "Sketched the verse.", day.AddDate(0, 0, -5)),
				session("s-new", "accepted", "Tracked the bass and fixed the chorus timing. Next: vocals.", day.AddDate(0, 0, -2)),
				session("s-draft", "draft", "Unreviewed note.", day.AddDate(0, 0, -1)),
				session("s-goal", "accepted", "", day.AddDate(0, 0, -1)), // a goal without a recap is not a last session
			)
			return "song-card-fixture", nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = []string{"child-a", "child-b"}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	grantSongDetails(t, file, scope)
	return s, scope, file, day
}

func TestSongCard_ProjectsFactsSaveKeeperSessionAndOpenWork(t *testing.T) {
	s, scope, _, day := songCardFixture(t)
	card, err := s.SongCard(scope, "child-a")
	if err != nil {
		t.Fatal(err)
	}
	if card.EntryID != "song-a" || card.Title != "Song A" || card.FactsState != FactsStateOn || card.Facts == nil ||
		card.Facts.TempoBPM != 92 || card.LastSavedAt == nil || !card.LastSavedAt.Equal(day.AddDate(0, 0, -3)) ||
		card.KeeperAgentName != "Studio Keeper" || card.HomeID != scope.HomeID {
		t.Fatalf("card: %+v", card)
	}
	if card.LastSession == nil || card.LastSession.FirstSentence != "Tracked the bass and fixed the chorus timing" ||
		!card.LastSession.AcceptedAt.Equal(day.AddDate(0, 0, -2)) {
		t.Fatalf("last session must be the newest accepted recap: %+v", card.LastSession)
	}
	if card.OpenWork.Review != 1 || card.OpenWork.Ready != 2 || card.OpenWork.Done != 1 {
		t.Fatalf("open work: %+v", card.OpenWork)
	}
	payload, _ := json.Marshal(card)
	if strings.Contains(string(payload), "Song.rpp") || strings.Contains(string(payload), "home-root") {
		t.Fatalf("card leaked a file name or root: %s", payload)
	}
}

func TestSongCard_FactsStateOffAndAbsent(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	absent, err := s.SongCard(scope, "child-b")
	if err != nil || absent.EntryID != "song-b" || absent.FactsState != FactsStateAbsent || absent.Facts != nil ||
		absent.Title != "Song B" || absent.LastSession != nil || absent.KeeperAgentName != "" {
		t.Fatalf("facts absent: %+v %v", absent, err)
	}
	switchSongDetailsOff(t, file, scope)
	off, err := s.SongCard(scope, "child-a")
	if err != nil || off.FactsState != FactsStateOff || off.Facts != nil || off.LastSavedAt == nil {
		t.Fatalf("facts off must hide facts but keep the save time: %+v %v", off, err)
	}
}

func TestSongCard_BrokenLinkKeepsTheCardWithoutAnEntry(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	card, err := s.SongCard(scope, "child-c")
	if err != nil || card.EntryID != "" || card.Facts != nil || card.FactsState != "" || card.LastSavedAt != nil ||
		card.Title != "Song C" {
		t.Fatalf("a link the Home does not list is not a library entry: %+v %v", card, err)
	}
	// A child whose link no longer matches the entry's revision is not connected.
	if err := file.Update("child-a", func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.StateRevision = 2
		child.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := s.SongCard(scope, "child-a")
	if err != nil || stale.EntryID != "" || stale.Facts != nil || stale.Title != "Child A folder name" {
		t.Fatalf("a stale link must not show facts: %+v %v", stale, err)
	}
}

func TestSongCard_RefusesForeignAndNonSongWorkspaces(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	foreign := scope
	foreign.OwnerUserID = "someone-else"
	if _, err := s.SongCard(foreign, "child-a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign owner: %v", err)
	}
	if _, err := s.SongCard(scope, scope.HomeID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("the Home itself is not a song: %v", err)
	}
	plain := &workspace.Workspace{ID: "plain", Name: "Plain", OwnerUserID: scope.OwnerUserID, Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := file.Save(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SongCard(scope, "plain"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a workspace without a link is not a song: %v", err)
	}
}

func TestRoster_ListsConnectedSongsBySessionThenSaveThenName(t *testing.T) {
	s, scope, _, day := songCardFixture(t)
	rows, err := s.Roster(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].WorkspaceID != "child-a" || rows[1].WorkspaceID != "child-b" {
		t.Fatalf("a song with a session sorts first; needs_review and catalog-only are left out: %+v", rows)
	}
	if rows[0].KeeperAgentName != "Studio Keeper" || rows[0].LastSession == nil || rows[0].OpenWork.Open() != 3 ||
		rows[0].Facts == nil || rows[0].Facts.TrackCount != 14 ||
		rows[1].Title != "Song B" || rows[1].LastSession != nil || rows[1].LastSavedAt == nil || rows[1].Facts != nil {
		t.Fatalf("rows: %+v", rows)
	}
	payload, _ := json.Marshal(rows)
	if strings.Contains(string(payload), "folder_slug") || strings.Contains(string(payload), "FolderSlug") {
		t.Fatalf("the slug is host-only: %s", payload)
	}
	// Without sessions, the newer save sorts first; equal saves fall back to name.
	a := RosterRow{WorkspaceID: "a", Title: "Zed", LastSavedAt: ptrTime(day)}
	b := RosterRow{WorkspaceID: "b", Title: "alpha", LastSavedAt: ptrTime(day)}
	c := RosterRow{WorkspaceID: "c", Title: "Beta", LastSavedAt: ptrTime(day.Add(time.Hour))}
	d := RosterRow{WorkspaceID: "d", Title: "Aardvark"}
	if !rosterLess(c, a) || !rosterLess(b, a) || !rosterLess(a, d) {
		t.Fatal("roster order must be session, then save, then name")
	}
}

func TestRoster_FactsFollowTheSwitch(t *testing.T) {
	s, scope, file, _ := songCardFixture(t)
	switchSongDetailsOff(t, file, scope)
	rows, err := s.Roster(scope)
	if err != nil || len(rows) != 2 || rows[0].Facts != nil {
		t.Fatalf("facts off must leave the roster without facts: %+v %v", rows, err)
	}
}

func TestRoster_AnEmptyOrUnsetHomeHasNoRows(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	rows, err := s.Roster(scope)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("a Home without a library: %+v %v", rows, err)
	}
	initializeLibrary(t, s, scope)
	rows, err = s.Roster(scope)
	if err != nil || len(rows) != 0 {
		t.Fatalf("an empty library: %+v %v", rows, err)
	}
	foreign := scope
	foreign.OwnerUserID = "someone-else"
	if _, err := s.Roster(foreign); err == nil {
		t.Fatal("a foreign owner must not read the roster")
	}
}

func ptrTime(at time.Time) *time.Time { return &at }

func TestStatusLine_OrderSkippingAndWording(t *testing.T) {
	now := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	saved := now.AddDate(0, 0, -3)
	facts := &SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14}
	session := &SongSession{AcceptedAt: now, FirstSentence: "Tracked the bass"}
	for _, tc := range []struct {
		name string
		card SongCard
		want string
	}{
		{"everything", SongCard{FactsState: FactsStateOn, Facts: facts, LastSavedAt: &saved, LastSession: session,
			OpenWork: workspace.TicketCounts{Review: 1, InProgress: 2, Ready: 3, Backlog: 4, Done: 5}},
			"14 tracks · 92 BPM · 3:41; saved 3 days ago; last session: Tracked the bass; 1 in review · 2 in progress · 3 ready"},
		{"facts off are not shown", SongCard{FactsState: FactsStateOff, Facts: facts, LastSavedAt: &saved},
			"Saved 3 days ago; no open work"},
		{"nothing known but the counts", SongCard{}, "No open work"},
		{"session without a save", SongCard{LastSession: session, OpenWork: workspace.TicketCounts{Ready: 1}},
			"Last session: Tracked the bass; 1 ready"},
	} {
		if got := StatusLine(tc.card, now); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestFactsLabel_MatchesTheShelfWording(t *testing.T) {
	for _, tc := range []struct {
		facts *SongFacts
		want  string
	}{
		{nil, ""},
		{&SongFacts{}, ""},
		{&SongFacts{TrackCount: 1}, "1 track"},
		{&SongFacts{TempoBPM: 96.25, TempoVaries: true}, "96.3 BPM, varies"},
		{&SongFacts{TempoBPM: 120, LengthSeconds: 3725.4, TrackCount: 24}, "24 tracks · 120 BPM · 1:02:05"},
		{&SongFacts{LengthSeconds: 0.4}, ""},
	} {
		if got := FactsLabel(tc.facts); got != tc.want {
			t.Errorf("FactsLabel(%+v) = %q, want %q", tc.facts, got, tc.want)
		}
	}
}

func TestSavedLabel_UsesCalendarDays(t *testing.T) {
	now := time.Date(2026, time.June, 10, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		saved time.Time
		want  string
	}{
		{time.Time{}, ""},
		{now.Add(2 * time.Hour), "Saved today"},
		{now.Add(-10 * time.Hour), "Saved yesterday"},
		{now.AddDate(0, 0, -13), "Saved 13 days ago"},
		{now.AddDate(0, 0, -20), "Saved 2 weeks ago"},
		{now.AddDate(0, 0, -60), "Saved 2 months ago"},
		{now.AddDate(0, 0, -400), "Saved May 2025"},
	} {
		if got := SavedLabel(tc.saved, now); got != tc.want {
			t.Errorf("SavedLabel(%v) = %q, want %q", tc.saved, got, tc.want)
		}
	}
}

func TestFirstSentence_CutsAndRefusesFilesPathsAndLinks(t *testing.T) {
	long := strings.Repeat("word ", 40)
	for _, tc := range []struct {
		recap, want string
	}{
		{"", ""},
		{"Tracked the bass. Then vocals.", "Tracked the bass"},
		{"Is the bridge too long? Maybe.", "Is the bridge too long?"},
		{"First line without a stop\nSecond line.", "First line without a stop"},
		{"Tempo moved to 92.5 BPM today. Fine.", "Tempo moved to 92.5 BPM today"},
		{"Bounced Song v2.wav for the band.", ""},
		{"Notes are in /Users/me/Music now.", ""},
		{"See https://example.com for refs.", ""},
		{"Moved stems to bounce/final.", ""},
	} {
		if got := firstSentence(tc.recap); got != tc.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tc.recap, got, tc.want)
		}
	}
	cut := firstSentence(long)
	if n := len([]rune(cut)); n > maxSessionSentence || !strings.HasSuffix(cut, "…") || strings.Contains(cut, "  ") {
		t.Fatalf("a long recap must be cut to %d characters on a word: %q (%d)", maxSessionSentence, cut, n)
	}
}

func TestStatusLine_MakesNoProgressClaim(t *testing.T) {
	now := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.UTC)
	saved := now.AddDate(0, 0, -1)
	line := strings.ToLower(StatusLine(SongCard{FactsState: FactsStateOn, Facts: &SongFacts{TempoBPM: 100},
		LastSavedAt: &saved}, now))
	for _, claim := range []string{"done", "finished", "complete", "final", "quality", "progress"} {
		if strings.Contains(line, claim) {
			t.Fatalf("status line made a claim %q: %s", claim, line)
		}
	}
}
