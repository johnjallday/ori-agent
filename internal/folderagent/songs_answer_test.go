package folderagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// waitFor polls until cond holds, for state a background reading writes on a timer.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAnswerSongs_NotNowReadsNothingAndTheGoalOfferFollows(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone}
	h, id := songsHarness(t, songs)

	answered, err := h.svc.AnswerSongs(context.Background(), "u1", id, false)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if grants, learns := songs.counts(); grants != 0 || learns != 0 {
		t.Fatalf("Not now granted %d and read %d times", grants, learns)
	}
	if rec, _ := h.svc.Record("u1", id); rec.SongsAnswer != SongsDismissed || rec.Learning != nil {
		t.Fatalf("record after Not now = answer %q learning %+v", rec.SongsAnswer, rec.Learning)
	}
	if answered.SongsOffer != nil || answered.Learning != nil {
		t.Fatalf("view after Not now = offer %+v learning %+v", answered.SongsOffer, answered.Learning)
	}
	if answered.GoalOffer == nil {
		t.Fatal("the goal offer did not follow Not now")
	}
	// It is not shown again by itself, and cannot be answered twice.
	if again, _ := h.svc.View(context.Background(), "u1", id); again.SongsOffer != nil {
		t.Fatalf("the offer came back: %+v", again.SongsOffer)
	}
	for _, learn := range []bool{false, true} {
		if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, learn); !errors.Is(err, ErrWrongState) {
			t.Fatalf("a second answer (learn=%v) = %v", learn, err)
		}
	}
	if grants, _ := songs.counts(); grants != 0 {
		t.Fatalf("a refused answer granted %d times", grants)
	}
}

func TestAnswerSongs_LearnThemGrantsThenReadsAndHoldsTheGoalUntilItEnds(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, block: make(chan struct{})}
	songs.learned = func(_ Record, total int, report func(Learning)) Learning {
		report(Learning{Status: LearningRunning, Total: total, Done: 2, Read: 2, Song: "Night Drive"})
		<-songs.block
		return Learning{Status: LearningDone, Total: total, Done: total, Read: 6}
	}
	h, id := songsHarness(t, songs)

	started, err := h.svc.AnswerSongs(context.Background(), "u1", id, true)
	if err != nil {
		t.Fatal(err)
	}
	// Right away: the answer is on the record, the consent was granted for the
	// Home the run named, and the reading is under way.
	if grants, _ := songs.counts(); grants != 1 || songs.grants[0] != "home-1" {
		t.Fatalf("grants = %v, want one for home-1", songs.grants)
	}
	if rec, _ := h.svc.Record("u1", id); rec.SongsAnswer != SongsLearn || !learningRunning(rec) || rec.Learning.Total != 7 {
		t.Fatalf("record while learning = answer %q learning %+v", rec.SongsAnswer, rec.Learning)
	}
	if started.SongsOffer != nil || started.Learning == nil || started.Learning.Status != LearningRunning {
		t.Fatalf("view while learning = offer %+v learning %+v", started.SongsOffer, started.Learning)
	}
	if started.GoalOffer != nil {
		t.Fatalf("the goal was offered while the songs were being read: %+v", started.GoalOffer)
	}
	if _, err := h.svc.AnswerGoal(context.Background(), "u1", id, true, nil); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a goal answer mid-reading = %v", err)
	}
	// A second "Learn them" mid-reading is refused: nothing is granted twice.
	if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, true); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a second answer mid-reading = %v", err)
	}

	// The progress the reading noted reaches the record on the timer, in words.
	waitFor(t, "the progress line", func() bool {
		view, _ := h.svc.View(context.Background(), "u1", id)
		return view.Learning != nil && view.Learning.Line == "Learning Night Drive… (3 of 7)"
	})

	close(songs.block)
	h.svc.Wait()
	done, _ := h.svc.View(context.Background(), "u1", id)
	if done.Learning == nil || done.Learning.Status != LearningDone || done.Learning.Line != "Learned 6 of 7 songs" ||
		done.Learning.NotRead != "1 Logic Pro song can't be read this way." {
		t.Fatalf("view after the reading = %+v", done.Learning)
	}
	if done.GoalOffer == nil {
		t.Fatal("the goal offer did not follow the reading")
	}
	if grants, learns := songs.counts(); grants != 1 || learns != 1 {
		t.Fatalf("grants %d, reads %d; want one each", grants, learns)
	}
}

