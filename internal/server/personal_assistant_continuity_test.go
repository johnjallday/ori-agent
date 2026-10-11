package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
)

func TestAssistantContinuityFailedOrToolCallingRecapKeepsAnswerAndWithholdsSecrets(t *testing.T) {
	for _, mode := range []string{"malformed", "oversized", "tool_call", "invented"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkspaceReaderFixture(t)
			first := f.ask(t, "", "My goal is membership, not coaching.")
			id := first["conversation"].(map[string]any)["id"].(string)
			secret := "PRIVATE_RECAP_SENTINEL api_key=sk-test-123456789abcdefghijklmnop"
			if err := f.builder.sessionStore.AddMessage(t.Context(), id, &session.Message{Role: session.RoleUser, Content: secret}); err != nil {
				t.Fatal(err)
			}
			for range 50 {
				if err := f.builder.sessionStore.AddMessage(t.Context(), id, &session.Message{Role: session.RoleUser, Content: strings.Repeat("Unrelated recipes. ", 140)}); err != nil {
					t.Fatal(err)
				}
			}
			bad := llm.ChatResponse{Content: "not JSON"}
			switch mode {
			case "oversized":
				bad.Content = `{"version":1,"items":[],"reason":"` + strings.Repeat("x", 16000) + `"}`
			case "tool_call":
				bad = readerCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "never authorized"})
			case "invented":
				bad.Content = `{"version":1,"items":[{"kind":"user_goal","message_id":"foreign","quote":"My goal is coaching."}]}`
			}
			before := len(f.provider.requests)
			f.provider.script = append(make([]llm.ChatResponse, before), bad, llm.ChatResponse{Content: "We can keep discussing; no setup ran."})
			reply := f.ask(t, id, "Let's discuss the community options.")
			if reply["model_unavailable"] == true || reply["research_review"] != nil || len(f.provider.requests) != before+2 {
				t.Fatal("recap broke answer or executed a tool", reply)
			}
			status := reply["conversation"].(map[string]any)
			if status["stored"] != true || status["continuity"].(map[string]any)["recap_unavailable"] != true {
				t.Fatal("dishonest failure state", reply)
			}
			if strings.Contains(mustJSON(t, f.provider.requests[before].Messages), "PRIVATE_RECAP_SENTINEL") {
				t.Fatal("credential chunk reached summary input")
			}
			if len(f.provider.requests[before].Tools) != 0 {
				t.Fatal("summary acquired tools")
			}
			var count int
			if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_conversation_checkpoints WHERE session_id=?`, id).Scan(&count); err != nil || count != 0 {
				t.Fatal("bad recap persisted", count, err)
			}
		})
	}
}

func TestAssistantContinuityProductionProviderInputIsGroundedBoundedAndToolFree(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	first := f.ask(t, "", "My goal is community membership. No, I do not want to develop anyone's talent.")
	id := first["conversation"].(map[string]any)["id"].(string)
	messages, err := f.builder.sessionStore.GetMessages(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	initialID := messages[0].ID
	for i := range 90 {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		body := strings.Repeat("A separate topic about gardening and recipes. ", 45)
		if i == 88 {
			body = "No, I want recurring membership only, not one-off sales or talent coaching."
		}
		if i == 89 {
			body = "One-off sales was an earlier tentative suggestion, not the user's chosen plan."
		}
		if err := f.builder.sessionStore.AddMessage(t.Context(), id, &session.Message{Role: role, Content: body}); err != nil {
			t.Fatal(err)
		}
	}
	recap := assistantcontext.ConversationRecap{Version: 1, Items: []assistantcontext.RecapItem{
		{Kind: "user_goal", MessageID: initialID, Quote: "My goal is community membership."},
		{Kind: "user_correction", MessageID: initialID, Quote: "No, I do not want to develop anyone's talent."},
	}}
	data, _ := json.Marshal(recap)
	before := len(f.provider.requests)
	f.provider.script = append(make([]llm.ChatResponse, before), llm.ChatResponse{Content: string(data)}, llm.ChatResponse{Content: "Compare membership options; this is advice, not setup."})
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{"prompt": "Return to community membership options.", "intent": "assistant_conversation", "conversation": map[string]string{"id": id}, "context": map[string]string{"origin": "personal_assistant_panel", "page_path": "/settings"}})
	if status != http.StatusOK || reply["model_unavailable"] == true {
		t.Fatal(status, reply)
	}
	if len(f.provider.requests) != before+2 {
		t.Fatal("unbounded model calls", len(f.provider.requests)-before)
	}
	summary, answer := f.provider.requests[before], f.provider.requests[before+1]
	if len(summary.Tools) != 0 || len(summary.MCPServers) != 0 || summary.WorkspaceID != "" || summary.Model != "sonnet" || summary.MaxTokens != 1800 || !strings.Contains(summary.Messages[0].Content, "Select a compact conversation recap") {
		t.Fatal("summary escalated or changed model", summary)
	}
	assertNoPanelExecutionAuthority(t, summary)
	assertNoPanelExecutionAuthority(t, answer)
	input := mustJSON(t, answer.Messages)
	if !strings.Contains(input, "No, I do not want to develop anyone's talent.") || !strings.Contains(input, "user_correction") || !strings.Contains(input, "not current instructions") || !strings.Contains(input, "Latest exact user corrections") || !strings.Contains(input, "No, I want recurring membership only, not one-off sales or talent coaching.") {
		t.Fatal("provider omitted grounded correction", input)
	}
	historyRunes := 0
	for _, message := range answer.Messages[1 : len(answer.Messages)-1] {
		historyRunes += utf8.RuneCountInString(message.Content)
		if message.Role == llm.RoleSystem {
			t.Fatal("recap promoted into system instructions")
		}
	}
	if historyRunes > 24000 {
		t.Fatal("history budget", historyRunes)
	}
	continuity := reply["conversation"].(map[string]any)["continuity"].(map[string]any)
	if continuity["recap_used"] != true || continuity["older_omitted"] != true {
		t.Fatal("dishonest continuity state", continuity)
	}
	// Restart the actual source owner: derived data is canonical, never an LRU
	// transcript or a second history store. Imported/edited data invalidates it.
	restarted := session.NewHybridStoreWithDB(f.builder.sessionStore.DB(), 10)
	owner := assistantcontext.SaveOwner{UserID: "local"}
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT hq_workspace_id,global_agent_profile_name,state_version FROM personal_assistant_state WHERE user_id='local'`).Scan(&owner.WorkspaceID, &owner.AgentName, &owner.StateVersion); err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.(session.AssistantConversationContextStore).ReadAssistantConversationContext(t.Context(), id, owner)
	if err != nil || loaded.Recap == nil || len(loaded.Recap.Items) != 2 {
		t.Fatal("restart lost recap", err)
	}
	var count int
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM assistant_conversation_checkpoints WHERE session_id=?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	before = len(f.provider.requests)
	newReply := f.ask(t, "", "A new discussion about cooking.")
	if len(f.provider.requests) != before+1 || newReply["conversation"].(map[string]any)["id"] == id || strings.Contains(mustJSON(t, f.provider.requests[before].Messages), "conversation_reference") {
		t.Fatal("new conversation inherited context")
	}
}
