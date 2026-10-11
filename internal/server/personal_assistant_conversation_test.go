package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
)

type fixedPersonalAssistantContext struct {
	context *agenthttp.PersonalAssistantWorkContext
}

func (p fixedPersonalAssistantContext) ResolvePersonalAssistantContext(context.Context, string) (*agenthttp.PersonalAssistantWorkContext, error) {
	return p.context, nil
}

type fixedSystemModel struct{}

func (fixedSystemModel) GetSystemModel() (string, string) { return "claude_code", "sonnet" }

// conversationServerFixture runs the real ask handler over the real session
// store through the production adapter. Only the model is a stand-in.
type conversationServerFixture struct {
	handler  *agenthttp.HomeAssistantAskHandler
	sessions session.HybridStore
	provider *capturingChatProvider
	context  *agenthttp.PersonalAssistantWorkContext
	hq       string
}

func newConversationServerFixture(t *testing.T) *conversationServerFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.Open(t)
	sessions := session.NewHybridStoreWithDB(db, 10)
	t.Cleanup(func() { _ = sessions.Close() })

	hq := &session.Workspace{Name: "My HQ"}
	if err := sessions.CreateWorkspace(ctx, hq); err != nil {
		t.Fatalf("create HQ workspace: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO personal_assistant_state(user_id,assistant_id,status,hq_workspace_id,global_agent_profile_name,state_version,created_at,updated_at) VALUES('local','fixture','active',?,'Atlas',3,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, hq.ID); err != nil {
		t.Fatal(err)
	}
	provider := &capturingChatProvider{}
	factory := llm.NewFactory()
	factory.Register("claude_code", provider)
	workContext := &agenthttp.PersonalAssistantWorkContext{
		State: "active", StateVersion: 3, DisplayName: "Atlas", Role: "Personal Assistant",
		HQWorkspaceID: hq.ID, ConversationAgent: "Atlas",
	}
	handler := agenthttp.NewHomeAssistantAskHandler(agenthttp.HomeSnapshotSources{}, factory, fixedSystemModel{})
	handler.SetPersonalAssistantContextProvider(fixedPersonalAssistantContext{context: workContext}, "local")
	handler.SetConversationStore(personalAssistantConversationAdapter{store: sessions})
	return &conversationServerFixture{handler: handler, sessions: sessions, provider: provider, context: workContext, hq: hq.ID}
}

func (f *conversationServerFixture) say(prompt, conversationID string) agenthttp.HomeAssistantAskResponse {
	return f.handler.Ask(context.Background(), agenthttp.HomeAssistantAskRequest{
		Prompt: prompt, Intent: "assistant_conversation",
		Conversation: &agenthttp.HomeAssistantConversationRef{ID: conversationID},
	})
}

