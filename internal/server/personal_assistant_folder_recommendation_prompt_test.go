package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFolderRecommendationDiscussion_PendingAlbumsUsesSameEvidenceAfterSend(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(body)
	}
	root := seedAlbumsCollectionVariant(t, true)
	hash := folderTreeHash(t, root)
	oldDraft := uuid.NewString()
	_, old := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": oldDraft, "revision": "", "mode": "chip", "chip": "music"})
	oldObservation := old["observation"].(map[string]any)
	oldChat, _, oldOffer := reviewFolder(t, f, map[string]any{"draft_id": oldDraft, "revision": "", "selection_id": oldObservation["id"], "candidate_id": "candidate-5"})
	draft := uuid.NewString()
	status, attached := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || attached["structure"] == nil || attached["blocker"] == nil || len(provider.requests) != 0 {
		t.Fatal("attach lost discovery or called model", attached)
	}
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	observation := attached["observation"].(map[string]any)
	recommendation := attached["structure"].(map[string]any)["recommendation"].(map[string]any)
	ask := map[string]any{"prompt": "Explain the setup recommendation for this folder", "intent": "assistant_conversation", "conversation": map[string]string{}, "folder_context": map[string]any{"draft_id": draft, "selection_id": observation["id"]}}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", ask)
	if status != http.StatusOK || len(provider.requests) != 1 || reply["folder_setup_suggestion"] != nil {
		t.Fatal("pending reply created executable handoff", reply)
	}
	call := provider.requests[0]
	prompt := call.Messages[len(call.Messages)-1].Content
	for _, want := range []string{"<folder_recommendation>", recommendation["evidence"].(string), `"operation":"library"`, `"supported_projects":5`, `"subject":"Album-5"`, `"blocks":true`, `<folder_setup_options>[]</folder_setup_options>`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q: %s", want, prompt)
		}
	}
	for _, secret := range []string{observation["id"].(string), oldChat, oldOffer, root} {
		if strings.Contains(prompt, secret) {
			t.Fatal("prompt leaked execution reference", secret)
		}
	}
	if !strings.Contains(call.SystemPrompt, "Recognition is not activation support") {
		t.Fatal("library reply still assumes group workspaces")
	}
	conversation := reply["conversation"].(map[string]any)
	folder := reply["folder_context"].(map[string]any)
	ask["prompt"] = "Yes, continue; focus on the Logic project"
	ask["conversation"] = map[string]any{"id": conversation["id"]}
	ask["folder_context"] = map[string]any{"selection_id": observation["id"], "revision": folder["revision"]}
	status, reply = f.call(t, http.MethodPost, "/api/home-assistant/ask", ask)
	if status != http.StatusOK || len(provider.requests) != 2 || !strings.Contains(provider.requests[1].SystemPrompt, "Treat this as a follow-up") {
		t.Fatal("followup lost context", reply)
	}
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+oldChat, nil)
	if history["folder_reviews"].(map[string]any)[oldOffer].(map[string]any)["status"] != "pending" || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || folderTreeHash(t, root) != hash {
		t.Fatal("discussion changed setup or source")
	}
}

func TestFolderRecommendationDiscussion_ModelFailureKeepsExplicitGroupEdits(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &overloadedChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(body)
	}
	seedAlbumsCollectionVariant(t, true)
	review := reviewAlbums(t, f)
	status, adjusted := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", map[string]any{"conversation_id": review.conversation, "revision": review.revision, "selection_id": review.selection, "candidate_id": "candidate-0", "structure": map[string]any{"group_type": "group", "group_name": "My explicit group", "group_name_set": true}})
	if status != http.StatusOK {
		t.Fatal(adjusted)
	}
	folder := adjusted["folder_context"].(map[string]any)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{"prompt": "Explain my chosen group", "intent": "assistant_conversation", "conversation": map[string]any{"id": review.conversation}, "folder_context": map[string]any{"selection_id": review.selection, "revision": folder["revision"]}})
	if status != http.StatusOK || reply["model_unavailable"] != true || len(provider.requests) != 1 {
		t.Fatal(reply)
	}
	structure, ok := reply["folder_structure"].(map[string]any)
	if !ok || structure["group"].(map[string]any)["type"] != "group" || structure["group"].(map[string]any)["name"] != "My explicit group" {
		t.Fatal("failure reset explicit alternative", reply)
	}
	call := provider.requests[0]
	prompt := call.Messages[len(call.Messages)-1].Content
	if !strings.Contains(prompt, `"operation":"group"`) || !strings.Contains(prompt, "My explicit group") {
		t.Fatal("model saw a different recommendation", prompt)
	}
}

func TestFolderRecommendationDiscussion_LibraryDoesNotAlsoPromiseGroupWorkspaces(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(body)
	}
	seedAlbumsCollectionVariant(t, true)
	draft := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatal(selected)
	}
	observation := selected["observation"].(map[string]any)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{"prompt": "Explain the library recommendation", "intent": "assistant_conversation", "conversation": map[string]string{}, "folder_context": map[string]any{"draft_id": draft, "selection_id": observation["id"]}})
	if status != http.StatusOK || len(provider.requests) != 1 {
		t.Fatal(reply)
	}
	call := provider.requests[0]
	prompt := call.Messages[len(call.Messages)-1].Content
	if !strings.Contains(prompt, `"operation":"library"`) || strings.Contains(prompt, "<folder_structure_proposal>") {
		t.Fatal("conflicting operation in actual prompt", prompt)
	}
}
