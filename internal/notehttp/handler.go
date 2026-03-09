package notehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
)

const (
	// GenerateTimeout is the maximum time for note generation
	GenerateTimeout = 60 * time.Second
	// MinOutputTokens is the minimum tokens required for note generation
	MinOutputTokens = 1500
	// DefaultOutputTokens is the default max tokens for note generation
	DefaultOutputTokens = 2000
)

// Handler handles note generation requests
type Handler struct {
	llmFactory    *llm.Factory
	configManager *config.Manager
	store         store.Store
}

// NewHandler creates a new note generation handler
func NewHandler(llmFactory *llm.Factory, configManager *config.Manager, store store.Store) *Handler {
	return &Handler{
		llmFactory:    llmFactory,
		configManager: configManager,
		store:         store,
	}
}

// GenerateRequest represents the request to generate note content
type GenerateRequest struct {
	Prompt      string `json:"prompt"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
}

// GenerateResponse represents the generated note content
type GenerateResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// GenerateHandler handles POST /api/notes/generate
func (h *Handler) GenerateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req GenerateRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Prompt) == "" {
		_ = orihttp.RespondBadRequest(w, "Prompt is required")
		return
	}

	// Get provider - use specified agent's settings or fall back to system model
	var provider llm.Provider
	var model string
	reasoningEffort := ""
	maxTokens := DefaultOutputTokens

	if req.AgentID != "" {
		// Try to get agent's configured provider and model
		agent, ok := h.store.GetAgent(req.AgentID)
		if ok && agent != nil && agent.Settings.Provider != "" && agent.Settings.Model != "" {
			p, err := h.llmFactory.GetProvider(agent.Settings.Provider)
			if err == nil {
				provider = p
				model = agent.Settings.Model
				reasoningEffort = agent.Settings.EffectiveReasoningEffort(agent.Settings.Provider)
				// Use agent's max output tokens if sufficient, otherwise use default
				if agent.Settings.MaxOutputTokens >= MinOutputTokens {
					maxTokens = agent.Settings.MaxOutputTokens
				}
				logger.Info("Using agent's LLM provider for note generation", logger.Fields{
					"agent":            req.AgentID,
					"provider":         agent.Settings.Provider,
					"model":            model,
					"agent_max_tokens": agent.Settings.MaxOutputTokens,
					"using_max_tokens": maxTokens,
				})
			} else {
				logger.Warn("Failed to get agent's provider, will use system model", logger.Fields{
					"agent":    req.AgentID,
					"provider": agent.Settings.Provider,
					"error":    err,
				})
			}
		} else {
			logger.Warn("Agent not found or not configured, will use system model", logger.Fields{
				"agent_id": req.AgentID,
				"found":    ok,
			})
		}
	}

	// Fall back to system model if no agent provider
	if provider == nil {
		systemProvider, systemModel := h.configManager.GetSystemModel()
		result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
		if err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
				"No LLM provider available. Please configure a system model in Settings.", err)
			return
		}
		provider = result.Provider
		model = result.Model
		reasoningEffort = h.configManager.GetSystemReasoningEffort()
		logger.Info("Using system model for note generation", logger.Fields{
			"provider":   systemProvider,
			"model":      model,
			"max_tokens": maxTokens,
		})
	}

	// Generate the note content
	ctx, cancel := context.WithTimeout(r.Context(), GenerateTimeout)
	defer cancel()

	generated, err := h.generateNoteContent(ctx, provider, model, reasoningEffort, req.Prompt, maxTokens)
	if err != nil {
		logger.Error("Note generation failed", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError,
			"Failed to generate note content: "+err.Error(), err)
		return
	}

	orihttp.WriteJSON(w, generated)
}

// generateNoteContent uses the LLM to generate note title and content
func (h *Handler) generateNoteContent(ctx context.Context, provider llm.Provider, model, reasoningEffort, prompt string, maxTokens int) (*GenerateResponse, error) {
	systemPrompt := `You are a note generation assistant. Based on the user's prompt, generate a note with a title and content.

You must respond with a valid JSON object (and nothing else) with these fields:
- title: A short, descriptive title for the note (max 100 characters)
- content: The full content of the note in markdown format

Guidelines:
- The title should be concise and capture the main topic
- The content should be well-organized with proper markdown formatting
- Use headings, lists, and code blocks where appropriate
- Be informative and helpful based on what the user requested

Example response:
{
  "title": "Meeting Notes - Project Planning",
  "content": "## Attendees\n- John\n- Jane\n\n## Discussion Points\n..."
}`

	chatReq := llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		SystemPrompt:    systemPrompt,
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: prompt,
			},
		},
		Temperature: 0.7,
		MaxTokens:   maxTokens,
	}

	logger.Info("Sending note generation request to LLM", logger.Fields{
		"model":      model,
		"max_tokens": maxTokens,
		"prompt_len": len(prompt),
	})

	resp, err := provider.Chat(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	logger.Info("LLM response received for note generation", logger.Fields{
		"content_length": len(resp.Content),
		"finish_reason":  resp.FinishReason,
	})

	// Parse the JSON response
	var result GenerateResponse
	content := strings.TrimSpace(resp.Content)

	// Handle empty response
	if content == "" {
		logger.Warn("LLM returned empty response for note generation")
		return &GenerateResponse{
			Title:   "Generated Note",
			Content: "The AI was unable to generate content. Please try again with a more specific prompt.",
		}, nil
	}

	// Try to extract JSON from the response
	jsonContent := extractJSON(content)

	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		// If JSON parsing fails, use the raw content as note content
		preview := jsonContent
		if len(preview) > 100 {
			preview = preview[:100]
		}
		logger.Warn("Failed to parse LLM response as JSON, using raw content", logger.Fields{
			"error":       err,
			"content_len": len(content),
			"extracted":   preview,
		})
		result = GenerateResponse{
			Title:   "Generated Note",
			Content: resp.Content,
		}
	}

	return &result, nil
}

// extractJSON attempts to extract a JSON object from the LLM response
func extractJSON(content string) string {
	// Remove markdown code blocks
	content = strings.TrimSpace(content)

	// Handle ```json ... ``` blocks
	if strings.HasPrefix(content, "```json") {
		content = strings.TrimPrefix(content, "```json")
		// Find the closing ```
		if idx := strings.LastIndex(content, "```"); idx != -1 {
			content = content[:idx]
		}
		content = strings.TrimSpace(content)
	} else if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```")
		// Find the closing ```
		if idx := strings.LastIndex(content, "```"); idx != -1 {
			content = content[:idx]
		}
		content = strings.TrimSpace(content)
	}

	// Try to find JSON object boundaries if not already clean JSON
	if !strings.HasPrefix(content, "{") {
		// Look for the start of JSON object
		if idx := strings.Index(content, "{"); idx != -1 {
			content = content[idx:]
		}
	}

	// Find the matching closing brace
	if strings.HasPrefix(content, "{") {
		braceCount := 0
		inString := false
		escaped := false
		for i, ch := range content {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inString {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if !inString {
				switch ch {
				case '{':
					braceCount++
				case '}':
					braceCount--
					if braceCount == 0 {
						return content[:i+1]
					}
				}
			}
		}
	}

	return content
}
