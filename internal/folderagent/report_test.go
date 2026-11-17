package folderagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// fakeReports records what the service announces and plays the conversation.
type fakeReports struct {
	mu     sync.Mutex
	got    []Report
	latest map[string]ChatRef
	seen   []string
}

func (f *fakeReports) Reported(_ context.Context, r Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	f.latest[r.WorkspaceID] = ChatRef{SessionID: "session-1", MessageID: "message-" + r.Key, Unread: true, DeliveredAt: time.Now()}
}

func (f *fakeReports) Latest(_ context.Context, workspaceID string) (ChatRef, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref, ok := f.latest[workspaceID]
	return ref, ok
}

func (f *fakeReports) Seen(_ context.Context, workspaceID, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latest[workspaceID].MessageID != messageID {
		return ErrChatUnavailable
	}
	f.seen = append(f.seen, messageID)
	return nil
}

func TestLook_PostsItsReportToTheCommandersConversation(t *testing.T) {
	h := newHarness(t, demo(t))
	reports := &fakeReports{latest: map[string]ChatRef{}}
	h.svc.deps.Reports = reports
	view := h.start(t, "")

	if len(reports.got) != 1 {
		t.Fatalf("announced %d reports, want 1: %+v", len(reports.got), reports.got)
	}
	got := reports.got[0]
	if got.WorkspaceID != view.WorkspaceID || got.AgentName != "Ori Music Demo Manager" || got.FolderName != "Ori Music Demo" ||
		!strings.HasPrefix(got.Key, string(StateReported)+"@") {
		t.Fatalf("report = %+v", got)
	}
	// The demo folder is asked what it is first, so the message asks too: a
	// lead-in, the question once, what it found, and where to answer.
	if !strings.HasPrefix(got.Text, "I looked around Ori Music Demo. One question first:\n\nThis looks like a music production folder: 7 REAPER songs and 1 Logic Pro song. Is that right? The card offers two answers: “Yes, show me the plan” and “It's something else”.\n\n") ||
		!strings.Contains(got.Text, "\n\nI found 7 REAPER songs") ||
		!strings.HasSuffix(got.Text, "\n\nAnswer it in the card below.") ||
		strings.Count(got.Text, "This looks like a music production folder") != 1 {
		t.Fatalf("text = %q", got.Text)
	}

	// Every view says where the report is, so the drawer can open it.
	report, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Chat == nil || report.Chat.MessageID != "message-"+got.Key || !report.Chat.Unread {
		t.Fatalf("chat = %+v", report.Chat)
	}
	// While the question is asked the drawer shows the lead-in and the card, so
	// the view says the lead-in, and it is the stored message's first paragraph.
	if report.Chat.Lead != "I looked around Ori Music Demo. One question first:" ||
		!strings.HasPrefix(got.Text, report.Chat.Lead+"\n\n") {
		t.Fatalf("lead = %q, text = %q", report.Chat.Lead, got.Text)
	}
	// Answered, the question's message describes a card that has no buttons any
	// more, so the drawer shows what the look found (the stored text, with its
	// question and "answer it in the card below", stays for the agent).
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	answered, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if answered.Chat == nil || !strings.HasPrefix(answered.Chat.Lead, "I looked around Ori Music Demo. I found 7 REAPER songs") ||
		strings.Contains(answered.Chat.Lead, "One question first") || strings.Contains(answered.Chat.Lead, "Answer it in the card") ||
		strings.Contains(answered.Chat.Lead, "Is that right") {
		t.Fatalf("lead after the answer = %+v", answered.Chat)
	}

	// Seen only by its owner, and only for its own message.
	if err := h.svc.ChatSeen(context.Background(), "someone-else", view.WorkspaceID, report.Chat.MessageID); err == nil {
		t.Fatal("another user marked the report seen")
	}
	if err := h.svc.ChatSeen(context.Background(), "u1", view.WorkspaceID, "other"); !errors.Is(err, ErrChatUnavailable) {
		t.Fatalf("another message: %v", err)
	}
	if err := h.svc.ChatSeen(context.Background(), "u1", view.WorkspaceID, report.Chat.MessageID); err != nil {
		t.Fatal(err)
	}
}

