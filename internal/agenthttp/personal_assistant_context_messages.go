package agenthttp

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

type PersonalAssistantConversationDisplayReader interface {
	ReadConversationDisplay(context.Context, string, assistantcontext.SaveOwner) (PersonalAssistantConversationRecord, []PersonalAssistantConversationMessage, bool, error)
}

type PersonalAssistantCanonicalMessageReader interface {
	ReadCanonicalMessage(context.Context, string, assistantcontext.SaveOwner, string) (PersonalAssistantConversationRecord, PersonalAssistantConversationMessage, error)
	ReadCanonicalFolderEvent(context.Context, string, assistantcontext.SaveOwner, string, string) (PersonalAssistantConversationRecord, []PersonalAssistantConversationMessage, error)
}

func (h *HomeAssistantAskHandler) canonicalConversationOwner(ctx context.Context) (assistantcontext.SaveOwner, error) {
	work, user, err := h.researchRelationship(ctx)
	if _, ok := h.PersonalAssistantContext.(interface {
		ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error)
	}); !ok {
		work, err = h.resolvePersonalAssistantContext(ctx)
		if err == nil {
			user, err = h.currentAssistantUser(ctx)
		}
	}
	if err != nil || work == nil || !work.ReadyForWork() {
		return assistantcontext.SaveOwner{}, ErrPersonalAssistantConversationOwnerChanged
	}
	return assistantcontext.SaveOwner{UserID: user, WorkspaceID: work.HQWorkspaceID, AgentName: work.ConversationAgent, StateVersion: work.StateVersion}, nil
}

func (h *HomeAssistantAskHandler) readCanonicalFolderEvents(ctx context.Context, id, offer, observation string) (PersonalAssistantConversationRecord, []PersonalAssistantConversationMessage, error) {
	if reader, ok := h.Conversations.(PersonalAssistantCanonicalMessageReader); ok {
		owner, err := h.canonicalConversationOwner(ctx)
		if err != nil {
			return PersonalAssistantConversationRecord{}, nil, err
		}
		return reader.ReadCanonicalFolderEvent(ctx, id, owner, offer, observation)
	}
	return h.folderStore().ReadFolderConversation(ctx, id)
}
