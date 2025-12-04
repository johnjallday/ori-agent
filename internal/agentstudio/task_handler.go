package agentstudio

import (
	"context"
	"fmt"
	"os"

	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// LLMTaskHandler executes tasks using the LLM system
type LLMTaskHandler struct {
	agentStore     store.Store
	llmFactory     *llm.Factory
	workspaceStore Store     // Added to access workspace attachments
	eventBus       *EventBus // Optional event bus for publishing execution events
}

// NewLLMTaskHandler creates a new LLM-based task handler
func NewLLMTaskHandler(agentStore store.Store, llmFactory *llm.Factory, workspaceStore Store) *LLMTaskHandler {
	return &LLMTaskHandler{
		agentStore:     agentStore,
		llmFactory:     llmFactory,
		workspaceStore: workspaceStore,
	}
}

// SetEventBus sets the event bus for publishing execution progress events
func (h *LLMTaskHandler) SetEventBus(eventBus *EventBus) {
	h.eventBus = eventBus
}

// ExecuteTask executes a task by sending it to the agent's LLM
func (h *LLMTaskHandler) ExecuteTask(ctx context.Context, agentName string, task Task) (string, error) {
	logger.Debug("🤖 Executing task for agent", logger.Fields{"task_id": task.ID, "agentName": agentName})

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

	// Use a task-specific system prompt that's more conservative about tool use
	// The agent's system prompt may encourage aggressive tool use which is inappropriate for workspace tasks
	taskSystemPrompt := "You are a helpful AI assistant completing a task in a collaborative workspace. "
	taskSystemPrompt += "You have access to tools, but only use them when they are clearly necessary to complete the specific task. "
	taskSystemPrompt += "For simple questions, greetings, or informational requests, respond naturally without calling tools. "
	taskSystemPrompt += "Be thoughtful and precise in your responses."

	messages = append([]llm.Message{llm.NewSystemMessage(taskSystemPrompt)}, messages...)

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
		logger.Debug("Task triggered tool call(s)", logger.Fields{"task_id": task.ID, "toolcalls)": len(resp.ToolCalls)})

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

// substitutePlaceholders replaces placeholders like {result}, {input}, {result1}, {result2} in task description
func (h *LLMTaskHandler) substitutePlaceholders(task Task) string {
	description := task.Description

	// Get input task results from context
	inputTaskResults, hasInputResults := task.Context["input_task_results"]
	if !hasInputResults {
		return description // No substitution needed if no input results
	}

	resultsMap, ok := inputTaskResults.(map[string]string)
	if !ok || len(resultsMap) == 0 {
		return description
	}

	// Extract actual result values (strip "Tool Results:" prefix if present)
	var resultValues []string
	for _, result := range resultsMap {
		cleanResult := h.cleanToolResult(result)
		if cleanResult != "" {
			resultValues = append(resultValues, cleanResult)
		}
	}

	if len(resultValues) == 0 {
		return description
	}

	// Replace placeholders
	// {result} or {input} - use the first result (or combined if multiple)
	if strings.Contains(description, "{result}") || strings.Contains(description, "{input}") {
		combinedResult := strings.Join(resultValues, ", ")
		description = strings.ReplaceAll(description, "{result}", combinedResult)
		description = strings.ReplaceAll(description, "{input}", combinedResult)
	}

	// {result1}, {result2}, etc. - use specific results by index
	for i, result := range resultValues {
		placeholder := fmt.Sprintf("{result%d}", i+1)
		description = strings.ReplaceAll(description, placeholder, result)
	}

	return description
}

// cleanToolResult extracts the actual result from tool output format
func (h *LLMTaskHandler) cleanToolResult(result string) string {
	// Remove "Tool Results:" prefix and clean up
	result = strings.TrimSpace(result)

	// If it starts with "Tool Results:", extract the actual result
	if strings.HasPrefix(result, "Tool Results:") {
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			// Look for lines like "- tool_name: actual_result"
			if strings.HasPrefix(line, "-") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					actualResult := strings.TrimSpace(parts[1])
					if actualResult != "" {
						return actualResult
					}
				}
			}
		}
	}

	// If not in tool format, return as-is
	return result
}

