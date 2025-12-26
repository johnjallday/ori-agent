package agenthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AutoConfigHandler handles auto-configuration requests for new agents
type AutoConfigHandler struct {
	llmFactory *llm.Factory
}

// NewAutoConfigHandler creates a new AutoConfigHandler
func NewAutoConfigHandler(llmFactory *llm.Factory) *AutoConfigHandler {
	return &AutoConfigHandler{
		llmFactory: llmFactory,
	}
}

// AutoConfigRequest represents the request to auto-configure an agent
type AutoConfigRequest struct {
	Description string `json:"description"`
}

// AutoConfigResponse represents the auto-generated configuration
type AutoConfigResponse struct {
	AgentType    string  `json:"agent_type"`
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Temperature  float64 `json:"temperature"`
	SystemPrompt string  `json:"system_prompt"`
	Reasoning    string  `json:"reasoning,omitempty"`
}

// LLMAvailabilityResponse represents the response for LLM availability check
type LLMAvailabilityResponse struct {
	Available bool     `json:"available"`
	Providers []string `json:"providers"`
	Message   string   `json:"message,omitempty"`
}

// CheckLLMAvailabilityHandler returns whether any LLM provider is configured
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

	response := LLMAvailabilityResponse{
		Available: len(availableProviders) > 0,
		Providers: availableProviders,
	}

	if !response.Available {
		response.Message = "No LLM provider configured. Please set up an API key (OpenAI or Anthropic) or install Ollama to use auto-config."
	}

	orihttp.WriteJSON(w, response)
}

// AutoConfigHandler handles the auto-configuration request
func (h *AutoConfigHandler) AutoConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req AutoConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.RespondBadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Description) == "" {
		orihttp.RespondBadRequest(w, "Description is required")
		return
	}

	// Check if any LLM provider is available
	providers := h.llmFactory.ListProviders()
	if len(providers) == 0 {
		orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
			"No LLM provider available. Please configure an API key or install Ollama.", nil)
		return
	}

	// Select the best available provider for analysis
	provider, model := h.selectAnalysisProvider(providers)
	if provider == nil {
		orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
			"Unable to find a suitable LLM provider for auto-config.", nil)
		return
	}

	// Generate auto-config using LLM
	config, err := h.generateAutoConfig(r.Context(), provider, model, req.Description)
	if err != nil {
		logger.Error("Auto-config generation failed", logger.Fields{"error": err})
		// Return defaults on failure
		config = h.getDefaultConfig()
		config.Reasoning = "Auto-config failed, using defaults: " + err.Error()
	}

	orihttp.WriteJSON(w, config)
}

// selectAnalysisProvider selects the best provider and model for analysis
// Prefers faster/cheaper models since this is just for configuration analysis
func (h *AutoConfigHandler) selectAnalysisProvider(providers []llm.ProviderInfo) (llm.Provider, string) {
	// Priority order: claude (haiku), openai (mini), ollama
	preferredProviders := []struct {
		name  string
		model string
	}{
		{"claude", "claude-3-haiku-20240307"},
		{"openai", "gpt-4o-mini"},
		{"ollama", ""}, // Will use first available model
	}

	for _, pref := range preferredProviders {
		for _, p := range providers {
			if p.Name == pref.name {
				provider, err := h.llmFactory.GetProvider(pref.name)
				if err != nil {
					continue
				}
				model := pref.model
				if model == "" && len(p.Models) > 0 {
					model = p.Models[0]
				}
				return provider, model
			}
		}
	}

	// Fallback: use first available provider
	if len(providers) > 0 {
		provider, err := h.llmFactory.GetProvider(providers[0].Name)
		if err == nil {
			model := ""
			if len(providers[0].Models) > 0 {
				model = providers[0].Models[0]
			}
			return provider, model
		}
	}

	return nil, ""
}

// generateAutoConfig uses LLM to analyze the description and generate configuration
func (h *AutoConfigHandler) generateAutoConfig(ctx context.Context, provider llm.Provider, model, description string) (*AutoConfigResponse, error) {
	systemPrompt := `You are an AI agent configuration assistant. Based on the user's description of what they want their agent to do, recommend the optimal configuration.

You must respond with a valid JSON object (and nothing else) with these fields:
- agent_type: One of "tool-calling", "general", or "research"
  - "tool-calling": Best for agents that primarily execute tools/plugins (e.g., file operations, API calls, automation tasks). Cheapest option.
  - "general": Best for balanced agents that need both conversation and tool use (e.g., assistants, chatbots with capabilities).
  - "research": Best for complex reasoning, analysis, and research tasks. Most capable but most expensive.
- model: The recommended model based on the agent type and task complexity
  - For tool-calling: "gpt-4o-mini" or "claude-3-haiku-20240307"
  - For general: "gpt-4o-mini", "gpt-4o", or "claude-3-5-sonnet-20241022"
  - For research: "gpt-4o", "claude-3-5-sonnet-20241022", or "claude-sonnet-4-5"
- provider: "openai" or "claude" based on the model chosen
- temperature: A float between 0.0 and 1.0
  - Use 0.0-0.3 for precise, deterministic tasks (coding, data processing)
  - Use 0.4-0.7 for balanced tasks (general assistance)
  - Use 0.7-1.0 for creative tasks (writing, brainstorming)
- system_prompt: A tailored system prompt for this agent based on its intended purpose
- reasoning: Brief explanation of why you chose these settings

Example response:
{
  "agent_type": "tool-calling",
  "model": "gpt-4o-mini",
  "provider": "openai",
  "temperature": 0.2,
  "system_prompt": "You are a helpful assistant that executes file operations efficiently.",
  "reasoning": "Tool-calling type selected because the task involves file automation. Low temperature for consistent results."
}`

	userMessage := fmt.Sprintf("Configure an agent for the following purpose:\n\n%s", description)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
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
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
	}

	// Validate and sanitize the response
	config = h.validateAndSanitizeConfig(config)

	return &config, nil
}

// validateAndSanitizeConfig ensures the config values are valid
func (h *AutoConfigHandler) validateAndSanitizeConfig(config AutoConfigResponse) AutoConfigResponse {
	// Validate agent type
	validTypes := map[string]bool{"tool-calling": true, "general": true, "research": true}
	if !validTypes[config.AgentType] {
		config.AgentType = "tool-calling"
	}

	// Validate provider
	validProviders := map[string]bool{"openai": true, "claude": true, "ollama": true}
	if !validProviders[config.Provider] {
		config.Provider = "openai"
	}

	// Validate temperature
	if config.Temperature < 0 {
		config.Temperature = 0
	} else if config.Temperature > 2 {
		config.Temperature = 1
	}

	// Ensure model is set
	if config.Model == "" {
		switch config.AgentType {
		case "tool-calling":
			config.Model = "gpt-4o-mini"
		case "general":
			config.Model = "gpt-4o"
		case "research":
			config.Model = "gpt-4o"
		}
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
		AgentType:    "tool-calling",
		Model:        "gpt-4o-mini",
		Provider:     "openai",
		Temperature:  0.7,
		SystemPrompt: "You are a helpful AI assistant.",
	}
}
