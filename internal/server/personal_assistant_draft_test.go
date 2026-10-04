package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// draftServerFixture is the production-wired server with a hired assistant and
// a Personal HQ, built through the real routes.
type draftServerFixture struct {
	builder *ServerBuilder
	handler http.Handler
	hqID    string
	hqSlug  string
}

func (f *draftServerFixture) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	var decoded map[string]any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: decode %v (%s)", method, path, err, recorder.Body.String())
		}
	}
	return recorder.Code, decoded
}

func newDraftServerFixture(t *testing.T) *draftServerFixture {
	t.Helper()
	builder, handler := newDailyBriefTestServer(t)
	f := &draftServerFixture{builder: builder, handler: handler}
	if status, body := f.call(t, http.MethodPost, "/api/settings/workspace-root", map[string]string{"workspace_root": ""}); status != http.StatusOK {
		t.Fatalf("workspace root: %d %v", status, body)
	}
	status, hired := f.call(t, http.MethodPost, "/api/personal-assistant/hire", map[string]any{
		"request_id": "draft-hire", "if_version": 0, "display_name": "Atlas", "mandate": "Help me plan.", "focus_areas": []string{"plan_my_day"},
	})
	if status != http.StatusCreated {
		t.Fatalf("hire: %d %v", status, hired)
	}
	version := hired["personal_assistant"].(map[string]any)["state_version"]
	if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/hq", map[string]any{
		"request_id": "draft-hq", "if_version": version, "name": "My HQ", "timezone": "UTC",
	}); status != http.StatusCreated {
		t.Fatalf("build HQ: %d %v", status, body)
	}
	projection, err := builder.personalAssistantService.Get(context.Background(), "local")
	if err != nil || projection.HQWorkspaceID == "" {
		t.Fatalf("relationship: %+v %v", projection, err)
	}
	ws, err := builder.workspaceFileStore.Get(projection.HQWorkspaceID)
	if err != nil {
		t.Fatalf("HQ workspace: %v", err)
	}
	f.hqID, f.hqSlug = ws.ID, ws.FolderSlug
	return f
}

// seedConversation stores a conversation the way an answered turn does: a
// canonical Session in HQ bound to the hired profile.
func (f *draftServerFixture) seedConversation(t *testing.T, reply string) (conversationID, messageID string) {
	t.Helper()
	ctx := context.Background()
	projection, err := f.builder.personalAssistantService.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Title: "Birthday greeting", AgentName: projection.GlobalAgentProfile, FolderID: f.hqID}
	if err := f.builder.sessionStore.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := f.builder.sessionStore.AddMessage(ctx, sess.ID, &session.Message{Role: session.RoleUser, Content: "Write a greeting"}); err != nil {
		t.Fatal(err)
	}
	message := &session.Message{Role: session.RoleAssistant, Content: reply}
	if err := f.builder.sessionStore.AddMessage(ctx, sess.ID, message); err != nil {
		t.Fatal(err)
	}
	return sess.ID, message.ID
}

