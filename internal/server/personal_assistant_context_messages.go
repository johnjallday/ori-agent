package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/session"
)

func (a personalAssistantConversationAdapter) ReadConversationDisplay(ctx context.Context, id string, owner assistantcontext.SaveOwner) (agenthttp.PersonalAssistantConversationRecord, []agenthttp.PersonalAssistantConversationMessage, bool, error) {
	store, ok := a.store.(session.AssistantConversationDisplayStore)
	if !ok {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, false, errors.New("canonical bounded conversation display unavailable")
	}
	sess, omitted, err := store.ReadAssistantConversationDisplay(ctx, id, owner)
	if err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, false, continuityStoreError(err)
	}
	return conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess), folderConversationMessages(sess.Messages), omitted, nil
}

func (a personalAssistantConversationAdapter) ReadCanonicalMessage(ctx context.Context, id string, owner assistantcontext.SaveOwner, messageID string) (agenthttp.PersonalAssistantConversationRecord, agenthttp.PersonalAssistantConversationMessage, error) {
	store, ok := a.store.(session.AssistantContextMessageStore)
	if !ok {
		return agenthttp.PersonalAssistantConversationRecord{}, agenthttp.PersonalAssistantConversationMessage{}, errors.New("canonical exact message unavailable")
	}
	sess, msg, err := store.ReadAssistantContextMessage(ctx, id, owner, messageID)
	if err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, agenthttp.PersonalAssistantConversationMessage{}, continuityStoreError(err)
	}
	record := conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess)
	if msg == nil {
		return record, agenthttp.PersonalAssistantConversationMessage{}, nil
	}
	return record, folderConversationMessages([]session.Message{*msg})[0], nil
}

func (a personalAssistantConversationAdapter) ReadCanonicalFolderEvent(ctx context.Context, id string, owner assistantcontext.SaveOwner, offer, observation string) (agenthttp.PersonalAssistantConversationRecord, []agenthttp.PersonalAssistantConversationMessage, error) {
	store, ok := a.store.(session.AssistantContextMessageStore)
	if !ok {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, errors.New("canonical bounded folder event unavailable")
	}
	sess, msg, err := store.ReadAssistantContextFolderEvent(ctx, id, owner, offer, observation)
	if err != nil {
		return agenthttp.PersonalAssistantConversationRecord{}, nil, continuityStoreError(err)
	}
	record := conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, sess)
	if msg == nil {
		return record, nil, nil
	}
	return record, folderConversationMessages([]session.Message{*msg}), nil
}
