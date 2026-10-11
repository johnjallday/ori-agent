package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/session"
)

func continuityStoreError(err error) error {
	if errors.Is(err, session.ErrSessionNotFound) {
		return agenthttp.ErrPersonalAssistantConversationNotFound
	}
	if errors.Is(err, session.ErrFolderContextConflict) {
		return agenthttp.ErrPersonalAssistantConversationOwnerChanged
	}
	return err
}

func (a personalAssistantConversationAdapter) ReadContinuity(ctx context.Context, id string, owner assistantcontext.SaveOwner) (*agenthttp.PersonalAssistantConversationContext, error) {
	store, ok := a.store.(session.AssistantConversationContextStore)
	if !ok {
		return nil, errors.New("bounded canonical conversation context unavailable")
	}
	value, err := store.ReadAssistantConversationContext(ctx, id, owner)
	if err != nil {
		return nil, continuityStoreError(err)
	}
	sess := value.Session
	return &agenthttp.PersonalAssistantConversationContext{Record: conversationRecord(sess.ID, sess.FolderID, sess.AgentName, sess.Title, sess.MessageCount, &sess), Messages: folderConversationMessages(value.Recent), Pin: value.Pin, Recap: value.Recap, Batch: value.Batch, NextThrough: value.NextThrough, Older: value.Older, HasOlder: value.HasOlder, StaleDiscarded: value.StaleDiscarded}, nil
}

func (a personalAssistantConversationAdapter) SaveRecap(ctx context.Context, id string, owner assistantcontext.SaveOwner, pin assistantcontext.ContextPin, through int64, recap *assistantcontext.ConversationRecap) error {
	store, ok := a.store.(session.AssistantConversationContextStore)
	if !ok {
		return errors.New("canonical conversation recap writer unavailable")
	}
	return continuityStoreError(store.SaveAssistantConversationRecap(ctx, id, owner, pin, through, recap))
}
