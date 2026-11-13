package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// assistant-reads-projects group 5: the Home's app assistant reads the listed
// songs through its plugin, in one bounded turn with one tool.

type installedList []plugin.InstalledPlugin

func (l installedList) List() ([]plugin.InstalledPlugin, error) { return l, nil }

// fakeAssistantChat answers each call with the next scripted response and
// records every request.
type fakeAssistantChat struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
	requests  []llm.ChatRequest
}

func (f *fakeAssistantChat) chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if len(f.responses) == 0 {
		return &llm.ChatResponse{Content: "done", Usage: llm.Usage{TotalTokens: 100}}, nil
	}
	next := f.responses[0]
	f.responses = f.responses[1:]
	return next, nil
}

func callsTheTool() *llm.ChatResponse {
	return &llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "c1", Name: FactsReadToolName, Arguments: "{}"}},
		Usage: llm.Usage{TotalTokens: 600}}
}

func saysNothing() *llm.ChatResponse {
	return &llm.ChatResponse{Content: "I will look.", Usage: llm.Usage{TotalTokens: 500}}
}

// pluginReader answers every file with fixed facts and counts its files.
type pluginReader struct {
	mu     sync.Mutex
	files  []string
	during func()
	err    error
}

func (p *pluginReader) call(_ context.Context, _ plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
	var in projectFactsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	p.mu.Lock()
	for _, file := range in.Files {
		p.files = append(p.files, filepath.Base(file.Path))
	}
	during, err := p.during, p.err
	p.mu.Unlock()
	if during != nil {
		during()
	}
	if err != nil {
		return nil, err
	}
	var parts []string
	for _, file := range in.Files {
		parts = append(parts, `{"id":`+itoa(file.ID)+`,"result":"read","tempo_bpm":120,"length_seconds":60,"track_count":4}`)
	}
	return json.RawMessage(`{"schema_version":1,"files":[` + strings.Join(parts, ",") + `]}`), nil
}

func (p *pluginReader) read() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.files...)
}

type readFixture struct {
	r         *Roots
	scope     Scope
	root      Root
	chat      *fakeAssistantChat
	reader    *pluginReader
	runner    *FactsReadRunner
	published *factsAtPublish
	opens     *openCounter
	assistant string // reason the host gives; "" means hired
	model     bool
}

func newReadFixture(t *testing.T) *readFixture {
	t.Helper()
	r, scope, file, tree, root := connectedMusicRoot(t)
	factsMusicTree(t, tree)
	grantSongDetails(t, file, scope)
	f := &readFixture{r: r, scope: scope, root: root, chat: &fakeAssistantChat{}, reader: &pluginReader{}, model: true}
	f.published = &factsAtPublish{store: r.library, scope: scope}
	r.library.WithEventBus(f.published)
	r.library.WithInstalledPlugins(installedList{readerPlugin(reviewedPluginID(t), true, 262144, "reaper")})
	f.opens = &openCounter{}
	r.factsOpened = f.opens.hook
	f.runner = NewFactsReadRunner(r, FactsReadHost{
		Team: func(Scope) (FactsReadTeam, bool) {
			return FactsReadTeam{PluginID: "app", BlueprintID: "song", Label: "App Assistant"}, true
		},
		Assistant: func(string, FactsReadTeam) (string, string) {
			if f.assistant != "" {
				return "", f.assistant
			}
			return "App Assistant", ""
		},
		Model: func(context.Context, string, string) (ManagerChat, string, error) {
			if !f.model {
				return nil, "", errors.New("no model")
			}
			return f.chat.chat, "test-model", nil
		},
		Reader: f.reader.call,
	})
	r.SetFactsReadRunner(f.runner)
	return f
}

// waitPublished waits for the scan's held event.
func (f *readFixture) waitPublished(t *testing.T, n int) []bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if events := f.published.all(); len(events) >= n {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the scan's event was not published (%d of %d)", len(f.published.all()), n)
	return nil
}

