package agenthttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/modelview"
)

// ModelHandler projects the same selection as runModel, without preparing a
// turn, reading messages, creating a session, or invoking provider/tool work.
func (h *HomeAssistantAskHandler) ModelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	view := modelview.Selection{Recipient: "personal", Source: "default", State: "unknown", SettingsHref: "/settings#system-model"}
	// Production supplies a relationship-only reader; avoid profile, memory and
	// transcript reads for a model label. Legacy adapters retain their reader.
	var work *PersonalAssistantWorkContext
	user, err := h.currentAssistantUser(r.Context())
	if err == nil {
		if provider, ok := h.PersonalAssistantContext.(interface {
			ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error)
		}); ok {
			work, err = provider.ResolvePersonalAssistantRelationship(r.Context(), user)
		} else {
			work, err = h.resolvePersonalAssistantContext(r.Context())
		}
	}
	if err != nil {
		orihttp.WriteJSON(w, view)
		return
	}
	if work != nil && !work.ReadyForWork() {
		view.State = "unavailable"
		orihttp.WriteJSON(w, view)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if id != "" {
		scope, ok := conversationScope(work)
		if !ok || h.Conversations == nil || len(id) > 128 {
			orihttp.BadRequest(w, "Conversation context unavailable")
			return
		}
		reader, ok := h.Conversations.(PersonalAssistantConversationOwnerReader)
		if !ok {
			orihttp.ServiceUnavailable(w, "Conversation context unavailable")
			return
		}
		owner := assistantcontext.SaveOwner{UserID: user, WorkspaceID: scope.workspaceID, AgentName: scope.agentName, StateVersion: work.StateVersion}
		record, err := reader.ReadConversationOwner(r.Context(), id, owner)
		if err != nil || record.ID != id || !scope.owns(record) {
			orihttp.NotFound(w, "Conversation context unavailable")
			return
		}
		view.SessionID = id
	}
	provider, model, err := h.resolveProvider()
	switch {
	case errors.Is(err, errHomeModelNotConfigured):
		view.State = "unconfigured"
	case err != nil || provider == nil:
		view.State = "unavailable"
	case strings.TrimSpace(model) == "":
		view.State = "unconfigured"
	default:
		view.State, view.Provider, view.Model = "configured", provider.Name(), model
	}
	orihttp.WriteJSON(w, view)
}
