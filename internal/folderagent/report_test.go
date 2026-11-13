package folderagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
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
	f.latest[r.WorkspaceID] = ChatRef{SessionID: "session-1", MessageID: "message-" + r.Key, Unread: true}
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
	// The demo folder is asked what it is first, so the message asks too.
	if !strings.HasPrefix(got.Text, "I looked around Ori Music Demo. I found 7 REAPER songs") ||
		!strings.Contains(got.Text, "One question first: This looks like a music production folder") ||
		!strings.HasSuffix(got.Text, "Answer it in the card below.") {
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

func TestReportText_SaysWhatItFoundSuggestsAndWhereToChoose(t *testing.T) {
	plan := &PlanView{Steps: []folderlook.Step{
		{Title: "Add File Janitor to Downloads", Chosen: true},
		{Title: "Make a group", Chosen: false},
	}}
	text := ReportText(View{FolderName: "Downloads", State: StateReported, FactsLine: "205 loose files of 21 kinds", Plan: plan,
		ModelSentence: "Downloads Manager could not look itself: No API key is set. This is what Ori found."})
	// The card under the message carries the cause sentence; the message does not repeat it.
	want := "I looked around Downloads. I found 205 loose files of 21 kinds.\n\n" +
		"I suggest one step: Add File Janitor to Downloads.\n\n" +
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

	asked := ReportText(View{FolderName: "Albums", State: StateReported, Premise: &PremiseView{State: PremiseAsked, Sentence: "This looks like a music folder."}, Plan: plan})
	if !strings.Contains(asked, "One question first: This looks like a music folder.") || strings.Contains(asked, "I suggest") {
		t.Fatalf("a premise waits before the plan: %q", asked)
	}

	failed := ReportText(View{FolderName: "Downloads", State: StateFailed, Failure: "Ori could not list this folder."})
	if failed != "I couldn't finish looking around Downloads. Ori could not list this folder.\n\nPress Look further below to try again." {
		t.Fatalf("failed = %q", failed)
	}
}
