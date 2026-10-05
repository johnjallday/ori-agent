package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestAssistantFolderSuggestion_RealSendHydrationAndExplicitReview(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	selected := stageReviewFolder(t, f)
	ask := map[string]any{
		"prompt": "Explore this folder", "intent": "assistant_conversation",
		"context": map[string]string{"origin": "personal_assistant_panel"}, "conversation": map[string]string{},
		"folder_context": map[string]any{"draft_id": selected["draft_id"], "selection_id": selected["selection_id"]},
	}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", ask)
	suggestion, ok := reply["folder_setup_suggestion"].(map[string]any)
	if status != http.StatusOK || !ok {
		t.Fatalf("missing handoff: %d %v", status, reply)
	}
	conversation := reply["conversation"].(map[string]any)
	folder := reply["folder_context"].(map[string]any)
	id := conversation["id"].(string)
	if suggestion["conversation_id"] != id || suggestion["message_id"] != conversation["assistant_message_id"] || suggestion["revision"] != folder["revision"] || suggestion["observation_id"] != selected["selection_id"] {
		t.Fatal("unbound handoff", suggestion)
	}
	if len(provider.requests) != 1 {
		t.Fatal("wrong model count")
	}
	call := provider.requests[0]
	prompt := call.Messages[len(call.Messages)-1].Content
	for _, required := range []string{"<folder_setup_options>", "Chosen", "Blank workspace"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("missing %s: %s", required, prompt)
		}
	}
	if strings.Contains(prompt, selected["selection_id"].(string)) || strings.Contains(prompt, selected["candidate_id"].(string)) {
		t.Fatal("opaque reference sent to model")
	}
	if !strings.Contains(call.Messages[0].Content, "Explore first") {
		t.Fatal("no recommendation guidance")
	}
	for range 2 {
		status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
		if status != http.StatusOK || mustJSON(t, history["folder_setup_suggestion"]) != mustJSON(t, suggestion) {
			t.Fatal("handoff not restored", history)
		}
	}
	_, digest := f.call(t, http.MethodGet, "/api/personal-assistant/folder-digest", nil)
	if digest["folder_digest"].(map[string]any)["offer"] != nil || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(provider.requests) != 1 {
		t.Fatal("Send or hydration created setup", digest)
	}
	// Conversational assent remains a model turn, never consent to create.
	ask["prompt"] = "Yes, that sounds useful"
	ask["conversation"] = map[string]string{"id": id}
	ask["folder_context"] = map[string]any{"selection_id": selected["selection_id"], "revision": folder["revision"]}
	_, reply = f.call(t, http.MethodPost, "/api/home-assistant/ask", ask)
	if reply["folder_setup_suggestion"] == nil || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("yes executed or lost review", reply)
	}
	folder = reply["folder_context"].(map[string]any)
	selected["conversation_id"], selected["revision"] = id, folder["revision"]
	delete(selected, "draft_id")
	_, _, offer := reviewFolder(t, f, selected)
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if history["folder_setup_suggestion"] != nil || history["folder_reviews"].(map[string]any)[offer] == nil || len(provider.requests) != 2 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("review bypassed canonical flow", history)
	}
}

func TestAssistantFolderSuggestion_ContentsAndFailedSaveDoNotOfferHandoff(t *testing.T) {
	for _, mode := range []string{"contents", "save-failure", "model-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			provider := &capturingChatProvider{}
			f.builder.llmFactory.Register("claude_code", provider)
			if mode != "model-unavailable" {
				status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"})
				if status != http.StatusOK {
					t.Fatal(status, body)
				}
			}
			selected := stageReviewFolder(t, f)
			prompt := "Explore this folder"
			if mode == "contents" {
				prompt = "Summarize these documents"
			}
			if mode == "save-failure" {
				if _, err := f.builder.sessionStore.DB().ExecContext(context.Background(), `CREATE TRIGGER fail_suggestion_reply BEFORE INSERT ON messages WHEN NEW.role='assistant' BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{"prompt": prompt, "intent": "assistant_conversation", "conversation": map[string]string{}, "folder_context": map[string]any{"draft_id": selected["draft_id"], "selection_id": selected["selection_id"]}})
			if reply["folder_setup_suggestion"] != nil {
				t.Fatal("unsuitable turn offered setup", reply)
			}
			if mode == "contents" {
				id := reply["conversation"].(map[string]any)["id"].(string)
				_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
				if history["folder_setup_suggestion"] != nil || len(provider.requests) != 0 {
					t.Fatal("contents refusal became setup on hydration", history)
				}
			}
		})
	}
}
