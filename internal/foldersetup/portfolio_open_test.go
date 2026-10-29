package foldersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// worldOpener is the library's reviewed open for the song: one listed entry,
// its project files, and whether an earlier pass already made its workspace.
type worldOpener struct {
	*portfolioWorld
	entries    map[string]string // folder name -> entry ID
	files      []string          // the song folder's project files
	staleTeam  bool
	noModel    bool
	connected  bool
	staffing   string
	firstTask  bool
	opens      []OpenInput
	workspaces int
}

func newWorldOpener(w *portfolioWorld) *worldOpener {
	return &worldOpener{portfolioWorld: w, entries: map[string]string{"Album-5": "entry-5"}, files: []string{"Album-5.rpp"},
		staffing: "added", firstTask: true}
}

func (o *worldOpener) Find(_ context.Context, homeID, folder string) (string, error) {
	o.log = append(o.log, "open:find:"+folder)
	if id, ok := o.entries[folder]; ok && homeID != "" {
		return id, nil
	}
	return "", ErrOpenNotListed
}

func (o *worldOpener) Open(_ context.Context, in OpenInput) (OpenOutcome, error) {
	o.log = append(o.log, "open:"+in.EntryID+":"+in.SelectedFile)
	o.opens = append(o.opens, in)
	if !o.connected {
		switch {
		case o.staleTeam:
			return OpenOutcome{}, &OpenStop{Reason: personalassistant.FolderStopConsentStale}
		case len(o.files) > 1 && in.SelectedFile == "":
			return OpenOutcome{}, &OpenStop{Reason: personalassistant.FolderStopNeedsChoice, Candidates: append([]string(nil), o.files...)}
		case o.noModel:
			return OpenOutcome{}, &OpenStop{Reason: personalassistant.FolderStopNeedsModel}
		}
		o.connected, o.workspaces = true, o.workspaces+1
		return OpenOutcome{EntryID: in.EntryID, WorkspaceID: "song-5", LinkID: "link-5", Route: "/workspaces/album-5", Created: true,
			Staffing: o.staffing, AgentName: "REAPER Assistant", FirstTask: o.firstTask}, nil
	}
	return OpenOutcome{EntryID: in.EntryID, WorkspaceID: "song-5", LinkID: "link-5", Route: "/workspaces/album-5",
		Staffing: "already", AgentName: "REAPER Assistant", FirstTask: true}, nil
}

func songPlan(existing bool) personalassistant.FolderSetupPlan {
	home := freshFacts()
	home.FolderName, home.Projects, home.IntegrationProjects = "Album-5", 1, 1
	home.HomeExists, home.HomeStaffed = existing, existing
	if existing {
		home.Sharing = SharingOn
	}
	song := PlanFacts{AppName: "REAPER", AppInstalled: true, BlueprintLabel: "Reaper Song", WorkspaceName: "Album-5", FolderName: "Album-5",
		Integration: *home.Integration}
	return BuildHandoffPlan(HandoffFacts{Home: home, Song: song, SongName: "Album-5", ManagerName: "Portfolio Manager"})
}

func runSong(t *testing.T, w *portfolioWorld, o *worldOpener, plan personalassistant.FolderSetupPlan, entry string) (Result, *recorder) {
	t.Helper()
	return runPortfolio(t, w, plan, func(r *PortfolioRunner, cfg *PortfolioConfig) {
		r.Opener, cfg.EntryName, cfg.Open = o, entry, true
	})
}

