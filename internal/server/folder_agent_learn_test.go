package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeLearnLibrary records the listings a reading asks for.
type fakeLearnLibrary struct {
	mu        sync.Mutex
	rootID    string
	stateErr  error
	rootErr   error
	rootKeys  []string
	scopeKeys []string
	folders   []string // folders PortfolioLibraryState was asked about
	// scopes are what FolderAgentScopes issues, or fails with scopesErr.
	scopes    []projectlibrary.ScanScopeChoice
	scopesErr error
	// songs are what FolderAgentSongs returns; songAsks records each call as
	// "owner|home|root".
	songs    []projectlibrary.LearnedSong
	songsErr error
	songAsks []string
}

func (f *fakeLearnLibrary) FolderAgentSongs(owner, home, root string) ([]projectlibrary.LearnedSong, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.songAsks = append(f.songAsks, owner+"|"+home+"|"+root)
	return f.songs, f.songsErr
}

func (f *fakeLearnLibrary) FolderAgentScopes(string, string, string) ([]projectlibrary.ScanScopeChoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scopes, f.scopesErr
}

func (f *fakeLearnLibrary) ScanFolderAgentRoot(_ context.Context, _, _, _, key string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rootKeys = append(f.rootKeys, key)
	return 7, false, f.rootErr
}

func (f *fakeLearnLibrary) ScanFolderAgentScope(_ context.Context, _, _, _, _, key string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopeKeys = append(f.scopeKeys, key)
	return 7, false, nil
}

func (f *fakeLearnLibrary) PortfolioLibraryState(_, _, folder string) (sessionhttp.PortfolioLibraryState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders = append(f.folders, folder)
	return sessionhttp.PortfolioLibraryState{Initialized: true, RootID: f.rootID}, f.stateErr
}

type learnFixture struct {
	songs   folderAgentSongs
	store   workspace.Store
	library *fakeLearnLibrary
	homeID  string
}

