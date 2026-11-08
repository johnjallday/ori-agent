package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type songCardBody struct {
	Linked           bool                        `json:"linked"`
	HomeID           string                      `json:"home_id"`
	HomeName         string                      `json:"home_name"`
	EntryID          string                      `json:"entry_id"`
	Title            string                      `json:"title"`
	Facts            *projectlibrary.SongFacts   `json:"facts"`
	FactsState       string                      `json:"facts_state"`
	LastSavedAt      *time.Time                  `json:"last_saved_at"`
	KeeperAgentName  string                      `json:"keeper_agent_name"`
	StatusLine       string                      `json:"status_line"`
	OpenWork         *workspace.TicketCounts     `json:"open_work_counts"`
	LastSession      *projectlibrary.SongSession `json:"last_session"`
	HomeRoute        string                      `json:"home_route"`
	HomeEntryRoute   string                      `json:"home_entry_route"`
	FactsSwitchRoute string                      `json:"facts_switch_route"`
	Words            *projectlibrary.Words       `json:"words"`
}

// countingPluginLister fails the test's "no provider check" assertion when
// anything asks for the installed plugins.
type countingPluginLister struct{ calls int }

func (c *countingPluginLister) List() ([]plugin.InstalledPlugin, error) {
	c.calls++
	return nil, nil
}

func songCardRequest(t *testing.T, handler *Handler, workspaceID, query string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.GetSongCard(response, assistantProgramRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/song-card"+query, workspaceID, ""))
	return response
}

func decodeSongCard(t *testing.T, response *httptest.ResponseRecorder) songCardBody {
	t.Helper()
	var body songCardBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("song card: %d %s", response.Code, response.Body.String())
	}
	return body
}

// songCardHTTPFixture is a Home with one linked song whose library entry is
// set up; the Home has a canonical slug so routes are built.
func songCardHTTPFixture(t *testing.T) (*Handler, *workspace.InMemoryStore, *workspace.Workspace, *workspace.Workspace, projectlibrary.Scope, *countingPluginLister) {
	t.Helper()
	handler, store, station, project := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	lister := &countingPluginLister{}
	handler.installedPluginLister = lister
	if err := store.Update(station.ID, func(home *workspace.Workspace) error {
		home.FolderSlug = "music-home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(project.ID, func(song *workspace.Workspace) error {
		song.AgentInstances = append(song.AgentInstances, workspace.AgentInstance{Name: "Studio Keeper"})
		song.Tasks = append(song.Tasks, workspace.Task{ID: "open-1", TicketState: workspace.TicketStateInProgress})
		return song.SetEntryAgentName("Studio Keeper")
	}); err != nil {
		t.Fatal(err)
	}
	state := station.GetAssistantProgramState()
	scope := projectlibrary.Scope{OwnerUserID: station.OwnerUserID, HomeID: station.ID,
		ProviderID: state.Key.PluginID, ProgramID: state.Key.ProgramID}
	station, _ = store.Get(station.ID)
	project, _ = store.Get(project.ID)
	return handler, store, station, project, scope, lister
}

