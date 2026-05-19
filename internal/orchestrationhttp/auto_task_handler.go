package orchestrationhttp

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
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// classifyAutoTaskError returns a user-friendly error message for context errors
func classifyAutoTaskError(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out - the AI took too long to respond. Please try again or simplify your task description."
	}
	if errors.Is(err, context.Canceled) {
		return "request was canceled - please try again."
	}

	errStr := err.Error()
	if strings.Contains(errStr, "context deadline exceeded") {
		return "request timed out - the AI took too long to respond. Please try again or simplify your task description."
	}
	if strings.Contains(errStr, "context canceled") {
		return "request was canceled - please try again."
	}

	return ""
}

// AutoTaskHandler handles auto-creation of tasks from natural language
type AutoTaskHandler struct {
	agentStore     store.Store
	workspaceStore workspace.Store
	llmFactory     *llm.Factory
	configManager  *config.Manager
}

// NewAutoTaskHandler creates a new AutoTaskHandler
func NewAutoTaskHandler(
	agentStore store.Store,
	workspaceStore workspace.Store,
	llmFactory *llm.Factory,
	configManager *config.Manager,
) *AutoTaskHandler {
	return &AutoTaskHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		llmFactory:     llmFactory,
		configManager:  configManager,
	}
}

// AutoTaskRequest represents the request to auto-create a task
type AutoTaskRequest struct {
	Description string `json:"description"`
	WorkspaceID string `json:"workspace_id"`
}

// AutoTaskResponse represents the parsed task configuration with jsonschema tags for structured output
type AutoTaskResponse struct {
	Title           string               `json:"title" jsonschema_description:"A concise title for the task"`
	Details         string               `json:"details" jsonschema_description:"Additional details or context, can be empty"`
	AgentName       string               `json:"agent_name" jsonschema_description:"Name of the agent to assign from the available list, or empty string"`
	Priority        int                  `json:"priority" jsonschema:"minimum=1,maximum=5" jsonschema_description:"Priority level 1-5, where 1 is highest"`
	Tasks           []AutoTaskStep       `json:"tasks" jsonschema_description:"Multi-step workflow tasks. Empty array if the request is a single task. When provided, create tasks in order and honor depends_on relationships."`
	Schedule        *ScheduleConfig      `json:"schedule" jsonschema_description:"Schedule configuration, null if no schedule"`
	ScheduleEnabled bool                 `json:"schedule_enabled" jsonschema_description:"True if a schedule was specified"`
	ScheduleName    string               `json:"schedule_name" jsonschema_description:"Descriptive name for the schedule like 'Daily at 9am'"`
	ResultStorage   *ResultStorageConfig `json:"result_storage" jsonschema_description:"Result storage configuration, null if no storage requested"`
	Reasoning       string               `json:"reasoning" jsonschema_description:"Brief explanation of how the request was interpreted"`
}

// AutoTaskStep represents a single step in a multi-task workflow.
type AutoTaskStep struct {
	ID        string   `json:"id" jsonschema_description:"Short unique ID for this step (e.g., weather_fetch)"`
	Title     string   `json:"title" jsonschema_description:"Short task title for this step"`
	Details   string   `json:"details" jsonschema_description:"Additional context for this step, can be empty"`
	AgentName string   `json:"agent_name" jsonschema_description:"Name of the agent to assign from the available list, or empty string"`
	Priority  int      `json:"priority" jsonschema:"minimum=1,maximum=5" jsonschema_description:"Priority level 1-5, where 1 is highest"`
	DependsOn []string `json:"depends_on" jsonschema_description:"List of step IDs this step depends on. Empty array if this step has no dependencies."`
}

// ScheduleConfig for auto task with jsonschema tags
type ScheduleConfig struct {
	Type            string `json:"type" jsonschema:"enum=once,enum=daily,enum=weekly,enum=interval" jsonschema_description:"Schedule type"`
	Time            string `json:"time" jsonschema_description:"Time in HH:MM format for daily/weekly schedules"`
	DayOfWeek       int    `json:"day_of_week" jsonschema:"minimum=0,maximum=6" jsonschema_description:"Day of week 0-6 where 0 is Sunday"`
	IntervalMinutes int    `json:"interval_minutes" jsonschema_description:"Minutes between runs for interval schedules"`
	OnceAt          string `json:"once_at" jsonschema_description:"ISO datetime for one-time scheduled tasks"`
}

