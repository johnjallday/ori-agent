package server

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
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

// One No thanks on the group is about that folder, and it is shown wherever
// the folder is shown again: the composer's attach and Home's scan both still
// propose the group, with when the user said no. Only a review waiting
// elsewhere keeps the composer's card away, and the response says so.
func TestHomeFolderCollection_ADeclinedFolderIsStillProposedAndSaysWhen(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)

	status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("scan: %d %v", status, body)
	}
	id := body["offer"].(map[string]any)["id"].(string)
	if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+id+"/decide",
		map[string]any{"decision": "no", "request_id": "no-thanks"}); status != http.StatusOK {
		t.Fatalf("No thanks: %d %v", status, body)
	}

	status, body = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	declined, _ := body["declined"].(map[string]any)
	if status != http.StatusOK || body["structure"] == nil || declined == nil || declined["name"] != "Music" ||
		len(declined["folder_key"].(string)) != 64 || declined["at"] == nil || body["structure_unavailable"] != nil {
		t.Fatalf("attach after No thanks: %d %v", status, body)
	}

	status, body = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	offer, _ := body["offer"].(map[string]any)
	again, _ := offer["declined"].(map[string]any)
	if status != http.StatusOK || offer["structure"] == nil || again == nil || again["folder_key"] != declined["folder_key"] {
		t.Fatalf("Home scan after No thanks: %d %v", status, offer)
	}

	// That scan's offer is now waiting on Home: the composer says so in one line.
	status, body = f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || body["structure"] == nil || body["blocker"] == nil || body["structure_unavailable"] != "review_pending" {
		t.Fatalf("attach while a review waits on Home: %d %v", status, body)
	}
}

// Ask again sends back only the key the decline carried, and the next attach
// is a folder nobody said no to.
func TestHomeFolderCollection_AskAgainUndoesTheNoThanks(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	_, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/scan", map[string]any{"chip": "music"})
	id := body["offer"].(map[string]any)["id"].(string)
	if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+id+"/decide",
		map[string]any{"decision": "no", "request_id": "no-thanks"}); status != http.StatusOK {
		t.Fatalf("No thanks: %d %v", status, body)
	}
	attach := func() map[string]any {
		t.Helper()
		status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
			map[string]any{"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": "music"})
		if status != http.StatusOK || body["structure"] == nil {
			t.Fatalf("attach: %d %v", status, body)
		}
		return body
	}
	declined := attach()["declined"].(map[string]any)
	// A path is never accepted in its place.
	if status, _ := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/forget",
		map[string]any{"folder_key": "/Users/riley/Music"}); status != http.StatusBadRequest {
		t.Fatalf("a path as the key: %d", status)
	}
	if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/forget",
		map[string]any{"folder_key": declined["folder_key"]}); status != http.StatusOK || body["folder_digest"] == nil {
		t.Fatalf("Ask again: %d %v", status, body)
	}
	if again := attach(); again["declined"] != nil {
		t.Fatalf("still declined after Ask again: %v", again["declined"])
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

	// The default library listing is distinct from this explicit generic setup.
	status, body = f.call(t, http.MethodPost, offers+"/structure", map[string]any{"structure": map[string]any{"group_type": "group"}})
	if status != http.StatusOK {
		t.Fatalf("choose group: %d %v", status, body)
	}
	offer = body["offer"].(map[string]any)

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
	if children != 6 || len(collectionReceiptRows(outcome, "workspace")) != 7 || len(files.CachedWorkspaces()) != before+7 {
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