func TestAnswerSongs_AGrantThatFailsPutsTheOfferBack(t *testing.T) {
	for name, grantErr := range map[string]error{
		"the Home's switch is off": ErrWrongState,
		"the grant failed":         errors.New("store unavailable"),
	} {
		t.Run(name, func(t *testing.T) {
			songs := &fakeSongs{state: workspace.SongDetailsNone, grantErr: grantErr}
			h, id := songsHarness(t, songs)
			if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, true); !errors.Is(err, grantErr) {
				t.Fatalf("answer = %v, want %v", err, grantErr)
			}
			h.svc.Wait()
			if _, learns := songs.counts(); learns != 0 {
				t.Fatalf("read %d times without a consent", learns)
			}
			rec, _ := h.svc.Record("u1", id)
			if rec.SongsAnswer != "" || rec.Learning != nil {
				t.Fatalf("the failed answer was kept: answer %q learning %+v", rec.SongsAnswer, rec.Learning)
			}
			view, _ := h.svc.View(context.Background(), "u1", id)
			if view.SongsOffer == nil || view.GoalOffer != nil {
				t.Fatalf("the offer did not come back: offer %+v goal %+v", view.SongsOffer, view.GoalOffer)
			}
			// And the owner can try again.
			songs.mu.Lock()
			songs.grantErr = nil
			songs.mu.Unlock()
			if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, true); err != nil {
				t.Fatalf("retry = %v", err)
			}
			h.svc.Wait()
		})
	}
}

func TestAnswerSongs_TwoClicksGrantAndReadOnce(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, block: make(chan struct{})}
	h, id := songsHarness(t, songs)
	const clicks = 8
	results := make(chan error, clicks)
	var start sync.WaitGroup
	start.Add(1)
	for range clicks {
		go func() {
			start.Wait()
			_, err := h.svc.AnswerSongs(context.Background(), "u1", id, true)
			results <- err
		}()
	}
	start.Done()
	accepted := 0
	for range clicks {
		switch err := <-results; {
		case err == nil:
			accepted++
		case !errors.Is(err, ErrWrongState):
			t.Fatalf("a click failed with %v", err)
		}
	}
	close(songs.block)
	h.svc.Wait()
	if grants, learns := songs.counts(); accepted != 1 || grants != 1 || learns != 1 {
		t.Fatalf("%d clicks accepted, %d grants, %d reads; want one of each", accepted, grants, learns)
	}
}

func TestAnswerSongs_RefusedWhenNoOfferIsWaiting(t *testing.T) {
	cases := map[string]func(*fakeSongs){
		"the Home already reads": func(f *fakeSongs) { f.state, f.names = workspace.SongDetailsOn, true },
		"the Home was switched off": func(f *fakeSongs) {
			f.state = workspace.SongDetailsOff
		},
		"the consent cannot be read": func(f *fakeSongs) { f.err = errors.New("boom") },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			songs := &fakeSongs{state: workspace.SongDetailsNone}
			h, id := songsHarness(t, songs)
			set(songs)
			for _, learn := range []bool{true, false} {
				if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, learn); !errors.Is(err, ErrWrongState) {
					t.Fatalf("answer (learn=%v) = %v", learn, err)
				}
			}
			if grants, learns := songs.counts(); grants != 0 || learns != 0 {
				t.Fatalf("a refused answer granted %d and read %d", grants, learns)
			}
			if rec, _ := h.svc.Record("u1", id); rec.SongsAnswer != "" {
				t.Fatalf("a refused answer was recorded: %q", rec.SongsAnswer)
			}
		})
	}
	t.Run("before the run is done", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsNone}
		h := newHarness(t, demo(t))
		h.svc.deps.Songs = songs
		view := h.start(t, "")
		if _, err := h.svc.AnswerSongs(context.Background(), "u1", view.WorkspaceID, true); !errors.Is(err, ErrWrongState) {
			t.Fatalf("answer before Set it up = %v", err)
		}
		if grants, _ := songs.counts(); grants != 0 {
			t.Fatalf("granted %d times before Set it up", grants)
		}
	})
}

