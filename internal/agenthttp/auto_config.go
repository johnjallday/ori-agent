package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AutoConfigHandler handles auto-configuration requests for new agents
type AutoConfigHandler struct {
	llmFactory    *llm.Factory
	configManager *config.Manager
}

// NewAutoConfigHandler creates a new AutoConfigHandler
func NewAutoConfigHandler(llmFactory *llm.Factory, configManager *config.Manager) *AutoConfigHandler {
	return &AutoConfigHandler{
		llmFactory:    llmFactory,
		configManager: configManager,
	}
}

// AutoConfigRequest represents the request to auto-configure an agent
type AutoConfigRequest struct {
	Description string `json:"description"`
}

// AutoConfigResponse represents the auto-generated configuration
type AutoConfigResponse struct {
	AgentName          string   `json:"agent_name"`
	Description        string   `json:"description"`
	AgentType          string   `json:"agent_type"`
	Model              string   `json:"model"`
	Provider           string   `json:"provider"`
	Temperature        float64  `json:"temperature"`
	SystemPrompt       string   `json:"system_prompt"`
	RecommendedPlugins []string `json:"recommended_plugins,omitempty"`
	Reasoning          string   `json:"reasoning,omitempty"`
}

// LLMAvailabilityResponse represents the response for LLM availability check
type LLMAvailabilityResponse struct {
	Available             bool     `json:"available"`
	Providers             []string `json:"providers"`
	SystemModelConfigured bool     `json:"system_model_configured"`
	SystemProvider        string   `json:"system_provider,omitempty"`
	SystemModel           string   `json:"system_model,omitempty"`
	Message               string   `json:"message,omitempty"`
}

// CheckLLMAvailabilityHandler returns whether any LLM provider is configured and system model is set
func (h *AutoConfigHandler) CheckLLMAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	providers := h.llmFactory.ListProviders()
	availableProviders := make([]string, 0)

	for _, p := range providers {
		availableProviders = append(availableProviders, p.Name)
	}

	// Check if system model is configured
	systemModelConfigured := h.configManager.IsSystemModelConfigured()
	systemProvider, systemModel := h.configManager.GetSystemModel()

	available, availabilityMessage := h.checkSystemModelAvailability(systemProvider, systemModel)

	response := LLMAvailabilityResponse{
		Available:             available,
		Providers:             availableProviders,
		SystemModelConfigured: systemModelConfigured,
		SystemProvider:        systemProvider,
		SystemModel:           systemModel,
	}

	if !available {
		if len(availableProviders) == 0 {
			response.Message = "No LLM provider configured. Please set up an API key (OpenAI, Anthropic, or Gemini) or install Ollama."
		} else if !systemModelConfigured {
			response.Message = "System model not configured. Please configure a system model in Settings to use auto-config."
		} else {
			response.Message = availabilityMessage
		}
	}

	orihttp.WriteJSON(w, response)
}

func (h *AutoConfigHandler) checkSystemModelAvailability(systemProvider, systemModel string) (bool, string) {
	if strings.TrimSpace(systemProvider) == "" || strings.TrimSpace(systemModel) == "" {
		return false, "System model not configured. Please configure a system model in Settings to use auto-config."
	}

	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		return false, fmt.Sprintf(
			"Configured system model %q for provider %q is not available. Update Settings or configure that provider before using auto-config.",
			systemModel,
			systemProvider,
		)
	}

	if checker, ok := result.Provider.(llm.ModelPresenceChecker); ok && !checker.HasModel(result.Model) {
		return false, unavailableLocalModelMessage(systemProvider)
	}

	return true, ""
}

func unavailableLocalModelMessage(providerName string) string {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "ollama":
		return "Configured Ollama system model is unavailable. Make sure the Ollama server is running and the selected model is installed."
	case "lmstudio":
		return "Configured LM Studio system model is unavailable. Make sure the LM Studio server is running and the selected model is loaded."
	case "mlx_lm":
		return "Configured MLX-LM system model is unavailable. Make sure mlx_lm.server is running and serving the selected model."
	default:
		return "Configured local system model is unavailable. Make sure the local model server is running and the selected model is available."
	}
}