// seedSongFacts gives the linked entry an available source with facts and an
// accepted recap, written straight into the Home's stored document. The root
// path does not exist: the card must not need a file to show the facts.
func seedSongFacts(t *testing.T, store *workspace.InMemoryStore, scope projectlibrary.Scope, projectID string) time.Time {
	t.Helper()
	doc, err := projectlibrary.NewStore(store).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	saved := time.Now().UTC().AddDate(0, 0, -2).Truncate(time.Second)
	finished := saved.AddDate(0, 0, -1)
	doc.Roots = append(doc.Roots, projectlibrary.Root{ID: "card-root", Path: filepath.Join(t.TempDir(), "missing"),
		FileIdentity: "card-root", Revision: 1, ApprovedAt: finished})
	doc.Scans = append(doc.Scans, projectlibrary.Scan{ID: "card-scan", RootID: "card-root", RootRevision: 1,
		RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64), Status: "complete",
		StartedAt: finished, FinishedAt: &finished})
	found := false
	for i := range doc.Entries {
		if doc.Entries[i].Link == nil || doc.Entries[i].Link.WorkspaceID != projectID {
			continue
		}
		found = true
		doc.Entries[i].Observations = append(doc.Entries[i].Observations, projectlibrary.Observation{RootID: "card-root",
			RelativeFolder: "Card Song", FileIdentity: "fixture:card", Format: "reaper", Alternates: []string{"Card Song.rpp"},
			ScanID: "card-scan", ScannedAt: finished, FileModifiedAt: saved, Availability: "available",
			Facts: &projectlibrary.ObservedFacts{SongFacts: projectlibrary.SongFacts{TempoBPM: 92, LengthSeconds: 221, TrackCount: 14},
				ReadFrom: projectlibrary.FactsSource{File: "Card Song.rpp", Identity: "fixture:card", Size: 10, ModifiedAt: saved}}})
		accepted := saved.Add(time.Hour)
		doc.Sessions = append(doc.Sessions, projectlibrary.StudioSession{ID: "card-session", EntryID: doc.Entries[i].ID,
			Revision: 1, Goal: "Bass", Recap: "Tracked the bass. Vocals next.", Author: "local", State: "accepted",
			CreatedAt: accepted, AcceptedAt: &accepted, UpdatedAt: accepted})
	}
	if !found {
		t.Fatal("fixture has no linked entry")
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = encoded
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := projectlibrary.NewStore(store).Read(scope); err != nil {
		t.Fatalf("seeded document must stay valid: %v", err)
	}
	return saved
}

func initializeSongCardLibrary(t *testing.T, store *workspace.InMemoryStore, scope projectlibrary.Scope) {
	t.Helper()
	library := projectlibrary.NewStore(store)
	review, err := library.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitInitialize(scope, review.Token, "song-card-init"); err != nil {
		t.Fatal(err)
	}
}

func TestSongCardHTTP_LinkedSongWithFacts(t *testing.T) {
	handler, store, station, project, scope, lister := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	saved := seedSongFacts(t, store, scope, project.ID)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Get(station.ID)
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if !body.Linked || body.HomeID != station.ID || body.HomeName != station.Name || body.EntryID == "" ||
		body.Title != project.Name || body.FactsState != "on" || body.Facts == nil || body.Facts.TrackCount != 14 ||
		body.LastSavedAt == nil || !body.LastSavedAt.Equal(saved) || body.KeeperAgentName != "Studio Keeper" ||
		body.OpenWork == nil || body.OpenWork.InProgress != 1 {
		t.Fatalf("linked card: %+v", body)
	}
	// No package is installed, so the card speaks the neutral words.
	if body.StatusLine != "Tracks 14 · Tempo 92 · 3:41; saved 2 days ago; last session: Tracked the bass; 1 in progress" {
		t.Fatalf("status line: %q", body.StatusLine)
	}
	if body.Words == nil || body.Words.Source != projectlibrary.WordsSourceNeutral || body.Words.Noun != "Project" {
		t.Fatalf("words: %+v", body.Words)
	}
	if body.HomeRoute != "/workspaces/music-home" || body.HomeEntryRoute != "/workspaces/music-home/assistant#projectLibraryPanel" ||
		body.FactsSwitchRoute != "" {
		t.Fatalf("routes: %+v", body)
	}
	// The card reads the installed package's words once and nothing else of
	// the provider (PRD #792 FR5).
	if lister.calls > 1 {
		t.Fatalf("the card asked for the installed plugins %d times", lister.calls)
	}
	after, _ := store.Get(station.ID)
	if after.Version != before.Version {
		t.Fatal("reading the card changed the Home")
	}
}

func TestSongCardHTTP_FactsOffLinksToTheSwitch(t *testing.T) {
	handler, store, _, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	seedSongFacts(t, store, scope, project.ID)
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if body.FactsState != "off" || body.Facts != nil || body.LastSavedAt == nil ||
		body.FactsSwitchRoute != "/workspaces/music-home/assistant#projectLibrarySongDetails" ||
		strings.Contains(body.StatusLine, "Tempo") {
		t.Fatalf("facts off: %+v", body)
	}
}

