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

const (
	defaultAutoTaskParseTimeout = 60 * time.Second
	codexAutoTaskParseTimeout   = 120 * time.Second
)

func autoTaskParseTimeout(providerName string) time.Duration {
	if strings.EqualFold(providerName, "codex") {
		return codexAutoTaskParseTimeout
	}
	return defaultAutoTaskParseTimeout
}

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
	eventBus       *workspace.EventBus
}

// NewAutoTaskHandler creates a new AutoTaskHandler
func NewAutoTaskHandler(
	agentStore store.Store,
	workspaceStore workspace.Store,
	llmFactory *llm.Factory,
	configManager *config.Manager,
	eventBus ...*workspace.EventBus,
) *AutoTaskHandler {
	var bus *workspace.EventBus
	if len(eventBus) > 0 {
		bus = eventBus[0]
	}
	return &AutoTaskHandler{
		agentStore:     agentStore,
		workspaceStore: workspaceStore,
		llmFactory:     llmFactory,
		configManager:  configManager,
		eventBus:       bus,
	}
}

// AutoTaskRequest represents the request to auto-create a task
type AutoTaskRequest struct {
	Description string `json:"description"`
	WorkspaceID string `json:"workspace_id"`
}

// AutoTaskResponse represents the parsed task configuration with jsonschema tags for structured output
type AutoTaskResponse struct {
	Title           string                `json:"title" jsonschema_description:"A concise title for the task"`
	Details         string                `json:"details" jsonschema_description:"Additional details or context, can be empty"`
	AgentName       string                `json:"agent_name" jsonschema_description:"Name of the agent to assign from the available list, or empty string"`
	Priority        int                   `json:"priority" jsonschema:"minimum=1,maximum=5" jsonschema_description:"Priority level 1-5, where 1 is highest"`
	Tasks           []AutoTaskStep        `json:"tasks" jsonschema_description:"Multi-step workflow tasks. Empty array if the request is a single task. When provided, create tasks in order and honor depends_on relationships."`
	Schedule        *ScheduleConfig       `json:"schedule" jsonschema_description:"Schedule configuration, null if no schedule"`
	ScheduleEnabled bool                  `json:"schedule_enabled" jsonschema_description:"True if a schedule was specified"`
	ScheduleName    string                `json:"schedule_name" jsonschema_description:"Descriptive name for the schedule like 'Daily at 9am'"`
	ResultStorage   *ResultStorageConfig  `json:"result_storage" jsonschema_description:"Result storage configuration, null if no storage requested"`
	OutputContract  *OutputContractConfig `json:"output_contract" jsonschema_description:"CSV output contract when result_storage.write_mode is append, null otherwise"`
	Reasoning       string                `json:"reasoning" jsonschema_description:"Brief explanation of how the request was interpreted"`
}

