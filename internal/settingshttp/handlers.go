package settingshttp

import (
	"encoding/json"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/client"
)

type Handler struct {
	store         store.Store
	configManager *config.Manager
	clientFactory *client.Factory
}

func NewHandler(store store.Store, configManager *config.Manager, clientFactory *client.Factory) *Handler {
	return &Handler{
		store:         store,
		configManager: configManager,
		clientFactory: clientFactory,
	}
}

// SettingsHandler handles agent settings operations
func (h *Handler) SettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Check if a specific agent name is requested
		agentName := r.URL.Query().Get("agent")
		if agentName == "" {
			// If no agent specified, use current agent
			_, agentName = h.store.ListAgents()
		}

		ag, ok := h.store.GetAgent(agentName)
		if !ok {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		// Wrap settings in the expected format for frontend compatibility
		response := struct {
			Settings types.Settings `json:"Settings"`
		}{
			Settings: ag.Settings,
		}
		_ = json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		var s types.Settings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check if a specific agent name is requested
		agentName := r.URL.Query().Get("agent")
		if agentName == "" {
			// If no agent specified, use current agent
			_, agentName = h.store.ListAgents()
		}

		ag, ok := h.store.GetAgent(agentName)
		if !ok {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		ag.Settings = s
		if err := h.store.SetAgent(agentName, ag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// APIKeyHandler handles API key management
func (h *Handler) APIKeyHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Return API key status including environment variables
		cfg := h.configManager.Get()
		response := struct {
			OpenAIKey          string `json:"openai_api_key"`
			AnthropicKey       string `json:"anthropic_api_key"`
			OpenAIMasked       string `json:"openai_masked"`
			AnthropicMasked    string `json:"anthropic_masked"`
			HasOpenAI          bool   `json:"has_openai"`
			HasAnthropic       bool   `json:"has_anthropic"`
		}{
			OpenAIKey:       cfg.OpenAIAPIKey,
			AnthropicKey:    cfg.AnthropicAPIKey,
			OpenAIMasked:    h.configManager.MaskAPIKey(),
			AnthropicMasked: maskAnthropicAPIKey(h.configManager.GetAnthropicAPIKey()),
			HasOpenAI:       h.configManager.GetAPIKey() != "",
			HasAnthropic:    h.configManager.GetAnthropicAPIKey() != "",
		}
		_ = json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		var req struct {
			OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
			AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Get current settings
		cfg := h.configManager.Get()

		// Update OpenAI API key if provided
		if req.OpenAIAPIKey != "" {
			if err := h.configManager.SetAPIKey(req.OpenAIAPIKey); err != nil {
				http.Error(w, "Invalid OpenAI API key: "+err.Error(), http.StatusBadRequest)
				return
			}
			// Update global client with new API key
			h.clientFactory.UpdateDefaultClient(req.OpenAIAPIKey)
		}

		// Update Anthropic API key if provided
		if req.AnthropicAPIKey != "" {
			cfg.AnthropicAPIKey = req.AnthropicAPIKey
			if err := h.configManager.Update(cfg); err != nil {
				http.Error(w, "Invalid Anthropic API key: "+err.Error(), http.StatusBadRequest)
				return
			}
		}

		// Save configuration
		if err := h.configManager.Save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// maskAnthropicAPIKey returns a masked version of the Anthropic API key
func maskAnthropicAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}

	if len(apiKey) < 12 {
		return "***"
	}
	return apiKey[:8] + "***..." + apiKey[len(apiKey)-4:]
}