// ResultStorageConfig for auto task with jsonschema tags
type ResultStorageConfig struct {
	Enabled     bool   `json:"enabled" jsonschema_description:"True if result storage was requested"`
	StoreNodeID string `json:"store_node_id" jsonschema_description:"Store node ID to save results to, empty string if not specified"`
	FilePath    string `json:"file_path" jsonschema_description:"Custom file path to save results, empty string if not specified"`
	Format      string `json:"format" jsonschema:"enum=text,enum=json,enum=markdown,enum=csv" jsonschema_description:"Output format: text, json, markdown, or csv"`
	WriteMode   string `json:"write_mode" jsonschema:"enum=new_file,enum=append" jsonschema_description:"Use append only when adding each run to the same CSV dataset"`
}

// Schema for structured output - generated at init time
var autoTaskResponseSchema = llm.GenerateSchema[AutoTaskResponse]()

// HandleAutoTask handles POST /api/orchestration/tasks/auto-parse
func (h *AutoTaskHandler) HandleAutoTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req AutoTaskRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Description) == "" {
		_ = orihttp.RespondBadRequest(w, "description is required")
		return
	}

	// Get all agents with their descriptions for intelligent matching
	agentNames := h.agentStore.ListAgents()
	agents := make([]string, 0, len(agentNames))
	agentDescriptions := make(map[string]string)
	for _, name := range agentNames {
		agents = append(agents, name)
		if ag, found := h.agentStore.GetAgent(name); found && ag.Metadata != nil && ag.Metadata.Description != "" {
			agentDescriptions[name] = ag.Metadata.Description
		}
	}

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
	systemReasoningEffort := h.configManager.GetSystemReasoningEffort()
	logger.Info("Auto-task using system model", logger.Fields{
		"provider": systemProvider,
		"model":    systemModel,
	})

	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		logger.Error("System model not available for auto-task", logger.Fields{"error": err})
		_ = orihttp.RespondServiceUnavailable(w, "System model not configured")
		return
	}

	// Parse the task description using LLM
	taskConfig, err := h.parseTaskDescription(r.Context(), result.Provider, systemProvider, result.Model, systemReasoningEffort, req.Description, agents, agentDescriptions)
	if err != nil {
		logger.Error("Auto-task parsing failed", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to parse task description: "+err.Error())
		return
	}

	orihttp.WriteJSON(w, taskConfig)
}

