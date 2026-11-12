package folderagent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeSongs is the Home's consent and the reading of its songs, set by the
// test. Grant turns the consent on, as the real one does; Learn finishes having
// read everything unless the test supplies another ending, and waits on block
// first when the test holds it.
type fakeSongs struct {
	mu       sync.Mutex
	state    string
	names    bool
	err      error
	reads    []string // homeIDs asked about
	grantErr error
	grants   []string // homeIDs granted
	learns   []string // homeIDs read
	learned  func(rec Record, total int, report func(Learning)) Learning
	block    chan struct{}
	// facts are the folder's songs the agent is given after the reading;
	// factsCalls counts how often it was asked.
	facts      []folderlook.Song
	factsErr   error
	factsCalls int
	// offAfterFacts turns the Home's switch off right after the facts are handed
	// over: the owner switches off while the agent is writing from them.
	offAfterFacts bool
}

func (f *fakeSongs) Facts(context.Context, Record, string) ([]folderlook.Song, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.factsCalls++
	if f.offAfterFacts {
		f.state, f.names = workspace.SongDetailsOff, false
	}
	return f.facts, f.factsErr
}

func (f *fakeSongs) factsAsked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.factsCalls
}

func (f *fakeSongs) Consent(_ context.Context, _, _, homeID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, homeID)
	return f.state, f.names, f.err
}

func (f *fakeSongs) Grant(_ context.Context, _, _, homeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grantErr != nil {
		return f.grantErr
	}
	f.grants = append(f.grants, homeID)
	// The owner's yes is to the offer's words, which include the names.
	f.state, f.names = workspace.SongDetailsOn, true
	return nil
}

func (f *fakeSongs) Learn(_ context.Context, rec Record, homeID string, total int, report func(Learning)) Learning {
	f.mu.Lock()
	f.learns = append(f.learns, homeID)
	learned, block := f.learned, f.block
	f.mu.Unlock()
	if learned != nil {
		return learned(rec, total, report)
	}
	if block != nil {
		<-block
	}
	return Learning{Status: LearningDone, Total: total, Done: total, Read: total}
}

func (f *fakeSongs) counts() (grants, learns int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.grants), len(f.learns)
}

// songsFacts is a map holding seven songs in a readable format and one in a
// format that has no offline grammar.
func songsFacts() foldermap.Facts {
	return foldermap.Facts{
		Portfolio: &foldermap.Portfolio{Shape: folderdigest.ShapeAudio, Count: 8},
		Members: []foldermap.Project{
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Marker: "*.rpp"},
			{Shape: folderdigest.ShapeAudio, Format: "logic", Marker: "*.logicx"},
		},
		Shapes: []foldermap.ShapeCount{
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 7},
			{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 1},
		},
	}
}

// doneRecord is a folder whose Set it up run finished and put it in Home home-1.
func doneRecord() *Record {
	return &Record{
		WorkspaceID: "ws-1", OwnerUserID: "u1", State: StateSetUp, OffersSongs: true, Facts: songsFacts(),
		Setup: &Setup{Status: personalassistant.FolderSetupDone,
			Receipt: &SetupReceipt{Home: &WorkspaceRef{ID: "home-1", Name: "Music", Route: "/workspaces/home-1"}}},
	}
}

func songsService(songs Songs) *Service { return &Service{deps: Deps{Songs: songs}} }