// Two presses, one workspace each: Set up builds the Home and lists the song,
// then stops at handoff_ready with the song's half waiting; Open has the
// Portfolio Manager open it, repeating none of the Home's work.
func TestPortfolioRunStopsAtTheHandoffUntilOpenIsPressed(t *testing.T) {
	w := newPortfolioWorld()
	o := newWorldOpener(w)
	result, progress := runPortfolio(t, w, songPlan(false), func(r *PortfolioRunner, _ *PortfolioConfig) { r.Opener = o })
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopHandoffReady || result.Cause != nil {
		t.Fatalf("set up = %+v", result)
	}
	if len(o.opens) != 0 || o.workspaces != 0 || o.called("open:find:Album-5") {
		t.Fatalf("the song was opened before Open was pressed: %v", w.log)
	}
	if progress.last().HomeID != w.homeID || w.homeID == "" || !w.scanned || len(progress.last().Receipt) != 0 {
		t.Fatalf("Home-ready must identify the listed Home without a final project receipt: %+v", progress.last())
	}
	// The shared assistant's standing consent comes with the Open, where it is
	// disclosed, never with the Home's Set up.
	if w.called("consent:grant") {
		t.Fatalf("Set up granted the song's shared assistant: %v", w.log)
	}
	if got := lineOrder(progress.last()); got != "integration=done,provider=done,home=done,agents=done,library=done,handoff=waiting,"+
		"workspace=waiting,folder=waiting,mode=waiting,agents=waiting,task=waiting" {
		t.Fatalf("lines after Set up = %s", got)
	}
	homeWork := len(w.log)
	result, _ = runSong(t, w, o, songPlan(false), "")
	if result.Status != personalassistant.FolderSetupDone || o.workspaces != 1 || !w.called("consent:grant") {
		t.Fatalf("open = %+v calls = %v", result, w.log)
	}
	for _, call := range w.log[homeWork:] {
		if call == "home:commit" || call == "root:connect" || call == "library:on" || call == "staff:Portfolio Manager" {
			t.Fatalf("Open repeated the Home's %s: %v", call, w.log[homeWork:])
		}
	}
}

// lineOrder is each line's kind and state in order: a song's plan has two agents lines.
func lineOrder(update Update) string {
	parts := make([]string, 0, len(update.Lines))
	for _, line := range update.Lines {
		parts = append(parts, line.Kind+"="+line.State)
	}
	return strings.Join(parts, ",")
}

// AC2's run: the Home is set up and lists the song, then the Home's manager
// opens it, last, under the same Set up; every line ends done.
func TestPortfolioRunOpensTheSongItListed(t *testing.T) {
	w := newPortfolioWorld()
	o := newWorldOpener(w)
	result, progress := runSong(t, w, o, songPlan(false), "")
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	want := "home:find,home:review,home:commit,staff:Portfolio Manager,details:grant,library:review,library:on,root:review,root:connect,scan:review,scan:commit,consent:grant,open:find:Album-5,open:entry-5:"
	if got := strings.Join(w.log, ","); got != want {
		t.Fatalf("calls =\n %s\nwant\n %s", got, want)
	}
	if got := lineOrder(progress.last()); strings.Contains(got, "=waiting") || strings.Contains(got, "=working") || strings.Contains(got, "=failed") {
		t.Fatalf("lines = %s", got)
	}
	opened := w.receiptFacts.Opened
	if opened == nil || opened.WorkspaceID != "song-5" || opened.LinkID != "link-5" || !opened.Created || opened.Staffing != "added" {
		t.Fatalf("receipt facts = %+v", w.receiptFacts)
	}
}

// The Home's staffing moves only the Home's agents line; the song's agents
// line waits for the open.
func TestPortfolioRunStaffingTheHomeLeavesTheSongsAgentsWaiting(t *testing.T) {
	w := newPortfolioWorld()
	w.modelReady = false
	o := newWorldOpener(w)
	result, progress := runSong(t, w, o, songPlan(false), "")
	if result.StopReason != personalassistant.FolderStopNeedsModel {
		t.Fatalf("result = %+v", result)
	}
	got := lineOrder(progress.last())
	if !strings.Contains(got, "agents=waiting,library=waiting,handoff=waiting") || !strings.HasSuffix(got, "agents=waiting,task=waiting") {
		t.Fatalf("lines = %s", got)
	}
	if len(o.opens) != 0 {
		t.Fatal("the song was opened before the Home was staffed")
	}
}

