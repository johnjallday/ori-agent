package settingshttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

var settingsLog = logger.New("settings")

type Handler struct {
	store                   store.Store
	configManager           *config.Manager
	clientFactory           *client.Factory
	llmFactory              *llm.Factory
	utilitySettingsReloader func()
}

func NewHandler(store store.Store, configManager *config.Manager, clientFactory *client.Factory, llmFactory *llm.Factory) *Handler {
	return &Handler{
		store:         store,
		configManager: configManager,
		clientFactory: clientFactory,
		llmFactory:    llmFactory,
	}
}

// SetUtilitySettingsReloader sets a callback invoked after utility settings are saved.
func (h *Handler) SetUtilitySettingsReloader(fn func()) {
	h.utilitySettingsReloader = fn
}

func resolveAssistantDefaultAgentName(st store.Store) string {
	if st == nil {
		return ""
	}
	if agent, ok := st.GetAgent("Ori"); ok && agent != nil {
		return "Ori"
	}
	return store.FirstAgentName(st)
}

func (h *Handler) resolveSettingsAgentName(r *http.Request) string {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agentName != "" {
		return agentName
	}
	return resolveAssistantDefaultAgentName(h.store)
}

// SettingsHandler handles agent settings operations
func (h *Handler) SettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		agentName := h.resolveSettingsAgentName(r)

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

		agentName := h.resolveSettingsAgentName(r)

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

