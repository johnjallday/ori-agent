package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func providerReviewContext(t *testing.T, f *workspaceAwarenessFixture) map[string]any {
	t.Helper()
	call := f.provider.requests[len(f.provider.requests)-1]
	assertNoPanelExecutionAuthority(t, call)
	user := call.Messages[len(call.Messages)-1].Content
	_, after, found := strings.Cut(user, "<folder_review_context>")
	data, _, closed := strings.Cut(after, "</folder_review_context>")
	if !found || !closed {
		t.Fatal("review not delivered to actual provider")
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{f.offerID, f.sentinel, f.source} {
		if strings.Contains(user, forbidden) {
			t.Fatal("review projection leaked source/authority")
		}
	}
	return result
}

func TestReviewContext_RealHostPlainConversationAndDetach(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	request := f.turn("Explain the existing setup review", f.other)
	delete(request, "folder_context")
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || body["conversation"].(map[string]any)["stored"] != true {
		t.Fatal(status, body)
	}
	projection := providerReviewContext(t, f)
	if projection["status"] != "awaiting_confirmation" || projection["subject"] != "Album-5 fixture" || projection["control_labels"] == nil {
		t.Fatal(projection)
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if status != http.StatusOK || history["folder_review_context"].(map[string]any)["status"] != projection["status"] {
		t.Fatal("UI/model status disagreed", status, history)
	}
	status, detached := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": f.conversationID, "revision": f.revision})
	if status != http.StatusOK {
		t.Fatal(status, detached)
	}
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || body["conversation"].(map[string]any)["stored"] != true {
		t.Fatal(status, body)
	}
	projection = providerReviewContext(t, f)
	if projection["historical"] != true || projection["control_labels"] != nil || projection["status"] != "reselection_needed" {
		t.Fatal("detach reactivated review", projection)
	}
}

func TestReviewContext_RealHostClosedReviewRemainsExplainable(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": f.conversationID, "revision": f.revision, "offer_id": f.offerID})
	if status != http.StatusOK {
		t.Fatal(status, body)
	}
	request := f.turn("Explain the previous setup review", f.other)
	delete(request, "folder_context")
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || body["conversation"].(map[string]any)["stored"] != true {
		t.Fatal(status, body)
	}
	projection := providerReviewContext(t, f)
	if projection["status"] != "closed" || projection["historical"] != true || projection["control_labels"] != nil {
		t.Fatal(projection)
	}
}
