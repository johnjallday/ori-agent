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
	eventBus   *EventBus // Optional event bus for publishing execution events
}

// NewLLMTaskHandler creates a new LLM-based task handler
func NewLLMTaskHandler(agentStore store.Store, llmFactory *llm.Factory) *LLMTaskHandler {
	return &LLMTaskHandler{
		agentStore: agentStore,
		llmFactory: llmFactory,
	}
}

// SetEventBus sets the event bus for publishing execution progress events
func (h *LLMTaskHandler) SetEventBus(eventBus *EventBus) {
	h.eventBus = eventBus
}

// ExecuteTask executes a task by sending it to the agent's LLM
func (h *LLMTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	log.Printf("🤖 Executing task %s for agent %s", task.ID, agentName)

	// Publish thinking event
	if h.eventBus != nil {
		event := NewTaskEvent(EventTaskThinking, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
			"message": "Agent is analyzing the task...",
		})
		h.eventBus.Publish(event)
	}

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
		toolResults := h.executeToolCalls(ctx, ag, agentName, task, resp.ToolCalls)

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

	prompt.WriteString("# Task Assignment\n\n")
	prompt.WriteString("You have been assigned a task in a collaborative studio.\n\n")
	prompt.WriteString(fmt.Sprintf("**Task ID**: %s\n", task.ID))
	prompt.WriteString(fmt.Sprintf("**From**: %s\n", task.From))
	prompt.WriteString(fmt.Sprintf("**Priority**: %d/5\n\n", task.Priority))

	prompt.WriteString(fmt.Sprintf("## Task Description\n\n%s\n\n", task.Description))

	// Handle input task results specially for better formatting
	inputTaskResults, hasInputResults := task.Context["input_task_results"]
	if hasInputResults {
		h.formatInputResults(&prompt, task, inputTaskResults)
	}

	// Include other context fields
	if len(task.Context) > 0 {
		hasOtherContext := false
		for key := range task.Context {
			if key != "input_task_results" {
				hasOtherContext = true
				break
			}
		}

		if hasOtherContext {
			prompt.WriteString("## Additional Context\n\n")
			for key, value := range task.Context {
				if key != "input_task_results" {
					prompt.WriteString(fmt.Sprintf("- **%s**: %v\n", key, value))
				}
			}
			prompt.WriteString("\n")
		}
	}

	if task.Timeout > 0 {
		prompt.WriteString(fmt.Sprintf("**Time Limit**: %v\n\n", task.Timeout))
	}

	prompt.WriteString("Please complete this task to the best of your ability. ")
	prompt.WriteString("Use any available tools if needed. ")
	prompt.WriteString("Provide a clear, concise response with your findings or results.")

	return prompt.String()
}

// formatInputResults formats input task results based on the combination mode
func (h *LLMTaskHandler) formatInputResults(prompt *strings.Builder, task Task, inputTaskResults interface{}) {
	resultsMap, ok := inputTaskResults.(map[string]string)
	if !ok {
		return
	}

	// Default mode or empty mode - use existing behavior
	mode := task.ResultCombinationMode
	if mode == "" || mode == string(CombineModeDefault) {
		prompt.WriteString("## Input from Previous Tasks\n\n")
		for taskID, result := range resultsMap {
			prompt.WriteString(fmt.Sprintf("**Task %s Result:**\n```\n%s\n```\n\n", taskID, result))
		}
		return
	}

	// For all other modes, we provide specific instructions on how to combine
	prompt.WriteString("## Input from Previous Tasks\n\n")

	// First, list all the input results
	for taskID, result := range resultsMap {
		prompt.WriteString(fmt.Sprintf("**Task %s Result:**\n```\n%s\n```\n\n", taskID, result))
	}

	// Then add mode-specific instructions
	switch ResultCombinationMode(mode) {
	case CombineModeAppend:
		prompt.WriteString("**Instruction:** Use the above results as additional context for your task. Build upon these results.\n\n")

	case CombineModeMerge:
		prompt.WriteString("**Instruction:** Merge and synthesize the above results into a coherent whole. Combine the information from all previous tasks, eliminating redundancies and creating a unified output.\n\n")

	case CombineModeSummarize:
		prompt.WriteString("**Instruction:** Create a comprehensive summary of all the above results. Extract key points and insights from each task result and present them in a concise, organized format.\n\n")

	case CombineModeCompare:
		prompt.WriteString("**Instruction:** Compare and contrast the above results. Identify similarities, differences, contradictions, and complementary information across all task results.\n\n")

	case CombineModeCustom:
		if task.CombinationInstruction != "" {
			prompt.WriteString(fmt.Sprintf("**Combination Instruction:** %s\n\n", task.CombinationInstruction))
		} else {
			prompt.WriteString("**Instruction:** Use the above results to inform your task completion.\n\n")
		}

	default:
		// Fallback to default behavior
		prompt.WriteString("**Instruction:** Use the above results as context for your task.\n\n")
	}
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

		// Definition is already in generic pluginapi.Tool format
		tools = append(tools, llm.Tool{
			Name:        def.Name,
			Description: def.Description,
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
func (h *LLMTaskHandler) executeToolCalls(ctx context.Context, ag *agent.Agent, agentName string, task Task, toolCalls []llm.ToolCall) []toolCallResult {
	results := make([]toolCallResult, len(toolCalls))

	for i, tc := range toolCalls {
		results[i] = h.executeToolCall(ctx, ag, agentName, task, tc)
	}

	return results
}

// executeToolCall executes a single tool call
func (h *LLMTaskHandler) executeToolCall(ctx context.Context, ag *agent.Agent, agentName string, task Task, toolCall llm.ToolCall) toolCallResult {
	log.Printf("🔧 Executing tool: %s", toolCall.Name)

	// Publish tool call event
	if h.eventBus != nil {
		event := NewTaskEvent(EventTaskToolCall, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
			"tool_name": toolCall.Name,
			"arguments": toolCall.Arguments,
		})
		h.eventBus.Publish(event)
	}

	// Find the tool
	var tool pluginapi.PluginTool
	for _, plugin := range ag.Plugins {
		if plugin.Tool != nil && plugin.Tool.Definition().Name == toolCall.Name {
			tool = plugin.Tool
			break
		}
	}

	if tool == nil {
		result := toolCallResult{
			Name:  toolCall.Name,
			Error: fmt.Errorf("tool %s not found", toolCall.Name),
		}

		// Publish tool result event (error)
		if h.eventBus != nil {
			event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, map[string]interface{}{
				"tool_name": toolCall.Name,
				"success":   false,
				"error":     result.Error.Error(),
			})
			h.eventBus.Publish(event)
		}

		return result
	}

	// Execute the tool
	result, err := tool.Call(ctx, toolCall.Arguments)

	// Publish tool result event
	if h.eventBus != nil {
		data := map[string]interface{}{
			"tool_name": toolCall.Name,
			"success":   err == nil,
		}
		if err != nil {
			data["error"] = err.Error()
		} else {
			// Truncate result if too long for event
			resultPreview := result
			if len(resultPreview) > 200 {
				resultPreview = resultPreview[:200] + "..."
			}
			data["result_preview"] = resultPreview
		}
		event := NewTaskEvent(EventTaskToolResult, task.WorkspaceID, task.ID, agentName, data)
		h.eventBus.Publish(event)
	}

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