// autoTaskHTTPResponse augments the parsed task config (the LLM structured
// output) with coordinator assignment provenance for the preview UI. These
// provenance fields are system-set and intentionally NOT part of the LLM output
// schema (AutoTaskResponse), so they live only on this HTTP-only wrapper.
type autoTaskHTTPResponse struct {
	*AutoTaskResponse
	AssignedBy       string `json:"assigned_by,omitempty"`
	AssignmentMode   string `json:"assignment_mode,omitempty"`
	AssignmentReason string `json:"assignment_reason,omitempty"`
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

// OutputContractConfig for auto task with jsonschema tags
type OutputContractConfig struct {
	Source  string                 `json:"source" jsonschema:"enum=ai_suggested,enum=manual,enum=csv_header" jsonschema_description:"Source of the contract suggestion"`
	Columns []OutputContractColumn `json:"columns" jsonschema_description:"Ordered CSV columns that each run should produce"`
}

// OutputContractColumn describes one suggested CSV output column.
type OutputContractColumn struct {
	Name        string `json:"name" jsonschema_description:"CSV column name"`
	Type        string `json:"type" jsonschema:"enum=string,enum=number,enum=boolean,enum=date" jsonschema_description:"Column type"`
	Required    bool   `json:"required" jsonschema_description:"Whether the column is required for every row"`
	Description string `json:"description" jsonschema_description:"Brief explanation of the column"`
}

// OutputContractSuggestionRequest asks the system model to propose an append-to-CSV contract.
type OutputContractSuggestionRequest struct {
	Title                  string               `json:"title"`
	Details                string               `json:"details"`
	WorkspaceID            string               `json:"workspace_id"`
	TaskID                 string               `json:"task_id,omitempty"`
	Schedule               *ScheduleConfig      `json:"schedule"`
	ScheduleEnabled        bool                 `json:"schedule_enabled"`
	ScheduleName           string               `json:"schedule_name"`
	ResultStorage          *ResultStorageConfig `json:"result_storage"`
	ExistingCSVHeader      []string             `json:"existing_csv_header,omitempty"`
	RecentExecutionSamples []string             `json:"recent_execution_samples,omitempty"`
	// ResultSample is an optional excerpt of a prior task result. When present
	// the model can ground its column suggestions in the concrete data the
	// task actually produces instead of just guessing from title/details.
	ResultSample string `json:"result_sample"`
}

// OutputContractSuggestionResponse is the AI-generated CSV output contract suggestion.
type OutputContractSuggestionResponse struct {
	OutputContract *OutputContractConfig     `json:"output_contract" jsonschema_description:"Suggested CSV output contract"`
	OutputSpec     *workspace.TaskOutputSpec `json:"output_spec,omitempty" jsonschema_description:"Suggested structured output spec including schema, contract, mappings, and metadata policy"`
	Reasoning      string                    `json:"reasoning" jsonschema_description:"Brief explanation of why these columns were selected"`
}

// suggestionPromptEcho is the verbatim prompt the suggestion sent to the model,
// echoed back so the UI can show exactly what was asked. It is HTTP-only and is
// deliberately NOT part of OutputContractSuggestionResponse (that struct defines
// the model's structured-output schema).
type suggestionPromptEcho struct {
	System          string `json:"system"`
	User            string `json:"user"`
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// outputContractSuggestionHTTPResponse embeds the model's suggestion fields
// (output_contract, output_spec, reasoning) at the top level and adds the
// verbatim prompt echo under "prompt".
type outputContractSuggestionHTTPResponse struct {
	*OutputContractSuggestionResponse
	Prompt *suggestionPromptEcho `json:"prompt,omitempty"`
}

type OutputContractTelemetryRequest struct {
	WorkspaceID      string `json:"workspace_id"`
	TaskID           string `json:"task_id,omitempty"`
	Action           string `json:"action"`
	Source           string `json:"source,omitempty"`
	ColumnCount      int    `json:"column_count,omitempty"`
	ContractVersion  string `json:"contract_version,omitempty"`
	ValidationStatus string `json:"validation_status,omitempty"`
	StorageStatus    string `json:"storage_status,omitempty"`
	ErrorCount       int    `json:"error_count,omitempty"`
}

type autoTaskAgentContext struct {
	Agents            []string
	AgentDescriptions map[string]string
	DefaultAgentName  string
}

// Schema for structured output - generated at init time
var autoTaskResponseSchema = llm.GenerateSchema[AutoTaskResponse]()
var outputContractSuggestionSchema = llm.GenerateSchema[OutputContractSuggestionResponse]()

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

	agentContext := h.autoTaskAgentContext(req.WorkspaceID)

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
	taskConfig, err := h.parseTaskDescription(
		r.Context(),
		result.Provider,
		systemProvider,
		result.Model,
		systemReasoningEffort,
		req.Description,
		agentContext.Agents,
		agentContext.AgentDescriptions,
		agentContext.DefaultAgentName,
	)
	if err != nil {
		logger.Error("Auto-task parsing failed", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to parse task description: "+err.Error())
		return
	}

	// Augment the response with coordinator provenance so the preview can show
	// that auto-parsed tasks are a coordinator static plan (the provenance the
	// frontend echoes back when persisting each task).
	resp := &autoTaskHTTPResponse{AutoTaskResponse: taskConfig}
	if coordinator := strings.TrimSpace(agentContext.DefaultAgentName); coordinator != "" {
		resp.AssignedBy = coordinator
		resp.AssignmentMode = string(workspace.TaskAssignmentModeStaticPlan)
		resp.AssignmentReason = strings.TrimSpace(taskConfig.Reasoning)
	}
	orihttp.WriteJSON(w, resp)
}

// HandleOutputContractSuggestion handles POST /api/orchestration/tasks/output-contract/suggest.
func (h *AutoTaskHandler) HandleOutputContractSuggestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req OutputContractSuggestionRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Title) == "" && strings.TrimSpace(req.Details) == "" {
		_ = orihttp.RespondBadRequest(w, "title or details is required")
		return
	}
	h.publishOutputSpecSuggestionEvent(req, "suggestion_requested", "")

	systemProvider, systemModel := h.configManager.GetSystemModel()
	systemReasoningEffort := h.configManager.GetSystemReasoningEffort()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		logger.Error("System model not available for output contract suggestion", logger.Fields{"error": err})
		h.publishOutputSpecSuggestionEvent(req, "suggestion_failed", err.Error())
		_ = orihttp.RespondServiceUnavailable(w, "System model not configured")
		return
	}

	suggestion, promptEcho, err := h.suggestOutputContract(r.Context(), result.Provider, systemProvider, result.Model, systemReasoningEffort, req)
	if err != nil {
		logger.Error("Output contract suggestion failed", logger.Fields{"error": err})
		h.publishOutputSpecSuggestionEvent(req, "suggestion_failed", err.Error())
		_ = orihttp.RespondInternalError(w, "Failed to suggest output contract: "+err.Error())
		return
	}

	orihttp.WriteJSON(w, &outputContractSuggestionHTTPResponse{
		OutputContractSuggestionResponse: suggestion,
		Prompt:                           promptEcho,
	})
}

