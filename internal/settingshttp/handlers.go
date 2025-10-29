package settingshttp

import (
	"encoding/json"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/client"
)

type Handler struct {
	store         store.Store
	configManager *config.Manager
	clientFactory *client.Factory
	llmFactory    *llm.Factory
}

func NewHandler(store store.Store, configManager *config.Manager, clientFactory *client.Factory, llmFactory *llm.Factory) *Handler {
	return &Handler{
		store:         store,
		configManager: configManager,
		clientFactory: clientFactory,
		llmFactory:    llmFactory,
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

// ProviderModel represents a model for a specific provider
type ProviderModel struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Type     string `json:"type"` // tool-calling, general, research
}

// ProviderInfo represents information about an LLM provider
type ProviderInfo struct {
	Name         string          `json:"name"`
	DisplayName  string          `json:"display_name"`
	Type         string          `json:"type"` // cloud, local, hybrid
	Available    bool            `json:"available"`
	RequiresKey  bool            `json:"requires_key"`
	Models       []ProviderModel `json:"models"`
}

// ProvidersHandler returns information about available LLM providers and their models
func (h *Handler) ProvidersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	providers := []ProviderInfo{}

	// Get all registered providers from the factory
	providerNames := []string{"openai", "claude", "ollama"}

	for _, name := range providerNames {
		provider, err := h.llmFactory.GetProvider(name)
		if err != nil {
			// Provider not available, skip
			continue
		}

		caps := provider.Capabilities()
		models := provider.DefaultModels()

		// Convert models to ProviderModel format with categorization
		providerModels := make([]ProviderModel, 0, len(models))
		for _, modelName := range models {
			// Categorize models based on name patterns
			modelType := categorizeModel(name, modelName)

			providerModels = append(providerModels, ProviderModel{
				Value:    modelName,
				Label:    modelName,
				Provider: name,
				Type:     modelType,
			})
		}

		providers = append(providers, ProviderInfo{
			Name:         provider.Name(),
			DisplayName:  getProviderDisplayName(name),
			Type:         string(provider.Type()),
			Available:    true,
			RequiresKey:  caps.RequiresAPIKey,
			Models:       providerModels,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers": providers,
	})
}

// categorizeModel categorizes models into tool-calling, general, or research tiers
func categorizeModel(provider, modelName string) string {
	switch provider {
	case "openai":
		// Tool-calling tier (cheapest)
		if modelName == "gpt-5-nano" || modelName == "gpt-4.1-nano" {
			return "tool-calling"
		}
		// General purpose tier (mid-tier)
		if modelName == "gpt-5-mini" || modelName == "gpt-4.1-mini" || modelName == "gpt-4o-mini" {
			return "general"
		}
		// Research tier (expensive)
		return "research"
	case "claude":
		if modelName == "claude-3-haiku-20240307" {
			return "tool-calling"
		} else if modelName == "claude-3-sonnet-20240229" {
			return "general"
		} else {
			return "research"
		}
	case "ollama":
		// Categorize Ollama models - smaller models for tool-calling, larger for research
		if modelName == "llama2" || modelName == "mistral" || modelName == "phi" {
			return "tool-calling"
		} else if modelName == "codellama" || modelName == "llama2:13b" {
			return "general"
		} else {
			return "research"
		}
	}
	return "general" // default
}

// getProviderDisplayName returns a human-readable name for the provider
func getProviderDisplayName(name string) string {
	switch name {
	case "openai":
		return "OpenAI"
	case "claude":
		return "Anthropic Claude"
	case "ollama":
		return "Ollama (Local)"
	default:
		return name
	}
}