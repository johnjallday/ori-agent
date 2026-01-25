package settingshttp

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
	"github.com/johnjallday/ori-agent/internal/platform"
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
			orihttp.NotFound(w, "agent not found")
			return
		}
		// Wrap settings in the expected format for frontend compatibility
		response := struct {
			Settings types.Settings `json:"Settings"`
		}{
			Settings: ag.Settings,
		}
		orihttp.WriteJSON(w, response)

	case http.MethodPost:
		var s types.Settings
		if !orihttp.ParseJSONBody(w, r, &s) {
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
			orihttp.NotFound(w, "agent not found")
			return
		}
		ag.Settings = s
		if err := h.store.SetAgent(agentName, ag); err != nil {
			orihttp.InternalError(w, err.Error())
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
		// Return API key status (masked only - never expose raw keys)
		response := struct {
			OpenAIMasked    string `json:"openai_masked"`
			AnthropicMasked string `json:"anthropic_masked"`
			GeminiMasked    string `json:"gemini_masked"`
			HasOpenAI       bool   `json:"has_openai"`
			HasAnthropic    bool   `json:"has_anthropic"`
			HasGemini       bool   `json:"has_gemini"`
			Masked          string `json:"masked,omitempty"`
		}{
			OpenAIMasked:    h.configManager.MaskAPIKey(),
			AnthropicMasked: maskAnthropicAPIKey(h.configManager.GetAnthropicAPIKey()),
			GeminiMasked:    maskGeminiAPIKey(h.configManager.GetGeminiAPIKey()),
			HasOpenAI:       h.configManager.GetAPIKey() != "",
			HasAnthropic:    h.configManager.GetAnthropicAPIKey() != "",
			HasGemini:       h.configManager.GetGeminiAPIKey() != "",
			Masked:          h.configManager.MaskAPIKey(),
		}
		orihttp.WriteJSON(w, response)

	case http.MethodPost:
		var req struct {
			OpenAIAPIKey    string `json:"openai_api_key,omitempty"`
			AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
			GeminiAPIKey    string `json:"gemini_api_key,omitempty"`
			APIKey          string `json:"api_key,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		// Backwards compatibility: accept api_key as OpenAI API key
		if req.OpenAIAPIKey == "" && req.APIKey != "" {
			req.OpenAIAPIKey = req.APIKey
		}

		// Get current settings
		cfg := h.configManager.Get()

		// Update OpenAI API key if provided
		if req.OpenAIAPIKey != "" {
			if err := h.configManager.SetAPIKey(req.OpenAIAPIKey); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid OpenAI API key", err)
				return
			}
			cfg.OpenAIAPIKey = req.OpenAIAPIKey
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

			// Register/update Claude provider in LLM factory
			claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
				APIKey: req.AnthropicAPIKey,
			})
			h.llmFactory.Register("claude", claudeProvider)
		}

		// Update Gemini API key if provided
		if req.GeminiAPIKey != "" {
			cfg.GeminiAPIKey = req.GeminiAPIKey

			// Register/update Gemini provider in LLM factory
			geminiProvider := llm.NewGeminiProvider(llm.ProviderConfig{
				APIKey: req.GeminiAPIKey,
			})
			h.llmFactory.Register("gemini", geminiProvider)
		}

		if req.AnthropicAPIKey != "" || req.GeminiAPIKey != "" {
			if err := h.configManager.Update(cfg); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid API key", err)
				return
			}
		}

		// Save configuration
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// SpeechSettingsHandler handles speech settings persistence
func (h *Handler) SpeechSettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := h.configManager.Get()
		response := struct {
			SpeechProvider string `json:"speech_provider"`
			SpeechModel    string `json:"speech_model,omitempty"`
			SpeechLanguage string `json:"speech_language"`
		}{
			SpeechProvider: cfg.SpeechProvider,
			SpeechModel:    cfg.SpeechModel,
			SpeechLanguage: cfg.SpeechLanguage,
		}
		orihttp.WriteJSON(w, response)

	case http.MethodPost:
		var req struct {
			SpeechProvider string `json:"speech_provider"`
			SpeechModel    string `json:"speech_model"`
			SpeechLanguage string `json:"speech_language"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		cfg := h.configManager.Get()
		cfg.SpeechProvider = strings.TrimSpace(req.SpeechProvider)
		cfg.SpeechModel = strings.TrimSpace(req.SpeechModel)
		cfg.SpeechLanguage = strings.TrimSpace(req.SpeechLanguage)

		if err := h.configManager.Update(cfg); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid speech settings", err)
			return
		}
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
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

func maskGeminiAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	if len(apiKey) < 12 {
		return "***"
	}
	return apiKey[:6] + "***..." + apiKey[len(apiKey)-4:]
}