func (f *readFixture) latest(t *testing.T) FactsReadRun {
	t.Helper()
	run, ok := f.r.library.LatestFactsRead(f.scope)
	if !ok {
		t.Fatal("no read receipt")
	}
	return run
}

// FR30, FR39, contract item 5: a completed scan hands the plugin's songs to the
// assistant's read; the host opens none of them; the event waits for the facts.
func TestFactsRead_AScanIsReadByTheAssistantAndTheEventWaits(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	scanRoot(t, f.r, f.scope, f.root.ID, "assistant-scan")
	if events := f.waitPublished(t, 1); !reflect.DeepEqual(events, []bool{true}) {
		t.Fatalf("published %v, want once with the facts already saved", events)
	}
	if got := f.opens.take(); len(got) != 0 {
		t.Fatalf("the host opened %v itself: a song must have one reader", got)
	}
	if files := f.reader.read(); len(files) != 2 {
		t.Fatalf("the plugin read %v, want each song's dated file", files)
	}
	run := f.latest(t)
	if run.Status != factsReadFinished || run.Reason != "" || run.Read != 2 || run.Targets != 2 || run.Left != 0 ||
		run.AgentName != "App Assistant" || run.Model != "test-model" || run.Trigger != FactsReadByScan {
		t.Fatalf("receipt = %+v", run)
	}
	if facts := factsAt(t, f.r, f.scope, f.root.ID, "Single"); facts == nil || facts.TempoBPM != 120 || facts.TrackCount != 4 {
		t.Fatalf("stored facts = %+v", facts)
	}
	// The one tool, no path or ID argument, in a request with no song names.
	request := f.chat.requests[0]
	if len(request.Tools) != 1 || request.Tools[0].Name != FactsReadToolName || request.MaxTokens != factsReadMaxTokens ||
		strings.Contains(request.SystemPrompt+request.Messages[0].Content, "Single") {
		t.Fatalf("request = %+v", request)
	}
}

// FR38: the turn is the same at any number of songs.
func TestFactsRead_TheRequestIsTheSameAtAnySize(t *testing.T) {
	requests := func(n int) []llm.ChatRequest {
		chat := &fakeAssistantChat{responses: []*llm.ChatResponse{callsTheTool()}}
		runner := &FactsReadRunner{roots: &Roots{library: &Store{}}, host: FactsReadHost{}}
		run := &FactsReadRun{Targets: n}
		targets := make([]songFactsTarget, n)
		for i := range targets {
			targets[i] = songFactsTarget{Relative: "Song " + itoa(i), Format: "unread"}
		}
		job := &factsReadJob{runner: runner, slices: []FactsReadSlice{{targets: targets}}, run: run}
		runner.turn(context.Background(), chat.chat, "m", "App Assistant", job)
		return chat.requests
	}
	six, many := requests(6), requests(500)
	if len(six) != 1 || !reflect.DeepEqual(six, many) {
		t.Fatalf("requests differ: %+v vs %+v", six, many)
	}
}

// No tool call: one nudge, then the read stops as no_call. The host never
// reads the songs itself (no fallback).
func TestFactsRead_NoToolCallNudgesOnceThenStops(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{saysNothing(), saysNothing()}
	scanRoot(t, f.r, f.scope, f.root.ID, "silent")
	f.waitPublished(t, 1)
	run := f.latest(t)
	if run.Status != factsReadStopped || run.Reason != FactsReadNoCall || run.Left != 2 || len(f.chat.requests) != 2 {
		t.Fatalf("receipt = %+v, calls %d", run, len(f.chat.requests))
	}
	if len(f.reader.read()) != 0 || len(f.opens.take()) != 0 {
		t.Fatal("a song was read without the tool being called")
	}
	if !strings.Contains(f.chat.requests[1].Messages[len(f.chat.requests[1].Messages)-1].Content, FactsReadToolName) {
		t.Fatal("the second call was not the nudge")
	}
	// The nudge also works.
	f2 := newReadFixture(t)
	f2.chat.responses = []*llm.ChatResponse{saysNothing(), callsTheTool()}
	scanRoot(t, f2.r, f2.scope, f2.root.ID, "nudged")
	f2.waitPublished(t, 1)
	if run := f2.latest(t); run.Status != factsReadFinished || run.Read != 2 {
		t.Fatalf("nudged receipt = %+v", run)
	}
}