func TestAnswerSongs_AStoppedReadingSaysSoAndTheGoalOfferStillFollows(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone}
	songs.learned = func(_ Record, total int, _ func(Learning)) Learning {
		return Learning{Status: LearningStopped, Total: total, Done: 3, Read: 3, Song: "Night Drive"}
	}
	h, id := songsHarness(t, songs)
	if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, true); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	view, _ := h.svc.View(context.Background(), "u1", id)
	if view.Learning == nil || view.Learning.Status != LearningStopped || view.Learning.Line != "Stopped before it finished. Learned 3 of 7 songs" {
		t.Fatalf("stopped reading = %+v", view.Learning)
	}
	if rec, _ := h.svc.Record("u1", id); rec.Learning.Song != "" && rec.Learning.Status == LearningDone {
		t.Fatalf("record = %+v", rec.Learning)
	}
	if view.GoalOffer == nil {
		t.Fatal("a stopped reading held the goal offer")
	}
}

func TestAnswerSongs_ASweepClosesAReadingAPreviousProcessLeftRunning(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsOn, names: true}
	h, id := songsHarness(t, songs)
	// A previous process was reading when it stopped.
	if _, err := h.svc.deps.Store.Update(id, func(r *Record) error {
		r.SongsAnswer = SongsLearn
		r.Learning = &Learning{Status: LearningRunning, Total: 7, Done: 2, Read: 2, Song: "Night Drive", StartedAt: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view, _ := h.svc.View(context.Background(), "u1", id); view.GoalOffer != nil {
		t.Fatalf("the goal was offered mid-reading: %+v", view.GoalOffer)
	}
	if closed := h.svc.SweepInterrupted(); closed != 1 {
		t.Fatalf("sweep closed %d, want 1", closed)
	}
	view, _ := h.svc.View(context.Background(), "u1", id)
	// FR37: a restart is a named reason, and Try again is offered for it.
	if view.Learning == nil || view.Learning.Status != LearningStopped ||
		view.Learning.Line != "Reading stopped when Ori restarted. Learned 2 of 7 songs." ||
		view.Learning.Reason != LearningReasonRestarted || view.Learning.Action != LearningActionRetry {
		t.Fatalf("after the sweep = %+v", view.Learning)
	}
	if view.GoalOffer == nil {
		t.Fatal("the goal offer stayed held after the sweep")
	}
}

func TestAnswerSongs_AYesThatNeverGrantedIsOfferedAgain(t *testing.T) {
	// Ori stopped between recording "Learn them" and granting it: the answer says
	// learn, the Home holds no consent, and nothing is running.
	songs := &fakeSongs{state: workspace.SongDetailsNone}
	h, id := songsHarness(t, songs)
	if _, err := h.svc.deps.Store.Update(id, func(r *Record) error {
		r.SongsAnswer = SongsLearn
		r.Learning = &Learning{Status: LearningStopped, Total: 7}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, _ := h.svc.View(context.Background(), "u1", id)
	if view.SongsOffer == nil || view.GoalOffer != nil {
		t.Fatalf("the offer was not made again: offer %+v goal %+v", view.SongsOffer, view.GoalOffer)
	}
	if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, true); err != nil {
		t.Fatalf("answering again = %v", err)
	}
	h.svc.Wait()
	if grants, learns := songs.counts(); grants != 1 || learns != 1 {
		t.Fatalf("grants %d, reads %d; want one each", grants, learns)
	}
}

// assistant-reads-projects FR33, FR35: the card names the reader, and each way a
// reading stops has its own sentence and its one action.
func TestLearningView_NamesTheReaderAndEachWayItStops(t *testing.T) {
	rec := doneRecord()
	reading := func(l Learning) *LearningView {
		rec.Learning = &l
		return learningView(rec)
	}
	if got := reading(Learning{Status: LearningRunning, Total: 6, Reader: "REAPER Assistant"}); got.Line != "REAPER Assistant is reading 6 songs…" || got.Reader != "REAPER Assistant" {
		t.Fatalf("starting = %+v", got)
	}
	if got := reading(Learning{Status: LearningRunning, Total: 7, Done: 2, Song: "Night Drive", Reader: "REAPER Assistant"}); got.Line != "REAPER Assistant is reading Night Drive… (3 of 7)" {
		t.Fatalf("on a song = %+v", got)
	}
	for _, tc := range []struct {
		reason, line, action string
		learning             Learning
	}{
		{LearningReasonNoModel, "REAPER Assistant has no model, so the songs were not read.", LearningActionChooseModel, Learning{}},
		{LearningReasonAssistantMissing, "Your assistant is gone, so the songs were not read.", LearningActionAddAssistant, Learning{}},
		{LearningReasonNoAssistant, "This Home has no assistant to read the songs yet.", LearningActionAddAssistant, Learning{}},
		{LearningReasonPluginUpdate, "The plugin cannot read songs yet. Update it to read them.", LearningActionUpdatePlugin, Learning{}},
		{LearningReasonPluginFailed, "Reading stopped after 3 of 7 songs. Learned 3 of 7 songs.", LearningActionRetry, Learning{Done: 3, Read: 3}},
		{LearningReasonTimeLimit, "Reading stopped after 0 of 7 songs.", LearningActionRetry, Learning{}},
		{LearningReasonDetailsOff, "Song details are off for this Home.", LearningActionOpenSwitch, Learning{}},
		{LearningReasonNoCall, "REAPER Assistant did not start the reading.", LearningActionRetry, Learning{}},
		{LearningReasonModelError, "REAPER Assistant did not start the reading.", LearningActionRetry, Learning{}},
	} {
		l := tc.learning
		l.Status, l.Total, l.Reason, l.Reader = LearningStopped, 7, tc.reason, "REAPER Assistant"
		if got := reading(l); got.Line != tc.line || got.Action != tc.action || got.Reason != tc.reason {
			t.Errorf("%s = %+v, want %q / %s", tc.reason, got, tc.line, tc.action)
		}
	}
	// A stop with no named reason keeps today's words and offers no action.
	if got := reading(Learning{Status: LearningStopped, Total: 7, Done: 3, Read: 3}); got.Action != "" || got.Reason != "" {
		t.Fatalf("an unnamed stop = %+v", got)
	}
}

// FR35: Try again runs the reading again without a second grant, only while the
// owner's yes stands and the Home's switch is on.
func TestRetryLearning(t *testing.T) {
	stopped := func(r *Record) error {
		r.SongsAnswer = SongsLearn
		r.Learning = &Learning{Status: LearningStopped, Total: 7, Done: 2, Read: 2, Reason: LearningReasonPluginFailed}
		return nil
	}
	t.Run("reads again without granting again", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOn, names: true}
		h, id := songsHarness(t, songs)
		if _, err := h.svc.deps.Store.Update(id, stopped); err != nil {
			t.Fatal(err)
		}
		view, err := h.svc.RetryLearning(context.Background(), "u1", id)
		if err != nil || view.Learning == nil || view.Learning.Status != LearningRunning {
			t.Fatalf("retry = %+v %v", view.Learning, err)
		}
		h.svc.Wait()
		if grants, learns := songs.counts(); grants != 0 || learns != 1 {
			t.Fatalf("grants %d, reads %d; want no grant and one read", grants, learns)
		}
		rec, _ := h.svc.Record("u1", id)
		if rec.Learning.Status != LearningDone || rec.Learning.Reason != "" {
			t.Fatalf("after the retry = %+v", rec.Learning)
		}
	})
	t.Run("refused when the switch is off", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOff}
		h, id := songsHarness(t, songs)
		_, _ = h.svc.deps.Store.Update(id, stopped)
		if _, err := h.svc.RetryLearning(context.Background(), "u1", id); !errors.Is(err, ErrWrongState) {
			t.Fatalf("retry with the switch off = %v", err)
		}
		if _, learns := songs.counts(); learns != 0 {
			t.Fatal("a read started with the switch off")
		}
	})
	t.Run("refused unless a reading stopped", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOn, names: true}
		h, id := songsHarness(t, songs)
		if _, err := h.svc.RetryLearning(context.Background(), "u1", id); !errors.Is(err, ErrWrongState) {
			t.Fatalf("retry before any reading = %v", err)
		}
		_, _ = h.svc.deps.Store.Update(id, func(r *Record) error {
			r.SongsAnswer = SongsLearn
			r.Learning = &Learning{Status: LearningRunning, Total: 7}
			return nil
		})
		if _, err := h.svc.RetryLearning(context.Background(), "u1", id); !errors.Is(err, ErrWrongState) {
			t.Fatalf("retry while reading = %v", err)
		}
	})
	t.Run("two presses read once", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOn, names: true, block: make(chan struct{})}
		h, id := songsHarness(t, songs)
		_, _ = h.svc.deps.Store.Update(id, stopped)
		if _, err := h.svc.RetryLearning(context.Background(), "u1", id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.RetryLearning(context.Background(), "u1", id); !errors.Is(err, ErrWrongState) {
			t.Fatalf("second press = %v", err)
		}
		close(songs.block)
		h.svc.Wait()
		if _, learns := songs.counts(); learns != 1 {
			t.Fatalf("reads = %d", learns)
		}
	})
}