func (h *AutoTaskHandler) publishOutputSpecSuggestionEvent(req OutputContractSuggestionRequest, action, message string) {
	if h.eventBus == nil {
		return
	}
	workspaceID := strings.TrimSpace(req.WorkspaceID)
	if workspaceID == "" {
		return
	}
	data := map[string]any{
		"task_id":        strings.TrimSpace(req.TaskID),
		"action":         action,
		"has_header":     len(req.ExistingCSVHeader) > 0,
		"sample_count":   len(req.RecentExecutionSamples),
		"storage_format": "",
	}
	if req.ResultStorage != nil {
		data["storage_format"] = strings.TrimSpace(req.ResultStorage.Format)
		data["write_mode"] = strings.TrimSpace(req.ResultStorage.WriteMode)
	}
	if message != "" {
		data["error"] = message
	}
	h.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventTaskOutput, workspaceID, "task.output_spec", data))
}

// HandleOutputContractTelemetry handles non-raw output-contract UX telemetry.
func (h *AutoTaskHandler) HandleOutputContractTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	var req OutputContractTelemetryRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.Action = normalizeOutputContractTelemetryAction(req.Action)
	if req.WorkspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace_id is required")
		return
	}
	if req.Action == "" {
		_ = orihttp.RespondBadRequest(w, "valid action is required")
		return
	}
	if h.eventBus != nil {
		h.eventBus.Publish(workspace.NewWorkspaceEvent(workspace.EventTaskOutput, req.WorkspaceID, "task.output_contract", map[string]any{
			"task_id":           req.TaskID,
			"action":            req.Action,
			"source":            strings.TrimSpace(req.Source),
			"column_count":      req.ColumnCount,
			"contract_version":  strings.TrimSpace(req.ContractVersion),
			"validation_status": strings.TrimSpace(req.ValidationStatus),
			"storage_status":    strings.TrimSpace(req.StorageStatus),
			"error_count":       req.ErrorCount,
		}))
	}
	orihttp.WriteJSON(w, map[string]any{"success": true})
}

func normalizeOutputContractTelemetryAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "suggestion_accepted", "suggestion_edited", "suggestion_regenerated", "suggestion_failed",
		"suggestion_requested", "draft_saved", "draft_approved", "draft_discarded",
		"validation_outcome", "review_action", "storage_gating_outcome":
		return strings.ToLower(strings.TrimSpace(action))
	default:
		return ""
	}
}

func (h *AutoTaskHandler) suggestOutputContract(
	ctx context.Context,
	provider llm.Provider,
	providerName string,
	model string,
	reasoningEffort string,
	req OutputContractSuggestionRequest,
) (*OutputContractSuggestionResponse, *suggestionPromptEcho, error) {
	// Column suggestion is a lightweight, well-constrained JSON task. Cap the
	// reasoning effort so it returns quickly regardless of the (possibly heavy)
	// system reasoning setting — only lower it, never raise it.
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "", "medium", "high":
		reasoningEffort = "low"
	}
	scheduleJSON, _ := json.Marshal(req.Schedule)
	storageJSON, _ := json.Marshal(req.ResultStorage)
	headerJSON, _ := json.Marshal(req.ExistingCSVHeader)
	samplesJSON, _ := json.Marshal(trimOutputSuggestionSamples(req.RecentExecutionSamples, 5, 1200))
	systemPrompt := `Suggest a compact structured output spec for a recurring task that appends one row per run.

Return a JSON object with exactly this shape:
{
  "output_spec": {
    "source": "ai_suggested",
    "schema": {
      "name": "short_schema_name",
      "description": "what one normalized row represents",
      "strict": true,
      "fields": [
        {"name": "date", "type": "string", "required": true, "description": "Observation date"}
      ]
    },
    "contract": {
      "source": "ai_suggested",
      "columns": [
        {"name": "date", "type": "date", "required": true, "description": "Observation date"}
      ]
    },
    "mappings": [
      {"schema_field": "date", "csv_column": "date", "transform": "identity"}
    ],
    "metadata_policy": {
      "fields": [
        {"name": "run_id", "include": true},
        {"name": "executed_at", "include": true},
        {"name": "status", "include": true},
        {"name": "duration_ms", "include": true}
      ]
    }
  },
  "output_contract": {
    "source": "ai_suggested",
    "columns": [
      {"name": "date", "type": "date", "required": true, "description": "Observation date"}
    ]
  },
  "reasoning": "brief explanation"
}

Rules:
- Use 3-8 practical task-data fields and columns that the task can realistically produce every run.
- Prefer stable facts over prose blobs.
- Do not include system/run-history fields like run_id, executed_at, status, or duration_ms in the contract columns; those belong in metadata_policy.
- Include a domain date or observation date column when the task result has one.
- Schema field types: string, number, integer, boolean, object, array.
- Contract column types: string, number, boolean, date.
- Mark columns required only when a run should always provide them.
- Every required contract column must have a mapping from a schema field.
- Mapping transform must be identity or json_string. Use json_string for array fields unless a scalar field is better.
- If an existing CSV header is provided, preserve that header order and names where possible. Suggest additions only when clearly needed.
- Do not include markdown fences or prose outside the JSON object.`

	resultSample := strings.TrimSpace(req.ResultSample)
	if len(resultSample) > 4000 {
		resultSample = resultSample[:4000] + "\n...[truncated]"
	}
	userMessage := fmt.Sprintf(`Task title: %s
Task details: %s
Schedule enabled: %t
Schedule name: %s
Schedule JSON: %s
Result storage JSON: %s
Existing CSV header JSON: %s
Recent execution samples JSON: %s
Sample result from a prior run (use this to derive concrete columns when available):
%s`, strings.TrimSpace(req.Title), strings.TrimSpace(req.Details), req.ScheduleEnabled, strings.TrimSpace(req.ScheduleName), string(scheduleJSON), string(storageJSON), string(headerJSON), string(samplesJSON), resultSample)

	// Echo the exact prompt back to the UI so users can see what was asked.
	promptEcho := &suggestionPromptEcho{
		System:          systemPrompt,
		User:            userMessage,
		Provider:        providerName,
		Model:           model,
		ReasoningEffort: reasoningEffort,
	}

	// Allow headroom for a slow first invocation (e.g. the Codex CLI cold
	// start can take ~50-90s; warm calls are ~15s).
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	logger.Info("Output contract suggestion request", logger.Fields{
		"model":    model,
		"provider": providerName,
	})

	if structuredProvider, ok := provider.(llm.StructuredOutputProvider); ok {
		resp, err := structuredProvider.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
			Model:           model,
			ReasoningEffort: reasoningEffort,
			Messages: []llm.Message{
				{Role: "user", Content: userMessage},
			},
			SystemPrompt: systemPrompt,
			SchemaName:   "output_contract_suggestion",
			Schema:       outputContractSuggestionSchema,
		})
		if err != nil {
			if friendlyMsg := classifyAutoTaskError(err); friendlyMsg != "" {
				return nil, promptEcho, fmt.Errorf("%s", friendlyMsg)
			}
			return nil, promptEcho, fmt.Errorf("structured output request failed: %w", err)
		}
		parsed, perr := parseOutputContractSuggestion(resp.Content)
		return parsed, promptEcho, perr
	}

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt + "\n\nRespond with valid JSON only.",
		Temperature:  0.2,
		MaxTokens:    1200,
	})
	if err != nil {
		if friendlyMsg := classifyAutoTaskError(err); friendlyMsg != "" {
			return nil, promptEcho, fmt.Errorf("%s", friendlyMsg)
		}
		return nil, promptEcho, fmt.Errorf("LLM request failed: %w", err)
	}
	parsed, perr := parseOutputContractSuggestion(resp.Content)
	return parsed, promptEcho, perr
}