// FR35, FR36: each preflight stop has its reason, the event still goes out,
// and nothing is read, by the plugin or by the host.
func TestFactsRead_PreflightStopsSayWhy(t *testing.T) {
	for name, set := range map[string]func(f *readFixture){
		FactsReadNoAssistant:      func(f *readFixture) { f.assistant = FactsReadNoAssistant },
		FactsReadAssistantMissing: func(f *readFixture) { f.assistant = FactsReadAssistantMissing },
		FactsReadNoModel:          func(f *readFixture) { f.model = false },
	} {
		t.Run(name, func(t *testing.T) {
			f := newReadFixture(t)
			set(f)
			scanRoot(t, f.r, f.scope, f.root.ID, "preflight")
			f.waitPublished(t, 1)
			run := f.latest(t)
			if run.Status != factsReadStopped || run.Reason != name || run.Left != 2 || run.Read != 0 {
				t.Fatalf("receipt = %+v", run)
			}
			if len(f.reader.read()) != 0 || len(f.opens.take()) != 0 || len(f.chat.requests) != 0 {
				t.Fatal("something was read, or the model was called")
			}
		})
	}
}

// FR34: song details switched off during a read stops it; nothing from the
// batch in flight is kept.
func TestFactsRead_DetailsOffMidCallKeepsNothingFromThatBatch(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	f.reader.during = func() {
		switchSongDetailsOff(t, f.r.library.workspaces, f.scope)
	}
	scanRoot(t, f.r, f.scope, f.root.ID, "off")
	f.waitPublished(t, 1)
	run := f.latest(t)
	if run.Status != factsReadStopped || run.Reason != FactsReadDetailsOff || run.Read != 0 {
		t.Fatalf("receipt = %+v", run)
	}
	if facts := factsAt(t, f.r, f.scope, f.root.ID, "Single"); facts != nil {
		t.Fatalf("facts from the cancelled batch were kept: %+v", facts)
	}
}

// FR35: a failed plugin call stops the read with its reason; FR27.
func TestFactsRead_APluginFailureStops(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	f.reader.err = errors.New("service exited")
	scanRoot(t, f.r, f.scope, f.root.ID, "broken-plugin")
	f.waitPublished(t, 1)
	if run := f.latest(t); run.Status != factsReadStopped || run.Reason != FactsReadPluginFailed || run.Left != 2 {
		t.Fatalf("receipt = %+v", run)
	}
}

// FR37: a read a previous process left started is shown, then stored, as
// stopped by a restart; it is never rerun.
func TestFactsRead_ARestartIsShownThenSweptAsStopped(t *testing.T) {
	f := newReadFixture(t)
	scanRoot(t, f.r, f.scope, f.root.ID, "before-restart")
	f.waitPublished(t, 1)
	left := FactsReadRun{ID: "left-started", Boot: "previous-process", Trigger: FactsReadByLearn, Status: factsReadStarted,
		Targets: 10, StartedAt: time.Now().UTC().Add(time.Minute)}
	if err := f.r.library.claimFactsRead(f.scope, left); err != nil {
		t.Fatal(err)
	}
	if run := f.latest(t); run.ID != "left-started" || run.Status != factsReadStopped || run.Reason != FactsReadRestarted {
		t.Fatalf("shown = %+v", run)
	}
	if closed := f.runner.Sweep(); closed != 1 {
		t.Fatalf("swept %d", closed)
	}
	doc, _ := f.r.library.Read(f.scope)
	for _, run := range doc.FactsReads {
		if run.ID == "left-started" && (run.Status != factsReadStopped || run.Reason != FactsReadRestarted || run.FinishedAt == nil) {
			t.Fatalf("stored = %+v", run)
		}
	}
	if f.runner.Sweep() != 0 {
		t.Fatal("a second sweep changed something")
	}
}