func TestSongsOffer_Eligibility(t *testing.T) {
	cases := []struct {
		name    string
		songs   *fakeSongs
		mutate  func(*Record)
		offered bool
	}{
		{"a finished run, no consent", &fakeSongs{state: workspace.SongDetailsNone}, nil, true},
		// The older folder card's consent covers the numbers only: the next folder
		// added to that Home is offered the upgrade, with the same words.
		{"the Home reads the numbers only (the upgrade)", &fakeSongs{state: workspace.SongDetailsOn}, nil, true},
		{"the Home already reads (names)", &fakeSongs{state: workspace.SongDetailsOn, names: true}, nil, false},
		{"the Home's switch was turned off", &fakeSongs{state: workspace.SongDetailsOff}, nil, false},
		{"the consent could not be read", &fakeSongs{err: errors.New("boom")}, nil, false},
		{"an older folder, set up before the offer", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.OffersSongs = false }, false},
		{"answered: learn, reading under way", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) {
			r.SongsAnswer, r.Learning = SongsLearn, &Learning{Status: LearningRunning, Total: 7}
		}, false},
		{"answered: learn, granted", &fakeSongs{state: workspace.SongDetailsOn, names: true}, func(r *Record) { r.SongsAnswer = SongsLearn }, false},
		// A folder that already learned its songs is not asked again when the Home's
		// consent is later lowered to the numbers (the demo showed it asking twice);
		// nor while the summary is being written.
		{"answered: learn, finished, consent later lowered", &fakeSongs{state: workspace.SongDetailsOn}, func(r *Record) {
			r.SongsAnswer, r.Learning = SongsLearn, &Learning{Status: LearningDone, Total: 7, Read: 7}
		}, false},
		{"answered: learn, summary being written", &fakeSongs{state: workspace.SongDetailsOn}, func(r *Record) {
			r.SongsAnswer, r.Learning = SongsLearn, &Learning{Status: LearningSummarizing, Total: 7, Read: 7}
		}, false},
		// A reading that was stopped before it finished can be offered again where the
		// consent does not yet cover names.
		{"answered: learn, stopped, numbers consent", &fakeSongs{state: workspace.SongDetailsOn}, func(r *Record) {
			r.SongsAnswer, r.Learning = SongsLearn, &Learning{Status: LearningStopped, Total: 7}
		}, true},
		// Ori stopped between recording the yes and granting it: asked again.
		{"answered: learn, never granted", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.SongsAnswer = SongsLearn }, true},
		{"answered: not now", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.SongsAnswer = SongsDismissed }, false},
		{"the run stopped", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.Setup.Status = personalassistant.FolderSetupStopped }, false},
		{"the run is still going", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.State = StateSettingUp }, false},
		{"the folder joined no Home", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.Setup.Receipt.Home = nil }, false},
		{"no receipt", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.Setup.Receipt = nil }, false},
		{"a Home with no id", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.Setup.Receipt.Home.ID = "  " }, false},
		{"no song can be read offline", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) {
			r.Facts.Shapes = []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 8}}
		}, false},
		{"no Home would hold these projects", &fakeSongs{state: workspace.SongDetailsNone}, func(r *Record) { r.Facts.Portfolio = nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doneRecord()
			if tc.mutate != nil {
				tc.mutate(rec)
			}
			offer := songsService(tc.songs).songsOffer(context.Background(), rec)
			if (offer != nil) != tc.offered {
				t.Fatalf("offer = %+v, want offered=%v", offer, tc.offered)
			}
		})
	}
	t.Run("no consent seam", func(t *testing.T) {
		if offer := songsService(nil).songsOffer(context.Background(), doneRecord()); offer != nil {
			t.Fatalf("offered without a way to read the consent: %+v", offer)
		}
	})
	t.Run("no record", func(t *testing.T) {
		if offer := songsService(&fakeSongs{}).songsOffer(context.Background(), nil); offer != nil {
			t.Fatalf("offered for no record: %+v", offer)
		}
	})
}

func TestSongsOffer_WordsAndCounts(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone}
	offer := songsService(songs).songsOffer(context.Background(), doneRecord())
	if offer == nil {
		t.Fatal("no offer")
	}
	want := SongsOfferView{
		Count:   7,
		Noun:    "songs",
		Ask:     "Want me to learn the 7 songs?",
		Reads:   "I'll read tempo, length, track count, track names, markers and regions from each project file. Nothing is changed, and audio is not opened.",
		NotRead: "1 Logic Pro song can't be read this way.",
	}
	if *offer != want {
		t.Fatalf("offer = %+v\nwant   %+v", *offer, want)
	}
	// The consent is read for the Home the run named, not for the folder.
	if len(songs.reads) != 1 || songs.reads[0] != "home-1" {
		t.Fatalf("consent read for %v, want [home-1]", songs.reads)
	}
	// One song: singular noun; nothing unreadable: no second sentence.
	rec := doneRecord()
	rec.Facts.Shapes = []foldermap.ShapeCount{{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 1}}
	one := songsService(songs).songsOffer(context.Background(), rec)
	if one == nil || one.Ask != "Want me to learn the 1 song?" || one.Noun != "song" || one.NotRead != "" {
		t.Fatalf("one song = %+v", one)
	}
}

