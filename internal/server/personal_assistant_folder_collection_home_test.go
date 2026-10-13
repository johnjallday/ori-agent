package server

import (
	"net/http"
	"testing"
)

// Choosing the Home in Adjust hands the offer back to the earlier collection
// setup, with that setup's own steps and refusals. It is no longer a proposed
// group, so its yes is the journey's, and nothing is created by choosing it.
func TestHomeFolderCollection_ChoosingTheHomeIsTheEarlierCollectionSetup(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	files := f.builder.workspaceFileStore
	before := len(files.CachedWorkspaces())

	status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	id := body["offer"].(map[string]any)["id"].(string)
	offers := "/api/personal-assistant/folder-digest/offers/" + id

	status, body = f.call(t, http.MethodPost, offers+"/structure",
		map[string]any{"structure": map[string]any{"group_type": "library"}})
	if status != http.StatusOK {
		t.Fatalf("choosing the Home: %d %v", status, body)
	}
	listed := body["offer"].(map[string]any)
	group := listed["structure"].(map[string]any)["group"].(map[string]any)
	if group["type"] != "library" || group["name"] != "Music Production Home" || listed["portfolio"] == nil || listed["capability"] == nil {
		t.Fatalf("the offer is not the collection setup: group %v, portfolio %v, capability %v", group, listed["portfolio"], listed["capability"])
	}

	// The collection card's Set up begins with the journey's yes, which a
	// proposed group refuses and this offer takes. The provider step that
	// follows asks the release registry, so it is left to the browser suite.
	status, body = f.call(t, http.MethodPost, offers+"/decide",
		map[string]any{"decision": "yes", "choice": "project", "request_id": "collection-yes"})
	if status != http.StatusOK || body["offer"].(map[string]any)["status"] != "awaiting_outcome" {
		t.Fatalf("the collection's yes: %d %v", status, body)
	}
	if len(files.CachedWorkspaces()) != before {
		t.Fatal("choosing the Home created a workspace")
	}
}

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
