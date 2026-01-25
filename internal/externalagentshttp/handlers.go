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

// GetAll handles GET /api/external-agents
// Returns all external agent data from all sources.
func (h *Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	claudeEnabled := h.configManager.GetExternalAgentsClaudeEnabled()
	codexEnabled := h.configManager.GetExternalAgentsCodexEnabled()

	response := map[string]interface{}{
		"claude_enabled": claudeEnabled,
		"codex_enabled":  codexEnabled,
	}

	// Only include data for enabled sources
	if claudeEnabled {
		response["claude"] = h.cache.GetClaudeData()
	}
	if codexEnabled {
		response["codex"] = h.cache.GetCodexData()
	}

	orihttp.WriteJSON(w, response)
}

// GetClaude handles GET /api/external-agents/claude
// Returns Claude Code data (agents, settings, plugins).
func (h *Handler) GetClaude(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if !h.configManager.GetExternalAgentsClaudeEnabled() {
		orihttp.WriteJSON(w, map[string]interface{}{
			"enabled": false,
			"message": "Claude Code agents are disabled. Enable in Settings.",
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

	if !h.configManager.GetExternalAgentsCodexEnabled() {
		orihttp.WriteJSON(w, map[string]interface{}{
			"enabled": false,
			"message": "Codex CLI agents are disabled. Enable in Settings.",
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

	claudeEnabled := h.configManager.GetExternalAgentsClaudeEnabled()
	codexEnabled := h.configManager.GetExternalAgentsCodexEnabled()

	if !claudeEnabled && !codexEnabled {
		orihttp.WriteJSON(w, map[string]interface{}{
			"status":  "skipped",
			"message": "Both Claude and Codex agents are disabled.",
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