// FR15/AC6: several project files stop once at needs_choice with the
// candidates; the chosen file arrives on the next pass and the run finishes
// with one workspace.
func TestPortfolioRunAsksWhichProjectFileOnce(t *testing.T) {
	w := newPortfolioWorld()
	o := newWorldOpener(w)
	o.files = []string{"Album-5.rpp", "Album-5 alt.rpp"}
	result, progress := runSong(t, w, o, songPlan(false), "")
	last := progress.last()
	if result.StopReason != personalassistant.FolderStopNeedsChoice || strings.Join(last.EntryCandidates, ",") != "Album-5.rpp,Album-5 alt.rpp" {
		t.Fatalf("result = %+v update = %+v", result, last)
	}
	if got := lineOrder(last); !strings.Contains(got, "handoff=waiting,workspace=waiting") {
		t.Fatalf("a question failed a line: %s", got)
	}
	result, progress = runSong(t, w, o, songPlan(false), "Album-5 alt.rpp")
	if result.Status != personalassistant.FolderSetupDone || o.workspaces != 1 || progress.last().EntryName != "Album-5 alt.rpp" ||
		o.opens[len(o.opens)-1].SelectedFile != "Album-5 alt.rpp" {
		t.Fatalf("after the answer = %+v opens=%+v", result, o.opens)
	}
}

// FR12.3, FR12.4: a stale consent and a missing model stop before any
// workspace is made; once cleared, the same run finishes with one workspace.
func TestPortfolioRunStopsBeforeOpeningAndResumes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		block  func(*worldOpener)
		clear  func(*worldOpener)
		reason string
	}{
		{"consent stale", func(o *worldOpener) { o.staleTeam = true }, func(o *worldOpener) { o.staleTeam = false }, personalassistant.FolderStopConsentStale},
		{"needs a model", func(o *worldOpener) { o.noModel = true }, func(o *worldOpener) { o.noModel = false }, personalassistant.FolderStopNeedsModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newPortfolioWorld()
			o := newWorldOpener(w)
			tc.block(o)
			result, progress := runSong(t, w, o, songPlan(false), "")
			if result.StopReason != tc.reason || o.workspaces != 0 {
				t.Fatalf("result = %+v workspaces = %d", result, o.workspaces)
			}
			if got := lineOrder(progress.last()); strings.Contains(got, "=failed") {
				t.Fatalf("a stop that asks failed a line: %s", got)
			}
			tc.clear(o)
			homeLog := len(w.log)
			result, _ = runSong(t, w, o, songPlan(false), "")
			if result.Status != personalassistant.FolderSetupDone || o.workspaces != 1 {
				t.Fatalf("resume = %+v workspaces = %d", result, o.workspaces)
			}
			for _, call := range w.log[homeLog:] {
				if call == "home:commit" || call == "root:connect" || call == "library:on" {
					t.Fatalf("the resume repeated %s: %v", call, w.log[homeLog:])
				}
			}
		})
	}
}

// FR14: a pass after the workspace was made finishes what is missing and
// never makes a second one.
func TestPortfolioRunFinishesAConnectedSong(t *testing.T) {
	w := newPortfolioWorld()
	o := newWorldOpener(w)
	o.staffing = "none"
	result, _ := runSong(t, w, o, songPlan(false), "")
	if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopFailed || o.workspaces != 1 {
		t.Fatalf("half-staffed = %+v", result)
	}
	result, progress := runSong(t, w, o, songPlan(false), "")
	if result.Status != personalassistant.FolderSetupDone || o.workspaces != 1 || w.receiptFacts.Opened.Created {
		t.Fatalf("finished = %+v workspaces=%d opened=%+v", result, o.workspaces, w.receiptFacts.Opened)
	}
	if got := lineOrder(progress.last()); strings.Contains(got, "=failed") || strings.Contains(got, "=waiting") {
		t.Fatalf("lines = %s", got)
	}
}

