package mcphttp

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// connectPollInterval/connectPollBudget bound how long ConnectServerHandler
// waits for Start() to leave the "starting" state before responding, so a
// fast local reconnect (silent token refresh) returns "running" in the same
// round-trip while a first-time auth returns "auth_required" with the
// authorize URL almost as quickly, instead of forcing the frontend into an
// immediate extra status poll.
const (
	connectPollInterval = 150 * time.Millisecond
	connectPollBudget   = 5 * time.Second
)

type connectServerRequest struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type connectServerResponse struct {
	Server       string           `json:"server"`
	Status       mcp.ServerStatus `json:"status"`
	AuthorizeURL string           `json:"authorize_url,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// ConnectServerHandler starts (or resumes) a server's connection. For remote
// servers this is the entry point into the OAuth flow: the first call must
// include client_id/client_secret (persisted to the vault, never echoed
// back); once configured, subsequent calls silently refresh from the stored
// token. Stdio servers behave like the existing enable+start path.
// POST /api/mcp/servers/{name}/connect
func (h *Handler) ConnectServerHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	serverName := r.PathValue("name")
	if serverName == "" {
		orihttp.BadRequest(w, "Server name required")
		return
	}

	cfg, err := h.configManager.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	var req connectServerRequest
	if r.ContentLength != 0 {
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
	}

	if mcp.IsRemoteTransport(*cfg) {
		if strings.TrimSpace(req.ClientID) != "" {
			if err := mcp.SaveOAuthClientCredentials(r.Context(), *cfg, req.ClientID, req.ClientSecret); err != nil {
				orihttp.InternalError(w, err.Error())
				return
			}
		} else if configured, err := mcp.HasOAuthCredentials(r.Context(), *cfg); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		} else if !configured {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(connectServerResponse{
				Server: serverName,
				Status: mcp.StatusAuthRequired,
				Error:  "credentials_required",
			})
			return
		}
	}

	server, err := h.registry.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	switch server.GetStatus() {
	case mcp.StatusRunning, mcp.StatusStarting:
		// already connected/connecting; fall through to report current state
	default:
		go func() {
			if startErr := h.registry.StartServer(serverName); startErr != nil {
				logger.Warn("mcp connect: start failed", logger.Fields{"server": serverName, "error": startErr})
			}
		}()
	}

	deadline := time.Now().Add(connectPollBudget)
	for {
		status := server.GetStatus()
		if status != mcp.StatusStarting || time.Now().After(deadline) {
			w.Header().Set("Content-Type", "application/json")
			if encErr := json.NewEncoder(w).Encode(connectServerResponse{
				Server:       serverName,
				Status:       status,
				AuthorizeURL: server.GetAuthorizeURL(),
			}); encErr != nil {
				logger.Error("Failed to encode response", logger.Fields{"error": encErr})
			}
			return
		}
		time.Sleep(connectPollInterval)
	}
}

// DisconnectServerHandler stops a server and revokes its OAuth credentials
// locally (deletes the vault record). It does not remove the server
// definition itself -- use RemoveServerHandler for that -- so a disconnected
// remote server can be reconnected without re-adding it.
// POST /api/mcp/servers/{name}/disconnect
func (h *Handler) DisconnectServerHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	serverName := r.PathValue("name")
	if serverName == "" {
		orihttp.BadRequest(w, "Server name required")
		return
	}

	cfg, err := h.configManager.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if status, statusErr := h.registry.GetServerStatus(serverName); statusErr == nil {
		switch status {
		case mcp.StatusRunning, mcp.StatusStarting, mcp.StatusRestarting, mcp.StatusError, mcp.StatusAuthRequired:
			if stopErr := h.registry.StopServer(serverName); stopErr != nil {
				logger.Warn("mcp disconnect: stop failed", logger.Fields{"server": serverName, "error": stopErr})
			}
		}
	}

	if mcp.IsRemoteTransport(*cfg) {
		if err := mcp.DisconnectOAuth(r.Context(), *cfg); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(map[string]string{"status": "success"}); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// GetServerOAuthStatusHandler returns the safe, secret-free OAuth
// configuration status for a remote server (whether client credentials have
// been submitted and whether a token is present) — used by the setup UI to
// decide whether to prompt for client id/secret again.
// GET /api/mcp/servers/{name}/oauth-status
func (h *Handler) GetServerOAuthStatusHandler(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("name")
	if serverName == "" {
		orihttp.BadRequest(w, "Server name required")
		return
	}

	cfg, err := h.configManager.GetServer(serverName)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	if !mcp.IsRemoteTransport(*cfg) || h.vaultOAuth == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(vault.MCPOAuthStatus{})
		return
	}

	// "" auto-resolves to the user's one vault (vault.DefaultVaultID is a
	// display-name sentinel, not a resolvable id -- see mcp_oauth_adapter.go).
	status, err := h.vaultOAuth.GetMCPOAuthStatus(r.Context(), "", mcp.NormalizedAuthRef(*cfg))
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(status); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// OAuthCallbackHandler is the browser redirect target for a remote MCP
// server's authorization flow. It hands the code/error back to the fetcher
// goroutine blocked inside Server.Start() (via mcp.DeliverOAuthCallback) and
// renders a small popup-closing page — mirroring the existing email-account
// OAuth callback UX in vaulthttp.
// GET /api/mcp/oauth/callback
func (h *Handler) OAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	oauthErr := r.URL.Query().Get("error")
	oauthErrDesc := r.URL.Query().Get("error_description")

	serverName, ok := mcp.DeliverOAuthCallback(state, code, oauthErr, oauthErrDesc)
	if !ok {
		writeMCPOAuthResultPage(w, http.StatusBadRequest, false, "", "This connection request expired. Start the connection again from the MCP settings page.")
		return
	}
	if oauthErr != "" {
		desc := oauthErrDesc
		if desc == "" {
			desc = oauthErr
		}
		writeMCPOAuthResultPage(w, http.StatusOK, false, serverName, fmt.Sprintf("Connection was canceled: %s", desc))
		return
	}

	writeMCPOAuthResultPage(w, http.StatusOK, true, serverName, "Ori is finishing the connection. You can close this window.")
}

const mcpOAuthPopupEventType = "ori:mcp-oauth"

func writeMCPOAuthResultPage(w http.ResponseWriter, status int, success bool, serverName, message string) {
	payload, _ := json.Marshal(map[string]any{
		"type":    mcpOAuthPopupEventType,
		"success": success,
		"server":  serverName,
		"message": message,
	})

	title := "Connection Incomplete"
	if success {
		title = "Connected"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	orihttp.WriteHTML(w, fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
</head>
<body style="font: 15px/1.5 system-ui, sans-serif; padding: 32px; max-width: 480px; margin: 0 auto;">
  <h1>%s</h1>
  <p>%s</p>
  <script>
    const payload = %s;
    try {
      if (window.opener && typeof window.opener.postMessage === "function") {
        window.opener.postMessage(payload, window.location.origin);
        window.setTimeout(() => window.close(), 150);
      }
    } catch (error) {
      console.error("Failed to deliver MCP OAuth result:", error);
    }
  </script>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(message),
		strings.ReplaceAll(string(payload), "</", `<\/`),
	))
}