// newLearnFixture is a Home with no consent and a folder workspace "ws-1"
// placed inside it, the state Set it up leaves behind.
func newLearnFixture(t *testing.T) *learnFixture {
	t.Helper()
	store := workspace.NewInMemoryStore()
	home := &workspace.Workspace{ID: "music-home", Name: "Music Home", OwnerUserID: "local", Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, PluginAvailable: true,
		Key: workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	for id, parent := range map[string]string{"ws-1": home.ID, "ws-elsewhere": ""} {
		if err := store.Save(&workspace.Workspace{ID: id, Name: id, OwnerUserID: "local", ParentID: parent, Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	library := &fakeLearnLibrary{rootID: "root-1"}
	n := 0
	return &learnFixture{store: store, library: library, homeID: home.ID, songs: folderAgentSongs{
		consents: workspace.NewSongDetailsConsents(store),
		store:    store,
		anchor:   func(string) (string, error) { return "/anchored/folder", nil },
		library:  func() folderAgentLearnLibrary { return library },
		token:    func() string { n++; return "t" + string(rune('0'+n)) },
	}}
}

func (f *learnFixture) consent(t *testing.T) *workspace.SongDetailsConsent {
	t.Helper()
	home, err := f.store.Get(f.homeID)
	if err != nil {
		t.Fatal(err)
	}
	return home.GetAssistantProgramState().GetSongDetailsConsent()
}

func (f *learnFixture) switchOff(t *testing.T) {
	t.Helper()
	if err := f.store.Update(f.homeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		off := time.Now().UTC().Add(time.Minute)
		state.SongDetailsConsent.RevokedAt = &off
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFolderAgentSongs_GrantRecordsTheFolderAgentsOfferAtNamesScope(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	if state, names, err := f.songs.Consent(ctx, "local", "ws-1", f.homeID); err != nil || state != workspace.SongDetailsNone || names {
		t.Fatalf("before: %q names=%v err=%v", state, names, err)
	}
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	got := f.consent(t)
	if got.Source != workspace.SongDetailsConsentLearnSongs || got.Scope != workspace.SongDetailsScopeNames || got.OfferID != "folder-agent:ws-1" || !got.Active() || !got.CoversNames() {
		t.Fatalf("consent = %+v", got)
	}
	if state, names, err := f.songs.Consent(ctx, "local", "ws-1", f.homeID); err != nil || state != workspace.SongDetailsOn || !names {
		t.Fatalf("after: %q names=%v err=%v", state, names, err)
	}
	// Granting again is the same grant.
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatalf("second grant = %v", err)
	}
}

// A Home that holds the older card's consent (numbers only) is widened by "Learn
// them", whose words promise names: as a new and broader grant, not by assuming
// the old one covered them.
func TestFolderAgentSongs_GrantWidensAnOlderNumbersOnlyConsent(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	if err := workspace.NewSongDetailsConsents(f.store).Grant(f.homeID, "older-card"); err != nil {
		t.Fatal(err)
	}
	if got := f.consent(t); got.Source != workspace.SongDetailsConsentFolderOffer || got.CoversNames() {
		t.Fatalf("setup: %+v", got)
	}
	if state, names, _ := f.songs.Consent(ctx, "local", "ws-1", f.homeID); state != workspace.SongDetailsOn || names {
		t.Fatalf("the older consent reads as %q names=%v", state, names)
	}
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	if got := f.consent(t); got.Source != workspace.SongDetailsConsentLearnSongs || got.OfferID != "folder-agent:ws-1" || !got.CoversNames() || got.RevokedAt != nil {
		t.Fatalf("after widening: %+v", got)
	}
}

func TestFolderAgentSongs_GrantRefusesWhatIsNotTheFoldersOwnHome(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	cases := map[string]struct{ workspaceID, homeID string }{
		"a workspace outside the Home": {"ws-elsewhere", f.homeID},
		"an unknown workspace":         {"ws-missing", f.homeID},
		"no Home named":                {"ws-1", ""},
		"a workspace that is no Home":  {"ws-1", "ws-elsewhere"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := f.songs.Grant(ctx, "local", tc.workspaceID, tc.homeID); err == nil {
				t.Fatal("granted")
			}
			if got := f.consent(t); got != nil {
				t.Fatalf("a refused grant wrote %+v", got)
			}
		})
	}
}

func TestFolderAgentSongs_GrantNeverTurnsASwitchBackOn(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	f.switchOff(t)
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); !errors.Is(err, folderagent.ErrWrongState) {
		t.Fatalf("grant on a switched-off Home = %v, want ErrWrongState", err)
	}
	if got := f.consent(t); got.State() != workspace.SongDetailsOff {
		t.Fatalf("the grant changed the switch: %+v", got)
	}
}

func TestFolderAgentSongs_LearnListsAgainUnderAFreshKeyForTheAnchoredFolder(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	rec := folderagent.Record{WorkspaceID: "ws-1", OwnerUserID: "local"}
	first := f.songs.Learn(ctx, rec, f.homeID, 7, nil)
	second := f.songs.Learn(ctx, rec, f.homeID, 7, nil)

	if first.Status != folderagent.LearningDone || first.Total != 7 || first.Done != 7 || first.Song != "" {
		t.Fatalf("first reading = %+v", first)
	}
	if second.Status != folderagent.LearningDone {
		t.Fatalf("second reading = %+v", second)
	}
	if len(f.library.rootKeys) != 2 || f.library.rootKeys[0] == f.library.rootKeys[1] {
		t.Fatalf("listing keys %v: each reading needs its own, or a replay reads nothing", f.library.rootKeys)
	}
	for _, key := range f.library.rootKeys {
		// Set it up's own key is "folder-agent-<workspace>-scan-<n>": never reused.
		if !strings.HasPrefix(key, "folder-agent-ws-1-learn-") || key == "folder-agent-ws-1-scan" {
			t.Fatalf("listing key %q", key)
		}
	}
	if len(f.library.folders) != 2 || f.library.folders[0] != "/anchored/folder" {
		t.Fatalf("the root was looked up for %v", f.library.folders)
	}
}

func TestFolderAgentSongs_LearnListsNothingWithoutAnActiveConsent(t *testing.T) {
	rec := folderagent.Record{WorkspaceID: "ws-1", OwnerUserID: "local"}
	t.Run("no consent", func(t *testing.T) {
		f := newLearnFixture(t)
		got := f.songs.Learn(context.Background(), rec, f.homeID, 7, nil)
		if got.Status != folderagent.LearningStopped || len(f.library.rootKeys) != 0 {
			t.Fatalf("reading = %+v after %d listings", got, len(f.library.rootKeys))
		}
	})
	t.Run("switched off", func(t *testing.T) {
		f := newLearnFixture(t)
		if err := f.songs.Grant(context.Background(), "local", "ws-1", f.homeID); err != nil {
			t.Fatal(err)
		}
		f.switchOff(t)
		got := f.songs.Learn(context.Background(), rec, f.homeID, 7, nil)
		if got.Status != folderagent.LearningStopped || len(f.library.rootKeys) != 0 {
			t.Fatalf("reading = %+v after %d listings", got, len(f.library.rootKeys))
		}
	})
	t.Run("a Home that does not exist", func(t *testing.T) {
		f := newLearnFixture(t)
		got := f.songs.Learn(context.Background(), rec, "no-such-home", 7, nil)
		if got.Status != folderagent.LearningStopped || len(f.library.rootKeys) != 0 {
			t.Fatalf("reading = %+v after %d listings", got, len(f.library.rootKeys))
		}
	})
}

func TestFolderAgentSongs_LearnStopsWhenItCannotList(t *testing.T) {
	rec := folderagent.Record{WorkspaceID: "ws-1", OwnerUserID: "local"}
	cases := map[string]func(*learnFixture){
		"the root listing failed": func(f *learnFixture) { f.library.rootErr = errors.New("busy") },
		"the library has no root": func(f *learnFixture) { f.library.rootID = "" },
		"the library state failed": func(f *learnFixture) {
			f.library.stateErr = errors.New("unavailable")
		},
		"the folder is gone": func(f *learnFixture) { f.songs.anchor = func(string) (string, error) { return "", errors.New("gone") } },
		"no library yet":     func(f *learnFixture) { f.songs.library = func() folderAgentLearnLibrary { return nil } },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLearnFixture(t)
			if err := f.songs.Grant(context.Background(), "local", "ws-1", f.homeID); err != nil {
				t.Fatal(err)
			}
			set(f)
			if got := f.songs.Learn(context.Background(), rec, f.homeID, 7, nil); got.Status != folderagent.LearningStopped {
				t.Fatalf("reading = %+v, want stopped", got)
			}
		})
	}
}

// songsRecord is a folder whose songs sit one folder down ("REAPER Projects/<song>"),
// so listing them takes a scoped scan after the root's.
func songsRecord() folderagent.Record {
	return folderagent.Record{WorkspaceID: "ws-1", OwnerUserID: "local", Facts: foldermap.Facts{
		Members: []foldermap.Project{
			{Path: "REAPER Projects/Night Drive", Name: "Night Drive", Depth: 2},
			{Path: "REAPER Projects/Glass Hour", Name: "Glass Hour", Depth: 2},
		},
	}}
}

// The demo's "Learn them" read 0 of 6 songs and said "No song could be read": the
// library's scopes could not be read for a moment (the Library Manager writes to
// the Home right after every scan), the listing stopped at the root without a word,
// and the reading ended "done". A folder the listing never reached is a reading
// that did not happen.
func TestFolderAgentSongs_LearnEndsStoppedWhenTheFoldersOfSongsWereNeverReached(t *testing.T) {
	rec := songsRecord()
	f := newLearnFixture(t)
	if err := f.songs.Grant(context.Background(), "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	f.library.scopesErr = errors.New("the library's revision moved")
	got := f.songs.Learn(context.Background(), rec, f.homeID, 6, nil)
	if got.Status != folderagent.LearningStopped {
		t.Fatalf("reading = %+v, want stopped: the songs' folder was never listed", got)
	}
	if len(f.library.scopeKeys) != 0 {
		t.Fatalf("scoped scans ran without scopes: %v", f.library.scopeKeys)
	}
}

func TestFolderAgentSongs_LearnListsTheFoldersOfSongsAndEndsDone(t *testing.T) {
	rec := songsRecord()
	f := newLearnFixture(t)
	if err := f.songs.Grant(context.Background(), "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	f.library.scopes = []projectlibrary.ScanScopeChoice{{ID: "scope-1", RelativeFolder: "REAPER Projects"}}
	got := f.songs.Learn(context.Background(), rec, f.homeID, 6, nil)
	if got.Status != folderagent.LearningDone {
		t.Fatalf("reading = %+v, want done", got)
	}
	if len(f.library.scopeKeys) != 1 || !strings.HasPrefix(f.library.scopeKeys[0], "folder-agent-ws-1-learn-") {
		t.Fatalf("scoped scan keys = %v, want one fresh key", f.library.scopeKeys)
	}
}

func TestFolderAgentSongs_FactsAreThisFoldersSongsForItsOwnAgent(t *testing.T) {
	rec := folderagent.Record{WorkspaceID: "ws-1", OwnerUserID: "local"}
	ctx := context.Background()
	t.Run("under a consent, for this folder's root and owner", func(t *testing.T) {
		f := newLearnFixture(t)
		f.library.songs = []projectlibrary.LearnedSong{
			{Name: "Night Drive", Facts: &projectlibrary.SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 4},
				Names: &projectlibrary.SongNames{Tracks: []string{"Kick", "Bass"}, TracksLeftOut: 2,
					Markers: []projectlibrary.SongMarker{{Name: "Chorus", At: 30, End: 45}}, MarkersLeftOut: 1}},
			{Name: "Broken Take", NotRead: projectlibrary.NotReadUnreadable},
		}
		if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
			t.Fatal(err)
		}
		songs, err := f.songs.Facts(ctx, rec, f.homeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(songs) != 2 || songs[0].Name != "Night Drive" || songs[0].TempoBPM != 92 || songs[0].TrackCount != 4 ||
			songs[0].LengthSeconds != 221 || strings.Join(songs[0].Tracks, ",") != "Kick,Bass" || songs[0].TracksLeftOut != 2 ||
			len(songs[0].Markers) != 1 || songs[0].Markers[0] != (folderlook.SongMarker{Name: "Chorus", At: 30, End: 45}) ||
			songs[0].MarkersLeftOut != 1 || songs[0].NotRead != "" {
			t.Fatalf("mapped song = %+v", songs[0])
		}
		if songs[1].Name != "Broken Take" || songs[1].NotRead != "unreadable" || songs[1].TrackCount != 0 || len(songs[1].Tracks) != 0 {
			t.Fatalf("a not-read song = %+v", songs[1])
		}
		// Asked of this owner's Home and this folder's root, nothing else.
		if len(f.library.songAsks) != 1 || f.library.songAsks[0] != "local|"+f.homeID+"|root-1" {
			t.Fatalf("the library was asked %v", f.library.songAsks)
		}
		// The agent's slice is its own: changing it leaves the library's songs alone.
		songs[0].Tracks[0] = "Changed"
		if f.library.songs[0].Names.Tracks[0] != "Kick" {
			t.Fatal("the mapped names share storage with the library's")
		}
	})
	t.Run("nothing without a consent", func(t *testing.T) {
		f := newLearnFixture(t)
		f.library.songs = []projectlibrary.LearnedSong{{Name: "X"}}
		songs, err := f.songs.Facts(ctx, rec, f.homeID)
		if err != nil || len(songs) != 0 || len(f.library.songAsks) != 0 {
			t.Fatalf("with no consent: %v, %v, asks %v", songs, err, f.library.songAsks)
		}
	})
	t.Run("nothing once the Home is switched off", func(t *testing.T) {
		f := newLearnFixture(t)
		f.library.songs = []projectlibrary.LearnedSong{{Name: "X"}}
		if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
			t.Fatal(err)
		}
		f.switchOff(t)
		songs, err := f.songs.Facts(ctx, rec, f.homeID)
		if err != nil || len(songs) != 0 || len(f.library.songAsks) != 0 {
			t.Fatalf("switched off: %v, %v, asks %v", songs, err, f.library.songAsks)
		}
	})
	t.Run("errors are reported, not swallowed", func(t *testing.T) {
		for name, set := range map[string]func(*learnFixture){
			"the library has no root":  func(f *learnFixture) { f.library.rootID = "" },
			"the library state fails":  func(f *learnFixture) { f.library.stateErr = errors.New("unavailable") },
			"the songs cannot be read": func(f *learnFixture) { f.library.songsErr = errors.New("busy") },
			"the folder is gone":       func(f *learnFixture) { f.songs.anchor = func(string) (string, error) { return "", errors.New("gone") } },
			"no library yet":           func(f *learnFixture) { f.songs.library = func() folderAgentLearnLibrary { return nil } },
		} {
			f := newLearnFixture(t)
			if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
				t.Fatal(err)
			}
			set(f)
			if songs, err := f.songs.Facts(ctx, rec, f.homeID); err == nil || len(songs) != 0 {
				t.Fatalf("%s: songs %v err %v", name, songs, err)
			}
		}
	})
}

