package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/session"
)

// personalAssistantConversationStore is the part of the canonical session
// store a hired-assistant conversation uses.
type personalAssistantConversationStore interface {
	CreateSession(ctx context.Context, session *session.Session) error
	GetSession(ctx context.Context, id string) (*session.Session, error)
	AddMessage(ctx context.Context, sessionID string, message *session.Message) error
	GetMessages(ctx context.Context, sessionID string) ([]session.Message, error)
	ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) (*session.ListResult, error)
	DeleteSession(ctx context.Context, id string) error
}

// personalAssistantConversationAdapter presents canonical Sessions as the
// hired assistant's conversations. It holds no state of its own: a
// conversation is a Session, a turn is a Message, and deleting the session
// with the existing controls deletes the conversation. Scope checks live in
// agenthttp, which decides the owner from the relationship on every request.
type personalAssistantConversationAdapter struct {
	store personalAssistantConversationStore
}

func conversationRecord(id, workspaceID, agentName, title string, count int, sess *session.Session) agenthttp.PersonalAssistantConversationRecord {
	record := agenthttp.PersonalAssistantConversationRecord{
		ID: id, WorkspaceID: workspaceID, AgentName: agentName, Title: title, MessageCount: count,
	}
	if sess != nil {
		record.UpdatedAt = sess.UpdatedAt
	}
	return record
}

func (a personalAssistantConversationAdapter) Create(ctx context.Context, workspaceID, agentName, title string) (agenthttp.PersonalAssistantConversationRecord, error) {
	sess := &session.Session{Title: title, AgentName: agentName, FolderID: workspaceID}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, err
	}
	return conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess), nil
}

func (a personalAssistantConversationAdapter) Get(ctx context.Context, id string) (agenthttp.PersonalAssistantConversationRecord, error) {
	sess, err := a.store.GetSession(ctx, id)
	if errors.Is(err, session.ErrSessionNotFound) || (err == nil && sess == nil) {
		return agenthttp.PersonalAssistantConversationRecord{}, agenthttp.ErrPersonalAssistantConversationNotFound
	}
	if err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, err
	}
	return conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess), nil
}

func (a personalAssistantConversationAdapter) Messages(ctx context.Context, id string) ([]agenthttp.PersonalAssistantConversationMessage, error) {
	messages, err := a.store.GetMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return folderConversationMessages(messages), nil
}

func (a personalAssistantConversationAdapter) Append(ctx context.Context, id, role, content string) (agenthttp.PersonalAssistantConversationMessage, error) {
	message := &session.Message{Role: session.MessageRole(role), Content: content}
	if err := a.store.AddMessage(ctx, id, message); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return agenthttp.PersonalAssistantConversationMessage{}, agenthttp.ErrPersonalAssistantConversationNotFound
		}
		return agenthttp.PersonalAssistantConversationMessage{}, err
	}
	return agenthttp.PersonalAssistantConversationMessage{
		ID: message.ID, Role: role, Content: content, CreatedAt: message.CreatedAt,
	}, nil
}

func (a personalAssistantConversationAdapter) AppendAttributedTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, event *foldercontext.Event, revision, user, answer string, attribution *assistantcontext.Attribution) ([]agenthttp.PersonalAssistantConversationMessage, error) {
	store, ok := a.store.(session.AssistantTurnStore)
	if !ok {
		return nil, errors.New("canonical attributed turn writer unavailable")
	}
	messages, err := store.AppendAttributedTurn(ctx, id, owner, event, revision, user, answer, attribution)
	if errors.Is(err, session.ErrFolderContextConflict) {
		return nil, agenthttp.ErrPersonalAssistantFolderConflict
	}
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil, agenthttp.ErrPersonalAssistantConversationNotFound
	}
	return folderConversationMessages(messages), err
}

// Discard deletes a session that Create just made and whose first turn could
// not be stored, so a failed first turn leaves no empty conversation behind.
func (a personalAssistantConversationAdapter) Discard(ctx context.Context, id string) error {
	return a.store.DeleteSession(ctx, id)
}

func (a personalAssistantConversationAdapter) List(ctx context.Context, workspaceID, agentName string, limit int) ([]agenthttp.PersonalAssistantConversationRecord, error) {
	result, err := a.store.ListSessions(ctx,
		&session.SessionFilter{AgentName: agentName, FolderID: &workspaceID},
		&session.ListOptions{Limit: limit, Sort: session.SortByUpdatedDesc})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	out := make([]agenthttp.PersonalAssistantConversationRecord, 0, len(result.Sessions))
	for _, item := range result.Sessions {
		record := conversationRecord(item.ID, item.FolderID, item.AgentName, item.Title, item.MessageCount, nil)
		record.UpdatedAt = item.UpdatedAt
		out = append(out, record)
	}
	return out, nil
}