func parseOutputContractSuggestion(content string) (*OutputContractSuggestionResponse, error) {
	payload := strings.TrimSpace(content)
	if payload == "" {
		return nil, fmt.Errorf("LLM returned empty response")
	}
	if strings.HasPrefix(payload, "```") {
		payload = extractJSONFromCodeFence(payload)
	}

	var suggestion OutputContractSuggestionResponse
	if err := json.Unmarshal([]byte(payload), &suggestion); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if suggestion.OutputSpec != nil {
		normalizedSpec, errs := workspace.NormalizeTaskOutputSpec(suggestion.OutputSpec)
		if normalizedSpec == nil {
			return nil, fmt.Errorf("suggestion did not include a usable output spec: %s", strings.Join(errs, "; "))
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("suggestion output spec is invalid: %s", strings.Join(errs, "; "))
		}
		normalizedSpec.Source = "ai_suggested"
		normalizedSpec.Approval = nil
		normalizedSpec.Version = ""
		if normalizedSpec.Contract != nil {
			normalizedSpec.Contract.Source = "ai_suggested"
			suggestion.OutputContract = &OutputContractConfig{
				Source:  normalizedSpec.Contract.Source,
				Columns: outputContractColumnsFromWorkspace(normalizedSpec.Contract.Columns),
			}
		}
		suggestion.OutputSpec = normalizedSpec
		suggestion.Reasoning = strings.TrimSpace(suggestion.Reasoning)
		return &suggestion, nil
	}

	if suggestion.OutputContract == nil {
		return nil, fmt.Errorf("suggestion did not include an output spec or output contract")
	}
	normalized := workspace.NormalizeTaskOutputContract(&workspace.TaskOutputContract{
		Source:  suggestion.OutputContract.Source,
		Columns: autoTaskOutputContractColumns(suggestion.OutputContract.Columns),
	})
	if normalized == nil {
		return nil, fmt.Errorf("suggestion did not include usable columns")
	}
	spec := synthesizeSuggestionSpecFromContract(normalized)
	suggestion.OutputContract = &OutputContractConfig{
		Source:  normalized.Source,
		Columns: outputContractColumnsFromWorkspace(normalized.Columns),
	}
	suggestion.OutputSpec = spec
	suggestion.Reasoning = strings.TrimSpace(suggestion.Reasoning)
	return &suggestion, nil
}

