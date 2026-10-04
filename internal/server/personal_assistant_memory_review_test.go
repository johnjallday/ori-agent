package server

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryFacts reads the canonical reviewed-memory list the Remember… review
// reads, and returns the current assistant state version with the items.
func (f *draftServerFixture) memoryFacts(t *testing.T) (stateVersion any, items []map[string]any) {
	t.Helper()
	status, body := f.call(t, http.MethodGet, "/api/personal-assistant/knowledge", nil)
	if status != http.StatusOK {
		t.Fatalf("knowledge: %d %v", status, body)
	}
	raw, _ := body["items"].([]any)
	for _, entry := range raw {
		if item, ok := entry.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return body["state_version"], items
}

func (f *draftServerFixture) approvedFacts(t *testing.T) []map[string]any {
	t.Helper()
	_, items := f.memoryFacts(t)
	var approved []map[string]any
	for _, item := range items {
		if item["state"] == "approved" {
			approved = append(approved, item)
		}
	}
	return approved
}

// workCount is everything a remembered fact must never create: HQ Tickets of
// any source and follow-ups (the reminder surface).
func (f *draftServerFixture) workCount(t *testing.T) (tickets, followUps int) {
	t.Helper()
	list, err := workspace.NewTicketService(f.builder.workspaceFileStore).List(workspace.TicketQuery{WorkspaceID: f.hqID})
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	reminders, err := f.builder.followUpService.List(context.Background(), followup.Filter{UserID: "local"})
	if err != nil {
		t.Fatalf("follow-ups: %v", err)
	}
	return len(list), len(reminders)
}

// Remember… on the production wiring: asking in a conversation only opens a
// review, the reviewed wording is saved through the existing explicit-fact
// route, a retry does not save twice, and nothing else is created or changed.
func TestMemoryReview_ProductionWiring(t *testing.T) {
	f := newDraftServerFixture(t)
	ctx := context.Background()
	const greeting = "Happy birthday, Mina! Wishing you a wonderful day."
	conversationID, _ := f.seedConversation(t, greeting)
	conversation := map[string]any{"id": conversationID}
	profileBefore := f.builder.onboardingMgr.GetUserProfile()
	ticketsBefore, followUpsBefore := f.workCount(t)
	messagesBefore, err := f.builder.sessionStore.GetMessages(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}

	// "remember this" has no fact in it: the review opens empty, and the
	// greeting the assistant wrote is not offered as one.
	status, bare := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "remember this", "intent": "assistant_conversation", "conversation": conversation,
	})
	review, _ := bare["memory_review"].(map[string]any)
	if status != http.StatusOK || review == nil || review["text"] != "" || review["max_bytes"] != float64(500) {
		t.Fatalf("bare request: %d %v", status, bare)
	}

	// A statement is offered as editable starting text, and still not saved.
	status, stated := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "remember that Mina's birthday is 3 March", "intent": "assistant_conversation", "conversation": conversation,
	})
	review, _ = stated["memory_review"].(map[string]any)
	if status != http.StatusOK || review == nil || review["text"] != "Mina's birthday is 3 March" {
		t.Fatalf("statement: %d %v", status, stated)
	}
	if got := f.approvedFacts(t); len(got) != 0 {
		t.Fatalf("opening the review remembered something: %v", got)
	}
	// Neither request is a conversation turn: nothing was stored, no model ran.
	messagesAfter, err := f.builder.sessionStore.GetMessages(ctx, conversationID)
	if err != nil || len(messagesAfter) != len(messagesBefore) {
		t.Fatalf("a memory request was stored as a turn: %d → %d (%v)", len(messagesBefore), len(messagesAfter), err)
	}

	// The user's final wording goes through the existing reviewed-memory route.
	const fact = "Mina's birthday is 3 March"
	stateVersion, _ := f.memoryFacts(t)
	save := map[string]any{"state_version": stateVersion, "request_id": "remember-fact-1", "category": "people", "text": fact}
	for attempt := 1; attempt <= 2; attempt++ {
		status, saved := f.call(t, http.MethodPost, "/api/personal-assistant/knowledge/explicit", save)
		item, _ := saved["item"].(map[string]any)
		if status != http.StatusOK || item == nil || item["text"] != fact || item["state"] != "approved" ||
			item["category"] != "people" || item["source_kind"] != "explicit" {
			t.Fatalf("save attempt %d: %d %v", attempt, status, saved)
		}
	}
	approved := f.approvedFacts(t)
	if len(approved) != 1 || approved[0]["text"] != fact {
		t.Fatalf("a retry must not remember the fact twice: %v", approved)
	}

	// A fact is not work: no Ticket, no reminder, no profile change, no turn.
	if tickets, followUps := f.workCount(t); tickets != ticketsBefore || followUps != followUpsBefore {
		t.Fatalf("remembering created work: tickets %d → %d, follow-ups %d → %d", ticketsBefore, tickets, followUpsBefore, followUps)
	}
	if after := f.builder.onboardingMgr.GetUserProfile(); !reflect.DeepEqual(profileBefore, after) {
		t.Fatalf("remembering changed the profile: %+v → %+v", profileBefore, after)
	}
	if messagesAfter, err = f.builder.sessionStore.GetMessages(ctx, conversationID); err != nil || len(messagesAfter) != len(messagesBefore) {
		t.Fatalf("remembering changed the conversation: %d → %d (%v)", len(messagesBefore), len(messagesAfter), err)
	}

	// Unsafe or over-long wording is refused whole; it is never cut to fit.
	stateVersion, _ = f.memoryFacts(t)
	for name, text := range map[string]string{
		"over 500 bytes": strings.Repeat("가", 167),
		"two lines":      "Mina's birthday\nis 3 March",
		"padded":         " Mina likes tea",
		// Not a real credential: only the shape the validator refuses.
		"secret-like": "The key is sk-" + "fictional-example-0000",
	} {
		status, refused := f.call(t, http.MethodPost, "/api/personal-assistant/knowledge/explicit", map[string]any{
			"state_version": stateVersion, "request_id": "remember-refused", "category": "people", "text": text,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("%s: %d %v", name, status, refused)
		}
	}
	if got := f.approvedFacts(t); len(got) != 1 {
		t.Fatalf("a refused save changed memory: %v", got)
	}

	// The remembered fact is forgotten through the same canonical review.
	status, forgotten := f.call(t, http.MethodPost, "/api/personal-assistant/knowledge/"+approved[0]["id"].(string)+"/forget",
		map[string]any{"version": approved[0]["version"], "request_id": "remember-forget-1"})
	if status != http.StatusOK {
		t.Fatalf("forget: %d %v", status, forgotten)
	}
	if got := f.approvedFacts(t); len(got) != 0 {
		t.Fatalf("the fact is still remembered after forget: %v", got)
	}
}