func TestLearningView_Words(t *testing.T) {
	rec := doneRecord()
	cases := []struct {
		name    string
		l       *Learning
		line    string
		notRead string
	}{
		{"none", nil, "", ""},
		{"starting", &Learning{Status: LearningRunning, Total: 7}, "Learning the 7 songs…", ""},
		{"on the first song", &Learning{Status: LearningRunning, Total: 7, Song: "Night Drive"}, "Learning Night Drive… (1 of 7)", ""},
		{"on the last song", &Learning{Status: LearningRunning, Total: 7, Done: 6, Song: "Closer"}, "Learning Closer… (7 of 7)", ""},
		{"never past the total", &Learning{Status: LearningRunning, Total: 7, Done: 9, Song: "Closer"}, "Learning Closer… (7 of 7)", ""},
		{"all read", &Learning{Status: LearningDone, Total: 7, Done: 7, Read: 7}, "Learned 7 songs", "1 Logic Pro song can't be read this way."},
		{"one read", &Learning{Status: LearningDone, Total: 1, Done: 1, Read: 1}, "Learned 1 song", "1 Logic Pro song can't be read this way."},
		{"some read", &Learning{Status: LearningDone, Total: 7, Done: 7, Read: 5}, "Learned 5 of 7 songs", "1 Logic Pro song can't be read this way."},
		{"none read", &Learning{Status: LearningDone, Total: 7, Done: 7}, "No song could be read", "1 Logic Pro song can't be read this way."},
		{"stopped with some", &Learning{Status: LearningStopped, Total: 7, Done: 3, Read: 3}, "Stopped before it finished. Learned 3 of 7 songs", "1 Logic Pro song can't be read this way."},
		{"stopped with none", &Learning{Status: LearningStopped, Total: 7}, "Stopped before it finished", "1 Logic Pro song can't be read this way."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.Learning = tc.l
			got := learningViewWith(rec, func(format string) bool { return format == "reaper" })
			if tc.l == nil {
				if got != nil {
					t.Fatalf("view = %+v, want nil", got)
				}
				return
			}
			if got.Line != tc.line || got.NotRead != tc.notRead {
				t.Fatalf("line %q / not read %q, want %q / %q", got.Line, got.NotRead, tc.line, tc.notRead)
			}
		})
	}
}
