package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
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
	for _, required := range []string{"about 80 words", "not a repeated inventory", "optional background", "Do not prefer a child", "no earlier locally saved answered turn"} {
		if !strings.Contains(call.Messages[0].Content, required) {
			t.Fatalf("missing conversational presentation guidance %q", required)
		}
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

// A No thanks on a collection does not take its group out of the conversation:
// the first turn on it still tells the model what Ori proposed.
func TestAssistantFolderSuggestion_ADeclinedCollectionIsStillInTheFirstPrompt(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
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
	draft := "6f1d3c2a-8b7e-4d5f-9a1b-2c3d4e5f6a7b"
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || selected["structure"] == nil || selected["declined"] == nil {
		t.Fatalf("select: %d %v", status, selected)
	}
	observation := selected["observation"].(map[string]any)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Explore this folder", "intent": "assistant_conversation", "conversation": map[string]string{},
		"folder_context": map[string]any{"draft_id": draft, "selection_id": observation["id"]},
	})
	if status != http.StatusOK || len(provider.requests) != 1 {
		t.Fatalf("ask: %d %v (%d model calls)", status, reply, len(provider.requests))
	}
	var sent strings.Builder
	for _, message := range provider.requests[0].Messages {
		sent.WriteString(message.Content)
	}
	if !strings.Contains(sent.String(), "folder_structure_proposal") {
		t.Fatalf("the declined collection's group was not in the prompt:\n%s", sent.String())
	}
}

// overloadedChatProvider is a system model with no capacity: every turn fails
// the way an overloaded provider reports it.
type overloadedChatProvider struct{ capturingChatProvider }

func (p *overloadedChatProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	return nil, llm.NewProviderError("claude_code", llm.CategoryProviderOverloaded, errors.New("server_overloaded: Selected model is at capacity"))
}

// A model at capacity costs the user one click, not the proposal: the failed
// turn says why in a safe category and carries the same group the attach did.
func TestAssistantFolderSuggestion_AModelAtCapacityKeepsTheProposal(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &overloadedChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	seedAlbumsCollection(t)
	draft := "7a2e4d3b-9c8f-4e6a-8b1c-3d4e5f6a7b8c"
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select",
		map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK || selected["structure"] == nil {
		t.Fatalf("select: %d %v", status, selected)
	}
	observation := selected["observation"].(map[string]any)
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "Propose workspaces for this folder", "intent": "assistant_conversation", "conversation": map[string]string{},
		"folder_context": map[string]any{"draft_id": draft, "selection_id": observation["id"]},
	})
	structure, _ := reply["folder_structure"].(map[string]any)
	if status != http.StatusOK || reply["model_unavailable"] != true || reply["failure_reason"] != "provider_overloaded" ||
		structure == nil || len(structure["members"].([]any)) != 6 || len(provider.requests) != 1 {
		t.Fatalf("overloaded turn: %d %v (%d model calls)", status, reply, len(provider.requests))
	}
	if mustJSON(t, structure) != mustJSON(t, selected["structure"]) {
		t.Fatalf("the failed turn's proposal differs from the attach:\n%s\n%s", mustJSON(t, structure), mustJSON(t, selected["structure"]))
	}
	if strings.Contains(mustJSON(t, reply), "Selected model is at capacity") {
		t.Fatal("the raw provider error reached the browser")
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
