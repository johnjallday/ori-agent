package folderagent

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	demoHomeID       = "home-1"
	firstLooksGoal   = "Keep these songs moving toward release."
	learnedLineAfter = "Learned 7 songs"
)

func viewOf(t *testing.T, h *harness, id string) View {
	t.Helper()
	view, err := h.svc.View(context.Background(), "u1", id)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

// Switching the Home's song details off removes the words the agent wrote from
// them (its summary, and the goal it suggested), for the folders of that Home and
// no other. What happened stays ("Learned 7 songs"); a goal offer still waiting
// goes back to the first look's.
func TestForgetLearned_DropsWhatWasWrittenFromTheSongs(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	view := learnThem(t, h, id)
	if view.Learning.Summary != learnedSummary || view.GoalOffer == nil || view.GoalOffer.Goal != learnedGoal {
		t.Fatalf("setup: learning %+v goal offer %+v", view.Learning, view.GoalOffer)
	}

	// Another Home's switch is not this folder's.
	if changed := h.svc.ForgetLearned("some-other-home"); changed != 0 {
		t.Fatalf("another Home's switch changed %d folders", changed)
	}
	if viewOf(t, h, id).Learning.Summary != learnedSummary {
		t.Fatal("another Home's switch removed this folder's summary")
	}

	if changed := h.svc.ForgetLearned(demoHomeID); changed != 1 {
		t.Fatalf("changed %d folders, want 1", changed)
	}
	after := viewOf(t, h, id)
	if after.Learning == nil || after.Learning.Summary != "" || after.Learning.SummaryBy != "" || after.Learning.Line != learnedLineAfter {
		t.Fatalf("learning after = %+v: the words go, what happened stays", after.Learning)
	}
	if after.GoalOffer == nil || after.GoalOffer.Goal != firstLooksGoal || after.GoalOffer.Basis != GoalBasisLook {
		t.Fatalf("goal offer after = %+v, want the first look's", after.GoalOffer)
	}
	rec, err := h.svc.Record("u1", id)
	if err != nil || rec.Learning.Goal != "" || rec.Learning.Summary != "" {
		t.Fatalf("the record still holds learned words: %+v (%v)", rec.Learning, err)
	}
	// Told again, it has nothing left to remove.
	if changed := h.svc.ForgetLearned(demoHomeID); changed != 0 {
		t.Fatalf("a second time changed %d folders", changed)
	}
	if changed := h.svc.ForgetLearned(""); changed != 0 {
		t.Fatalf("no Home changed %d folders", changed)
	}
}

// A goal the owner already accepted is theirs: the workspace keeps it.
func TestForgetLearned_AnAcceptedGoalStays(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	learnThem(t, h, id)
	if _, err := h.svc.AnswerGoal(context.Background(), "u1", id, true, nil); err != nil {
		t.Fatal(err)
	}
	h.svc.ForgetLearned(demoHomeID)
	if got := h.ws.goals[id]; got != learnedGoal {
		t.Fatalf("the owner's goal became %q", got)
	}
}

// The switch can go off while the agent is still writing. Words made from what
// the consent let Ori read are not kept past that, and the first look's goal is
// what is offered.
func TestSummary_ASwitchOffWhileTheAgentWritesKeepsNothing(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts(), offAfterFacts: true}
	h, id, turns := afterLooking(t, songs, learnedReply())
	view := learnThem(t, h, id)

	if len(turns.requests) != 2 {
		t.Fatalf("%d model calls: the turn was meant to be under way when the switch went off", len(turns.requests))
	}
	if view.Learning == nil || view.Learning.Status != LearningDone || view.Learning.Summary != "" || view.Learning.SummaryBy != "" {
		t.Fatalf("learning = %+v: the summary survived the switch going off", view.Learning)
	}
	if view.GoalOffer == nil || view.GoalOffer.Goal != firstLooksGoal || view.GoalOffer.Basis != GoalBasisLook {
		t.Fatalf("goal offer = %+v, want the first look's", view.GoalOffer)
	}
}

// The next folder added to a Home that holds the older numbers-only consent is
// offered the upgrade; a yes widens the consent and reads, a no reads nothing.
func TestSongsOffer_ANumbersOnlyHomeIsOfferedTheUpgrade(t *testing.T) {
	t.Run("yes widens the consent and reads", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOn, names: false, facts: sampleFacts()}
		h, id, _ := afterLooking(t, songs, learnedReply())
		if offer := viewOf(t, h, id).SongsOffer; offer == nil || offer.Count != 7 {
			t.Fatalf("offer = %+v, want the offer to learn 7 songs", offer)
		}
		view := learnThem(t, h, id)
		if grants, learns := songs.counts(); grants != 1 || learns != 1 {
			t.Fatalf("grants=%d learns=%d, want one each: a yes widens and reads", grants, learns)
		}
		if state, names, _ := songs.Consent(context.Background(), "u1", id, demoHomeID); state != workspace.SongDetailsOn || !names {
			t.Fatalf("consent after the yes = %q names=%v", state, names)
		}
		if view.Learning == nil || view.Learning.Status != LearningDone || view.SongsOffer != nil {
			t.Fatalf("view after = learning %+v offer %+v", view.Learning, view.SongsOffer)
		}
	})
	t.Run("not now reads nothing and widens nothing", func(t *testing.T) {
		songs := &fakeSongs{state: workspace.SongDetailsOn, names: false, facts: sampleFacts()}
		h, id, _ := afterLooking(t, songs, learnedReply())
		if _, err := h.svc.AnswerSongs(context.Background(), "u1", id, false); err != nil {
			t.Fatal(err)
		}
		h.svc.Wait()
		view := viewOf(t, h, id)
		if grants, learns := songs.counts(); grants != 0 || learns != 0 {
			t.Fatalf("grants=%d learns=%d after Not now", grants, learns)
		}
		if view.SongsOffer != nil || view.Learning != nil {
			t.Fatalf("after Not now: offer %+v learning %+v", view.SongsOffer, view.Learning)
		}
		if state, names, _ := songs.Consent(context.Background(), "u1", id, demoHomeID); state != workspace.SongDetailsOn || names {
			t.Fatalf("Not now changed the consent: %q names=%v", state, names)
		}
	})
}
