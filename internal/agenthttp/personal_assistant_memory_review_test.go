package agenthttp

import (
	"context"
	"strings"
	"testing"
)

// A6: a clear statement in a conversation opens the editable review with the
// user's own words. It writes nothing and calls no model.
func TestMemoryRequest_AStatementOpensTheEditableReview(t *testing.T) {
	f := newConversationFixture(t)
	f.store.seed("mine", "hq-owned", "nova-profile-key")
	resp := f.say("Please remember that Mina's birthday is 3 March.", "mine")
	if resp.MemoryReview == nil || resp.MemoryReview.Text != "Mina's birthday is 3 March" {
		t.Fatalf("review: %+v", resp)
	}
	if resp.MemoryReview.Destination != "Personal HQ memory" || resp.MemoryReview.MaxBytes != 500 {
		t.Fatalf("review destination/limit: %+v", resp.MemoryReview)
	}
	if resp.RequiresConfirmation || resp.Confirmation != nil || !strings.Contains(resp.Response, "Nothing is saved yet") {
		t.Fatalf("a review is not a confirmation and saves nothing: %+v", resp)
	}
	if len(f.memory.requests) != 0 || len(f.provider.requests) != 0 || f.mutator.writes != 0 || f.store.messageCount() != 0 {
		t.Fatalf("opening the review wrote memory, called the model, or stored a turn")
	}
}

// A6: "remember this" has no clear referent, so it asks instead of guessing —
// even right after a draft, which must never be remembered whole by default.
func TestMemoryRequest_BareRequestAsksInsteadOfGuessing(t *testing.T) {
	f := newConversationFixture(t)
	f.store.seed("mine", "hq-owned", "nova-profile-key",
		PersonalAssistantConversationMessage{ID: "m1", Role: "user", Content: "Write a birthday greeting for Mina, she turns 30 on 3 March"},
		PersonalAssistantConversationMessage{ID: "m2", Role: "assistant", Content: "Happy 30th birthday, Mina!"},
	)
	for _, prompt := range []string{"remember this", "Remember that.", "can you remember it please"} {
		resp := f.say(prompt, "mine")
		if resp.MemoryReview == nil || resp.MemoryReview.Text != "" {
			t.Fatalf("%q must open an empty review: %+v", prompt, resp.MemoryReview)
		}
		if !strings.Contains(resp.Response, "Tell me what to remember") {
			t.Fatalf("%q response=%q", prompt, resp.Response)
		}
	}
	if len(f.memory.requests) != 0 || len(f.provider.requests) != 0 {
		t.Fatalf("a bare request wrote memory or called the model")
	}
}

// An allowlisted global preference keeps its existing confirmation, in or out
// of a conversation. The new review does not widen or replace that path.
func TestMemoryRequest_GlobalPreferencesKeepTheirExistingConfirmation(t *testing.T) {
	f := newConversationFixture(t)
	f.store.seed("mine", "hq-owned", "nova-profile-key")
	for _, conversationID := range []string{"mine", ""} {
		resp := f.say("remember that I prefer concise responses", conversationID)
		if resp.MemoryReview != nil || !resp.RequiresConfirmation || resp.Confirmation == nil ||
			resp.Confirmation.Arguments["destination"] != "profile" || resp.Confirmation.Arguments["preference"] != "response_style" {
			t.Fatalf("conversation=%q: %+v", conversationID, resp)
		}
	}
	// Translating a draft is not a language preference and not a memory request.
	f.provider.answers = []string{"번역"}
	translate := f.say("give it to me in Korean", "mine")
	if translate.MemoryReview != nil || translate.RequiresConfirmation || len(f.memory.requests) != 0 {
		t.Fatalf("a translation was treated as memory: %+v", translate)
	}
}

// Outside a hired-assistant conversation nothing changes: the existing
// confirmation is returned for a statement, and a bare request is not memory.
func TestMemoryRequest_OutsideAConversationIsUnchanged(t *testing.T) {
	f := newConversationFixture(t, "an ordinary answer")
	statement := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "remember that my launch is Friday", Intent: "general_task"})
	if statement.MemoryReview != nil || !statement.RequiresConfirmation || statement.Confirmation.Arguments["destination"] != "personal_hq" {
		t.Fatalf("statement without a conversation: %+v", statement)
	}
	bare := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "remember this", Intent: "general_task"})
	if bare.MemoryReview != nil || bare.RequiresConfirmation || bare.Response != "an ordinary answer" {
		t.Fatalf("bare request without a conversation: %+v", bare)
	}
}

