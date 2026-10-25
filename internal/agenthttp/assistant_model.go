package agenthttp

import (
	"errors"
	"net/http"
	"strings"

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
	work, err := h.resolvePersonalAssistantContext(r.Context())
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
		record, err := h.Conversations.Get(r.Context(), id)
		if err != nil || !scope.owns(record) {
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
