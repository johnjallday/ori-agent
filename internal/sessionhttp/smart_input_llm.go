package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type smartInputLLMResponse struct {
	Decision   SmartInputDecision `json:"decision"`
	Confidence float64            `json:"confidence"`
	Reasoning  string             `json:"reasoning,omitempty"`
}

func classifySmartInputLLM(ctx context.Context, provider llm.Provider, model, reasoningEffort string, input string) (smartInputHeuristicResult, error) {
	systemPrompt := `You classify a user's input into exactly one intent: "task" or "chat".

Return ONLY a JSON object with:
- decision: "task" or "chat"
- confidence: number between 0 and 1
- reasoning: brief explanation

Guidance:
- "task" means an actionable to-do item or instruction.
- "chat" means a question, discussion, or request for explanation.
- If unclear, choose the best guess but lower the confidence.`

	userMessage := fmt.Sprintf("Input:\n%s", strings.TrimSpace(input))

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		Temperature:  0.2,
		MaxTokens:    200,
	})
	if err != nil {
		return smartInputHeuristicResult{}, fmt.Errorf("LLM request failed: %w", err)
	}

	responseText := llm.StripCodeFence(resp.Content)

	var parsed smartInputLLMResponse
	if err := json.Unmarshal([]byte(responseText), &parsed); err != nil {
		return smartInputHeuristicResult{}, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
	}

	switch parsed.Decision {
	case SmartInputDecisionTask, SmartInputDecisionChat:
	default:
		return smartInputHeuristicResult{}, fmt.Errorf("invalid LLM decision: %s", parsed.Decision)
	}

	if parsed.Confidence < 0 {
		parsed.Confidence = 0
	}
	if parsed.Confidence > 1 {
		parsed.Confidence = 1
	}

	return smartInputHeuristicResult{
		Decision:   parsed.Decision,
		Confidence: parsed.Confidence,
	}, nil
}

func classifySmartInputWithSystemModel(ctx context.Context, llmFactory *llm.Factory, configManager *config.Manager, input string) (smartInputHeuristicResult, error) {
	systemProvider, systemModel := configManager.GetSystemModel()
	systemReasoningEffort := configManager.GetSystemReasoningEffort()
	result, err := llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		return smartInputHeuristicResult{}, err
	}

	return classifySmartInputLLM(ctx, result.Provider, result.Model, systemReasoningEffort, input)
}
