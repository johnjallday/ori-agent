package folderagent

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The demo kept a summary that stopped mid-word ("…Drop and Bre") and offered the
// first look's goal, because the reply was cut at the token limit before its goal
// line. Such a reply is never shown as the agent's words: the host writes its own
// line from the ranges and the offer keeps the first look's goal.
func TestSummary_AReplyCutAtTheTokenLimitLeavesTheHostsLineNotHalfASummary(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	cut := &llm.ChatResponse{Content: "Summary: Seven songs. Night Drive has markers, and Glass Hour has Drop and Bre", FinishReason: "max_tokens"}
	h, id, _ := afterLooking(t, songs, cut)
	view := learnThem(t, h, id)

	if view.Learning == nil || view.Learning.Status != LearningDone || view.Learning.Summary != "" || view.Learning.SummaryBy != "" {
		t.Fatalf("learning = %+v: half a summary was kept as the agent's words", view.Learning)
	}
	if !strings.HasPrefix(view.Learning.Line, "Learned 7 songs: ") || !strings.Contains(view.Learning.Line, "BPM") {
		t.Fatalf("line = %q, want the host's own line with the ranges", view.Learning.Line)
	}
	if view.GoalOffer == nil || view.GoalOffer.Goal != firstLooksGoal || view.GoalOffer.Basis != GoalBasisLook {
		t.Fatalf("goal offer = %+v, want the first look's", view.GoalOffer)
	}
}

// A summary that ends mid-sentence but whose reply stopped on its own is dropped by
// the parser instead (a model that simply trailed off), with its goal kept.
func TestSummary_ASummaryThatTrailsOffIsDroppedAndItsGoalKept(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	trailing := &llm.ChatResponse{Content: "Summary: Seven songs, Glass Hour has Dr\nGoal: " + learnedGoal, FinishReason: "end_turn"}
	h, id, _ := afterLooking(t, songs, trailing)
	view := learnThem(t, h, id)

	// Nothing is shown as the agent's words; the host's ranges are folded into the line.
	if view.Learning.Summary != "" || view.Learning.SummaryBy != "" {
		t.Fatalf("learning = %+v", view.Learning)
	}
	if view.GoalOffer == nil || view.GoalOffer.Goal != learnedGoal || view.GoalOffer.Basis != GoalBasisLearned {
		t.Fatalf("goal offer = %+v: the goal that survived is the one offered", view.GoalOffer)
	}
}
