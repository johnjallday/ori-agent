package server

import (
	"net/http"
	"testing"
)

func TestFolderReviewHandoff_ContinueOnlyFocusesExistingCanonicalReview(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	status, result := f.call(t, http.MethodPost, "/api/home-assistant/ask", f.turn("Continue with the reviewed setup", f.project))
	if status != http.StatusOK {
		t.Fatal(status, result)
	}
	handoff, ok := result["folder_setup_suggestion"].(map[string]any)
	if !ok || handoff["offer_id"] != f.offerID || handoff["conversation_id"] != f.conversationID || handoff["options"] != nil {
		t.Fatal("missing canonical focus handoff", result)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if saved["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)["status"] != "pending" || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("chat consent executed setup")
	}
	if saved["folder_setup_suggestion"].(map[string]any)["offer_id"] != f.offerID {
		t.Fatal("focus lost on hydration")
	}
}
