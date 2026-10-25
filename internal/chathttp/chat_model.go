package chathttp

import (
	"net/http"
	"net/url"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/modelview"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type chatDefinitionReader interface {
	ResolveAgentDefinition(agentName, workspaceID string) (*workspace.ResolvedAgentRuntime, error)
}

// ModelHandler describes the configured ordinary-chat choice. It deliberately
// does not call resolveEffectiveAgent: that path attaches tools and can register
// native MCP servers. Execution can still fail; this is not a health check.
func (h *Handler) ModelHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if !isTrustedChatRequestSource(r) {
		orihttp.Forbidden(w, "Request origin not allowed")
		return
	}
	query := r.URL.Query()
	workspaceID := strings.TrimSpace(query.Get("studio_id"))
	sessionID := strings.TrimSpace(query.Get("session_id"))
	headerID := strings.TrimSpace(h.getSessionID(r))
	if sessionID != "" && headerID != "" && sessionID != headerID {
		orihttp.BadRequest(w, "Conflicting session context")
		return
	}
	if sessionID == "" {
		sessionID = headerID
	}
	if workspaceID == "" || len(workspaceID) > 128 || len(sessionID) > 128 || h.workspaceStore == nil {
		orihttp.BadRequest(w, "Workspace context required")
		return
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		orihttp.NotFound(w, "Workspace context unavailable")
		return
	}
	if sessionID != "" {
		if h.sessionStore == nil {
			orihttp.NotFound(w, "Session context unavailable")
			return
		}
		sess, err := h.sessionStore.GetSession(r.Context(), sessionID)
		if err != nil || sess == nil || sess.FolderID != ws.ID {
			orihttp.NotFound(w, "Session context unavailable")
			return
		}
	}
	resolution := h.resolveExecutionAgentName(r.Context(), sessionID, query.Get("agent_name"), workspaceID)
	view := modelview.Selection{Recipient: resolution.Name, StudioID: ws.ID, SessionID: sessionID, Source: "agent", State: "unavailable"}
	if !resolution.isResolved() {
		orihttp.WriteJSON(w, view)
		return
	}
	var ag *resolvedChatAgent
	if h.runtimeResolver != nil {
		reader, ok := h.runtimeResolver.(chatDefinitionReader)
		if !ok {
			view.State = "unknown"
			orihttp.WriteJSON(w, view)
			return
		}
		definition, err := reader.ResolveAgentDefinition(resolution.Name, workspaceID)
		if err != nil || definition == nil || definition.Agent == nil {
			orihttp.WriteJSON(w, view)
			return
		}
		ag = &resolvedChatAgent{Agent: definition.Agent, WorkspaceCopy: definition.FromWorkspaceCopy}
	} else if h.store != nil {
		// Matches resolveEffectiveAgent when no workspace runtime is installed.
		if base, ok := h.store.GetAgent(resolution.Name); ok && base != nil {
			ag = &resolvedChatAgent{Agent: cloneAgentForChat(base)}
		}
	}
	if ag == nil || isAgentPaused(ag.Agent) {
		orihttp.WriteJSON(w, view)
		return
	}
	// The workspace's existing editor reads snapshot-first and saves to the
	// effective definition, rather than accidentally editing a global namesake.
	workspacePath := ws.FolderSlug
	if workspacePath == "" {
		workspacePath = ws.ID
	}
	view.SettingsHref = "/workspaces/" + url.PathEscape(workspacePath) + "?editAgentModel=" + url.QueryEscape(resolution.Name)
	provider, err := resolveChatProviderName(resolution.Name, ag, h.llmFactory)
	if err != nil || strings.TrimSpace(ag.Settings.Model) == "" {
		view.State = "unconfigured"
		orihttp.WriteJSON(w, view)
		return
	}
	available := false
	if provider == "openai" {
		available = h.clientFactory != nil && h.clientFactory.HasKeyForAgent(ag.Agent)
	} else if h.llmFactory != nil {
		_, err := h.llmFactory.GetProvider(provider)
		available = err == nil
	}
	if available {
		view.State, view.Provider, view.Model = "configured", provider, ag.Settings.Model
	}
	orihttp.WriteJSON(w, view)
}