// The Home reads the newest receipt from the library summary, with no new
// request, and a turn another process left started shows as stopped.
func TestFactsRead_TheSummaryCarriesTheNewestReceipt(t *testing.T) {
	f := newReadFixture(t)
	f.assistant = FactsReadNoAssistant
	scanRoot(t, f.r, f.scope, f.root.ID, "summary")
	f.waitPublished(t, 1)
	summary, err := f.r.library.Summary(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FactsRead == nil || summary.FactsRead.Status != factsReadStopped || summary.FactsRead.Reason != FactsReadNoAssistant {
		t.Fatalf("summary read = %+v", summary.FactsRead)
	}
	encoded, _ := json.Marshal(summary)
	if !strings.Contains(string(encoded), `"facts_read":{`) || strings.Contains(string(encoded), f.root.Path) {
		t.Fatalf("summary json = %s", encoded)
	}
	cut := FactsReadRun{ID: "cut", Boot: "previous-process", Trigger: FactsReadByScan, Status: factsReadStarted,
		Targets: 4, StartedAt: time.Now().UTC().Add(time.Minute)}
	if err := f.r.library.claimFactsRead(f.scope, cut); err != nil {
		t.Fatal(err)
	}
	summary, _ = f.r.library.Summary(f.scope)
	if summary.FactsRead == nil || summary.FactsRead.ID != "cut" || summary.FactsRead.Reason != FactsReadRestarted {
		t.Fatalf("a cut-off read = %+v", summary.FactsRead)
	}
}

// FR41 to FR43: the saved facts of ONE song, found by the workspace linked to
// it, and nothing once song details are off.
func TestLearnedSongForWorkspace(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	scanRoot(t, f.r, f.scope, f.root.ID, "for-workspace")
	f.waitPublished(t, 1)

	// Link two songs to their own workspaces, as opening them does.
	doc, err := f.r.library.Read(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	link := func(relative, workspaceID string) {
		t.Helper()
		entry, _, ok := observationAt(doc, f.root.ID, relative)
		if !ok {
			t.Fatalf("no entry for %q", relative)
		}
		current, _ := f.r.library.Read(f.scope)
		_, _, err := f.r.library.mutateWithHomePolicy(f.scope, current.Revision,
			operation{key: "test-link:" + workspaceID, action: "test_link", digest: proposalRunOpDigest(f.scope, workspaceID)}, nil,
			func(d *Document) (string, error) {
				for i := range d.Entries {
					if d.Entries[i].ID == entry.ID {
						d.Entries[i].Link = &ExactLink{WorkspaceID: workspaceID, LinkID: "link-" + workspaceID, Revision: 1}
					}
				}
				return entry.ID, nil
			})
		if err != nil {
			t.Fatal(err)
		}
	}
	link("Single", "ws-single")
	link("Alternates", "ws-alternates")

	song, ok, err := f.r.library.LearnedSongForWorkspace(f.scope, "ws-single")
	if err != nil || !ok || song.Name != "Single" || song.Facts == nil || song.Facts.TempoBPM != 120 || song.Facts.TrackCount != 4 {
		t.Fatalf("song = %+v ok=%v err=%v", song, ok, err)
	}
	// Names only under a consent that covers them: the numbers-only consent has none.
	if song.Names != nil && (len(song.Names.Tracks) > 0 || len(song.Names.Markers) > 0) {
		t.Fatalf("names under a numbers-only consent: %+v", song.Names)
	}
	// Another workspace gets its own song, never this one's.
	other, ok, _ := f.r.library.LearnedSongForWorkspace(f.scope, "ws-alternates")
	if !ok || other.Name != "Alternates" || other.Name == song.Name {
		t.Fatalf("other song = %+v ok=%v", other, ok)
	}
	// A workspace no song is linked to, or no workspace at all.
	for _, id := range []string{"ws-nowhere", "", "  "} {
		if got, ok, err := f.r.library.LearnedSongForWorkspace(f.scope, id); ok || err != nil || got.Name != "" {
			t.Fatalf("%q = %+v ok=%v err=%v", id, got, ok, err)
		}
	}
	// FR43: song details off, and the next ask has nothing.
	switchSongDetailsOff(t, f.r.library.workspaces, f.scope)
	if got, ok, err := f.r.library.LearnedSongForWorkspace(f.scope, "ws-single"); ok || err != nil || got.Name != "" {
		t.Fatalf("after song details went off = %+v ok=%v err=%v", got, ok, err)
	}
}

// assistant-reads-projects 6.1: the folder's agent is given what the app
// assistant's read saved, through the same listing it always used. Nothing in
// that path knows who read the file, so the summary and the goal are written from
// the same facts as before.
func TestFactsRead_WhatTheReadSavedIsWhatTheFolderAgentIsGiven(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	scanRoot(t, f.r, f.scope, f.root.ID, "agent-facts")
	f.waitPublished(t, 1)
	if got := f.opens.take(); len(got) != 0 {
		t.Fatalf("the host opened %v itself, so these facts did not come from the read", got)
	}
	songs, err := f.r.library.LearnedSongsInRoot(f.scope, f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	byName, withFacts := map[string]LearnedSong{}, 0
	for _, song := range songs {
		byName[song.Name] = song
		if song.Facts != nil {
			withFacts++
		}
	}
	if withFacts != 2 {
		t.Fatalf("the folder agent is given facts for %d of %+v, want the 2 the plugin read", withFacts, songs)
	}
	single := byName["Single"]
	if single.NotRead != "" || single.Facts == nil || single.Facts.TempoBPM != 120 || single.Facts.TrackCount != 4 {
		t.Fatalf("Single = %+v", single)
	}
	if other := byName["Alternates"]; other.NotRead != "" || other.Facts == nil {
		t.Fatalf("Alternates = %+v", other)
	}
	// Song details off: the next ask has nothing, as for any other reader.
	switchSongDetailsOff(t, f.r.library.workspaces, f.scope)
	if songs, err := f.r.library.LearnedSongsInRoot(f.scope, f.root.ID); err != nil || len(songs) != 0 {
		t.Fatalf("after song details went off the folder agent is given %d songs (%v)", len(songs), err)
	}
}

// One press, one turn: a collector gathers the scans of a listing and keeps
// one target per song.
func TestFactsRead_ACollectorMakesOneTurnForSeveralScans(t *testing.T) {
	f := newReadFixture(t)
	f.chat.responses = []*llm.ChatResponse{callsTheTool()}
	ctx, collector := WithDeferredFactsRead(context.Background())
	doc, _ := f.r.library.Read(f.scope)
	review, err := f.r.ReviewScan(f.scope, f.root.ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.r.CommitScan(ctx, f.scope, f.root.ID, review.Token, "collected"); err != nil {
		t.Fatal(err)
	}
	if len(f.published.all()) != 0 || collector.Targets() != 2 {
		t.Fatalf("published %v before the read; collected %d", f.published.all(), collector.Targets())
	}
	run := f.runner.Read(context.Background(), collector.Slices(), FactsReadByLearn)
	if run.Status != factsReadFinished || run.Read != 2 || run.Trigger != FactsReadByLearn || len(f.chat.requests) != 1 {
		t.Fatalf("receipt = %+v", run)
	}
	if events := f.published.all(); !reflect.DeepEqual(events, []bool{true}) {
		t.Fatalf("published %v", events)
	}
}
