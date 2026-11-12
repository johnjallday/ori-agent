package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
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
}

func (f *fakeLearnLibrary) FolderAgentScopes(string, string, string) ([]projectlibrary.ScanScopeChoice, error) {
	return nil, nil
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

func TestFolderAgentSongs_GrantRecordsTheFolderAgentsOfferAtNumbersScope(t *testing.T) {
	f := newLearnFixture(t)
	ctx := context.Background()
	if state, names, err := f.songs.Consent(ctx, "local", "ws-1", f.homeID); err != nil || state != workspace.SongDetailsNone || names {
		t.Fatalf("before: %q names=%v err=%v", state, names, err)
	}
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatal(err)
	}
	got := f.consent(t)
	if got.Source != workspace.SongDetailsConsentLearnSongs || got.Scope != workspace.SongDetailsScopeNumbers || got.OfferID != "folder-agent:ws-1" || !got.Active() {
		t.Fatalf("consent = %+v", got)
	}
	if state, names, err := f.songs.Consent(ctx, "local", "ws-1", f.homeID); err != nil || state != workspace.SongDetailsOn || names {
		t.Fatalf("after: %q names=%v err=%v", state, names, err)
	}
	// Granting again is the same grant.
	if err := f.songs.Grant(ctx, "local", "ws-1", f.homeID); err != nil {
		t.Fatalf("second grant = %v", err)
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