// FR11: a song the library does not list exactly once is a changed plan, and
// nothing is opened.
func TestPortfolioRunRefusesASongNotListedOnce(t *testing.T) {
	w := newPortfolioWorld()
	o := newWorldOpener(w)
	o.entries = map[string]string{}
	result, _ := runSong(t, w, o, songPlan(false), "")
	if result.StopReason != personalassistant.FolderStopPlanChanged || len(o.opens) != 0 {
		t.Fatalf("result = %+v opens = %v", result, o.opens)
	}
}

// OQ4: a root of the existing Home already covers the song: no second root,
// the library line says so, and that entry is opened.
func TestPortfolioRunOpensASongAnExistingRootAlreadyLists(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing, w.initialized, w.rootID, w.scanned, w.sharing = "home-1", nil, true, "root-albums", true, SharingOn
	o := newWorldOpener(w)
	plan := songPlan(true)
	result, progress := runPortfolio(t, w, plan, func(r *PortfolioRunner, cfg *PortfolioConfig) {
		r.Opener = o
		cfg.HomeID, cfg.Open = "home-1", true
	})
	if result.Status != personalassistant.FolderSetupDone || w.called("root:connect") || w.called("root:review") {
		t.Fatalf("result = %+v calls = %v", result, w.log)
	}
	for _, line := range progress.last().Lines {
		if line.Kind == personalassistant.FolderPlanLibrary && (line.Detail != AlreadyListedDetail || line.State != personalassistant.FolderLineDone) {
			t.Fatalf("library line = %+v", line)
		}
	}
}

// AC3: a second song fed into the staffed Home touches neither the Home nor
// its manager; it connects its own root, lists it, and the same shared
// assistant joins it.
func TestPortfolioRunAddsASecondSongToTheHome(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing, w.initialized, w.sharing = "home-1", nil, true, SharingOn
	o := newWorldOpener(w)
	o.entries = map[string]string{"Album-5": "entry-6"}
	o.staffing = "joined"
	result, progress := runPortfolio(t, w, songPlan(true), func(r *PortfolioRunner, cfg *PortfolioConfig) {
		r.Opener = o
		cfg.HomeID, cfg.Open = "home-1", true
	})
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	for _, call := range []string{"home:commit", "home:review", "library:on", "consent:grant"} {
		if w.called(call) {
			t.Fatalf("the second song repeated %s: %v", call, w.log)
		}
	}
	if !w.called("root:connect") || !w.called("scan:commit") || o.workspaces != 1 {
		t.Fatalf("calls = %v workspaces = %d", w.log, o.workspaces)
	}
	if w.receiptFacts == nil || w.receiptFacts.HomeCreated || len(w.receiptFacts.AddedAgents) != 0 ||
		w.receiptFacts.Opened == nil || w.receiptFacts.Opened.Staffing != "joined" {
		t.Fatalf("receipt facts = %+v", w.receiptFacts)
	}
	if got := lineOrder(progress.last()); strings.Contains(got, "=waiting") || strings.Contains(got, "=failed") {
		t.Fatalf("lines = %s", got)
	}
}

// A plan that opens a song cannot run without the opener.
func TestPortfolioRunRefusesToOpenWithoutAnOpener(t *testing.T) {
	w := newPortfolioWorld()
	runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Sharing: worldSharing{w},
		Receipts: worldReceipts{w}, SongDetails: worldSongDetails{w}, Folder: portfolioFolder, Progress: &recorder{}}
	if _, err := runner.Run(context.Background(), PortfolioConfig{Plan: songPlan(false)}); !errors.Is(err, ErrPortfolioNotWired) {
		t.Fatalf("err = %v", err)
	}
	if len(w.log) != 0 {
		t.Fatalf("an unwired run did something: %v", w.log)
	}
}