// A look after the owner's answer reports a new message, posted after the
// answer: it is not the question's message, so it is drawn as stored.
func TestChat_ALaterLooksMessageIsDrawnAsStored(t *testing.T) {
	h := newHarness(t, demo(t))
	h.svc.deps.Reports = &fakeReports{latest: map[string]ChatRef{}}
	view := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	answered, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil || answered.Chat == nil || answered.Chat.Lead == "" {
		t.Fatalf("the answered question's message should show its opening: %+v %v", answered.Chat, err)
	}
	// Posted a moment later, so it is after the answer whatever the clock's grain.
	time.Sleep(2 * time.Millisecond)
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	later, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if later.Chat == nil || later.Chat.MessageID == answered.Chat.MessageID || later.Chat.Lead != "" {
		t.Fatalf("a later look's message must be drawn as stored: %+v (the answered one was %s)", later.Chat, answered.Chat.MessageID)
	}
}

// "It's something else" answers the question too: the message that asked it
// describes a card with no question buttons any more ("Nothing is installed or
// created. Tell the agent what it is"), so the drawer shows what the look found,
// the same as after "Yes". The stored text keeps the question for the agent.
func TestChat_AWithdrawnQuestionIsDrawnAsItsOpeningToo(t *testing.T) {
	h := newHarness(t, demo(t))
	h.svc.deps.Reports = &fakeReports{latest: map[string]ChatRef{}}
	view := h.start(t, "")
	asked, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil || asked.Chat == nil || asked.Chat.Lead != "I looked around Ori Music Demo. One question first:" {
		t.Fatalf("while asked: %+v %v", asked.Chat, err)
	}
	withdrawn, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseSomethingElse)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Premise == nil || !withdrawn.Premise.Withdrawn {
		t.Fatalf("the question should be withdrawn: %+v", withdrawn.Premise)
	}
	got, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chat == nil || !strings.HasPrefix(got.Chat.Lead, "I looked around Ori Music Demo.") ||
		strings.Contains(got.Chat.Lead, "One question first") || strings.Contains(got.Chat.Lead, "Answer it in the card") ||
		strings.Contains(got.Chat.Lead, "Is that right") {
		t.Fatalf("lead after something else = %+v", got.Chat)
	}
}

func TestLook_WithoutReportsPostsNothingAndViewsHaveNoChat(t *testing.T) {
	h := newHarness(t, demo(t))
	view := h.start(t, "")
	report, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Chat != nil {
		t.Fatalf("chat without Reports = %+v", report.Chat)
	}
	if err := h.svc.ChatSeen(context.Background(), "u1", view.WorkspaceID, "m"); !errors.Is(err, ErrChatUnavailable) {
		t.Fatalf("seen without Reports: %v", err)
	}
}