// buildTaskPrompt creates a prompt for the task
func (h *LLMTaskHandler) buildTaskPrompt(task Task, ag *agent.Agent) string {
	var prompt strings.Builder

	prompt.WriteString("# Task Assignment\n\n")
	prompt.WriteString("You have been assigned a task in a collaborative studio.\n\n")
	prompt.WriteString(fmt.Sprintf("**Task ID**: %s\n", task.ID))
	prompt.WriteString(fmt.Sprintf("**From**: %s\n", task.From))
	prompt.WriteString(fmt.Sprintf("**Priority**: %d/5\n\n", task.Priority))

	// Process task description with placeholder substitution
	processedDescription := h.substitutePlaceholders(task)
	prompt.WriteString(fmt.Sprintf("## Task Description\n\n%s\n\n", processedDescription))

	// Include attachments if any are connected to this task
	attachmentContents := h.getAttachedFileContents(task)
	if len(attachmentContents) > 0 {
		prompt.WriteString("## Attached Files\n\n")
		prompt.WriteString("The following files are attached to this task:\n\n")
		for _, att := range attachmentContents {
			prompt.WriteString(fmt.Sprintf("### %s\n\n", att.Title))
			if att.FilePath != "" {
				prompt.WriteString(fmt.Sprintf("**File**: `%s`\n\n", att.FilePath))
			}
			if att.Body != "" {
				prompt.WriteString(fmt.Sprintf("**Note**: %s\n\n", att.Body))
			}
			if att.Content != "" {
				prompt.WriteString("**Content**:\n```\n")
				prompt.WriteString(att.Content)
				prompt.WriteString("\n```\n\n")
			}
		}
	}

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
	prompt.WriteString("**Important**: Only use tools when they are explicitly necessary to complete the task. ")
	prompt.WriteString("For informational requests, meta-commands (like /tools, /help), or simple questions, ")
	prompt.WriteString("respond directly without calling tools. ")
	prompt.WriteString("Provide a clear, concise response with your findings or results.")

	return prompt.String()
}

// AttachmentContent holds attachment info and file contents
type AttachmentContent struct {
	Title    string
	Body     string
	FilePath string
	Content  string
}

// getAttachedFileContents finds attachments connected to this task and reads their file contents
func (h *LLMTaskHandler) getAttachedFileContents(task Task) []AttachmentContent {
	// Get the workspace to access attachments and connections
	workspace, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil {
		logger.Error("Failed to get workspace for attachment reading", logger.Fields{"workspace_id": task.WorkspaceID, "error": err})
		return nil
	}

	var attachmentContents []AttachmentContent

	// Find connections from attachments to this task
	if workspace.Layout != nil && workspace.Layout.WorkflowConnections != nil {
		for _, conn := range workspace.Layout.WorkflowConnections {
			// Check if connection points to this task
			if conn.To == task.ID {
				// Check if source is an attachment
				for _, att := range workspace.Attachments {
					if att.ID == conn.From {
						attContent := AttachmentContent{
							Title: att.Title,
							Body:  att.Body,
						}

						// Read file contents if a file path is provided
						if att.File != nil && att.File.URL != "" {
							filePath := att.File.URL
							attContent.FilePath = filePath

							// Try to read the file
							content, err := os.ReadFile(filePath)
							if err != nil {
								logger.Warn("Failed to read attachment file", logger.Fields{"file": filePath, "error": err})
								attContent.Content = fmt.Sprintf("[Failed to read file: %v]", err)
							} else {
								attContent.Content = string(content)
							}
						}

						attachmentContents = append(attachmentContents, attContent)
					}
				}
			}
		}
	}

	return attachmentContents
}

// formatInputResults formats input task results based on the combination mode
func (h *LLMTaskHandler) formatInputResults(prompt *strings.Builder, task Task, inputTaskResults interface{}) {
	resultsMap, ok := inputTaskResults.(map[string]string)
	if !ok {
		return
	}

	// Include input task results as context
	prompt.WriteString("## Input from Previous Tasks\n\n")
	for taskID, result := range resultsMap {
		prompt.WriteString(fmt.Sprintf("**Task %s Result:**\n```\n%s\n```\n\n", taskID, result))
	}
}

// getProviderForModel determines which LLM provider to use (dynamic detection)
func (h *LLMTaskHandler) getProviderForModel(model string) string {
	// Check for Claude models (prefix-based)
	if strings.HasPrefix(model, "claude-") {
		return "claude"
	}

	// Check if Ollama has this model (dynamic detection)
	if ollamaProvider, err := h.llmFactory.GetProvider("ollama"); err == nil {
		if ollamaProv, ok := ollamaProvider.(*llm.OllamaProvider); ok {
			if ollamaProv.HasModel(model) {
				logger.Info("Model '' found in Ollama, using Ollama provider", logger.Fields{"model": model})
				return "ollama"
			}
		}
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
	logger.Debug("Executing tool", logger.Fields{"tool": toolCall.Name})

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