// AutoConfigHandler handles the auto-configuration request
func (h *AutoConfigHandler) AutoConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req AutoConfigRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Description) == "" {
		_ = orihttp.RespondBadRequest(w, "Description is required")
		return
	}

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
	systemReasoningEffort := h.configManager.GetSystemReasoningEffort()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		if errors.Is(err, llm.ErrSystemModelNotConfigured) {
			orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
				"System model not configured. Please configure a system model in Settings to use auto-config.", err)
		} else {
			orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
				"System model provider not available: "+err.Error(), err)
		}
		return
	}

	// Generate auto-config using the configured system model
	config, err := h.generateAutoConfig(r.Context(), result.Provider, result.Model, systemReasoningEffort, req.Description)
	if err != nil {
		logger.Warn("Auto-config generation failed; using defaults", logger.Fields{
			"provider": systemProvider,
			"model":    systemModel,
			"error":    err,
		})
		// Return defaults on failure
		config = h.getDefaultConfig()
		config.Description = resolveAutoConfigDescription("", req.Description, config.AgentName)
		config.Reasoning = "Auto-config failed, using defaults: " + err.Error()
	}

	orihttp.WriteJSON(w, config)
}

// generateAutoConfig uses LLM to analyze the description and generate configuration
func (h *AutoConfigHandler) generateAutoConfig(ctx context.Context, provider llm.Provider, model, reasoningEffort, description string) (*AutoConfigResponse, error) {
	systemPrompt := `You are an AI agent configuration assistant. Based on the user's description, generate optimal configuration as a JSON object.

IMPORTANT: All string values must be on a single line. Do not use literal newlines in strings - use \n for line breaks if needed.

Required JSON fields:
- agent_name: A short, descriptive name for the agent (e.g., "Weather Assistant", "Code Reviewer")
- description: A short, polished 1-2 sentence description for the agent details field
- agent_type: One of "tool-calling" (for tool/plugin tasks), "general" (balanced), "orchestration" (multi-agent coordination), or "research" (complex reasoning)
- model: Choose a model that matches the requested role. For orchestration agents, prefer the currently configured system model when it fits. Valid families include OpenAI, Codex, Claude Code, Claude, Gemini, Ollama, LM Studio, and MLX-LM.
- provider: One of "openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", or "mlx_lm" based on model
- temperature: 0.0-0.3 for precise tasks, 0.4-0.7 for balanced, 0.7-1.0 for creative
- system_prompt: A concise system prompt for this agent (single line, use \n for breaks)
- recommended_plugins: Array of plugin keywords that would be useful (e.g., ["weather", "math", "file", "web", "calendar"])
- reasoning: Brief explanation (single line)

Example:
{"agent_name":"Weather Assistant","description":"Provides current conditions, forecasts, and weather-related guidance with clear, reliable answers.","agent_type":"tool-calling","model":"gpt-4.1-nano","provider":"openai","temperature":0.2,"system_prompt":"You are a weather assistant that provides accurate weather information.","recommended_plugins":["weather"],"reasoning":"Tool-calling for API-based weather lookups."}

If the request describes multi-agent coordination, return "orchestration" as the agent_type.`

	userMessage := fmt.Sprintf("Configure an agent for the following purpose:\n\n%s", description)

	// Create a context with timeout
	// Use a longer timeout for local LLM providers (Ollama) which may need to load models
	// and perform inference locally, which is slower than cloud APIs
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		Temperature:  0.3, // Low temperature for consistent config generation
		MaxTokens:    1000,
	})

	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	// Parse the JSON response
	var config AutoConfigResponse
	responseText := strings.TrimSpace(resp.Content)

	// Try to extract JSON if wrapped in markdown code blocks
	if strings.HasPrefix(responseText, "```") {
		lines := strings.Split(responseText, "\n")
		var jsonLines []string
		inJSON := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inJSON = !inJSON
				continue
			}
			if inJSON {
				jsonLines = append(jsonLines, line)
			}
		}
		responseText = strings.Join(jsonLines, "\n")
	}

	if err := json.Unmarshal([]byte(responseText), &config); err != nil {
		// Try to fix common LLM JSON issues (literal newlines in strings)
		fixedJSON := fixMalformedJSON(responseText)
		if err2 := json.Unmarshal([]byte(fixedJSON), &config); err2 != nil {
			return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
		}
	}

	// Validate and sanitize the response
	config = h.validateAndSanitizeConfig(config)
	config.Description = resolveAutoConfigDescription(config.Description, description, config.AgentName)

	return &config, nil
}