// Pausing stops the assistant's own routines and suggestions, not what the
// user tells it. The conversation rule adds nothing of its own here: the review
// still opens, and the canonical reviewed-memory route decides the save. That
// route refuses a save made against the state the browser saw before the pause
// and takes one the user makes against the current state.
func TestMemoryReview_PausedAssistantFollowsCanonicalMemory(t *testing.T) {
	f := newDraftServerFixture(t)
	conversationID, _ := f.seedConversation(t, "Happy birthday, Mina!")
	const fact = "Mina's birthday is 3 March"
	activeVersion, _ := f.memoryFacts(t)

	if status, paused := f.call(t, http.MethodPost, "/api/personal-assistant/pause", map[string]any{"if_version": activeVersion}); status != http.StatusOK {
		t.Fatalf("pause: %d %v", status, paused)
	}
	status, asked := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "remember that " + fact, "intent": "assistant_conversation",
		"conversation": map[string]any{"id": conversationID},
	})
	if review, _ := asked["memory_review"].(map[string]any); status != http.StatusOK || review == nil || review["text"] != fact {
		t.Fatalf("paused review: %d %v", status, asked)
	}
	if got := f.approvedFacts(t); len(got) != 0 {
		t.Fatalf("opening the review while paused remembered something: %v", got)
	}

	status, stale := f.call(t, http.MethodPost, "/api/personal-assistant/knowledge/explicit", map[string]any{
		"state_version": activeVersion, "request_id": "remember-paused-stale", "category": "people", "text": fact,
	})
	if status != http.StatusConflict || len(f.approvedFacts(t)) != 0 {
		t.Fatalf("a save against the pre-pause state must be refused whole: %d %v", status, stale)
	}

	pausedVersion, _ := f.memoryFacts(t)
	if pausedVersion == activeVersion {
		t.Fatalf("pausing did not move the state version: %v", pausedVersion)
	}
	status, saved := f.call(t, http.MethodPost, "/api/personal-assistant/knowledge/explicit", map[string]any{
		"state_version": pausedVersion, "request_id": "remember-paused-current", "category": "people", "text": fact,
	})
	approved := f.approvedFacts(t)
	if status != http.StatusOK || len(approved) != 1 || approved[0]["text"] != fact {
		t.Fatalf("canonical memory while paused: %d %v (approved=%v)", status, saved, approved)
	}
}
