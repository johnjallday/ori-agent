// Package externalagentshttp provides HTTP handlers for external agent data.
package externalagentshttp

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// Handler provides HTTP handlers for external agent data.
type Handler struct {
	cache         *externalagents.Cache
	configManager *config.Manager
}

// New creates a new Handler with the given cache and config manager.
func New(cache *externalagents.Cache, configManager *config.Manager) *Handler {
	return &Handler{
		cache:         cache,
		configManager: configManager,
	}
}

// disabledResponse is returned when external agents feature is disabled.
type disabledResponse struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message"`
}

// GetAll handles GET /api/external-agents
// Returns all external agent data from all sources.
func (h *Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Check if external agents feature is enabled
	if !h.configManager.GetExternalAgentsEnabled() {
		orihttp.WriteJSON(w, disabledResponse{
			Enabled: false,
			Message: "External agents feature is disabled. Enable it in Settings to view agents from Claude Code and Codex CLI.",
		})
		return
	}

	data := h.cache.GetAll()
	orihttp.WriteJSON(w, map[string]interface{}{
		"enabled": true,
		"claude":  data.Claude,
		"codex":   data.Codex,
	})
}

// GetClaude handles GET /api/external-agents/claude
// Returns Claude Code data (agents, settings, plugins).
func (h *Handler) GetClaude(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if !h.configManager.GetExternalAgentsEnabled() {
		orihttp.WriteJSON(w, disabledResponse{
			Enabled: false,
			Message: "External agents feature is disabled.",
		})
		return
	}

	data := h.cache.GetClaudeData()
	orihttp.WriteJSON(w, data)
}

// GetCodex handles GET /api/external-agents/codex
// Returns Codex data (config, skills, rules).
func (h *Handler) GetCodex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if !h.configManager.GetExternalAgentsEnabled() {
		orihttp.WriteJSON(w, disabledResponse{
			Enabled: false,
			Message: "External agents feature is disabled.",
		})
		return
	}

	data := h.cache.GetCodexData()
	orihttp.WriteJSON(w, data)
}

// Refresh handles POST /api/external-agents/refresh
// Invalidates and reloads the cache from disk.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	if !h.configManager.GetExternalAgentsEnabled() {
		orihttp.WriteJSON(w, disabledResponse{
			Enabled: false,
			Message: "External agents feature is disabled.",
		})
		return
	}

	if err := h.cache.Refresh(); err != nil {
		logger.Error("Failed to refresh external agents cache", logger.Fields{"error": err})
		orihttp.InternalError(w, "Failed to refresh external agents data")
		return
	}

	orihttp.WriteJSON(w, map[string]string{
		"status":  "ok",
		"message": "External agents cache refreshed",
	})
}