func trimOutputSuggestionSamples(samples []string, maxSamples int, maxLen int) []string {
	if maxSamples <= 0 || maxLen <= 0 {
		return nil
	}
	trimmed := make([]string, 0, min(len(samples), maxSamples))
	for _, sample := range samples {
		value := strings.TrimSpace(sample)
		if value == "" {
			continue
		}
		if len(value) > maxLen {
			value = value[:maxLen] + "\n...[truncated]"
		}
		trimmed = append(trimmed, value)
		if len(trimmed) >= maxSamples {
			break
		}
	}
	return trimmed
}

func synthesizeSuggestionSpecFromContract(contract *workspace.TaskOutputContract) *workspace.TaskOutputSpec {
	if contract == nil {
		return nil
	}
	fields := make([]workspace.TaskOutputField, 0, len(contract.Columns))
	mappings := make([]workspace.TaskOutputMapping, 0, len(contract.Columns))
	for _, column := range contract.Columns {
		fieldType := "string"
		switch column.Type {
		case "number":
			fieldType = "number"
		case "boolean":
			fieldType = "boolean"
		}
		fields = append(fields, workspace.TaskOutputField{
			Name:        column.Name,
			Type:        fieldType,
			Required:    column.Required,
			Description: column.Description,
		})
		mappings = append(mappings, workspace.TaskOutputMapping{
			SchemaField: column.Name,
			CSVColumn:   column.Name,
			Transform:   workspace.TaskOutputMappingTransformIdentity,
		})
	}
	spec, _ := workspace.NormalizeTaskOutputSpec(&workspace.TaskOutputSpec{
		Source: "ai_suggested",
		Schema: &workspace.TaskOutputSchema{
			Name:        "task_result",
			Description: "One normalized task result row.",
			Strict:      true,
			Fields:      fields,
		},
		Contract: contract,
		Mappings: mappings,
	})
	return spec
}

func (h *AutoTaskHandler) autoTaskAgentContext(workspaceID string) autoTaskAgentContext {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" && h.workspaceStore != nil {
		ws, err := h.workspaceStore.Get(workspaceID)
		if err != nil {
			logger.Warn("Failed to load workspace agents for auto-task parsing", logger.Fields{
				"workspace_id": workspaceID,
				"error":        err,
			})
		} else if ws != nil {
			agents := workspaceAutoTaskAgentNames(ws)
			if len(agents) > 0 {
				// Default ordinary requests to the workspace coordinator. Use the
				// coordinator resolver (not EntryAgentName, which falls back to the
				// first agent) so a multi-agent workspace with no explicit entry
				// agent yields no default rather than silently picking one.
				defaultAgentName := ""
				if coordinator, source := ws.ResolveCoordinator(); source != workspace.CoordinatorSourceMissing {
					defaultAgentName = canonicalAutoTaskAgentName(coordinator, agents)
				}
				return autoTaskAgentContext{
					Agents:            agents,
					AgentDescriptions: h.autoTaskAgentDescriptions(agents, ws),
					DefaultAgentName:  defaultAgentName,
				}
			}
		}
	}

	agents := h.globalAutoTaskAgentNames()
	return autoTaskAgentContext{
		Agents:            agents,
		AgentDescriptions: h.autoTaskAgentDescriptions(agents, nil),
	}
}