// parseTaskDescription uses LLM to parse natural language into task configuration
func (h *AutoTaskHandler) parseTaskDescription(
	ctx context.Context,
	provider llm.Provider,
	providerName string,
	model string,
	reasoningEffort string,
	description string,
	agents []string,
	agentDescriptions map[string]string,
) (*AutoTaskResponse, error) {

	// Get current time for context
	now := time.Now()
	currentTime := now.Format("2006-01-02 15:04:05")
	currentDay := now.Weekday().String()

	// Build agent list with descriptions for intelligent matching
	var agentList string
	if len(agents) == 0 {
		agentList = "Available agents: (none)"
	} else {
		agentList = "Available agents:\n"
		for _, name := range agents {
			if desc, ok := agentDescriptions[name]; ok {
				agentList += fmt.Sprintf("- %s: %s\n", name, desc)
			} else {
				agentList += fmt.Sprintf("- %s\n", name)
			}
		}
	}

	systemPrompt := fmt.Sprintf(`Parse the task description and return a JSON object with EXACTLY this structure:

{
  "title": "short task title",
  "details": "additional context or empty string",
  "agent_name": "name of matching agent or empty string",
  "priority": 3,
  "tasks": [],
  "schedule_enabled": false,
  "schedule_name": "",
  "schedule": null,
  "result_storage": null,
  "reasoning": "brief explanation"
}

Current time: %s (%s)
%s

Schedule parsing rules:
- "at 6pm" or "at 18:00" -> schedule_enabled=true, schedule={"type":"once","once_at":"ISO datetime"}
- "every day at 9am" -> schedule_enabled=true, schedule={"type":"daily","time":"09:00"}
- "every Monday at 10am" -> schedule_enabled=true, schedule={"type":"weekly","day_of_week":1,"time":"10:00"}
- "every 30 minutes" -> schedule_enabled=true, schedule={"type":"interval","interval_minutes":30}
- No time mentioned -> schedule_enabled=false, schedule=null

Result storage: set result_storage={"enabled":true,"format":"text|json|markdown|csv","write_mode":"new_file|append"} only if user mentions saving results. Use write_mode="append" and format="csv" when the user asks to append runs to the same CSV file.

Agent assignment: Match the task to an agent based on their description. If no agent matches, use empty string.

Multi-step tasks:
- If the request has multiple distinct steps (e.g., "do X then Y"), populate "tasks" with each step.
- Each task must include: id, title, details, agent_name, priority, depends_on.
- Use depends_on to indicate ordering (e.g., step2 depends_on ["step1"]).
- When tasks are provided, keep the top-level fields as a brief summary (title/details) or mirror the first step.
- Apply schedule fields to the first step only.
- Apply result_storage to the final step only.

IMPORTANT: Always return the exact JSON structure shown above. Never return error messages or different formats.`, currentTime, currentDay, agentList)

	userMessage := description

	// Create a context with timeout (60s to handle slow LLM responses)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	logger.Info("Auto-task parsing request", logger.Fields{
		"model":       model,
		"provider":    providerName,
		"description": description,
	})

	// Try structured output for providers that support it
	if structuredProvider, ok := provider.(llm.StructuredOutputProvider); ok {
		return h.parseWithStructuredOutput(ctx, structuredProvider, model, reasoningEffort, systemPrompt, userMessage, agents)
	}

	// Fallback to regular chat for non-OpenAI providers
	return h.parseWithRegularChat(ctx, provider, model, reasoningEffort, systemPrompt, userMessage, agents)
}

// parseWithStructuredOutput uses a provider's structured output feature
func (h *AutoTaskHandler) parseWithStructuredOutput(
	ctx context.Context,
	provider llm.StructuredOutputProvider,
	model string,
	reasoningEffort string,
	systemPrompt string,
	userMessage string,
	agents []string,
) (*AutoTaskResponse, error) {
	logger.Info("Using structured output for auto-task parsing", logger.Fields{})

	resp, err := provider.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		SchemaName:   "auto_task_response",
		Schema:       autoTaskResponseSchema,
	})

	if err != nil {
		if friendlyMsg := classifyAutoTaskError(err); friendlyMsg != "" {
			return nil, fmt.Errorf("%s", friendlyMsg)
		}
		return nil, fmt.Errorf("structured output request failed: %w", err)
	}

	logger.Info("Auto-task structured output response", logger.Fields{
		"content_length": len(resp.Content),
	})

	if resp.Content == "" {
		return nil, fmt.Errorf("LLM returned empty response")
	}

	payload := strings.TrimSpace(resp.Content)
	if strings.HasPrefix(payload, "```") {
		payload = extractJSONFromCodeFence(payload)
	}

	var taskConfig AutoTaskResponse
	if err := json.Unmarshal([]byte(payload), &taskConfig); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (content: %s)", err, resp.Content)
	}

	// Validate and sanitize
	taskConfig = h.validateTaskConfig(taskConfig, agents)
	return &taskConfig, nil
}

func extractJSONFromCodeFence(content string) string {
	lines := strings.Split(content, "\n")
	var jsonLines []string
	inJSON := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inJSON = !inJSON
			continue
		}
		if inJSON {
			jsonLines = append(jsonLines, line)
		}
	}
	if len(jsonLines) == 0 {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(strings.Join(jsonLines, "\n"))
}

