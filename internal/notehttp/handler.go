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

// AssistRequest is a selection-based AI request. Used by the inline AI Assist
// sidebar in the notes UI. Unlike GenerateRequest it operates on a text
// selection within an existing note rather than producing a whole note.
type AssistRequest struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	// Action is one of: expand, summarize, rewrite, counter, cite, ask.
	Action string `json:"action"`
	// Selection is the highlighted text the action should operate on.
	Selection string `json:"selection"`
	// Context is the rest of the note, sent so the model can ground itself.
	Context string `json:"context,omitempty"`
	// Prompt is required for action="ask" and ignored otherwise.
	Prompt string `json:"prompt,omitempty"`
	// History (optional) is used by refinement requests — prior assistant outputs
	// for the same selection so the model can iterate.
	History []AssistHistoryEntry `json:"history,omitempty"`
}

// AssistHistoryEntry represents a previous suggestion in a refinement chain.
type AssistHistoryEntry struct {
	Prompt string `json:"prompt"`
	Output string `json:"output"`
}

// AssistResponse is the model's reply.
type AssistResponse struct {
	Content string `json:"content"`
}

// assistActionPromptTemplates maps each preset action to a short instruction the
// model receives in addition to the selection + context. They live server-side
// per PRD §9 Open Q4 so they can be tuned without a frontend release.
var assistActionPromptTemplates = map[string]string{
	"expand":    "Extend the selected passage with more detail. Stay in the same voice and tone. Do not introduce facts that contradict the surrounding context. Output ONLY the rewritten passage in markdown — no preamble, no commentary.",
	"summarize": "Produce a tighter, shorter version of the selected passage. Preserve every claim and key detail. Output ONLY the summarized passage in markdown — no preamble, no commentary.",
	"rewrite":   "Rewrite the selected passage for clarity, flow, and consistency with the surrounding context. Preserve meaning. Output ONLY the rewritten passage in markdown — no preamble, no commentary.",
	"counter":   "Generate concise counterargument bullets relating to the selected passage. Each bullet must challenge a specific claim and be defensible. Output a markdown bullet list (using `-`). No preamble.",
	"cite":      "Generate a 'Sources' sub-list with citations relevant to the selected passage. Each entry should be a markdown bullet of the form `- [Title or claim]: brief justification`. If you cannot identify reliable sources, say so explicitly in one bullet. No preamble outside the list.",
}

// AssistHandler handles POST /api/notes/assist.
func (h *Handler) AssistHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req AssistRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Selection) == "" {
		_ = orihttp.RespondBadRequest(w, "selection is required")
		return
	}
	if req.Action == "ask" {
		if strings.TrimSpace(req.Prompt) == "" {
			_ = orihttp.RespondBadRequest(w, "prompt is required for action=ask")
			return
		}
	} else if _, ok := assistActionPromptTemplates[req.Action]; !ok {
		_ = orihttp.RespondBadRequest(w, "unknown action: "+req.Action)
		return
	}

	provider, model, reasoningEffort, maxTokens, err := h.resolveProvider(req.AgentID)
	if err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusServiceUnavailable,
			"No LLM provider available. Please configure a system model in Settings.", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), GenerateTimeout)
	defer cancel()

	content, err := h.runAssist(ctx, provider, model, reasoningEffort, &req, maxTokens)
	if err != nil {
		logger.Error("Note assist failed", logger.Fields{"action": req.Action, "error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError,
			"Failed to run AI assist: "+err.Error(), err)
		return
	}

	orihttp.WriteJSON(w, AssistResponse{Content: content})
}

// resolveProvider picks the LLM provider for either an agent (if available)
// or the system fallback. Mirrors the logic in GenerateHandler.
func (h *Handler) resolveProvider(agentID string) (llm.Provider, string, string, int, error) {
	maxTokens := DefaultOutputTokens
	if agentID != "" {
		agent, ok := h.store.GetAgent(agentID)
		if ok && agent != nil && agent.Settings.Provider != "" && agent.Settings.Model != "" {
			p, err := h.llmFactory.GetProvider(agent.Settings.Provider)
			if err == nil {
				if agent.Settings.MaxOutputTokens >= MinOutputTokens {
					maxTokens = agent.Settings.MaxOutputTokens
				}
				return p, agent.Settings.Model,
					agent.Settings.EffectiveReasoningEffort(agent.Settings.Provider), maxTokens, nil
			}
		}
	}
	systemProvider, systemModel := h.configManager.GetSystemModel()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		return nil, "", "", maxTokens, err
	}
	return result.Provider, result.Model, h.configManager.GetSystemReasoningEffort(), maxTokens, nil
}

// runAssist builds the chat messages for an assist request and invokes the LLM.
// Refinement chains are passed through req.History so the model sees its prior
// outputs and the user's refinement instructions.
func (h *Handler) runAssist(ctx context.Context, provider llm.Provider, model, reasoningEffort string, req *AssistRequest, maxTokens int) (string, error) {
	systemPrompt := `You are an AI writing assistant inside a Markdown note editor. The user has highlighted a passage and asked you to act on it. Always output ONLY the new content in Markdown — never preamble, never JSON, never quoted code blocks unless they are part of the actual content.`

	var actionInstruction string
	if req.Action == "ask" {
		actionInstruction = strings.TrimSpace(req.Prompt)
	} else {
		actionInstruction = assistActionPromptTemplates[req.Action]
	}

	userText := actionInstruction + "\n\n"
	if strings.TrimSpace(req.Context) != "" {
		userText += "Surrounding note context (for grounding only — do NOT include in your output):\n---\n" + req.Context + "\n---\n\n"
	}
	userText += "Selected passage:\n---\n" + req.Selection + "\n---"

	messages := []llm.Message{{Role: llm.RoleUser, Content: userText}}
	for _, h := range req.History {
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: h.Output})
		if strings.TrimSpace(h.Prompt) != "" {
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: h.Prompt})
		}
	}

	chatReq := llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		SystemPrompt:    systemPrompt,
		Messages:        messages,
		Temperature:     0.5,
		MaxTokens:       maxTokens,
	}

	resp, err := provider.Chat(ctx, chatReq)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
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
