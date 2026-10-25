package server

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestFolderRecommendationRefresh_PendingOwnerAndCloseKeepTheHeldSelection(t *testing.T) {
	f := newDraftServerFixture(t)
	root := seedAlbumsCollection(t)
	hash := folderTreeHash(t, root)
	old := reviewAlbums(t, f)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	draft := uuid.NewString()
	status, attached := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || attached["structure"] == nil {
		t.Fatalf("attach: %d %v", status, attached)
	}
	blocker, _ := attached["blocker"].(map[string]any)
	if blocker["conversation_id"] != old.conversation || blocker["subject"] != "Music" || blocker["blocks"] != true {
		t.Fatalf("owner: %v", blocker)
	}
	selection := attached["observation"].(map[string]any)["id"]
	refresh := func(owner string) (int, map[string]any) {
		return f.call(t, http.MethodPost, "/api/home-assistant/folder-context/recommendation", map[string]any{"draft_id": owner, "revision": "", "selection_id": selection})
	}
	status, got := refresh(draft)
	if status != http.StatusOK || got["observation_id"] != selection || got["structure_unavailable"] != "review_pending" || got["structure"] == nil {
		t.Fatalf("refresh: %d %v", status, got)
	}
	// A foreign draft cannot recover the selection or learn its blocker.
	if status, got := refresh(uuid.NewString()); status == http.StatusOK || got["blocker"] != nil {
		t.Fatalf("foreign selection: %d %v", status, got)
	}
	// Refresh cannot dismiss the review. Only its existing canonical Close does.
	if old.read(t, f)["status"] != "pending" {
		t.Fatal("read dismissed the pending offer")
	}
	status, closed := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": old.conversation, "revision": old.revision, "offer_id": old.offer})
	if status != http.StatusOK {
		t.Fatalf("close: %d %v", status, closed)
	}
	status, got = refresh(draft)
	if status != http.StatusOK || got["observation_id"] != selection || got["blocker"] != nil || got["structure_unavailable"] != "" || got["structure"] == nil {
		t.Fatalf("return: %d %v", status, got)
	}
	if folderTreeHash(t, root) != hash || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("refresh or navigation produced effects")
	}
}

func TestFolderRecommendationRefresh_RejectsStaleRevisionAndForgedNavigation(t *testing.T) {
	f := newDraftServerFixture(t)
	seedAlbumsCollection(t)
	review := reviewAlbums(t, f)
	base := map[string]any{"conversation_id": review.conversation, "revision": review.revision, "selection_id": review.selection}
	for _, mode := range []string{"revision", "selection", "extra"} {
		req := map[string]any{}
		for key, value := range base {
			req[key] = value
		}
		switch mode {
		case "revision":
			req["revision"] = "stale"
		case "selection":
			req["selection_id"] = uuid.NewString()
		case "extra":
			req["blocker"] = map[string]any{"conversation_id": "foreign"}
		}
		status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/recommendation", req)
		if status == http.StatusOK || body["structure"] != nil || body["blocker"] != nil {
			t.Fatalf("%s: %d %v", mode, status, body)
		}
	}
}
