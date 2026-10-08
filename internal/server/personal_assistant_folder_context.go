package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/session"
)

func folderConversationMessages(messages []session.Message) []agenthttp.PersonalAssistantConversationMessage {
	out := make([]agenthttp.PersonalAssistantConversationMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, agenthttp.PersonalAssistantConversationMessage{
			ID: message.ID, Role: string(message.Role), Content: message.Content,
			CreatedAt: message.CreatedAt, Imported: message.Imported, FolderContext: message.FolderContext, WorkspaceContext: message.WorkspaceContext,
		})
	}
	return out
}

func (a personalAssistantConversationAdapter) ReadFolderConversation(ctx context.Context, id string) (agenthttp.PersonalAssistantConversationRecord, []agenthttp.PersonalAssistantConversationMessage, error) {
	store, ok := a.store.(session.FolderContextStore)
	if !ok {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, foldercontext.ErrInvalid
	}
	sess, err := store.GetFolderSession(ctx, id)
	if errors.Is(err, session.ErrSessionNotFound) {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, agenthttp.ErrPersonalAssistantConversationNotFound
	}
	if err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, err
	}
	return conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess), folderConversationMessages(sess.Messages), nil
}

func (a personalAssistantConversationAdapter) AppendFolderTurn(ctx context.Context, id, workspaceID, agentName, revision string, event foldercontext.Event, user, answer string) ([]agenthttp.PersonalAssistantConversationMessage, error) {
	store, ok := a.store.(session.FolderContextStore)
	if !ok {
		return nil, foldercontext.ErrInvalid
	}
	messages, err := store.AppendFolderTurn(ctx, id, workspaceID, agentName, revision, event, user, answer)
	if errors.Is(err, session.ErrFolderContextConflict) {
		return nil, agenthttp.ErrPersonalAssistantFolderConflict
	}
	if errors.Is(err, session.ErrSessionNotFound) {
		return nil, agenthttp.ErrPersonalAssistantConversationNotFound
	}
	return folderConversationMessages(messages), err
}
