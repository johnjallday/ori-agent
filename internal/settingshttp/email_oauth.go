package settingshttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// EmailOAuthSettingsHandler serves GET/POST /api/settings/email-oauth: the
// in-app configuration of the Google email OAuth client credentials, so a
// self-hosted user can enable Personal HQ email without environment variables.
//
// The client SECRET is never returned by GET (only whether one is set); the
// client ID is public and returned for display.
func (h *Handler) EmailOAuthSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.configManager == nil {
		orihttp.ServiceUnavailable(w, "configuration is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		clientID, _ := h.configManager.GetEmailGoogleOAuth()
		orihttp.Success(w, map[string]any{
			"provider":          "google",
			"configured":        h.configManager.GetEmailGoogleOAuthConfigured(),
			"client_id":         clientID, // public identifier, safe to display
			"has_client_secret": h.configManager.GetEmailGoogleOAuthConfigured() && strings.TrimSpace(clientID) != "",
		})
	case http.MethodPost:
		var req struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		clientID := strings.TrimSpace(req.ClientID)
		clientSecret := strings.TrimSpace(req.ClientSecret)
		if clientID == "" || clientSecret == "" {
			orihttp.BadRequest(w, "both client_id and client_secret are required")
			return
		}
		if err := h.configManager.SetEmailGoogleOAuth(clientID, clientSecret); err != nil {
			orihttp.InternalError(w, "Failed to save email OAuth credentials: "+err.Error())
			return
		}
		orihttp.Success(w, map[string]any{"configured": h.configManager.GetEmailGoogleOAuthConfigured()})
	default:
		orihttp.MethodNotAllowed(w)
	}
}