// The drawer shows ReportLead in place of the stored question message, so the
// owner reads the question once (the card carries it) while the agent still
// reads the whole stored text. The lead-in is the stored message's first
// paragraph, and exists only for the message that asks the question.
func TestReportLead_IsTheFirstParagraphOfTheQuestionAndNothingElse(t *testing.T) {
	asked := View{FolderName: "Albums", State: StateReported,
		Premise: &PremiseView{State: PremiseAsked, Sentence: "This looks like a music folder."}}
	lead := ReportLead(asked)
	if lead != "I looked around Albums. One question first:" {
		t.Fatalf("lead = %q", lead)
	}
	text := ReportText(asked)
	if !strings.HasPrefix(text, lead+"\n\n") || strings.Count(text, "This looks like a music folder.") != 1 {
		t.Fatalf("the stored text must open with the lead-in and say the question once: %q", text)
	}
	// With nothing found to add, it is lead-in, question, where to answer. With no
	// answer labels on the view it still asks the question.
	if text != lead+"\n\nThis looks like a music folder. Is that right?\n\nAnswer it in the card below." {
		t.Fatalf("text = %q", text)
	}
	// The agent reads this stored text on its next turn and never sees the card,
	// so it must hold the question (the sentence is a statement) and both answers:
	// a real turn once answered "the question itself never made it into the message".
	full := ReportText(View{FolderName: "Albums", State: StateReported,
		Premise: &PremiseView{State: PremiseAsked, Sentence: "This looks like a music folder.", Confirm: "Yes, show me the plan", Other: "It's something else"}})
	for _, want := range []string{"This looks like a music folder.", "Is that right?", "“Yes, show me the plan”", "“It's something else”"} {
		if !strings.Contains(full, want) {
			t.Fatalf("the stored question lacks %q: %q", want, full)
		}
	}
	// The folder falls back to the workspace's name.
	if got := ReportLead(View{WorkspaceName: "Albums 2", State: StateReported,
		Premise: &PremiseView{State: PremiseAsked, Sentence: "x"}}); got != "I looked around Albums 2. One question first:" {
		t.Fatalf("lead for a workspace name = %q", got)
	}
	// A name from disk is plain text in the lead-in too.
	if got := ReportLead(View{FolderName: "<b>Albums</b>", State: StateReported,
		Premise: &PremiseView{State: PremiseAsked, Sentence: "x"}}); got != "I looked around <b>Albums</b>. One question first:" {
		t.Fatalf("lead for a markup name = %q", got)
	}

	for name, v := range map[string]View{
		"a plan":              {FolderName: "Albums", State: StateReported, Plan: &PlanView{}},
		"a failure":           {FolderName: "Albums", State: StateFailed, Premise: &PremiseView{State: PremiseAsked, Sentence: "x"}},
		"an answered premise": {FolderName: "Albums", State: StateReported, Premise: &PremiseView{State: PremiseConfirmed, Sentence: "x"}},
		"something else":      {FolderName: "Albums", State: StateReported, Premise: &PremiseView{State: PremiseSomethingElse, Sentence: "x"}},
		"an empty sentence":   {FolderName: "Albums", State: StateReported, Premise: &PremiseView{State: PremiseAsked, Sentence: "  "}},
		"no premise":          {FolderName: "Albums", State: StateKept},
	} {
		if got := ReportLead(v); got != "" {
			t.Fatalf("%s: lead = %q, want none", name, got)
		}
	}
	// Whatever the lead-in, a failed look's message is not the question.
	if text := ReportText(View{FolderName: "Albums", State: StateFailed, Failure: "Ori could not list this folder.",
		Premise: &PremiseView{State: PremiseAsked, Sentence: "x"}}); strings.Contains(text, "One question first") {
		t.Fatalf("a failure asks no question: %q", text)
	}
}

// premiseRecord is a record the premise is asked of (a recognised music
// portfolio), carrying the owner's answer when one is given.
func premiseRecord(answer *PremiseAnswer) *Record {
	return &Record{AsksPremise: true, Map: &foldermap.Map{}, Premise: answer,
		Facts: foldermap.Facts{Portfolio: &foldermap.Portfolio{HomeProviderKey: "music_project_management"}}}
}

