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
	// claudeDetected reports whether the Claude Code CLI is available. It is
	// evaluated lazily (per request) so it works regardless of init ordering.
	// May be nil, in which case detection is treated as false.
	claudeDetected func() bool
	// codexDetected reports whether the Codex CLI is available. It is evaluated
	// lazily (per request) so it works regardless of init ordering. May be nil,
	// in which case detection is treated as false.
	codexDetected func() bool
}

// New creates a new Handler with the given cache and config manager.
// claudeDetected and codexDetected report whether the corresponding CLI is
// available (may be nil).
func New(cache *externalagents.Cache, configManager *config.Manager, claudeDetected func() bool, codexDetected func() bool) *Handler {
	return &Handler{
		cache:          cache,
		configManager:  configManager,
		claudeDetected: claudeDetected,
		codexDetected:  codexDetected,
	}
}

// claudeEnabled reports whether Claude Code agent reading is effectively active,
// honoring the explicit opt-out and auto-enable-on-CLI-detection.
func (h *Handler) claudeEnabled() bool {
	detected := false
	if h.claudeDetected != nil {
		detected = h.claudeDetected()
	}
	return h.configManager.EffectiveExternalAgentsClaudeEnabled(detected)
}

// codexEnabled reports whether Codex agent reading is effectively active,
// honoring the explicit opt-out and auto-enable-on-CLI-detection.
func (h *Handler) codexEnabled() bool {
	detected := false
	if h.codexDetected != nil {
		detected = h.codexDetected()
	}
	return h.configManager.EffectiveExternalAgentsCodexEnabled(detected)
}

// ClaudeSyncData returns the cached, read-only Claude ~/.claude data when Claude
// agent reading is effectively enabled, or nil otherwise. The agent-detail
// endpoints use this to attach synced state to the Claude Code agent without
// importing the externalagents package.
func (h *Handler) ClaudeSyncData() any {
	if !h.claudeEnabled() {
		return nil
	}
	return h.cache.GetClaudeData()
}

// CodexSyncData returns the cached, read-only Codex ~/.codex data when Codex
// agent reading is enabled, or nil otherwise. The agent-detail endpoints use
// this to attach synced state to the Codex CLI agent without importing the
// externalagents package.
func (h *Handler) CodexSyncData() any {
	if !h.codexEnabled() {
		return nil
	}
	return h.cache.GetCodexData()
}

// GetAll handles GET /api/external-agents
// Returns all external agent data from all sources.
func (h *Handler) GetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	claudeEnabled := h.claudeEnabled()
	codexEnabled := h.codexEnabled()

	response := map[string]any{
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

	if !h.claudeEnabled() {
		orihttp.WriteJSON(w, map[string]any{
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

	if !h.codexEnabled() {
		orihttp.WriteJSON(w, map[string]any{
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

	claudeEnabled := h.claudeEnabled()
	codexEnabled := h.codexEnabled()

	if !claudeEnabled && !codexEnabled {
		orihttp.WriteJSON(w, map[string]any{
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