func TestLearnedSongs_MapsEverythingAndNothingElse(t *testing.T) {
	if got := learnedSongs(nil); len(got) != 0 {
		t.Fatalf("no songs = %+v", got)
	}
	got := learnedSongs([]projectlibrary.LearnedSong{{Name: "Bare"}})
	if len(got) != 1 || got[0].Name != "Bare" || got[0].TempoBPM != 0 || got[0].Tracks != nil || got[0].Markers != nil {
		t.Fatalf("a song with nothing learned = %+v", got)
	}
	got = learnedSongs([]projectlibrary.LearnedSong{{Name: "Varies", Facts: &projectlibrary.SongFacts{TempoBPM: 96, TempoVaries: true}}})
	if !got[0].TempoVaries || got[0].TempoBPM != 96 {
		t.Fatalf("tempo that varies = %+v", got[0])
	}
}

func TestLearnProgress_AddsUpPassesAndNeverPassesTheTotal(t *testing.T) {
	var snapshots []folderagent.Learning
	p := &learnProgress{total: 7, report: func(l folderagent.Learning) { snapshots = append(snapshots, l) }}

	// Pass one: the root listing reaches three songs.
	p.note(projectlibrary.SongFactsProgress{Song: "Night Drive", Done: 0, Targets: 3})
	p.note(projectlibrary.SongFactsProgress{Song: "Closer", Done: 1, Targets: 3, Read: 1})
	p.note(projectlibrary.SongFactsProgress{Done: 3, Targets: 3, Read: 3, Finished: true})
	// Pass two: a scoped listing reaches the other four.
	p.note(projectlibrary.SongFactsProgress{Song: "Bridge", Done: 0, Targets: 4})
	p.note(projectlibrary.SongFactsProgress{Done: 4, Targets: 4, Read: 4, Finished: true})
	// A third listing that meets songs again cannot count past the total.
	p.note(projectlibrary.SongFactsProgress{Song: "Night Drive", Done: 2, Targets: 5, Read: 2})

	want := []struct {
		song       string
		done, read int
	}{
		{"Night Drive", 0, 0},
		{"Closer", 1, 1},
		{"Closer", 3, 3}, // the finished pass keeps the last song's name
		{"Bridge", 3, 3},
		{"Bridge", 7, 7},
		{"Night Drive", 7, 7}, // 7 + 2 clamps to 7
	}
	if len(snapshots) != len(want) {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	for i, w := range want {
		s := snapshots[i]
		if s.Status != folderagent.LearningRunning || s.Total != 7 || s.Song != w.song || s.Done != w.done || s.Read != w.read {
			t.Errorf("snapshot %d = %+v, want song %q done %d read %d", i, s, w.song, w.done, w.read)
		}
	}
	final := p.final(true)
	if final.Status != folderagent.LearningDone || final.Done != 7 || final.Read != 7 || final.Song != "" {
		t.Fatalf("final = %+v", final)
	}
}

func TestLearnProgress_AStoppedPassOrAMissedListingEndsStopped(t *testing.T) {
	for name, stop := range map[string]func(*learnProgress) folderagent.Learning{
		"a pass stopped early": func(p *learnProgress) folderagent.Learning {
			p.note(projectlibrary.SongFactsProgress{Song: "A", Targets: 5})
			p.note(projectlibrary.SongFactsProgress{Done: 5, Targets: 5, Read: 2, Finished: true, Stopped: "time"})
			return p.final(true)
		},
		"the consent was switched off mid-pass": func(p *learnProgress) folderagent.Learning {
			p.note(projectlibrary.SongFactsProgress{Done: 5, Targets: 5, Finished: true, Stopped: "authority"})
			return p.final(true)
		},
		"the listing failed": func(p *learnProgress) folderagent.Learning { return p.final(false) },
	} {
		t.Run(name, func(t *testing.T) {
			final := stop(&learnProgress{total: 7})
			if final.Status != folderagent.LearningStopped {
				t.Fatalf("final = %+v, want stopped", final)
			}
			if final.Done == 7 {
				t.Fatalf("a stopped reading claims it went through every song: %+v", final)
			}
		})
	}
	// Read keeps what was read.
	p := &learnProgress{total: 7}
	p.note(projectlibrary.SongFactsProgress{Done: 5, Targets: 5, Read: 2, Finished: true, Stopped: "time"})
	if final := p.final(true); final.Read != 2 {
		t.Fatalf("a stopped reading dropped what it read: %+v", final)
	}
}
