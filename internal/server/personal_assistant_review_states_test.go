package server

import (
	"net/http"
	"testing"
)

// A review pending in another conversation is neither "no review" nor
// "unsupported": the model and the drawer get the same canonical summary, and
// only the drawer gets the reference that opens that conversation.
func TestReviewContext_RealHostPendingElsewhereIsSharedByModelAndDrawer(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Add this to my workspace", "intent": "assistant_conversation",
		"context":      map[string]string{"origin": "personal_assistant_panel", "surface": "workspace", "page_path": "/workspaces/" + f.home.FolderSlug, "workspace_id": f.home.ID},
		"conversation": map[string]string{},
	})
	if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true {
		t.Fatalf("new conversation turn: %d %v", status, reply)
	}
	projection := providerReviewContext(t, f)
	if projection["status"] != "pending_elsewhere" || projection["subject"] != "Album-5 fixture" {
		t.Fatalf("model was not told about the review elsewhere: %v", projection)
	}
	drawer, _ := reply["folder_review_context"].(map[string]any)
	if drawer == nil || drawer["status"] != projection["status"] || drawer["subject"] != projection["subject"] {
		t.Fatalf("drawer and model disagree: %v vs %v", drawer, projection)
	}
	elsewhere, _ := reply["folder_review_elsewhere"].(map[string]any)
	if elsewhere == nil || elsewhere["conversation_id"] != f.conversationID {
		t.Fatalf("no way to open the owning conversation: %v", reply["folder_review_elsewhere"])
	}
	// Reload states the same thing, and nothing moved, ran or was created.
	other := reply["conversation"].(map[string]any)["id"].(string)
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+other, nil)
	if saved["folder_review_context"].(map[string]any)["status"] != "pending_elsewhere" || saved["folder_review_elsewhere"].(map[string]any)["conversation_id"] != f.conversationID {
		t.Fatalf("reload lost the review elsewhere: %v", saved)
	}
	_, owner := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if owner["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)["status"] != "pending" || owner["folder_review_elsewhere"] != nil || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("the owning review changed, or a workspace was created")
	}
}

// After an explicit confirmation the model and the card both report the
// canonical receipt, and a chat "yes" still creates nothing more.
func TestReviewContext_RealHostCompletedSetupIsReportedFromItsReceipt(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	view := history["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+f.offerID+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "album-5", "review_digest": view["review_digest"]})
	if status != http.StatusOK {
		t.Fatal(status, result)
	}
	count := len(f.builder.workspaceFileStore.CachedWorkspaces())
	request := f.turn("Yes, add this to my workspace", f.home)
	delete(request, "folder_context")
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || reply["conversation"].(map[string]any)["stored"] != true {
		t.Fatal(status, reply)
	}
	projection := providerReviewContext(t, f)
	if projection["status"] != "completed" || projection["subject"] != "Album-5 fixture" || len(projection["effects"].([]any)) == 0 {
		t.Fatalf("completed setup was not reported from its receipt: %v", projection)
	}
	if reply["folder_review_context"].(map[string]any)["status"] != "completed" || reply["folder_setup_suggestion"] != nil {
		t.Fatalf("drawer disagrees or offers setup again: %v", reply)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if saved["folder_review_context"].(map[string]any)["status"] != "completed" || len(f.builder.workspaceFileStore.CachedWorkspaces()) != count {
		t.Fatal("reload disagrees, or chat consent created another workspace")
	}
}

// Pausing the assistant withdraws the "remember this project" effect and changes
// the relationship the folder was picked under. Neither the confirmation
// prepared before the pause nor the refreshed one can set anything up until the
// folder is picked again; the card says so instead of going quiet.
func TestFolderPlacement_PausingChangesTheReviewedEffectsBeforeConfirmation(t *testing.T) {
	f := newDraftServerFixture(t)
	id, _, offer := reviewFolder(t, f, stageReviewFolder(t, f))
	read := func() map[string]any {
		t.Helper()
		_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
		return history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	}
	reviewed := read()
	if reviewed["remember"] != true {
		t.Skip("this fixture's review does not promise remembering")
	}
	version, _ := f.memoryFacts(t)
	if status, paused := f.call(t, http.MethodPost, "/api/personal-assistant/pause", map[string]any{"if_version": version}); status != http.StatusOK {
		t.Fatalf("pause: %d %v", status, paused)
	}
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	confirm := func(digest any, request string) (int, map[string]any) {
		return f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": request, "review_digest": digest})
	}
	if status, result := confirm(reviewed["review_digest"], "before-pause"); status == http.StatusOK || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatalf("a review that promised remembering was confirmed while paused: %d %v", status, result)
	}
	current := read()
	if current["remember"] != false || current["review_digest"] == reviewed["review_digest"] || current["status"] != "pending" {
		t.Fatalf("the card still promises remembering while paused: %v", current)
	}
	status, result := confirm(current["review_digest"], "after-pause")
	if status != http.StatusConflict || result["needs_pick"] != true || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatalf("folder access picked under the earlier relationship was reused: %d %v", status, result)
	}
	if facts := f.approvedFacts(t); len(facts) != 0 {
		t.Fatalf("a fact was remembered while paused: %v", facts)
	}
	_, saved := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if review := saved["folder_review_context"].(map[string]any); review["status"] != "reselection_needed" || review["control_labels"] != nil {
		t.Fatalf("the drawer does not ask for the folder again: %v", review)
	}
}