func TestAssistantConversationRoute_ProductionAdapterChecksCanonicalOwner(t *testing.T) {
	f := newConversationServerFixture(t)
	ctx := context.Background()
	chat := &session.Session{AgentName: "Atlas", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	ref := &agenthttp.HomeAssistantConversationRef{ID: chat.ID}
	refs := &agenthttp.HomeAssistantRouteContext{Origin: "personal_assistant_panel", WorkspaceID: "browsing-is-not-owner"}
	if err := f.handler.ValidateRouteConversation(ctx, ref, refs); err != nil {
		t.Fatal("canonical route refused", err)
	}
	// Database ownership changes cannot be concealed by a cached Session or
	// an unchanged browser relationship projection.
	if _, err := f.sessions.GetSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sessions.DB().ExecContext(ctx, `UPDATE sessions SET agent_name='Other' WHERE id=?`, chat.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.handler.ValidateRouteConversation(ctx, ref, refs); err == nil {
		t.Fatal("foreign cached reference accepted")
	}
	if err := f.sessions.DeleteSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.handler.ValidateRouteConversation(ctx, ref, refs); err == nil {
		t.Fatal("deleted reference accepted")
	}
	_, err := (personalAssistantConversationAdapter{store: f.sessions}).ReadConversationOwner(ctx, chat.ID,
		assistantcontext.SaveOwner{UserID: "local", WorkspaceID: f.hq, AgentName: "Atlas", StateVersion: 3})
	if !errors.Is(err, agenthttp.ErrPersonalAssistantConversationNotFound) {
		t.Fatal("adapter lost refusal identity", err)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("metadata route invoked provider")
	}
}

// A conversation is a canonical Session in Personal HQ: its turns are ordinary
// messages, a restart reads them back, a rename keeps them, and deleting the
// session with the existing control ends the conversation without recreating it.
func TestAssistantConversation_IsACanonicalSessionInPersonalHQ(t *testing.T) {
	ctx := context.Background()
	f := newConversationServerFixture(t)

	first := f.say("생일 축하 인사를 써줘 🎂", "")
	if first.Conversation == nil || !first.Conversation.Started || !first.Conversation.Stored {
		t.Fatalf("first turn: %+v", first.Conversation)
	}
	id := first.Conversation.ID

	stored, err := f.sessions.GetSession(ctx, id)
	if err != nil || stored.FolderID != f.hq || stored.AgentName != "Atlas" {
		t.Fatalf("session=%+v err=%v", stored, err)
	}
	messages, err := f.sessions.GetMessages(ctx, id)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if messages[0].Role != session.RoleUser || messages[0].Content != "생일 축하 인사를 써줘 🎂" || messages[0].ID != first.Conversation.UserMessageID {
		t.Fatalf("user message changed in storage: %+v", messages[0])
	}
	if messages[1].Role != session.RoleAssistant || messages[1].ID != first.Conversation.AssistantMessageID {
		t.Fatalf("assistant message: %+v", messages[1])
	}

	// The HQ's own session list shows it; no second history store exists.
	hq := f.hq
	listed, err := f.sessions.ListSessions(ctx, &session.SessionFilter{FolderID: &hq}, &session.ListOptions{Limit: 10})
	if err != nil || len(listed.Sessions) != 1 || listed.Sessions[0].ID != id {
		t.Fatalf("HQ session list=%+v err=%v", listed, err)
	}

	// "Restart": a fresh store over the same database has an empty cache.
	restarted := session.NewHybridStoreWithDB(f.sessions.DB(), 10)
	f.handler.SetConversationStore(personalAssistantConversationAdapter{store: restarted})
	second := f.say("make it warmer", id)
	if second.Conversation == nil || second.Conversation.ID != id || second.Conversation.Started || !second.Conversation.Stored {
		t.Fatalf("after restart: %+v", second.Conversation)
	}
	request := f.provider.requests[len(f.provider.requests)-1]
	if len(request.Messages) != 4 || request.Messages[1].Content != "생일 축하 인사를 써줘 🎂" {
		t.Fatalf("history after restart: %+v", request.Messages)
	}

	// A rename carries the session binding to the new profile key.
	renamer, ok := restarted.(interface {
		RenameSessionsByAgent(ctx context.Context, oldName, newName string) (int, error)
	})
	if !ok {
		t.Fatal("session store does not carry bindings through a rename")
	}
	if _, err := renamer.RenameSessionsByAgent(ctx, "Atlas", "Aria"); err != nil {
		t.Fatalf("rename sessions: %v", err)
	}
	if stale := f.say("still there?", id); stale.Conversation == nil || stale.Conversation.Error != agenthttp.PersonalAssistantConversationOutOfScope {
		t.Fatalf("old profile key kept the thread: %+v", stale.Conversation)
	}
	f.context.ConversationAgent, f.context.DisplayName, f.context.StateVersion = "Aria", "Aria", 4
	// The production rename owner updates the relationship as well as Sessions;
	// this fixture's context stand-in must reflect that canonical binding.
	if _, err := f.sessions.DB().ExecContext(ctx, `UPDATE personal_assistant_state SET global_agent_profile_name='Aria',state_version=4 WHERE user_id='local'`); err != nil {
		t.Fatal(err)
	}
	if renamed := f.say("still there?", id); renamed.Conversation == nil || renamed.Conversation.ID != id || !renamed.Conversation.Stored {
		t.Fatalf("renamed assistant lost its thread: %+v", renamed.Conversation)
	}

	// Deleting the session ends the conversation. It is not recreated.
	if err := restarted.DeleteSession(ctx, id); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	calls := len(f.provider.requests)
	gone := f.say("make it shorter", id)
	if gone.Conversation == nil || gone.Conversation.Error != agenthttp.PersonalAssistantConversationNotFound || len(f.provider.requests) != calls {
		t.Fatalf("deleted conversation: %+v calls=%d->%d", gone.Conversation, calls, len(f.provider.requests))
	}
	remaining, err := restarted.ListSessions(ctx, &session.SessionFilter{FolderID: &hq}, &session.ListOptions{Limit: 10})
	if err != nil || len(remaining.Sessions) != 0 {
		t.Fatalf("a deleted conversation came back: %+v err=%v", remaining, err)
	}
}

// A session that belongs to another workspace or another agent is never the
// assistant's conversation, even when its ID is sent explicitly.
func TestAssistantConversation_RefusesSessionsOutsideItsScope(t *testing.T) {
	ctx := context.Background()
	f := newConversationServerFixture(t)
	project := &session.Workspace{Name: "Thesis"}
	if err := f.sessions.CreateWorkspace(ctx, project); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]*session.Session{
		"project session":        {Title: "Thesis chat", AgentName: "Atlas", FolderID: project.ID},
		"other HQ agent":         {Title: "Journal", AgentName: "Journal", FolderID: f.hq},
		"unattached direct chat": {Title: "Direct", AgentName: "Atlas"},
	}
	for name, sess := range foreign {
		if err := f.sessions.CreateSession(ctx, sess); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp := f.say("make it warmer", sess.ID)
		if resp.Conversation == nil || resp.Conversation.Error != agenthttp.PersonalAssistantConversationOutOfScope {
			t.Fatalf("%s was accepted: %+v", name, resp.Conversation)
		}
		if messages, _ := f.sessions.GetMessages(ctx, sess.ID); len(messages) != 0 {
			t.Fatalf("%s was written to: %+v", name, messages)
		}
	}
	if len(f.provider.requests) != 0 {
		t.Fatalf("a refused conversation reached the model")
	}
}