// SessionSettingsHandler handles session cleanup configuration operations.
func (h *Handler) SessionSettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		orihttp.WriteJSON(w, map[string]any{
			"session_cleanup_enabled": h.configManager.GetSessionCleanupEnabled(),
			"session_cleanup_days":    h.configManager.GetSessionCleanupDays(),
			"session_max_count":       h.configManager.GetSessionMaxCount(),
		})

	case http.MethodPost:
		var req struct {
			SessionCleanupEnabled *bool `json:"session_cleanup_enabled"`
			SessionCleanupDays    *int  `json:"session_cleanup_days"`
			SessionMaxCount       *int  `json:"session_max_count"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		enabled := h.configManager.GetSessionCleanupEnabled()
		days := h.configManager.GetSessionCleanupDays()
		maxCount := h.configManager.GetSessionMaxCount()

		if req.SessionCleanupEnabled != nil {
			enabled = *req.SessionCleanupEnabled
		}
		if req.SessionCleanupDays != nil {
			days = *req.SessionCleanupDays
		}
		if req.SessionMaxCount != nil {
			maxCount = *req.SessionMaxCount
		}

		h.configManager.SetSessionCleanupSettings(enabled, days, maxCount)
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, map[string]any{
			"success":                 true,
			"session_cleanup_enabled": enabled,
			"session_cleanup_days":    days,
			"session_max_count":       maxCount,
		})

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
			settingsLog.Info("OpenAI API key updated")
		}

		// Update Anthropic API key if provided
		var setupTokenExchanged bool
		if req.AnthropicAPIKey != "" {
			req.AnthropicAPIKey = strings.TrimSpace(req.AnthropicAPIKey)

			// Setup tokens (sk-ant-oat01-) are scoped to Claude Code only.
			// Exchange for a permanent API key before saving.
			if strings.HasPrefix(req.AnthropicAPIKey, "sk-ant-oat01-") {
				settingsLog.Info("Exchanging Claude setup token for permanent API key")
				permanentKey, err := exchangeSetupTokenForAPIKey(req.AnthropicAPIKey)
				if err != nil {
					settingsLog.Error("Setup token exchange failed", logger.Fields{"error": err})
					orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to exchange setup token: "+err.Error(), err)
					return
				}
				req.AnthropicAPIKey = permanentKey
				setupTokenExchanged = true
				settingsLog.Info("Setup token exchanged for permanent API key")
			}

			cfg.AnthropicAPIKey = req.AnthropicAPIKey

			// Register/update Claude provider in LLM factory
			claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
				APIKey: req.AnthropicAPIKey,
			})
			h.llmFactory.Register("claude", claudeProvider)
			settingsLog.Info("Anthropic API key updated", logger.Fields{
				"setup_token_exchanged": setupTokenExchanged,
			})
		}

		// Update Gemini API key if provided
		if req.GeminiAPIKey != "" {
			cfg.GeminiAPIKey = req.GeminiAPIKey

			// Register/update Gemini provider in LLM factory
			geminiProvider := llm.NewGeminiProvider(llm.ProviderConfig{
				APIKey: req.GeminiAPIKey,
			})
			h.llmFactory.Register("gemini", geminiProvider)
			settingsLog.Info("Gemini API key updated")
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

		orihttp.WriteJSON(w, map[string]any{
			"success":               true,
			"setup_token_exchanged": setupTokenExchanged,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// exchangeSetupTokenForAPIKey exchanges a Claude Code setup token for a permanent API key.
// Setup tokens (sk-ant-oat01-) are scoped to Claude Code only; they must be exchanged
// via Anthropic's create_api_key endpoint to obtain a standard key for the Messages API.
func exchangeSetupTokenForAPIKey(setupToken string) (string, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/api/oauth/claude_cli/create_api_key", strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("failed to create exchange request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+setupToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse exchange response: %w", err)
	}
	if result.RawKey == "" {
		return "", fmt.Errorf("empty API key in exchange response")
	}

	return result.RawKey, nil
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

type utilitySettingsResponse struct {
	Enabled                 bool     `json:"enabled"`
	TimeoutMs               int      `json:"timeout_ms"`
	RetryAttempts           int      `json:"retry_attempts"`
	RetryDelayMs            int      `json:"retry_delay_ms"`
	SearchProvider          string   `json:"search_provider"`
	BrowserControlProvider  string   `json:"browser_control_provider"`
	PlaywrightBrowser       string   `json:"playwright_browser"`
	PlaywrightExecutable    string   `json:"playwright_executable_path"`
	WeatherProvider         string   `json:"weather_provider"`
	WeatherGeocodingURL     string   `json:"weather_geocoding_url,omitempty"`
	WeatherForecastURL      string   `json:"weather_forecast_url,omitempty"`
	WebFetchMaxResponseSize int64    `json:"web_fetch_max_response_size"`
	BrowserMaxResponseSize  int64    `json:"browser_max_response_size"`
	BrowserAllowedDomains   []string `json:"browser_allowed_domains,omitempty"`
	BlockPrivateHosts       bool     `json:"block_private_hosts"`
	UserAgent               string   `json:"user_agent,omitempty"`
	HasBraveAPIKey          bool     `json:"has_brave_api_key"`
	BraveAPIKeyMasked       string   `json:"brave_api_key_masked,omitempty"`
}

type utilitySettingsUpdateRequest struct {
	Enabled                 *bool     `json:"enabled,omitempty"`
	TimeoutMs               *int      `json:"timeout_ms,omitempty"`
	RetryAttempts           *int      `json:"retry_attempts,omitempty"`
	RetryDelayMs            *int      `json:"retry_delay_ms,omitempty"`
	SearchProvider          *string   `json:"search_provider,omitempty"`
	BrowserControlProvider  *string   `json:"browser_control_provider,omitempty"`
	PlaywrightBrowser       *string   `json:"playwright_browser,omitempty"`
	PlaywrightExecutable    *string   `json:"playwright_executable_path,omitempty"`
	BraveAPIKey             *string   `json:"brave_api_key,omitempty"`
	WeatherProvider         *string   `json:"weather_provider,omitempty"`
	WeatherGeocodingURL     *string   `json:"weather_geocoding_url,omitempty"`
	WeatherForecastURL      *string   `json:"weather_forecast_url,omitempty"`
	WebFetchMaxResponseSize *int64    `json:"web_fetch_max_response_size,omitempty"`
	BrowserMaxResponseSize  *int64    `json:"browser_max_response_size,omitempty"`
	BrowserAllowedDomains   *[]string `json:"browser_allowed_domains,omitempty"`
	BlockPrivateHosts       *bool     `json:"block_private_hosts,omitempty"`
	UserAgent               *string   `json:"user_agent,omitempty"`
}

// UtilitySettingsHandler handles utility provider/runtime settings.
func (h *Handler) UtilitySettingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := h.configManager.Get()
		orihttp.WriteJSON(w, map[string]any{
			"utility": toUtilitySettingsResponse(cfg.Utility),
		})

	case http.MethodPost:
		var req utilitySettingsUpdateRequest
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}

		cfg := h.configManager.Get()
		next := cfg.Utility
		if req.Enabled != nil {
			next.Enabled = *req.Enabled
		}
		if req.TimeoutMs != nil {
			next.TimeoutMs = *req.TimeoutMs
		}
		if req.RetryAttempts != nil {
			next.RetryAttempts = *req.RetryAttempts
		}
		if req.RetryDelayMs != nil {
			next.RetryDelayMs = *req.RetryDelayMs
		}
		if req.SearchProvider != nil {
			next.SearchProvider = strings.TrimSpace(*req.SearchProvider)
		}
		if req.BrowserControlProvider != nil {
			next.BrowserControlProvider = strings.TrimSpace(*req.BrowserControlProvider)
		}
		if req.PlaywrightBrowser != nil {
			next.PlaywrightBrowser = strings.TrimSpace(*req.PlaywrightBrowser)
		}
		if req.PlaywrightExecutable != nil {
			next.PlaywrightExecutable = strings.TrimSpace(*req.PlaywrightExecutable)
		}
		if req.BraveAPIKey != nil {
			next.BraveAPIKey = strings.TrimSpace(*req.BraveAPIKey)
		}
		if req.WeatherProvider != nil {
			next.WeatherProvider = strings.TrimSpace(*req.WeatherProvider)
		}
		if req.WeatherGeocodingURL != nil {
			next.WeatherGeocodingURL = strings.TrimSpace(*req.WeatherGeocodingURL)
		}
		if req.WeatherForecastURL != nil {
			next.WeatherForecastURL = strings.TrimSpace(*req.WeatherForecastURL)
		}
		if req.WebFetchMaxResponseSize != nil {
			next.WebFetchMaxResponseSize = *req.WebFetchMaxResponseSize
		}
		if req.BrowserMaxResponseSize != nil {
			next.BrowserMaxResponseSize = *req.BrowserMaxResponseSize
		}
		if req.BrowserAllowedDomains != nil {
			next.BrowserAllowedDomains = append([]string{}, (*req.BrowserAllowedDomains)...)
		}
		if req.BlockPrivateHosts != nil {
			next.BlockPrivateHosts = *req.BlockPrivateHosts
		}
		if req.UserAgent != nil {
			next.UserAgent = strings.TrimSpace(*req.UserAgent)
		}

		cfg.Utility = next
		if err := h.configManager.Update(cfg); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid utility settings", err)
			return
		}
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		if h.utilitySettingsReloader != nil {
			h.utilitySettingsReloader()
		}

		saved := h.configManager.Get()
		orihttp.WriteJSON(w, map[string]any{
			"success": true,
			"utility": toUtilitySettingsResponse(saved.Utility),
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func toUtilitySettingsResponse(settings config.UtilitySettings) utilitySettingsResponse {
	return utilitySettingsResponse{
		Enabled:                 settings.Enabled,
		TimeoutMs:               settings.TimeoutMs,
		RetryAttempts:           settings.RetryAttempts,
		RetryDelayMs:            settings.RetryDelayMs,
		SearchProvider:          settings.SearchProvider,
		BrowserControlProvider:  settings.BrowserControlProvider,
		PlaywrightBrowser:       settings.PlaywrightBrowser,
		PlaywrightExecutable:    settings.PlaywrightExecutable,
		WeatherProvider:         settings.WeatherProvider,
		WeatherGeocodingURL:     settings.WeatherGeocodingURL,
		WeatherForecastURL:      settings.WeatherForecastURL,
		WebFetchMaxResponseSize: settings.WebFetchMaxResponseSize,
		BrowserMaxResponseSize:  settings.BrowserMaxResponseSize,
		BrowserAllowedDomains:   append([]string{}, settings.BrowserAllowedDomains...),
		BlockPrivateHosts:       settings.BlockPrivateHosts,
		UserAgent:               settings.UserAgent,
		HasBraveAPIKey:          strings.TrimSpace(settings.BraveAPIKey) != "",
		BraveAPIKeyMasked:       maskUtilityAPIKey(settings.BraveAPIKey),
	}
}

func maskUtilityAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if len(apiKey) < 12 {
		return "***"
	}
	return apiKey[:6] + "***..." + apiKey[len(apiKey)-4:]
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
	providerNames := []string{"openai", "codex", "claude_code", "claude", "gemini", "ollama"}

	for _, name := range providerNames {
		provider, err := h.llmFactory.GetProvider(name)

		var providerModels []ProviderModel
		var displayName string
		var providerType string
		var requiresKey bool
		var available bool

		if err != nil {
			// Provider not registered (likely missing credentials or CLI)
			// Mark as unavailable and return empty models list
			available = false
			displayName = getProviderDisplayName(name)
			providerType = "cloud"
			requiresKey = providerRequiresKey(name)
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
				if name == "codex" {
					// Codex (CLI) usage is billed through the CLI integration path,
					// so we intentionally hide per-model API pricing on the models page.
					pricing = ""
				} else if pricingInfo != nil {
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

	case "claude", "claude_code":
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
	case "codex":
		tier := categorizeModel(provider, modelName)
		switch tier {
		case "tool-calling":
			return []string{"tool-calling", "general"}
		case "general":
			// Treat Codex mini as both tool-calling and general so it can be used
			// for lightweight agents while remaining available in general flows.
			return []string{"tool-calling", "general", "orchestration"}
		default:
			return []string{"research", "orchestration"}
		}

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
	case "codex":
		lowerName := strings.ToLower(modelName)
		if strings.Contains(lowerName, "nano") {
			return "tool-calling"
		}
		if strings.Contains(lowerName, "mini") {
			return "general"
		}
		// Standard and max Codex variants are best treated as research-tier.
		return "research"
	case "claude", "claude_code":
		// Haiku is the lightweight model for tool calling
		if strings.Contains(modelName, "haiku") {
			return "tool-calling"
		}
		// Sonnet 4.5 and 4 are general purpose
		if modelName == "claude-sonnet-4-5" || modelName == "claude-sonnet-4" {
			return "general"
		}
		if modelName == "sonnet" {
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
	case "codex":
		return "OpenAI Codex (CLI)"
	case "claude_code":
		return "Claude Code (CLI)"
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

func providerRequiresKey(name string) bool {
	switch name {
	case "openai", "claude", "gemini":
		return true
	default:
		return false
	}
}

// SystemModelRequest represents a request to update the system model
type SystemModelRequest struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// SystemModelResponse represents the system model configuration
type SystemModelResponse struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Configured      bool   `json:"configured"`
}

// AvailableModelOption represents UI metadata for a model option.
// The model ID remains the source of truth for API requests.
type AvailableModelOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// SystemModelHandler handles system model configuration
func (h *Handler) SystemModelHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		provider, model := h.configManager.GetSystemModel()
		reasoningEffort := ""
		if strings.EqualFold(provider, "codex") && model != "" {
			reasoningEffort = h.configManager.GetSystemReasoningEffort()
		}
		response := SystemModelResponse{
			Provider:        provider,
			Model:           model,
			ReasoningEffort: reasoningEffort,
			Configured:      h.configManager.IsSystemModelConfigured(),
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
		reasoningEffort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort))

		// If clearing the system model (both empty), allow it
		if provider == "" && model == "" {
			if err := h.configManager.SetSystemModel("", ""); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to clear system model", err)
				return
			}
			if err := h.configManager.SetSystemReasoningEffort(""); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Failed to clear system reasoning", err)
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

		// Persist reasoning effort for Codex only.
		if provider == "codex" {
			if reasoningEffort == "" {
				reasoningEffort = "medium"
			}
			if err := h.configManager.SetSystemReasoningEffort(reasoningEffort); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid system reasoning configuration", err)
				return
			}
		} else {
			if err := h.configManager.SetSystemReasoningEffort(""); err != nil {
				orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid system reasoning configuration", err)
				return
			}
		}

		// Save configuration
		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		// Ensure the system assistant agent exists and is aligned with the new system model.
		// Without this, chat won't work until a server restart.
		if err := agenthttp.EnsureSystemAssistantAgentWithSystemModel(h.store, provider, model); err != nil {
			logger.Warn("Failed to ensure system assistant agent after system model update", logger.Fields{
				"provider": provider,
				"model":    model,
				"error":    err,
			})
		}

		savedReasoning := ""
		if provider == "codex" {
			savedReasoning = h.configManager.GetSystemReasoningEffort()
		}
		orihttp.WriteJSON(w, SystemModelResponse{
			Provider:        provider,
			Model:           model,
			ReasoningEffort: savedReasoning,
			Configured:      true,
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
			"provider":      providerName,
			"available":     false,
			"models":        []string{},
			"model_options": []AvailableModelOption{},
			"message":       "Provider not configured. Please add credentials first.",
		})
		return
	}

	models := provider.DefaultModels()
	orihttp.WriteJSON(w, map[string]interface{}{
		"provider":      providerName,
		"available":     true,
		"models":        models,
		"model_options": buildAvailableModelOptions(providerName, models),
	})
}

func buildAvailableModelOptions(providerName string, models []string) []AvailableModelOption {
	options := make([]AvailableModelOption, 0, len(models))

	switch providerName {
	case "claude_code":
		metadata := map[string]AvailableModelOption{
			"opus": {
				Label:       "Opus",
				Description: "Most capable for complex work",
			},
			"sonnet": {
				Label:       "Sonnet",
				Description: "Best for everyday tasks",
			},
			"haiku": {
				Label:       "Haiku",
				Description: "Fastest for quick answers",
				Recommended: true,
			},
		}

		for _, modelName := range models {
			key := strings.ToLower(strings.TrimSpace(modelName))
			option := AvailableModelOption{
				ID:    modelName,
				Label: modelName,
			}
			if meta, ok := metadata[key]; ok {
				option.Label = meta.Label
				option.Description = meta.Description
				option.Recommended = meta.Recommended
			}
			options = append(options, option)
		}
	default:
		for _, modelName := range models {
			options = append(options, AvailableModelOption{
				ID:    modelName,
				Label: modelName,
			})
		}
	}

	return options
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
			if *req.ClaudeEnabled {
				// Register Claude provider if discovery works
				apiKey := h.configManager.GetAnthropicAPIKey()
				if apiKey != "" {
					claudeProvider := llm.NewClaudeProvider(llm.ProviderConfig{
						APIKey: apiKey,
					})
					h.llmFactory.Register("claude", claudeProvider)
				}
			}
		}
		var codexExchangeStatus string // legacy response field used by the settings UI
		if req.CodexEnabled != nil {
			// This toggle controls external Codex agent/skills visibility only.
			// Codex model-provider availability is handled independently at startup.
			h.configManager.SetExternalAgentsCodexEnabled(*req.CodexEnabled)
		}

		if err := h.configManager.Save(); err != nil {
			orihttp.InternalError(w, err.Error())
			return
		}

		orihttp.WriteJSON(w, map[string]interface{}{
			"claude_enabled":        h.configManager.GetExternalAgentsClaudeEnabled(),
			"codex_enabled":         h.configManager.GetExternalAgentsCodexEnabled(),
			"codex_exchange_status": codexExchangeStatus,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
