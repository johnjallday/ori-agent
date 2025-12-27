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
	AgentType    string  `json:"agent_type"`
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Temperature  float64 `json:"temperature"`
	SystemPrompt string  `json:"system_prompt"`
	Reasoning    string  `json:"reasoning,omitempty"`
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

	// For auto-config to be available, we need BOTH:
	// 1. At least one LLM provider configured
	// 2. System model configured
	available := len(availableProviders) > 0 && systemModelConfigured

	response := LLMAvailabilityResponse{
		Available:             available,
		Providers:             availableProviders,
		SystemModelConfigured: systemModelConfigured,
		SystemProvider:        systemProvider,
		SystemModel:           systemModel,
	}

	if !available {
		if len(availableProviders) == 0 {
			response.Message = "No LLM provider configured. Please set up an API key (OpenAI or Anthropic) or install Ollama."
		} else if !systemModelConfigured {
			response.Message = "System model not configured. Please configure a system model in Settings to use auto-config."
		}
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
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Description) == "" {
		orihttp.RespondBadRequest(w, "Description is required")
		return
	}

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
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
	config, err := h.generateAutoConfig(r.Context(), result.Provider, result.Model, req.Description)
	if err != nil {
		logger.Error("Auto-config generation failed", logger.Fields{"error": err})
		// Return defaults on failure
		config = h.getDefaultConfig()
		config.Reasoning = "Auto-config failed, using defaults: " + err.Error()
	}

	orihttp.WriteJSON(w, config)
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