// The question's own message is the one posted before the owner's answer. A
// later look's report is posted after it, a record with no answer has none, and
// a message with no known time is never claimed.
func TestAnsweredQuestionMessage_IsThePostedBeforeTheAnswer(t *testing.T) {
	answeredAt := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	rec := premiseRecord(&PremiseAnswer{Answer: PremiseConfirmed, Provider: "music_project_management", At: answeredAt})
	at := func(d time.Duration) *ChatRef { return &ChatRef{DeliveredAt: answeredAt.Add(d)} }
	for name, tc := range map[string]struct {
		rec  *Record
		ref  *ChatRef
		want bool
	}{
		"posted before the answer (the question's message)": {rec, at(-time.Second), true},
		"posted at the very moment of the answer":           {rec, at(0), true},
		"posted after the answer (a later look)":            {rec, at(time.Second), false},
		"no known time":                                     {rec, &ChatRef{}, false},
		"no reference":                                      {rec, nil, false},
		"no answer yet":                                     {premiseRecord(nil), at(-time.Second), false},
		"an answer for another kind":                        {premiseRecord(&PremiseAnswer{Answer: PremiseConfirmed, Provider: "manuscript_home", At: answeredAt}), at(-time.Second), false},
	} {
		if got := answeredQuestionMessage(tc.rec, tc.ref); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// ReportOpening is the plain first words of a report; a plan's message starts
// with it, and the drawer shows it for the question's message once answered.
func TestReportOpening_NamesTheFolderAndWhatItFound(t *testing.T) {
	for name, tc := range map[string]struct {
		v    View
		want string
	}{
		"the agent's own words": {View{FolderName: "Albums", WhatItIs: "A folder of six songs.", FactsLine: "6 songs"}, "I looked around Albums. A folder of six songs."},
		"the host's facts":      {View{FolderName: "Albums", FactsLine: "6 songs"}, "I looked around Albums. I found 6 songs."},
		"nothing found":         {View{WorkspaceName: "Albums 2"}, "I looked around Albums 2."},
	} {
		if got := ReportOpening(tc.v); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestReportText_SaysWhatItFoundSuggestsAndWhereToChoose(t *testing.T) {
	plan := &PlanView{Steps: []folderlook.Step{
		{Title: "Organize loose files in Downloads (you approve every move)", Chosen: true},
		{Title: "Make a group", Chosen: false},
	}}
	text := ReportText(View{FolderName: "Downloads", State: StateReported, FactsLine: "205 loose files of 21 kinds", Plan: plan,
		ModelSentence: "Downloads Manager could not look itself: No API key is set. This is what Ori found."})
	// The card under the message carries the cause sentence; the message does not repeat it.
	want := "I looked around Downloads. I found 205 loose files of 21 kinds.\n\n" +
		"I suggest one step: Organize loose files in Downloads (you approve every move).\n\n" +
		"Choose Set it up, Keep the folder as it is, or Look further below."
	if text != want {
		t.Fatalf("text =\n%s\nwant\n%s", text, want)
	}

	two := ReportText(View{FolderName: "Albums", State: StateReported, WhatItIs: "It holds five albums of REAPER songs.",
		SuggestedGoal: "Release the album", Plan: &PlanView{Steps: []folderlook.Step{{Title: "Make Albums a Home", Chosen: true}, {Title: "Add REAPER tools", Chosen: true}}}})
	for _, part := range []string{"I looked around Albums. It holds five albums of REAPER songs.", "I suggest two steps: Make Albums a Home; Add REAPER tools.", "A goal I'd work toward: Release the album"} {
		if !strings.Contains(two, part) {
			t.Fatalf("missing %q in %q", part, two)
		}
	}

	asked := ReportText(View{FolderName: "Albums", State: StateReported, WhatItIs: "A folder of six songs.",
		Premise: &PremiseView{State: PremiseAsked, Sentence: "This looks like a music folder."}, Plan: plan})
	if asked != "I looked around Albums. One question first:\n\nThis looks like a music folder. Is that right?\n\nA folder of six songs.\n\nAnswer it in the card below." ||
		strings.Contains(asked, "I suggest") {
		t.Fatalf("a premise waits before the plan: %q", asked)
	}

	failed := ReportText(View{FolderName: "Downloads", State: StateFailed, Failure: "Ori could not list this folder."})
	if failed != "I couldn't finish looking around Downloads. Ori could not list this folder.\n\nPress Look further below to try again." {
		t.Fatalf("failed = %q", failed)
	}
}