func (h *AutoTaskHandler) globalAutoTaskAgentNames() []string {
	if h.agentStore == nil {
		return nil
	}
	return uniqueAutoTaskAgentNames(h.agentStore.ListAgents()...)
}

func workspaceAutoTaskAgentNames(ws *workspace.Workspace) []string {
	if ws == nil {
		return nil
	}

	names := make([]string, 0, len(ws.AgentInstances)+1)
	names = append(names, ws.EntryAgentName())
	for _, inst := range ws.AgentInstances {
		names = append(names, inst.Name)
	}

	return uniqueAutoTaskAgentNames(names...)
}

func uniqueAutoTaskAgentNames(names ...string) []string {
	unique := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, name)
	}
	return unique
}

func (h *AutoTaskHandler) autoTaskAgentDescriptions(agents []string, ws *workspace.Workspace) map[string]string {
	descriptions := make(map[string]string, len(agents))
	if ws != nil {
		for _, inst := range ws.AgentInstances {
			name := canonicalAutoTaskAgentName(inst.Name, agents)
			if name == "" {
				continue
			}
			descriptionParts := make([]string, 0, 2)
			if role := strings.TrimSpace(inst.Role); role != "" {
				descriptionParts = append(descriptionParts, "Role: "+role)
			}
			if description := strings.TrimSpace(inst.Description); description != "" {
				descriptionParts = append(descriptionParts, description)
			}
			if len(descriptionParts) > 0 {
				descriptions[name] = strings.Join(descriptionParts, ". ")
			}
		}
	}

	if h.agentStore == nil {
		return descriptions
	}

	for _, name := range agents {
		ag, found := h.agentStore.GetAgent(name)
		if !found || ag == nil || ag.Metadata == nil {
			continue
		}
		description := strings.TrimSpace(ag.Metadata.Description)
		if description == "" {
			continue
		}
		if existing := strings.TrimSpace(descriptions[name]); existing != "" {
			descriptions[name] = existing + ". " + description
		} else {
			descriptions[name] = description
		}
	}

	return descriptions
}

