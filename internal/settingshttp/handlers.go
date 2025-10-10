package settingshttp

import (
	"encoding/json"
	"net/http"

	"github.com/johnjallday/dolphin-agent/internal/config"
	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/types"
	"github.com/johnjallday/dolphin-agent/internal/client"
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
		// Return masked API key for display
		response := struct {
			APIKey string `json:"api_key"`
			Masked string `json:"masked"`
		}{
			APIKey: h.configManager.Get().OpenAIAPIKey,
			Masked: h.configManager.MaskAPIKey(),
		}
		_ = json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		var req struct {
			APIKey string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Set API key in config manager (includes validation)
		if err := h.configManager.SetAPIKey(req.APIKey); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Save configuration
		if err := h.configManager.Save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Update global client with new API key (only if not empty)
		if req.APIKey != "" {
			h.clientFactory.UpdateDefaultClient(req.APIKey)
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}