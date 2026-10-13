package server

import (
	"net/http"
	"testing"
)

func TestHomeFolderCollection_AdjustThenSetUpCreatesTheRenamedGroup(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	root := seedAlbumsCollection(t)
	hash := folderTreeHash(t, root)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())

	// Home's Explore a folder.
	status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	offer := body["offer"].(map[string]any)
	id := offer["id"].(string)
	offers := "/api/personal-assistant/folder-digest/offers/" + id

	// A bare yes is not how a group is confirmed: it is confirmed on its plan.
	if status, body := f.call(t, http.MethodPost, offers+"/decide",
		map[string]any{"decision": "yes", "choice": "project", "create": true, "request_id": "bare-yes"}); status != http.StatusConflict {
		t.Fatalf("a bare yes on a proposed group: %d %v", status, body)
	}

	// An adjustment the proposal cannot take says which part, and saves nothing.
	status, body = f.call(t, http.MethodPost, offers+"/structure",
		map[string]any{"structure": map[string]any{"members": []any{map[string]any{"candidate_id": "not-a-member", "included": true}}}})
	if message, _ := body["message"].(string); status != http.StatusBadRequest || message != "A folder is not part of this proposal. Nothing was saved" {
		t.Fatalf("an unknown folder was accepted: %d %v", status, body)
	}

	// Adjust: rename the group. The card's plan is the adjusted one at once.
	status, body = f.call(t, http.MethodPost, offers+"/structure",
		map[string]any{"request_id": "rename", "structure": map[string]any{"group_name": "Studio"}})
	if status != http.StatusOK {
		t.Fatalf("adjust: %d %v", status, body)
	}
	adjusted := body["offer"].(map[string]any)
	if lines := planLineNames(adjusted); lines[0] != "group: Creates group Studio" ||
		adjusted["plan"].(map[string]any)["digest"] == offer["plan"].(map[string]any)["digest"] {
		t.Fatalf("the adjusted offer kept the first plan: %v", lines)
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("adjusting created a workspace")
	}

	resolved := setUpAndWait(t, f, id, adjusted, "home-set-up")
	outcome := resolved["outcome"].(map[string]any)
	group, err := files.Get(outcome["workspace_id"].(string))
	if err != nil || group.Kind != "group" || group.Name != "Studio" || outcome["kind"] != "group" {
		t.Fatalf("group: %+v, %v; outcome %v", group, err, outcome)
	}
	children := 0
	for _, ws := range files.CachedWorkspaces() {
		if ws != nil && ws.ParentID == group.ID {
			children++
		}
	}
	if children != 6 || len(outcome["receipt"].([]any)) != 7 || len(files.CachedWorkspaces()) != before+7 {
		t.Fatalf("%d children, receipt %v, %d workspaces (was %d)", children, outcome["receipt"], len(files.CachedWorkspaces()), before)
	}
	if folderTreeHash(t, root) != hash || len(provider.requests) != 0 {
		t.Fatalf("the folder changed or a model was called (%d calls)", len(provider.requests))
	}
	// A finished offer takes no more adjusting.
	if status, body := f.call(t, http.MethodPost, offers+"/structure",
		map[string]any{"structure": map[string]any{"group_name": "Again"}}); status != http.StatusConflict {
		t.Fatalf("a resolved offer was adjusted: %d %v", status, body)
	}
}