func TestSongCardHTTP_LinkedBeforeTheLibraryIsSetUp(t *testing.T) {
	handler, _, station, project, _, _ := songCardHTTPFixture(t)
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if !body.Linked || body.EntryID != "" || body.HomeEntryRoute != "" || body.Facts != nil ||
		body.HomeName != station.Name || body.HomeRoute != "/workspaces/music-home" || body.StatusLine != "1 in progress" {
		t.Fatalf("a song without a library entry: %+v", body)
	}
}

func TestSongCardHTTP_UnlinkedForeignAndQuery(t *testing.T) {
	handler, store, station, project, _, _ := songCardHTTPFixture(t)
	plain := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Plain"})
	plain.OwnerUserID = station.OwnerUserID
	if err := store.Save(plain); err != nil {
		t.Fatal(err)
	}
	response := songCardRequest(t, handler, plain.ID, "")
	if strings.TrimSpace(response.Body.String()) != `{"linked":false}` {
		t.Fatalf("a non-song workspace must return only linked:false: %s", response.Body.String())
	}
	// The Home itself is not a song.
	if body := decodeSongCard(t, songCardRequest(t, handler, station.ID, "")); body.Linked {
		t.Fatalf("Home read as a song: %+v", body)
	}
	if response := songCardRequest(t, handler, project.ID, "?refresh=1"); response.Code != http.StatusBadRequest {
		t.Fatalf("query accepted: %d", response.Code)
	}
	handler.currentUserID = func(context.Context) (string, error) { return "other-owner", nil }
	if response := songCardRequest(t, handler, project.ID, ""); response.Code != http.StatusNotFound {
		t.Fatalf("foreign owner: %d %s", response.Code, response.Body.String())
	}
	if response := songCardRequest(t, handler, "missing", ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing workspace: %d", response.Code)
	}
}

// divergedChildMirror is a primary store whose folder mirror holds the song
// with its link one revision ahead, as a half-finished write leaves it.
type divergedChildMirror struct {
	workspace.Store
	childID string
}

func (m divergedChildMirror) GetMirrorWorkspace(id string) (*workspace.Workspace, bool, error) {
	found, err := m.Get(id)
	if err != nil || found == nil || id != m.childID {
		return found, true, err
	}
	encoded, err := json.Marshal(found)
	if err != nil {
		return nil, true, err
	}
	var clone workspace.Workspace
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, true, err
	}
	link := clone.GetAssistantProjectLink()
	link.StateRevision++
	clone.SetAssistantProjectLink(link)
	return &clone, true, nil
}

func TestSongCardHTTP_DivergedMirrorShowsNoFactsAndLeavesTheRoster(t *testing.T) {
	handler, store, station, project, scope, _ := songCardHTTPFixture(t)
	initializeSongCardLibrary(t, store, scope)
	seedSongFacts(t, store, scope, project.ID)
	if err := workspace.NewSongDetailsConsents(store).Grant(station.ID, "offer-1"); err != nil {
		t.Fatal(err)
	}
	if body := decodeSongCard(t, songCardRequest(t, handler, project.ID, "")); body.EntryID == "" || body.Facts == nil {
		t.Fatalf("agreeing mirrors must show the entry: %+v", body)
	}
	handler.SetWorkspaceTaskStore(divergedChildMirror{Store: store, childID: project.ID})
	body := decodeSongCard(t, songCardRequest(t, handler, project.ID, ""))
	if !body.Linked || body.EntryID != "" || body.Facts != nil || body.FactsState != "" || body.LastSavedAt != nil ||
		body.HomeEntryRoute != "" || body.FactsSwitchRoute != "" || body.HomeRoute != "/workspaces/music-home" ||
		strings.Contains(body.StatusLine, "Tempo") || body.KeeperAgentName != "Studio Keeper" {
		t.Fatalf("a diverged link must keep the card without facts or Open in Home: %+v", body)
	}
	roster := decodeRoster(t, rosterRequest(t, handler, station.ID, ""))
	if roster.Total != 0 || len(roster.Rows) != 0 {
		t.Fatalf("a diverged song must leave the roster: %+v", roster)
	}
}