// parseWithRegularChat uses standard chat completion (fallback for non-OpenAI)
func (h *AutoTaskHandler) parseWithRegularChat(
	ctx context.Context,
	provider llm.Provider,
	model string,
	reasoningEffort string,
	systemPrompt string,
	userMessage string,
	agents []string,
) (*AutoTaskResponse, error) {
	logger.Info("Using regular chat for auto-task parsing", logger.Fields{})

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt + "\n\nRespond with valid JSON only.",
		Temperature:  0.2,
		MaxTokens:    2000,
	})

	if err != nil {
		if friendlyMsg := classifyAutoTaskError(err); friendlyMsg != "" {
			return nil, fmt.Errorf("%s", friendlyMsg)
		}
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	logger.Info("Auto-task LLM response", logger.Fields{
		"content_length": len(resp.Content),
		"content":        resp.Content,
	})

	if resp.Content == "" {
		return nil, fmt.Errorf("LLM returned empty response - check model configuration")
	}

	// Parse the JSON response
	var taskConfig AutoTaskResponse
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

	if err := json.Unmarshal([]byte(responseText), &taskConfig); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
	}

	// Validate and sanitize
	taskConfig = h.validateTaskConfig(taskConfig, agents)
	return &taskConfig, nil
}

// validateTaskConfig ensures the task config values are valid
func (h *AutoTaskHandler) validateTaskConfig(config AutoTaskResponse, agents []string) AutoTaskResponse {
	// Validate multi-step tasks if provided
	if len(config.Tasks) > 0 {
		seen := make(map[string]bool, len(config.Tasks))
		for i := range config.Tasks {
			step := &config.Tasks[i]

			if strings.TrimSpace(step.Title) == "" {
				step.Title = fmt.Sprintf("Task %d", i+1)
			}

			if strings.TrimSpace(step.ID) == "" {
				step.ID = fmt.Sprintf("step-%d", i+1)
			}

			if seen[step.ID] {
				step.ID = fmt.Sprintf("%s-%d", step.ID, i+1)
			}
			seen[step.ID] = true

			if step.Priority < 1 || step.Priority > 5 {
				step.Priority = 3
			}

			if step.AgentName != "" {
				found := false
				for _, agent := range agents {
					if strings.EqualFold(agent, step.AgentName) {
						step.AgentName = agent
						found = true
						break
					}
				}
				if !found {
					step.AgentName = ""
				}
			}
		}
	}

	// Ensure title is not empty
	if strings.TrimSpace(config.Title) == "" {
		if len(config.Tasks) > 0 {
			config.Title = config.Tasks[0].Title
		} else {
			config.Title = "New Task"
		}
	}

	// Validate priority (1-5)
	if config.Priority < 1 || config.Priority > 5 {
		if len(config.Tasks) > 0 {
			config.Priority = config.Tasks[0].Priority
		} else {
			config.Priority = 3
		}
	}

	// Validate agent name exists
	if config.AgentName != "" {
		found := false
		for _, agent := range agents {
			if strings.EqualFold(agent, config.AgentName) {
				config.AgentName = agent // Use exact case
				found = true
				break
			}
		}
		if !found {
			config.AgentName = "" // Clear invalid agent
		}
	}

	// Validate schedule configuration
	if config.Schedule != nil {
		validTypes := map[string]bool{"once": true, "daily": true, "weekly": true, "interval": true}
		if !validTypes[config.Schedule.Type] {
			config.Schedule = nil
			config.ScheduleEnabled = false
		}
	}

	// Validate result storage configuration
	if config.ResultStorage != nil {
		// Validate format - default to "text" if invalid
		validFormats := map[string]bool{"text": true, "json": true, "markdown": true, "csv": true}
		if !validFormats[config.ResultStorage.Format] {
			config.ResultStorage.Format = "text"
		}
		if config.ResultStorage.WriteMode != "append" {
			config.ResultStorage.WriteMode = "new_file"
		}
		if config.ResultStorage.WriteMode == "append" {
			config.ResultStorage.Format = "csv"
		}

		// If not enabled and no storage details, set to nil
		if !config.ResultStorage.Enabled &&
			config.ResultStorage.StoreNodeID == "" &&
			config.ResultStorage.FilePath == "" {
			config.ResultStorage = nil
		}
	}

	return config
}
