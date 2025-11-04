package agentstudio

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// LLMTaskHandler executes tasks using the LLM system
type LLMTaskHandler struct {
	agentStore store.Store
	llmFactory *llm.Factory
}

// NewLLMTaskHandler creates a new LLM-based task handler
func NewLLMTaskHandler(agentStore store.Store, llmFactory *llm.Factory) *LLMTaskHandler {
	return &LLMTaskHandler{
		agentStore: agentStore,
		llmFactory: llmFactory,
	}
}

// ExecuteTask executes a task by sending it to the agent's LLM
func (h *LLMTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	log.Printf("🤖 Executing task %s for agent %s", task.ID, agentName)

	// Get the agent
	ag, ok := h.agentStore.GetAgent(agentName)
	if !ok {
		return "", fmt.Errorf("agent %s not found", agentName)
	}

	// Determine which provider to use based on model
	providerName := h.getProviderForModel(ag.Settings.Model)
	provider, err := h.llmFactory.GetProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("failed to get LLM provider: %w", err)
	}

	// Build the prompt for the task
	prompt := h.buildTaskPrompt(task, ag)

	// Prepare messages
	messages := []llm.Message{
		llm.NewUserMessage(prompt),
	}

	// Add system prompt if available
	if ag.Settings.SystemPrompt != "" {
		messages = append([]llm.Message{llm.NewSystemMessage(ag.Settings.SystemPrompt)}, messages...)
	}

	// Convert tools (plugins) to LLM format
	tools := h.convertPluginsToTools(ag)

	// Call the LLM
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:       ag.Settings.Model,
		Messages:    messages,
		Temperature: ag.Settings.Temperature,
		Tools:       tools,
	})

	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	// Handle tool calls if present
	if len(resp.ToolCalls) > 0 {
		log.Printf("🔧 Task %s triggered %d tool call(s)", task.ID, len(resp.ToolCalls))

		// Execute tool calls
		toolResults := h.executeToolCalls(ctx, ag, resp.ToolCalls)

		// Build result summary
		var resultBuilder strings.Builder
		if resp.Content != "" {
			resultBuilder.WriteString(resp.Content)
			resultBuilder.WriteString("\n\n")
		}

		resultBuilder.WriteString("Tool Results:\n")
		for _, tr := range toolResults {
			resultBuilder.WriteString(fmt.Sprintf("- %s: %s\n", tr.Name, tr.Result))
		}

		return resultBuilder.String(), nil
	}

	// Return the response content
	if resp.Content == "" {
		return "Task completed (no output)", nil
	}

	return resp.Content, nil
}

// buildTaskPrompt creates a prompt for the task
func (h *LLMTaskHandler) buildTaskPrompt(task Task, ag *agent.Agent) string {
	var prompt strings.Builder

	prompt.WriteString(fmt.Sprintf("# Task Assignment\n\n"))
	prompt.WriteString(fmt.Sprintf("You have been assigned a task in a collaborative studio.\n\n"))
	prompt.WriteString(fmt.Sprintf("**Task ID**: %s\n", task.ID))
	prompt.WriteString(fmt.Sprintf("**From**: %s\n", task.From))
	prompt.WriteString(fmt.Sprintf("**Priority**: %d/5\n\n", task.Priority))

	prompt.WriteString(fmt.Sprintf("## Task Description\n\n%s\n\n", task.Description))

	if len(task.Context) > 0 {
		prompt.WriteString("## Context\n\n")
		for key, value := range task.Context {
			prompt.WriteString(fmt.Sprintf("- **%s**: %v\n", key, value))
		}
		prompt.WriteString("\n")
	}

	if task.Timeout > 0 {
		prompt.WriteString(fmt.Sprintf("**Time Limit**: %v\n\n", task.Timeout))
	}

	prompt.WriteString("Please complete this task to the best of your ability. ")
	prompt.WriteString("Use any available tools if needed. ")
	prompt.WriteString("Provide a clear, concise response with your findings or results.")

	return prompt.String()
}

// getProviderForModel determines which LLM provider to use
func (h *LLMTaskHandler) getProviderForModel(model string) string {
	modelLower := strings.ToLower(model)

	// Check for Claude models
	if strings.Contains(modelLower, "claude") {
		return "claude"
	}

	// Check for Ollama models
	if strings.Contains(modelLower, "llama") ||
		strings.Contains(modelLower, "mistral") ||
		strings.Contains(modelLower, "mixtral") ||
		strings.Contains(modelLower, "phi") ||
		strings.Contains(modelLower, "qwen") ||
		strings.Contains(modelLower, "codellama") ||
		strings.Contains(modelLower, "orca") ||
		strings.Contains(modelLower, "vicuna") ||
		strings.Contains(modelLower, "neural-chat") ||
		strings.Contains(modelLower, "starling") {
		return "ollama"
	}

	// Default to OpenAI
	return "openai"
}

// convertPluginsToTools converts agent plugins to LLM tools
func (h *LLMTaskHandler) convertPluginsToTools(ag *agent.Agent) []llm.Tool {
	var tools []llm.Tool

	for _, plugin := range ag.Plugins {
		if plugin.Tool == nil {
			continue
		}

		def := plugin.Tool.Definition()

		// Extract description
		desc := def.Description.String()

		tools = append(tools, llm.Tool{
			Name:        def.Name,
			Description: desc,
			Parameters:  def.Parameters,
		})
	}

	return tools
}

// toolCallResult represents the result of a tool call
type toolCallResult struct {
	Name   string
	Result string
	Error  error
}

// executeToolCalls executes tool calls and returns results
func (h *LLMTaskHandler) executeToolCalls(ctx context.Context, ag *agent.Agent, toolCalls []llm.ToolCall) []toolCallResult {
	results := make([]toolCallResult, len(toolCalls))

	for i, tc := range toolCalls {
		results[i] = h.executeToolCall(ctx, ag, tc)
	}

	return results
}

// executeToolCall executes a single tool call
func (h *LLMTaskHandler) executeToolCall(ctx context.Context, ag *agent.Agent, toolCall llm.ToolCall) toolCallResult {
	log.Printf("🔧 Executing tool: %s", toolCall.Name)

	// Find the tool
	var tool pluginapi.Tool
	for _, plugin := range ag.Plugins {
		if plugin.Tool != nil && plugin.Tool.Definition().Name == toolCall.Name {
			tool = plugin.Tool
			break
		}
	}

	if tool == nil {
		return toolCallResult{
			Name:  toolCall.Name,
			Error: fmt.Errorf("tool %s not found", toolCall.Name),
		}
	}

	// Execute the tool
	result, err := tool.Call(ctx, toolCall.Arguments)
	if err != nil {
		return toolCallResult{
			Name:  toolCall.Name,
			Error: err,
		}
	}

	return toolCallResult{
		Name:   toolCall.Name,
		Result: result,
	}
}