// ProviderModel represents a model for a specific provider
type ProviderModel struct {
	Value           string   `json:"value"`
	Label           string   `json:"label"`
	Provider        string   `json:"provider"`
	Type            string   `json:"type"`                       // tool-calling, general, research
	GoodFor         []string `json:"good_for"`                   // use-case recommendations
	Pricing         string   `json:"pricing,omitempty"`          // pricing info (e.g., "$2.50 in / $10 out")
	DeprecationDate string   `json:"deprecation_date,omitempty"` // when the model will be deprecated (YYYY-MM-DD)
	IsLegacy        bool     `json:"is_legacy,omitempty"`        // true if model is past deprecation date
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
	providerNames := []string{"openai", "claude", "gemini", "ollama"}

	for _, name := range providerNames {
		provider, err := h.llmFactory.GetProvider(name)

		var providerModels []ProviderModel
		var displayName string
		var providerType string
		var requiresKey bool
		var available bool

		if err != nil {
			// Provider not registered (likely missing API key)
			// Mark as unavailable and return empty models list
			available = false
			displayName = getProviderDisplayName(name)
			providerType = "cloud"
			requiresKey = true
			providerModels = []ProviderModel{} // Empty list - no models shown without API key
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
				goodFor := modelinfo.GetGoodFor(modelName)
				pricingInfo := modelinfo.GetPricing(modelName)
				pricing := ""
				if pricingInfo != nil {
					pricing = modelinfo.FormatPricing(pricingInfo)
				} else if provider.Type() == llm.ProviderTypeLocal {
					pricing = modelinfo.FormatPricing(nil)
				}
				var deprecationDate string
				var isLegacy bool
				if pricingInfo != nil {
					deprecationDate = pricingInfo.DeprecationDate
					isLegacy = pricingInfo.IsLegacy()
				}
				for _, category := range categories {
					providerModels = append(providerModels, ProviderModel{
						Value:           modelName,
						Label:           modelName,
						Provider:        name,
						Type:            category,
						GoodFor:         goodFor,
						Pricing:         pricing,
						DeprecationDate: deprecationDate,
						IsLegacy:        isLegacy,
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

	orihttp.WriteJSON(w, map[string]interface{}{
		"providers": providers,
	})
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
	case "gemini":
		lowerName := strings.ToLower(modelName)
		if strings.Contains(lowerName, "pro") {
			return []string{"research", "orchestration"}
		}
		if strings.Contains(lowerName, "flash") {
			return []string{"tool-calling", "general"}
		}
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
	case "gemini":
		lowerName := strings.ToLower(modelName)
		if strings.Contains(lowerName, "flash") {
			return "tool-calling"
		}
		if strings.Contains(lowerName, "pro") {
			return "research"
		}
		return "general"
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
	case "gemini":
		return "Google Gemini"
	default:
		return name
	}
}

// SystemModelRequest represents a request to update the system model
type SystemModelRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// SystemModelResponse represents the system model configuration
type SystemModelResponse struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"`
}

// SystemModelHandler handles system model configuration
func (h *Handler) SystemModelHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		provider, model := h.configManager.GetSystemModel()
		response := SystemModelResponse{
			Provider:   provider,
			Model:      model,
			Configured: h.configManager.IsSystemModelConfigured(),
		}
		orihttp.WriteJSON(w, response)

	case http.MethodPost:
		var req SystemModelRequest
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		// Normalize provider name to lowercase
		provider := strings.ToLower(strings.TrimSpace(req.Provider))
		model := strings.TrimSpace(req.Model)

		// If clearing the system model (both empty), allow it
		if provider == "" && model == "" {
			if err := h.configManager.SetSystemModel("", ""); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to clear system model", err)
				return
			}
			if err := h.configManager.Save(); err != nil {
				orihttp.InternalError(w, err.Error())
				return
			}
			orihttp.WriteJSON(w, SystemModelResponse{
				Provider:   "",
				Model:      "",
				Configured: false,
			})
			return
		}

		// Validate that the provider exists and is available
		_, err := h.llmFactory.GetProvider(provider)
		if err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Provider not available. Please configure the API key first.", err)
			return
		}

		// Set the system model
		if err := h.configManager.SetSystemModel(provider, model); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid system model configuration", err)
			return
		}

		// Save configuration
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, SystemModelResponse{
			Provider:   provider,
			Model:      model,
			Configured: true,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// AvailableModelsHandler returns models available for a specific provider (for system model dropdown)
func (h *Handler) AvailableModelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		orihttp.BadRequest(w, "provider query parameter is required")
		return
	}

	providerName = strings.ToLower(strings.TrimSpace(providerName))

	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		// Provider not available - return empty models list with available=false
		orihttp.WriteJSON(w, map[string]interface{}{
			"provider":  providerName,
			"available": false,
			"models":    []string{},
			"message":   "Provider not configured. Please add the API key first.",
		})
		return
	}

	models := provider.DefaultModels()
	orihttp.WriteJSON(w, map[string]interface{}{
		"provider":  providerName,
		"available": true,
		"models":    models,
	})
}