func canonicalAutoTaskAgentName(name string, agents []string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	for _, agent := range agents {
		if strings.EqualFold(agent, trimmed) {
			return agent
		}
	}
	return ""
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
	defaultAgentName string,
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
	var defaultAgentContext string
	if defaultAgentName = strings.TrimSpace(defaultAgentName); defaultAgentName != "" {
		defaultAgentContext = fmt.Sprintf("\nDefault workspace agent: %s\n", defaultAgentName)
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
  "output_contract": null,
  "reasoning": "brief explanation"
}

Current time: %s (%s)
%s%s

Schedule parsing rules:
- "at 6pm" or "at 18:00" -> schedule_enabled=true, schedule={"type":"once","once_at":"ISO datetime"}
- "every day at 9am" -> schedule_enabled=true, schedule={"type":"daily","time":"09:00"}
- "every Monday at 10am" -> schedule_enabled=true, schedule={"type":"weekly","day_of_week":1,"time":"10:00"}
- "every 30 minutes" -> schedule_enabled=true, schedule={"type":"interval","interval_minutes":30}
- No time mentioned -> schedule_enabled=false, schedule=null

Result storage: set result_storage={"enabled":true,"format":"text|json|markdown|csv","write_mode":"new_file|append"} only if user mentions saving results. Use write_mode="append" and format="csv" when the user asks to append runs to the same CSV file.

Output contract: when result_storage.write_mode is "append", also set output_contract with source="ai_suggested" and 3-8 practical CSV columns. Use column types string, number, boolean, or date. Include columns the recurring task can realistically produce every run.

Agent assignment: Match the task to one of the available agents based on their description. If a default workspace agent is provided, use that agent for ordinary workspace tasks and unmatched everyday requests unless another available agent is clearly better. Use empty string only when no default workspace agent is provided and no available agent matches.

Multi-step tasks:
- If the request has multiple distinct steps (e.g., "do X then Y"), populate "tasks" with each step.
- Each task must include: id, title, details, agent_name, priority, depends_on.
- Use depends_on to indicate ordering (e.g., step2 depends_on ["step1"]).
- When tasks are provided, keep the top-level fields as a brief summary (title/details) or mirror the first step.
- Apply schedule fields to the first step only.
- Apply result_storage to the final step only.

IMPORTANT: Always return the exact JSON structure shown above. Never return error messages or different formats.`, currentTime, currentDay, agentList, defaultAgentContext)

	userMessage := description

	timeout := autoTaskParseTimeout(providerName)
	// Codex runs through the CLI and can spend most of the first minute on
	// startup/schema setup before the model finishes a structured response.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.Info("Auto-task parsing request", logger.Fields{
		"model":           model,
		"provider":        providerName,
		"description":     description,
		"timeout_seconds": int(timeout / time.Second),
	})

	// Try structured output for providers that support it
	if structuredProvider, ok := provider.(llm.StructuredOutputProvider); ok {
		return h.parseWithStructuredOutput(ctx, structuredProvider, model, reasoningEffort, systemPrompt, userMessage, agents, defaultAgentName)
	}

	// Fallback to regular chat for non-OpenAI providers
	return h.parseWithRegularChat(ctx, provider, model, reasoningEffort, systemPrompt, userMessage, agents, defaultAgentName)
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
	defaultAgentName string,
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
	taskConfig = h.validateTaskConfig(taskConfig, agents, defaultAgentName)
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
	defaultAgentName string,
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
	taskConfig = h.validateTaskConfig(taskConfig, agents, defaultAgentName)
	return &taskConfig, nil
}

// validateTaskConfig ensures the task config values are valid
func (h *AutoTaskHandler) validateTaskConfig(config AutoTaskResponse, agents []string, defaultAgentName string) AutoTaskResponse {
	defaultAgentName = canonicalAutoTaskAgentName(defaultAgentName, agents)
	normalizeAgentName := func(agentName string) string {
		return canonicalAutoTaskAgentName(agentName, agents)
	}
	normalizeAgentNameWithDefault := func(agentName string) string {
		if normalized := normalizeAgentName(agentName); normalized != "" {
			return normalized
		}
		return defaultAgentName
	}

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

			step.AgentName = normalizeAgentNameWithDefault(step.AgentName)
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

	// Validate agent name exists and default ordinary workspace requests to the entry agent.
	config.AgentName = normalizeAgentNameWithDefault(config.AgentName)

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
	if config.ResultStorage == nil || config.ResultStorage.WriteMode != "append" {
		config.OutputContract = nil
	} else if config.OutputContract != nil {
		workspaceContract := workspace.NormalizeTaskOutputContract(&workspace.TaskOutputContract{
			Source:  config.OutputContract.Source,
			Columns: autoTaskOutputContractColumns(config.OutputContract.Columns),
		})
		if workspaceContract == nil {
			config.OutputContract = nil
		} else {
			config.OutputContract = &OutputContractConfig{
				Source:  workspaceContract.Source,
				Columns: outputContractColumnsFromWorkspace(workspaceContract.Columns),
			}
		}
	}

	return config
}

func autoTaskOutputContractColumns(columns []OutputContractColumn) []workspace.TaskOutputContractColumn {
	converted := make([]workspace.TaskOutputContractColumn, 0, len(columns))
	for _, column := range columns {
		converted = append(converted, workspace.TaskOutputContractColumn{
			Name:        column.Name,
			Type:        column.Type,
			Required:    column.Required,
			Description: column.Description,
		})
	}
	return converted
}

func outputContractColumnsFromWorkspace(columns []workspace.TaskOutputContractColumn) []OutputContractColumn {
	converted := make([]OutputContractColumn, 0, len(columns))
	for _, column := range columns {
		converted = append(converted, OutputContractColumn{
			Name:        column.Name,
			Type:        column.Type,
			Required:    column.Required,
			Description: column.Description,
		})
	}
	return converted
}
