package agenthttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

// A metadata-only canonical read is deliberately distinct from Get/ReadFolder-
// Conversation, whose existing adapters may materialize the entire transcript.
// Routing never needs historic instructions to authorize a current action.
type PersonalAssistantConversationOwnerReader interface {
	ReadConversationOwner(context.Context, string, assistantcontext.SaveOwner) (PersonalAssistantConversationRecord, error)
}

var ErrPersonalAssistantConversationOwnerChanged = errors.New("assistant conversation owner changed")

type conversationRouteRefusal struct{ code string }

func (e *conversationRouteRefusal) Error() string { return e.code }

func writeConversationRouteRefusal(w http.ResponseWriter, err error) bool {
	var refusal *conversationRouteRefusal
	if !errors.As(err, &refusal) {
		return false
	}
	status := http.StatusConflict
	if refusal.code == PersonalAssistantConversationNotFound {
		status = http.StatusNotFound
	} else if refusal.code == PersonalAssistantConversationUnavailable {
		status = http.StatusServiceUnavailable
	}
	message := conversationRefusal(refusal.code, "", nil).Response
	writeConversationError(w, status, refusal.code, message)
	return true
}

// ValidateRouteConversation shares Ask's fresh current-user, relationship and
// HQ/profile rules. It does not infer a latest session, read message bodies,
// create a thread, or promote a historical suggestion/approval into an action.
// Ask independently resolves and validates the reference when a turn is sent.
func (h *HomeAssistantAskHandler) ValidateRouteConversation(ctx context.Context, ref *HomeAssistantConversationRef, refs *HomeAssistantRouteContext) error {
	if ref == nil {
		return nil
	}
	refuse := func(code string) error { return &conversationRouteRefusal{code: code} }
	if refs != nil && refs.Origin != "" && refs.Origin != "personal_assistant_panel" {
		return refuse(PersonalAssistantConversationOutOfScope)
	}
	id := strings.TrimSpace(ref.ID)
	if id == "" {
		return nil // New conversation: normal hire/HQ gates still apply.
	}
	work, err := h.resolvePersonalAssistantContext(ctx)
	if err != nil {
		return refuse(PersonalAssistantConversationUnavailable)
	}
	scope, ok := conversationScope(work)
	if !ok {
		return refuse(PersonalAssistantConversationOutOfScope)
	}
	user, err := h.currentAssistantUser(ctx)
	if err != nil {
		return refuse(PersonalAssistantConversationUnavailable)
	}
	reader, ok := h.Conversations.(PersonalAssistantConversationOwnerReader)
	if !ok {
		return refuse(PersonalAssistantConversationUnavailable)
	}
	owner := assistantcontext.SaveOwner{UserID: user, WorkspaceID: scope.workspaceID, AgentName: scope.agentName, StateVersion: work.StateVersion}
	record, err := reader.ReadConversationOwner(ctx, id, owner)
	switch {
	case errors.Is(err, ErrPersonalAssistantConversationNotFound):
		return refuse(PersonalAssistantConversationNotFound)
	case errors.Is(err, ErrPersonalAssistantConversationOwnerChanged):
		return refuse(PersonalAssistantConversationOutOfScope)
	case err != nil:
		return refuse(PersonalAssistantConversationUnavailable)
	case record.ID != id || !scope.owns(record):
		return refuse(PersonalAssistantConversationOutOfScope)
	}
	// A slow read cannot return a route for a replaced/paused/resumed owner.
	fresh, err := h.resolvePersonalAssistantContext(ctx)
	freshScope, ready := conversationScope(fresh)
	if err != nil || ctx.Err() != nil {
		return refuse(PersonalAssistantConversationUnavailable)
	}
	if !ready || freshScope != scope || fresh.StateVersion != owner.StateVersion {
		return refuse(PersonalAssistantConversationOutOfScope)
	}
	return nil
}
