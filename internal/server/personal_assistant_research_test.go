package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
)

// Actual HTTP Ask, hired relationship, dedicated broker, compiled source owner,
// SQLite attributed save and reload; only provider decisions are controlled.
func TestPersonalAssistantResearchHTTPExactEditableReviewCancelAndNoHistoryGrant(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	first := f.ask(t, "", "Let's discuss the options before searching.")
	id := first["conversation"].(map[string]any)["id"].(string)
	refs := placementContext(f.project)
	before, err := f.builder.sessionStore.GetMessages(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(f.provider.requests)
	body := map[string]any{"conversation": map[string]string{"id": id}, "context": refs, "lookup": map[string]string{"operation": "skills_catalog", "query": "Telegram community management"}}
	status, prepared := f.call(t, http.MethodPost, "/api/home-assistant/research/review", body)
	if status != http.StatusOK {
		t.Fatal(status, prepared)
	}
	original := prepared["review"].(map[string]any)
	body["lookup"] = map[string]string{"operation": "skills_catalog", "query": "Telegram moderation"}
	status, edited := f.call(t, http.MethodPost, "/api/home-assistant/research/review", body)
	if status != http.StatusOK {
		t.Fatal(status, edited)
	}
	oldApproval := map[string]any{"conversation": map[string]string{"id": id}, "context": refs, "research_approval": original}
	status, rejected := f.call(t, http.MethodPost, "/api/home-assistant/ask", oldApproval)
	if status != http.StatusOK || rejected["model_unavailable"] != true || len(f.provider.requests) != calls {
		t.Fatal("replaced lookup executed", rejected)
	}
	current := edited["review"].(map[string]any)
	status, cancelled := f.call(t, http.MethodPost, "/api/home-assistant/research/cancel", map[string]any{"conversation": map[string]string{"id": id}, "context": refs, "token": current["token"]})
	if status != http.StatusOK || cancelled["cancelled"] != true || cancelled["sent"] != false {
		t.Fatal(status, cancelled)
	}
	oldApproval["research_approval"] = current
	_, rejected = f.call(t, http.MethodPost, "/api/home-assistant/ask", oldApproval)
	if rejected["model_unavailable"] != true || len(f.provider.requests) != calls {
		t.Fatal("cancelled lookup executed")
	}
	after, err := f.builder.sessionStore.GetMessages(context.Background(), id)
	if err != nil || len(after) != len(before) {
		t.Fatal("review/cancel persisted executable history", err)
	}
}

func TestPersonalAssistantResearchProductionAskReadsCitesSavesAndProposesWithoutEgress(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	f.provider.script = []llm.ChatResponse{
		readerCall("assistant_mcp_catalog", map[string]any{"query": "filesystem"}),
		readerCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "Telegram community management"}),
		{Content: "This is compiled catalog metadata, not a tested connection [S1]. Unknown grants and dependencies need review. Invented [S999]."},
	}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{"prompt": "Could existing tools help a community?", "intent": "assistant_conversation", "conversation": map[string]string{}, "context": map[string]string{"origin": "personal_assistant_panel", "page_path": "/settings"}})
	if status != http.StatusOK {
		t.Fatal(status, reply)
	}
	answer, _ := reply["response"].(string)
	if !strings.Contains(answer, "[S1]") || strings.Contains(answer, "[S999]") {
		t.Fatal("production evidence missing", reply)
	}
	current := reply["workspace_context"].(map[string]any)
	refs, _ := current["research"].([]any)
	if len(refs) == 0 || current["subject"] != nil {
		t.Fatal("app-wide evidence was workspace-only", current)
	}
	reference := refs[0].(map[string]any)
	if reference["level"] != "metadata" || reference["freshness"] != "compiled" || reference["cited"] != true {
		t.Fatal(reference)
	}
	review, _ := reply["research_review"].(map[string]any)
	if review == nil || review["token"] == "" {
		t.Fatal("saved turn did not prepare review", reply)
	}
	id := reply["conversation"].(map[string]any)["id"].(string)
	saved := reloadedConversation(t, f.draftServerFixture, id)
	messages := saved["messages"].([]any)
	for _, value := range messages {
		message := value.(map[string]any)
		text, _ := message["content"].(string)
		if strings.Contains(text, review["token"].(string)) {
			t.Fatal("approval persisted")
		}
		attribution := message["workspace_context"].(map[string]any)
		if attribution["historical"] != true || len(attribution["research"].([]any)) == 0 {
			t.Fatal("historical reference not retained")
		}
	}
	for _, request := range f.provider.requests {
		assertNoPanelExecutionAuthority(t, request)
		for _, message := range request.Messages {
			if strings.Contains(message.Content, review["token"].(string)) {
				t.Fatal("token reached model")
			}
		}
	}
	restarted := session.NewHybridStoreWithDB(f.builder.sessionStore.DB(), 10)
	canonical, err := restarted.GetSession(context.Background(), id)
	if err != nil || len(canonical.Messages) != 2 {
		t.Fatal("canonical restart lost turn", err)
	}
	for _, message := range canonical.Messages {
		data, err := assistantcontext.EncodeAttribution(message.WorkspaceContext)
		if err != nil || strings.Contains(data, "excerpt") || strings.Contains(data, "token") || strings.Contains(data, "readiness") {
			t.Fatal("executable/body metadata persisted", err)
		}
	}
}
