package orchestrationhttp

import (
	"context"
	"encoding/json"
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
	Schedule        *ScheduleConfig      `json:"schedule" jsonschema_description:"Schedule configuration, null if no schedule"`
	ScheduleEnabled bool                 `json:"schedule_enabled" jsonschema_description:"True if a schedule was specified"`
	ScheduleName    string               `json:"schedule_name" jsonschema_description:"Descriptive name for the schedule like 'Daily at 9am'"`
	ResultStorage   *ResultStorageConfig `json:"result_storage" jsonschema_description:"Result storage configuration, null if no storage requested"`
	Reasoning       string               `json:"reasoning" jsonschema_description:"Brief explanation of how the request was interpreted"`
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
	Format      string `json:"format" jsonschema:"enum=text,enum=json,enum=markdown" jsonschema_description:"Output format: text, json, or markdown"`
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

	// Get all agents
	agents, _ := h.agentStore.ListAgents()

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
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
	taskConfig, err := h.parseTaskDescription(r.Context(), result.Provider, systemProvider, result.Model, req.Description, agents)
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
	description string,
	agents []string,
) (*AutoTaskResponse, error) {

	// Get current time for context
	now := time.Now()
	currentTime := now.Format("2006-01-02 15:04:05")
	currentDay := now.Weekday().String()

	// Build agent list
	agentList := "Available agents: "
	if len(agents) == 0 {
		agentList += "(none)"
	} else {
		agentList += strings.Join(agents, ", ")
	}

	systemPrompt := fmt.Sprintf(`Parse the task description and extract structured information.

Current time: %s (%s)
%s

Schedule parsing rules:
- "at 6pm" or "at 18:00" -> type=once, once_at=today at that time (or tomorrow if passed)
- "every day at 9am" -> type=daily, time="09:00"
- "every Monday at 10am" -> type=weekly, day_of_week=1, time="10:00"
- "every 30 minutes" -> type=interval, interval_minutes=30
- No time mentioned -> schedule_enabled=false, schedule=null

Result storage parsing rules:
- "save to file", "save results", "store output" -> enabled=true, format based on context
- "save as json" or "output json" -> enabled=true, format="json"
- "save as markdown" or "as md" -> enabled=true, format="markdown"
- "save to /path/to/file" -> enabled=true, file_path="/path/to/file"
- No save/store mentioned -> result_storage=null

Agent assignment: Match task type to available agent names. Leave empty if no clear match.`, currentTime, currentDay, agentList)

	userMessage := description

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logger.Info("Auto-task parsing request", logger.Fields{
		"model":       model,
		"provider":    providerName,
		"description": description,
	})

	// Try structured output for OpenAI provider
	if providerName == "openai" {
		if openaiProvider, ok := provider.(*llm.OpenAIProvider); ok {
			return h.parseWithStructuredOutput(ctx, openaiProvider, model, systemPrompt, userMessage, agents)
		}
	}

	// Fallback to regular chat for non-OpenAI providers
	return h.parseWithRegularChat(ctx, provider, model, systemPrompt, userMessage, agents)
}

// parseWithStructuredOutput uses OpenAI's structured output feature
func (h *AutoTaskHandler) parseWithStructuredOutput(
	ctx context.Context,
	provider *llm.OpenAIProvider,
	model string,
	systemPrompt string,
	userMessage string,
	agents []string,
) (*AutoTaskResponse, error) {
	logger.Info("Using structured output for auto-task parsing", logger.Fields{})

	resp, err := provider.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		SchemaName:   "auto_task_response",
		Schema:       autoTaskResponseSchema,
	})

	if err != nil {
		return nil, fmt.Errorf("structured output request failed: %w", err)
	}

	logger.Info("Auto-task structured output response", logger.Fields{
		"content_length": len(resp.Content),
	})

	if resp.Content == "" {
		return nil, fmt.Errorf("LLM returned empty response")
	}

	var taskConfig AutoTaskResponse
	if err := json.Unmarshal([]byte(resp.Content), &taskConfig); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (content: %s)", err, resp.Content)
	}

	// Validate and sanitize
	taskConfig = h.validateTaskConfig(taskConfig, agents)
	return &taskConfig, nil
}

// parseWithRegularChat uses standard chat completion (fallback for non-OpenAI)
func (h *AutoTaskHandler) parseWithRegularChat(
	ctx context.Context,
	provider llm.Provider,
	model string,
	systemPrompt string,
	userMessage string,
	agents []string,
) (*AutoTaskResponse, error) {
	logger.Info("Using regular chat for auto-task parsing", logger.Fields{})

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt + "\n\nRespond with valid JSON only.",
		Temperature:  0.2,
		MaxTokens:    2000,
	})

	if err != nil {
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
	// Ensure title is not empty
	if strings.TrimSpace(config.Title) == "" {
		config.Title = "New Task"
	}

	// Validate priority (1-5)
	if config.Priority < 1 || config.Priority > 5 {
		config.Priority = 3
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
		validFormats := map[string]bool{"text": true, "json": true, "markdown": true}
		if !validFormats[config.ResultStorage.Format] {
			config.ResultStorage.Format = "text"
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
