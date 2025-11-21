package settingshttp

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
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
			OpenAIKey       string `json:"openai_api_key"`
			AnthropicKey    string `json:"anthropic_api_key"`
			OpenAIMasked    string `json:"openai_masked"`
			AnthropicMasked string `json:"anthropic_masked"`
			HasOpenAI       bool   `json:"has_openai"`
			HasAnthropic    bool   `json:"has_anthropic"`
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

			// Register/update OpenAI provider in LLM factory
			openaiProvider := llm.NewOpenAIProvider(llm.ProviderConfig{
				APIKey: req.OpenAIAPIKey,
			})
			h.llmFactory.Register("openai", openaiProvider)
		}

		// Update Anthropic API key if provided
		if req.AnthropicAPIKey != "" {
			cfg.AnthropicAPIKey = req.AnthropicAPIKey
			if err := h.configManager.Update(cfg); err != nil {
				http.Error(w, "Invalid Anthropic API key: "+err.Error(), http.StatusBadRequest)
				return
			}

			// Register/update Claude provider in LLM factory
			claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
				APIKey: req.AnthropicAPIKey,
			})
			h.llmFactory.Register("claude", claudeProvider)
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
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Type        string          `json:"type"` // cloud, local, hybrid
	Available   bool            `json:"available"`
	RequiresKey bool            `json:"requires_key"`
	Models      []ProviderModel `json:"models"`
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

		var providerModels []ProviderModel
		var displayName string
		var providerType string
		var requiresKey bool
		var available bool

		if err != nil {
			// Provider not registered (likely missing API key)
			// Still show it but mark as unavailable
			available = false
			displayName = getProviderDisplayName(name)
			providerType = "cloud"
			requiresKey = true

			// Get default models for unregistered providers
			var defaultModels []string
			if name == "claude" {
				// Hardcode Claude models since provider isn't registered
				defaultModels = []string{
					"claude-sonnet-4-5",
					"claude-sonnet-4",
					"claude-opus-4-1",
					"claude-3-opus-20240229",
					"claude-3-sonnet-20240229",
					"claude-3-haiku-20240307",
				}
			} else if name == "openai" {
				// Hardcode OpenAI models since provider isn't registered
				defaultModels = []string{
					"gpt-5-nano",
					"gpt-4.1-nano",
					"gpt-5-mini",
					"gpt-4.1-mini",
					"gpt-5",
					"gpt-4.1",
					"o1-preview",
					"o1-mini",
				}
			} else {
				// Skip other unregistered providers
				continue
			}

			for _, modelName := range defaultModels {
				categories := getModelCategories(name, modelName)
				for _, category := range categories {
					providerModels = append(providerModels, ProviderModel{
						Value:    modelName,
						Label:    modelName,
						Provider: name,
						Type:     category,
					})
				}
			}
		} else {
			// Provider is registered
			available = true
			caps := provider.Capabilities()
			models := provider.DefaultModels()
			displayName = getProviderDisplayName(name)
			providerType = string(provider.Type())
			requiresKey = caps.RequiresAPIKey

			// Convert models to ProviderModel format with categorization
			providerModels = make([]ProviderModel, 0, len(models)*2)
			for _, modelName := range models {
				categories := getModelCategories(name, modelName)
				for _, category := range categories {
					providerModels = append(providerModels, ProviderModel{
						Value:    modelName,
						Label:    modelName,
						Provider: name,
						Type:     category,
					})
				}
			}
		}

		providers = append(providers, ProviderInfo{
			Name:        name,
			DisplayName: displayName,
			Type:        providerType,
			Available:   available,
			RequiresKey: requiresKey,
			Models:      providerModels,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"providers": providers,
	}); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// getModelCategories returns all categories a model should appear in
// Some models (like llama3) appear in multiple categories
func getModelCategories(provider, modelName string) []string {
	switch provider {
	case "openai":
		// Flagship models (gpt-5, gpt-4.1) appear in orchestration and research
		if modelName == "gpt-5" || modelName == "gpt-4.1" {
			return []string{"orchestration", "research"}
		}
		// O-series models (reasoning models) are perfect for orchestration
		if strings.HasPrefix(modelName, "o1") || strings.HasPrefix(modelName, "o3") {
			return []string{"orchestration", "research"}
		}
		// General tier models can do orchestration too
		if modelName == "gpt-5-mini" || modelName == "gpt-4.1-mini" {
			return []string{"general", "orchestration"}
		}
		return []string{categorizeModel(provider, modelName)}

	case "claude":
		// Sonnet and Opus are great for orchestration
		if strings.Contains(modelName, "sonnet") || strings.Contains(modelName, "opus") {
			return []string{categorizeModel(provider, modelName), "orchestration"}
		}
		return []string{categorizeModel(provider, modelName)}

	case "ollama":
		lowerName := strings.ToLower(modelName)

		// llama3 models appear in all categories (they're versatile local models)
		if strings.Contains(lowerName, "llama3") {
			return []string{"tool-calling", "general", "orchestration", "research"}
		}

		// Larger models can do orchestration
		if strings.Contains(lowerName, "70b") || strings.Contains(lowerName, "mixtral") {
			return []string{"general", "orchestration", "research"}
		}

		// Other models get their single category
		return []string{categorizeModel(provider, modelName)}

	default:
		// Non-Ollama providers use single category
		return []string{categorizeModel(provider, modelName)}
	}
}

// categorizeModel categorizes models into tool-calling, general, orchestration, or research tiers
func categorizeModel(provider, modelName string) string {
	switch provider {
	case "openai":
		// Tool-calling tier (cheapest - nano models)
		if modelName == "gpt-5-nano" || modelName == "gpt-4.1-nano" {
			return "tool-calling"
		}
		// General purpose tier (mid-tier - mini models)
		if modelName == "gpt-5-mini" || modelName == "gpt-4.1-mini" {
			return "general"
		}
		// Flagship models (gpt-5, gpt-4.1) - research tier
		if modelName == "gpt-5" || modelName == "gpt-4.1" {
			return "research"
		}
		// All other OpenAI models default to research tier (expensive)
		return "research"
	case "claude":
		// Haiku is the lightweight model for tool calling
		if strings.Contains(modelName, "haiku") {
			return "tool-calling"
		}
		// Sonnet 4.5 and 4 are general purpose
		if modelName == "claude-sonnet-4-5" || modelName == "claude-sonnet-4" {
			return "general"
		}
		// Claude 3 Sonnet is general
		if modelName == "claude-3-sonnet-20240229" {
			return "general"
		}
		// Opus models are research tier (most capable)
		return "research"
	case "ollama":
		// Categorize Ollama models - use pattern matching for flexibility
		lowerName := strings.ToLower(modelName)

		// Tool-calling tier - smaller/faster models (good for function calling)
		if strings.Contains(lowerName, "llama3") ||
			strings.Contains(lowerName, "llama2") && !strings.Contains(lowerName, "70b") ||
			strings.Contains(lowerName, "mistral") ||
			strings.Contains(lowerName, "phi") ||
			strings.Contains(lowerName, "qwen") {
			return "tool-calling"
		}

		// General purpose tier - mid-size models
		if strings.Contains(lowerName, "codellama") ||
			strings.Contains(lowerName, "13b") ||
			strings.Contains(lowerName, "mixtral") {
			return "general"
		}

		// Research tier - large models
		if strings.Contains(lowerName, "70b") ||
			strings.Contains(lowerName, "neural-chat") ||
			strings.Contains(lowerName, "starling") {
			return "research"
		}

		// Default to tool-calling for unknown Ollama models (they're local, so cost is not a concern)
		return "tool-calling"
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
