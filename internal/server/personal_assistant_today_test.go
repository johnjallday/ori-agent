package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
)

func TestAssistantToday_RealRouteAndAskUseHomeRecordsWithoutCallingModel(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	item, err := f.builder.followUpService.Capture(t.Context(), followup.CaptureInput{
		UserID: "local", WorkspaceID: f.hqID, Category: followup.CategoryWaitingOn,
		Title: "Reply to Mina", Detail: "Detail not for chat", Counterparty: "Counterparty not for chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, home := f.call(t, http.MethodGet, "/api/personal-assistant/today", nil)
	if status != http.StatusOK {
		t.Fatalf("Home Today: %d %v", status, home)
	}
	needs := home["today"].(map[string]any)["needs_you"].(map[string]any)
	var href any
	for _, raw := range needs["items"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == item.ID {
			href = row["route"]
		}
	}
	if href == nil {
		t.Fatal("follow-up missing from Home")
	}
	const prompt = "What needs me today?"
	for _, page := range []string{"/", "/workspaces/" + f.hqSlug} {
		refs := map[string]string{"origin": "personal_assistant_panel", "page_path": page}
		status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", map[string]any{"prompt": prompt, "context": refs})
		if status != http.StatusOK || route["intent"] != "assistant_conversation" || route["route_mode"] != "home_inline" {
			t.Fatalf("Route: %d %v", status, route)
		}
		status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
			"prompt": prompt, "intent": route["intent"], "context": refs, "conversation": map[string]string{},
		})
		conversation, _ := reply["conversation"].(map[string]any)
		answer, _ := reply["today"].(map[string]any)
		rows, _ := answer["rows"].([]any)
		if status != http.StatusOK || conversation["stored"] != true || len(rows) == 0 || len(f.provider.requests) != 0 {
			t.Fatalf("host answer: %d %v (calls: %d)", status, reply, len(f.provider.requests))
		}
		matched := false
		for _, raw := range rows {
			row := raw.(map[string]any)
			matched = matched || (row["title"] == item.Title && row["href"] == href && row["owner"] == "My HQ")
		}
		if !matched || strings.Contains(mustJSON(t, reply), "not for chat") {
			t.Fatalf("not the minimal Home record: %v", rows)
		}
		saved := reloadedConversation(t, f.draftServerFixture, conversation["id"].(string))
		if text := mustJSON(t, saved); !strings.Contains(text, item.Title) || strings.Contains(text, "today_selection") {
			t.Fatalf("history must be inert: %s", text)
		}
	}
	refs := map[string]string{"origin": "personal_assistant_panel", "page_path": "/", "subject_workspace_id": f.project.ID}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": prompt, "context": refs, "conversation": map[string]string{},
	})
	if status != http.StatusOK || reply["today"] != nil || strings.Contains(mustJSON(t, reply), item.Title) {
		t.Fatalf("pinned project widened to personal scope: %d %v", status, reply)
	}
}