// SystemPathsResponse represents system paths information
type SystemPathsResponse struct {
	DefaultOutputDir string `json:"default_output_dir"`
	HomeDir          string `json:"home_dir"`
}

// SystemPathsHandler returns system paths including default output directory
func (h *Handler) SystemPathsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get default output directory
	defaultOutputDir, err := platform.GetDefaultOutputDir()
	if err != nil {
		orihttp.InternalError(w, "Failed to get default output directory: "+err.Error())
		return
	}

	// Get home directory
	homeDir, err := platform.GetHomeDir()
	if err != nil {
		homeDir = "" // Non-fatal, just return empty
	}

	orihttp.WriteJSON(w, SystemPathsResponse{
		DefaultOutputDir: defaultOutputDir,
		HomeDir:          homeDir,
	})
}

// ExternalAgentsSettingsHandler handles external agents enabled settings
func (h *Handler) ExternalAgentsSettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		orihttp.WriteJSON(w, map[string]bool{
			"claude_enabled": h.configManager.GetExternalAgentsClaudeEnabled(),
			"codex_enabled":  h.configManager.GetExternalAgentsCodexEnabled(),
		})

	case http.MethodPost:
		var req struct {
			ClaudeEnabled *bool `json:"claude_enabled,omitempty"`
			CodexEnabled  *bool `json:"codex_enabled,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		// Update only the settings that were provided
		if req.ClaudeEnabled != nil {
			h.configManager.SetExternalAgentsClaudeEnabled(*req.ClaudeEnabled)
		}
		if req.CodexEnabled != nil {
			h.configManager.SetExternalAgentsCodexEnabled(*req.CodexEnabled)
		}

		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, map[string]bool{
			"claude_enabled": h.configManager.GetExternalAgentsClaudeEnabled(),
			"codex_enabled":  h.configManager.GetExternalAgentsCodexEnabled(),
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