func TestNotReadSentence(t *testing.T) {
	audio := folderdigest.ShapeAudio
	cases := []struct {
		name string
		in   []foldermap.FormatCount
		want string
	}{
		{"none", nil, ""},
		{"one named format", []foldermap.FormatCount{{Format: "logic", Name: "Logic Pro", Count: 1}}, "1 Logic Pro song can't be read this way."},
		{"a named format, several", []foldermap.FormatCount{{Format: "logic", Name: "Logic Pro", Count: 3}}, "3 Logic Pro songs can't be read this way."},
		{"two named formats", []foldermap.FormatCount{{Format: "a", Name: "Alpha", Count: 2}, {Format: "b", Name: "Beta", Count: 1}},
			"2 Alpha songs and 1 Beta song can't be read this way."},
		{"three, one unnamed", []foldermap.FormatCount{{Format: "a", Name: "Alpha", Count: 2}, {Count: 1}, {Format: "b", Name: "Beta", Count: 4}},
			"2 Alpha songs, 4 Beta songs and 1 other song can't be read this way."},
		{"unnamed only", []foldermap.FormatCount{{Format: "z", Count: 2}, {Count: 1}}, "3 other songs can't be read this way."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notReadSentence(foldermap.Readability{Shape: audio, Unreadable: tc.in}); got != tc.want {
				t.Fatalf("sentence = %q, want %q", got, tc.want)
			}
		})
	}
}

// songsHarness runs a folder through Set it up with a fake run that placed it
// in a Home, so the finished record carries the receipt the offer reads.
func songsHarness(t *testing.T, songs *fakeSongs) (*harness, string) {
	t.Helper()
	return songsHarnessWith(t, songs, &scriptedTurns{responses: []*llm.ChatResponse{{Content: demoReport}}})
}

// songsHarnessWith is songsHarness with the model's script supplied: the first
// response is the first look's, the next are for turns after the reading.
func songsHarnessWith(t *testing.T, songs *fakeSongs, turns *scriptedTurns) (*harness, string) {
	t.Helper()
	h := newHarness(t, demo(t))
	withTurns(h, turns)
	h.setups.receipt = &SetupReceipt{Home: &WorkspaceRef{ID: "home-1", Name: "Music", Route: "/workspaces/home-1"}}
	h.svc.deps.Songs = songs
	view := h.start(t, "")
	// The demo folder looks like a Home's songs: its owner confirms that first.
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	report, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is offered on the report: the run has not happened.
	if report.SongsOffer != nil {
		t.Fatalf("offered on the report card: %+v", report.SongsOffer)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	return h, view.WorkspaceID
}

func TestSongsOffer_ComesBeforeTheGoalOfferAndTheGoalWaitsForTheAnswer(t *testing.T) {
	h, id := songsHarness(t, &fakeSongs{state: workspace.SongDetailsNone})
	rec, _ := h.svc.Record("u1", id)
	if !rec.OffersSongs {
		t.Fatal("Set it up did not mark the record as one that offers the songs")
	}
	view, err := h.svc.View(context.Background(), "u1", id)
	if err != nil {
		t.Fatal(err)
	}
	if view.SongsOffer == nil || view.SongsOffer.Count != 7 {
		t.Fatalf("songs offer after a done run = %+v", view.SongsOffer)
	}
	if view.GoalOffer != nil {
		t.Fatalf("the goal was offered while the songs offer waits: %+v", view.GoalOffer)
	}
	if _, err := h.svc.AnswerGoal(context.Background(), "u1", id, true, nil); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a goal answer before the songs offer was answered = %v", err)
	}
	if goal, set := h.ws.goals[id]; set {
		t.Fatalf("the goal was set to %q before the songs offer was answered", goal)
	}
}

func TestSongsOffer_TheGoalOfferProceedsOnceTheSongsOfferIsSettled(t *testing.T) {
	settle := map[string]func(t *testing.T, h *harness, id string, songs *fakeSongs){
		"answered": func(t *testing.T, h *harness, id string, _ *fakeSongs) {
			if _, err := h.svc.deps.Store.Update(id, func(r *Record) error { r.SongsAnswer = SongsDismissed; return nil }); err != nil {
				t.Fatal(err)
			}
		},
		"the Home already reads": func(_ *testing.T, _ *harness, _ string, songs *fakeSongs) {
			songs.state, songs.names = workspace.SongDetailsOn, true
		},
		"the Home was switched off": func(_ *testing.T, _ *harness, _ string, songs *fakeSongs) {
			songs.state = workspace.SongDetailsOff
		},
	}
	for name, do := range settle {
		t.Run(name, func(t *testing.T) {
			songs := &fakeSongs{state: workspace.SongDetailsNone}
			h, id := songsHarness(t, songs)
			do(t, h, id, songs)
			view, err := h.svc.View(context.Background(), "u1", id)
			if err != nil {
				t.Fatal(err)
			}
			if view.SongsOffer != nil {
				t.Fatalf("the songs offer stayed: %+v", view.SongsOffer)
			}
			if view.GoalOffer == nil {
				t.Fatal("the goal offer did not follow")
			}
			if _, err := h.svc.AnswerGoal(context.Background(), "u1", id, true, nil); err != nil {
				t.Fatalf("goal answer = %v", err)
			}
		})
	}
}
