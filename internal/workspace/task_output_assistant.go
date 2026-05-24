package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

const taskOutputAssistantMaxRawBytes = 12000

// NormalizeTaskOutputSpec asks the task's assigned agent to convert arbitrary
// task output into exactly one JSON row matching the active output spec.
func (h *LLMTaskHandler) NormalizeTaskOutputSpec(ctx context.Context, task Task, rawResult string) (string, error) {
	prompt := buildTaskOutputNormalizationPrompt(task, rawResult)
	return h.callTaskOutputAssistant(ctx, task, prompt)
}

// RepairTaskOutputSpec asks the assigned agent for one corrected row after the
// deterministic projection/validation step found errors.
func (h *LLMTaskHandler) RepairTaskOutputSpec(ctx context.Context, task Task, rawResult string, invalidRow map[string]any, validationErrors []TaskValidationError) (string, error) {
	prompt := buildTaskOutputRepairPrompt(task, rawResult, invalidRow, validationErrors)
	return h.callTaskOutputAssistant(ctx, task, prompt)
}

func (h *LLMTaskHandler) callTaskOutputAssistant(ctx context.Context, task Task, prompt string) (string, error) {
	if h == nil || h.llmFactory == nil {
		return "", fmt.Errorf("task output assistant is not configured")
	}
	agentName := strings.TrimSpace(task.To)
	if agentName == "" {
		return "", fmt.Errorf("task has no assigned agent")
	}
	ag, err := h.resolveExecutionAgent(agentName, task)
	if err != nil {
		return "", err
	}
	providerName := h.getProviderForAgent(ag.Settings.Provider, ag.Settings.Model)
	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("failed to get LLM provider: %w", err)
	}
	modelName := h.normalizeModelForProvider(providerName, ag.Settings.Model)

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: modelName,
		Messages: []llm.Message{
			llm.NewSystemMessage("You normalize task results into strict JSON for storage. Return only one JSON object. Do not include markdown fences or commentary."),
			llm.NewUserMessage(prompt),
		},
		Temperature:     0,
		ReasoningEffort: ag.Settings.EffectiveReasoningEffort(providerName),
	})
	if err != nil {
		if friendlyMsg := classifyContextError(err); friendlyMsg != "" {
			return "", fmt.Errorf("%s", friendlyMsg)
		}
		return "", err
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", fmt.Errorf("assistant returned an empty normalized row")
	}
	return trimTaskOutputAssistantJSON(content), nil
}

func buildTaskOutputNormalizationPrompt(task Task, rawResult string) string {
	var prompt strings.Builder
	prompt.WriteString("Convert the raw task result into exactly one JSON object matching this approved output spec.\n")
	prompt.WriteString("If the raw result contains a list, aggregate or select the single best row implied by the task; do not return an array.\n")
	prompt.WriteString("Output spec:\n")
	prompt.WriteString(taskOutputSpecPromptJSON(task.OutputSpec))
	prompt.WriteString("\n\nTask:\n")
	prompt.WriteString(strings.TrimSpace(task.Description))
	prompt.WriteString("\n\nRaw result:\n")
	prompt.WriteString(limitTaskOutputAssistantText(rawResult))
	return strings.TrimSpace(prompt.String())
}

func buildTaskOutputRepairPrompt(task Task, rawResult string, invalidRow map[string]any, validationErrors []TaskValidationError) string {
	var prompt strings.Builder
	prompt.WriteString("Repair this normalized task output row so it matches the approved output spec and CSV projection.\n")
	prompt.WriteString("Return exactly one corrected JSON object. Do not return an array.\n")
	prompt.WriteString("Output spec:\n")
	prompt.WriteString(taskOutputSpecPromptJSON(task.OutputSpec))
	prompt.WriteString("\n\nValidation errors:\n")
	prompt.WriteString(taskOutputSpecPromptJSON(validationErrors))
	prompt.WriteString("\n\nInvalid row:\n")
	prompt.WriteString(taskOutputSpecPromptJSON(invalidRow))
	prompt.WriteString("\n\nOriginal raw result:\n")
	prompt.WriteString(limitTaskOutputAssistantText(rawResult))
	return strings.TrimSpace(prompt.String())
}

func taskOutputSpecPromptJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func limitTaskOutputAssistantText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= taskOutputAssistantMaxRawBytes {
		return value
	}
	return value[:taskOutputAssistantMaxRawBytes] + "\n...[truncated]"
}

func trimTaskOutputAssistantJSON(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= 2 {
		return trimmed
	}
	var body []string
	for _, line := range lines[1:] {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			break
		}
		body = append(body, line)
	}
	if len(body) == 0 {
		return trimmed
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}