func TestMemoryRequest_RequiresAReadyRelationship(t *testing.T) {
	for _, state := range []string{"needs_hire", "needs_hq", "repair_needed"} {
		f := newConversationFixture(t)
		f.context.State = state
		resp := f.say("remember that my launch is Friday", "")
		if resp.MemoryReview != nil || resp.RequiresConfirmation || len(f.memory.requests) != 0 {
			t.Fatalf("%s offered memory: %+v", state, resp)
		}
	}
	// Paused still follows the canonical service: the review opens, and
	// whether the save is accepted is the reviewed-memory API's decision.
	f := newConversationFixture(t)
	f.context.State = "paused"
	if resp := f.say("remember that my launch is Friday", ""); resp.MemoryReview == nil {
		t.Fatalf("paused: %+v", resp)
	}
}

func TestDetectAssistantMemoryRequest(t *testing.T) {
	statements := []string{
		"remember that Mina's birthday is 3 March", "Please remember that I park on level 2",
		"can you remember that the wifi name is Ferns",
	}
	bare := []string{"remember this", "Remember it", "remember that", "please remember this for me", "remember this fact."}
	neither := []string{
		"", "remember to call Sam", "remind me tomorrow", "do you remember this draft?",
		"I remember that day", "write a note to remember that trip", "remember",
		"Happy birthday, Mina! I will always remember this.",
	}
	for _, prompt := range statements {
		if got := detectAssistantMemoryRequest(prompt); !got.memory || !got.statement {
			t.Errorf("%q should be a memory statement: %+v", prompt, got)
		}
	}
	for _, prompt := range bare {
		if got := detectAssistantMemoryRequest(prompt); !got.memory || got.statement {
			t.Errorf("%q should be a bare memory request: %+v", prompt, got)
		}
	}
	for _, prompt := range neither {
		if got := detectAssistantMemoryRequest(prompt); got.memory {
			t.Errorf("%q should not be a memory request", prompt)
		}
	}
	for prompt, want := range map[string]string{
		"remember that   Mina's  birthday is 3 March.": "Mina's birthday is 3 March",
		"Please remember that 미나 생일은 3월 3일":            "미나 생일은 3월 3일",
		"remember this": "",
		// Any whitespace after "that" is the same request: the statement is
		// never dropped because it was typed after a tab or on the next line.
		"remember that\tMina likes jasmine tea":     "Mina likes jasmine tea",
		"Remember That\nthe wifi name is Ferns.":    "the wifi name is Ferns",
		"remember  that   I park on level 2  .  ":   "I park on level 2",
		"İstanbul trip: remember that I fly at ten": "I fly at ten",
	} {
		if got := memoryStatementText(prompt); got != want {
			t.Errorf("memoryStatementText(%q) = %q; want %q", prompt, got, want)
		}
	}
}

// A7: a memory request stays with the assistant even when a word in the fact
// matches a specialist, and a workspace context still wins.
func TestRoute_MemoryRequestIsTheAssistantsOwnAction(t *testing.T) {
	handler, st := newHiredRouteHandler(t, "active")
	addHomeRouteTestAgent(t, st, "Calendar Scheduler", nil, "Manages the calendar, meetings, and schedule", []string{"calendar", "meeting"}, nil)
	resp, err := handler.RoutePrompt(context.Background(), "remember that my calendar meeting with Mina is on Fridays", homePanelRouteContext())
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	requireConversationRoute(t, resp)

	inProject, err := handler.RoutePrompt(context.Background(), "remember that the build is on Fridays",
		&HomeAssistantRouteContext{Surface: "home", PagePath: "/", WorkspaceID: "ws-project"})
	if err != nil || inProject.RouteMode != "workspace_task" {
		t.Fatalf("explicit workspace context lost: %+v err=%v", inProject, err)
	}
}