func (f *draftServerFixture) hqTickets(t *testing.T) []workspace.Ticket {
	t.Helper()
	page, err := workspace.NewTicketService(f.builder.workspaceFileStore).Search(workspace.TicketQuery{
		WorkspaceID: f.hqID, Sources: []string{workspace.TicketSourceAssistant}, Archive: workspace.TicketArchiveAll,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return page.Tickets
}

// The whole path on the production wiring: the conversation is listed and read
// through the validated routes, the review writes nothing, the save creates one
// canonical Backlog Ticket in the real HQ, and a retry returns the same one.
func TestDraftSave_ProductionWiring(t *testing.T) {
	f := newDraftServerFixture(t)
	const draft = "미나야, 생일 정말 축하해! 🎂\n오늘 하루가 기쁨으로 가득하길 바라."
	conversationID, messageID := f.seedConversation(t, draft)

	status, listed := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	conversations, _ := listed["conversations"].([]any)
	if status != http.StatusOK || len(conversations) != 1 || conversations[0].(map[string]any)["id"] != conversationID {
		t.Fatalf("list: %d %v", status, listed)
	}
	if listed["manage_href"] != "/workspaces/"+f.hqSlug {
		t.Fatalf("manage_href=%v", listed["manage_href"])
	}
	status, read := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversationID, nil)
	if messages, _ := read["messages"].([]any); status != http.StatusOK || len(messages) != 2 || messages[1].(map[string]any)["id"] != messageID {
		t.Fatalf("read: %d %v", status, read)
	}

	status, reviewed := f.call(t, http.MethodPost, "/api/home-assistant/drafts/review", map[string]string{
		"conversation_id": conversationID, "message_id": messageID,
	})
	review, _ := reviewed["review"].(map[string]any)
	if status != http.StatusOK || review == nil || review["body"] != draft {
		t.Fatalf("review: %d %v", status, reviewed)
	}
	target := review["target"].(map[string]any)
	if target["workspace_id"] != f.hqID || target["name"] != "My HQ" {
		t.Fatalf("review target: %v", target)
	}
	if got := f.hqTickets(t); len(got) != 0 {
		t.Fatalf("a review wrote %d ticket(s)", len(got))
	}

	save := map[string]any{
		"operation_id": review["operation_id"], "title": "Birthday greeting for Mina", "body": review["body"],
		"target_workspace_id": target["workspace_id"], "source": review["source"],
	}
	status, saved := f.call(t, http.MethodPost, "/api/home-assistant/drafts/save", save)
	receipt, _ := saved["receipt"].(map[string]any)
	if status != http.StatusOK || receipt == nil || receipt["created"] != true {
		t.Fatalf("save: %d %v", status, saved)
	}
	tickets := f.hqTickets(t)
	if len(tickets) != 1 || tickets[0].Description != draft || tickets[0].Title != "Birthday greeting for Mina" {
		t.Fatalf("tickets: %+v", tickets)
	}
	ticket := tickets[0]
	if ticket.State != workspace.TicketStateBacklog || ticket.Assignee != "" || ticket.ScheduleEnabled || ticket.DueDate != nil {
		t.Fatalf("saved draft must be unassigned, unscheduled Backlog: %+v", ticket)
	}
	if receipt["ticket_id"] != ticket.ID || receipt["href"] != "/workspaces/"+f.hqSlug+"?ticket="+ticket.ID {
		t.Fatalf("receipt: %v", receipt)
	}

	// The canonical Ticket route serves the same record with the exact body.
	status, canonical := f.call(t, http.MethodGet, "/api/workspaces/"+f.hqID+"/tickets/"+ticket.ID, nil)
	if status != http.StatusOK || !strings.Contains(mustJSON(t, canonical), "생일 정말 축하해") {
		t.Fatalf("canonical ticket read: %d %v", status, canonical)
	}

	// Retry: same Ticket, nothing new.
	status, again := f.call(t, http.MethodPost, "/api/home-assistant/drafts/save", save)
	replay, _ := again["receipt"].(map[string]any)
	if status != http.StatusOK || replay["created"] != false || replay["ticket_id"] != ticket.ID || len(f.hqTickets(t)) != 1 {
		t.Fatalf("retry: %d %v", status, again)
	}

	// The HQ's BACKLOG.md was rendered with the new item, like any other capture.
	folder, err := f.builder.workspaceFileStore.GetFolderPath(f.hqID)
	if err != nil {
		t.Fatalf("HQ folder: %v", err)
	}
	rendered, err := os.ReadFile(filepath.Join(folder, "BACKLOG.md"))
	if err != nil || !strings.Contains(string(rendered), "Birthday greeting for Mina") {
		t.Fatalf("BACKLOG.md was not rendered with the saved draft (err=%v):\n%s", err, rendered)
	}

	// No personal memory, no extra workspace, no agent came from saving a draft.
	if status, knowledge := f.call(t, http.MethodGet, "/api/personal-assistant/knowledge", nil); status == http.StatusOK && strings.Contains(mustJSON(t, knowledge), "생일") {
		t.Fatalf("saving a draft wrote memory: %v", knowledge)
	}
}

// Resume and update on the production wiring: the conversation read lists the
// saved item, the update review writes nothing, the update changes the same
// canonical Ticket, an outside edit makes the next update stale, and deleting
// the chat leaves the Ticket in place without recreating the chat.
func TestDraftResumeAndUpdate_ProductionWiring(t *testing.T) {
	f := newDraftServerFixture(t)
	ctx := context.Background()
	const first = "Hi Jun, thank you for watering my plants while I was away."
	const revised = "Jun — thank you for watering my plants! 🌱"
	conversationID, messageID := f.seedConversation(t, first)

	_, reviewed := f.call(t, http.MethodPost, "/api/home-assistant/drafts/review", map[string]string{"conversation_id": conversationID, "message_id": messageID})
	review := reviewed["review"].(map[string]any)
	status, saved := f.call(t, http.MethodPost, "/api/home-assistant/drafts/save", map[string]any{
		"operation_id": review["operation_id"], "title": "Thank-you note for Jun", "body": review["body"],
		"target_workspace_id": f.hqID, "source": review["source"],
	})
	if status != http.StatusOK {
		t.Fatalf("save: %d %v", status, saved)
	}
	ticketID := saved["receipt"].(map[string]any)["ticket_id"].(string)
	draftPath := "/api/home-assistant/drafts/" + ticketID

	// A later reply in the same conversation is not part of the saved item.
	revision := &session.Message{Role: session.RoleAssistant, Content: revised}
	if err := f.builder.sessionStore.AddMessage(ctx, conversationID, revision); err != nil {
		t.Fatal(err)
	}
	status, read := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversationID, nil)
	items, _ := read["saved"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Fatalf("conversation read: %d %v", status, read)
	}
	if item := items[0].(map[string]any); item["ticket_id"] != ticketID || item["message_id"] != messageID || item["newer_replies"] != float64(1) || item["matches_source"] != true {
		t.Fatalf("saved item labels: %v", item)
	}

	status, body := f.call(t, http.MethodPost, draftPath+"/review", map[string]string{"conversation_id": conversationID, "message_id": revision.ID})
	update, _ := body["update"].(map[string]any)
	if status != http.StatusOK || update["body"] != revised || update["current"].(map[string]any)["body"] != first {
		t.Fatalf("update review: %d %v", status, body)
	}
	version := update["current"].(map[string]any)["version"]

	status, body = f.call(t, http.MethodPost, draftPath+"/update", map[string]any{
		"if_version": version, "title": update["title"], "body": revised, "target_workspace_id": f.hqID,
	})
	if receipt, _ := body["receipt"].(map[string]any); status != http.StatusOK || receipt["applied"] != true || receipt["ticket_id"] != ticketID {
		t.Fatalf("update: %d %v", status, body)
	}
	tickets := f.hqTickets(t)
	if len(tickets) != 1 || tickets[0].ID != ticketID || tickets[0].Description != revised || tickets[0].State != workspace.TicketStateBacklog || tickets[0].Assignee != "" {
		t.Fatalf("after update: %+v", tickets)
	}

	// An edit through the canonical Ticket route, then an update reviewed
	// against the old version: refused, with the current text returned.
	status, patched := f.call(t, http.MethodPatch, "/api/workspaces/"+f.hqID+"/tickets/"+ticketID, map[string]any{
		"description": "Edited in Personal HQ", "version": tickets[0].Version,
	})
	if status != http.StatusOK {
		t.Fatalf("outside edit: %d %v", status, patched)
	}
	status, body = f.call(t, http.MethodPost, draftPath+"/update", map[string]any{
		"if_version": tickets[0].Version, "title": update["title"], "body": first, "target_workspace_id": f.hqID,
	})
	current, _ := body["current"].(map[string]any)
	if status != http.StatusConflict || body["error"] != agenthttp.PersonalAssistantSavedDraftChanged || current["body"] != "Edited in Personal HQ" {
		t.Fatalf("stale update: %d %v", status, body)
	}
	if after := f.hqTickets(t); len(after) != 1 || after[0].Description != "Edited in Personal HQ" {
		t.Fatalf("a stale update overwrote the edit: %+v", after)
	}

	// Delete the chat with the existing session route. The Ticket stays, the
	// chat is reported gone, and nothing recreates it.
	if status, _ := f.call(t, http.MethodDelete, "/api/sessions/"+conversationID, nil); status != http.StatusNoContent {
		t.Fatalf("delete session: %d", status)
	}
	status, body = f.call(t, http.MethodGet, draftPath, nil)
	conversation, _ := body["conversation"].(map[string]any)
	if status != http.StatusOK || body["draft"].(map[string]any)["body"] != "Edited in Personal HQ" || conversation["available"] != false || conversation["reason"] != agenthttp.PersonalAssistantConversationNotFound {
		t.Fatalf("saved draft after chat deletion: %d %v", status, body)
	}
	if _, err := f.builder.sessionStore.GetSession(ctx, conversationID); err == nil {
		t.Fatal("reading the saved draft recreated its deleted conversation")
	}
	if status, listed := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil); status != http.StatusOK || len(listed["conversations"].([]any)) != 0 {
		t.Fatalf("conversation list after deletion: %d %v", status, listed)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