// validateAndSanitizeConfig ensures the config values are valid
func (h *AutoConfigHandler) validateAndSanitizeConfig(config AutoConfigResponse) AutoConfigResponse {
	// Validate agent type
	validTypes := map[string]bool{"tool-calling": true, "general": true, "orchestration": true, "research": true}
	if !validTypes[config.AgentType] {
		config.AgentType = "tool-calling"
	}

	systemProvider := ""
	systemModel := ""
	if h != nil && h.configManager != nil {
		systemProvider, systemModel = h.configManager.GetSystemModel()
	}

	// Validate provider
	validProviders := map[string]bool{"openai": true, "codex": true, "claude_code": true, "claude": true, "gemini": true, "ollama": true, "lmstudio": true, "mlx_lm": true}
	if !validProviders[config.Provider] {
		config.Provider = ""
	}

	// Validate temperature
	if config.Temperature < 0 {
		config.Temperature = 0
	} else if config.Temperature > 2 {
		config.Temperature = 1
	}

	// For orchestration agents, always prefer the configured system model over
	// whatever the LLM suggested. The LLM tends to echo the example model
	// (gpt-4.1-nano) from its prompt, which isn't suitable for coordination —
	// and the system model represents the user's explicit choice for
	// orchestration-grade work.
	if config.AgentType == "orchestration" && systemModel != "" {
		config.Model = systemModel
		if systemProvider != "" {
			config.Provider = systemProvider
		}
	}

	// Ensure model is set
	if config.Model == "" {
		switch config.AgentType {
		case "tool-calling":
			config.Model = "gpt-4.1-nano"
		case "general":
			config.Model = "gpt-5"
		case "orchestration":
			// systemModel was empty; fall back to a capable default.
			config.Model = "gpt-5"
		case "research":
			config.Model = "gpt-5"
		}
	}

	if config.Provider == "" {
		switch config.AgentType {
		case "orchestration":
			if systemProvider != "" {
				config.Provider = systemProvider
			}
		}
	}

	if config.Provider == "" {
		config.Provider = "openai"
	}

	// Ensure system prompt is set
	if config.SystemPrompt == "" {
		config.SystemPrompt = "You are a helpful AI assistant."
	}

	return config
}

// getDefaultConfig returns default configuration when auto-config fails
func (h *AutoConfigHandler) getDefaultConfig() *AutoConfigResponse {
	return &AutoConfigResponse{
		AgentName:    "New Agent",
		Description:  "Helpful AI assistant for general tasks.",
		AgentType:    "tool-calling",
		Model:        "gpt-4.1-nano",
		Provider:     "openai",
		Temperature:  0.7,
		SystemPrompt: "You are a helpful AI assistant.",
	}
}

func resolveAutoConfigDescription(generatedDescription, sourceDescription, agentName string) string {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	}

	if description := normalize(generatedDescription); description != "" {
		return description
	}
	if description := normalize(sourceDescription); description != "" {
		return description
	}
	if name := normalize(agentName); name != "" {
		return fmt.Sprintf("%s helps with specialized tasks.", name)
	}
	return "Helpful AI assistant for general tasks."
}

// fixMalformedJSON attempts to fix common LLM JSON issues like literal newlines in strings
func fixMalformedJSON(input string) string {
	var result strings.Builder
	inString := false
	escaped := false

	for i := 0; i < len(input); i++ {
		c := input[i]

		if escaped {
			result.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inString {
			result.WriteByte(c)
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			result.WriteByte(c)
			continue
		}

		// If we're inside a string and encounter a literal newline, escape it
		if inString && (c == '\n' || c == '\r') {
			if c == '\r' {
				// Skip \r, handle \n separately
				continue
			}
			result.WriteString("\\n")
			continue
		}

		result.WriteByte(c)
	}

	return result.String()
}
