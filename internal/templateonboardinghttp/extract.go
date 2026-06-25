package templateonboardinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
)

const extractTimeout = 60 * time.Second

var structuredExtractionSchema = llm.GenerateSchema[structuredExtractionOutput]()

type ModelProviderResolver interface {
	GetSystemModelProvider(providerName, modelName string) (*llm.SystemModelResult, error)
}

type SystemModelResolver interface {
	GetSystemModel() (provider, model string)
	GetSystemReasoningEffort() string
}

type extractRequest struct {
	Message string         `json:"message"`
	Text    string         `json:"text,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
}

type extractResponse struct {
	StatusResponse
	ExtractedValues map[string]any `json:"extracted_values"`
}

type structuredExtractionOutput struct {
	Values                []structuredExtractedValue `json:"values" jsonschema_description:"Values extracted from the user's message. Include only declared onboarding fields that were clearly answered."`
	MissingRequiredFields []string                   `json:"missing_required_fields" jsonschema_description:"Required field IDs that remain unfilled after applying the extracted values."`
	Reasoning             string                     `json:"reasoning" jsonschema_description:"Brief explanation of what was extracted or why fields remain missing."`
}

type structuredExtractedValue struct {
	FieldID string `json:"field_id" jsonschema_description:"Declared onboarding field ID."`
	Value   string `json:"value" jsonschema_description:"Extracted value as plain text. Numbers and booleans should still be represented as text."`
}

func (h *Handler) SetExtractionDeps(providerResolver ModelProviderResolver, systemModelResolver SystemModelResolver) {
	if h == nil {
		return
	}
	h.modelProviderResolver = providerResolver
	h.systemModelResolver = systemModelResolver
}

func (h *Handler) Extract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	session, entryAgentName, ok := h.loadFreshSession(w, r)
	if !ok {
		return
	}
	if !h.requireEntryAgent(w, entryAgentName) {
		return
	}
	switch session.Status {
	case templateonboarding.StatusCollecting,
		templateonboarding.StatusReadyToComplete,
		templateonboarding.StatusBlocked,
		templateonboarding.StatusFailed:
	default:
		_ = orihttp.RespondConflict(w, "template onboarding extract is not allowed in the current state")
		return
	}

	var req extractRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	values := req.Values
	if values == nil {
		message := strings.TrimSpace(req.Message)
		if message == "" {
			message = strings.TrimSpace(req.Text)
		}
		if message == "" {
			_ = orihttp.RespondBadRequest(w, "message is required")
			return
		}
		extracted, err := h.extractDeclaredValues(r.Context(), session, message)
		if err != nil {
			respondExtractionError(w, err)
			return
		}
		values = extracted
	}
	if len(values) == 0 {
		orihttp.WriteJSON(w, extractResponse{
			StatusResponse:  h.statusResponse(session, entryAgentName),
			ExtractedValues: map[string]any{},
		})
		return
	}

	if ok := h.mergeValidatedValues(w, r, session, entryAgentName, values); !ok {
		return
	}
	orihttp.WriteJSON(w, extractResponse{
		StatusResponse:  h.statusResponse(session, entryAgentName),
		ExtractedValues: values,
	})
}

func (h *Handler) extractDeclaredValues(ctx context.Context, session *templateonboarding.Session, message string) (map[string]any, error) {
	if h == nil || h.modelProviderResolver == nil || h.systemModelResolver == nil {
		return ExtractValuesFromText(session.Spec.Fields, message), nil
	}

	providerName, modelName := h.systemModelResolver.GetSystemModel()
	reasoningEffort := h.systemModelResolver.GetSystemReasoningEffort()
	result, err := h.modelProviderResolver.GetSystemModelProvider(providerName, modelName)
	if err != nil {
		return nil, err
	}

	systemPrompt := buildExtractionSystemPrompt(session)
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	var output *structuredExtractionOutput
	if structuredProvider, ok := result.Provider.(llm.StructuredOutputProvider); ok {
		output, err = extractWithStructuredOutput(ctx, structuredProvider, result.Model, reasoningEffort, systemPrompt, message)
	} else {
		output, err = extractWithRegularChat(ctx, result.Provider, result.Model, reasoningEffort, systemPrompt, message)
	}
	if err != nil {
		return nil, err
	}
	return extractedOutputValues(output, session.Spec.Fields), nil
}

func extractWithStructuredOutput(ctx context.Context, provider llm.StructuredOutputProvider, model, reasoningEffort, systemPrompt, message string) (*structuredExtractionOutput, error) {
	resp, err := provider.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages:        []llm.Message{{Role: "user", Content: message}},
		SystemPrompt:    systemPrompt,
		SchemaName:      "template_onboarding_extraction",
		Schema:          structuredExtractionSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("structured output request failed: %w", err)
	}
	return parseStructuredExtraction(resp.Content)
}

func extractWithRegularChat(ctx context.Context, provider llm.Provider, model, reasoningEffort, systemPrompt, message string) (*structuredExtractionOutput, error) {
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages:        []llm.Message{{Role: "user", Content: message}},
		SystemPrompt:    systemPrompt + "\n\nRespond with valid JSON only.",
		Temperature:     0.2,
		MaxTokens:       1200,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}
	return parseStructuredExtraction(resp.Content)
}

func parseStructuredExtraction(content string) (*structuredExtractionOutput, error) {
	payload := strings.TrimSpace(content)
	if payload == "" {
		return nil, fmt.Errorf("LLM returned empty response")
	}
	if strings.HasPrefix(payload, "```") {
		payload = extractJSONFromCodeFence(payload)
	}
	var output structuredExtractionOutput
	if err := json.Unmarshal([]byte(payload), &output); err != nil {
		return nil, fmt.Errorf("failed to parse extraction response: %w", err)
	}
	return &output, nil
}

func buildExtractionSystemPrompt(session *templateonboarding.Session) string {
	fieldsJSON, _ := json.Marshal(session.Spec.Fields)
	valuesJSON, _ := json.Marshal(session.Values)
	missingJSON, _ := json.Marshal(missingRequiredFieldIDs(session))
	return fmt.Sprintf(`Extract project-template onboarding values from the user's message.

Return a JSON object with exactly this shape:
{
  "values": [
    {"field_id": "declared_field_id", "value": "plain text value"}
  ],
  "missing_required_fields": ["field_id"],
  "reasoning": "brief explanation"
}

Rules:
- Only include field IDs declared in Fields JSON.
- Do not invent values. Omit uncertain or ambiguous values.
- Use the declared field type and enum options to normalize obvious values.
- Return numbers and booleans as plain text strings; validation will coerce them.
- missing_required_fields must reflect required fields still unfilled after applying extracted values to Current Values JSON.
- Do not include markdown fences or prose outside the JSON object.

Fields JSON: %s
Current Values JSON: %s
Currently Missing Required Fields JSON: %s`, string(fieldsJSON), string(valuesJSON), string(missingJSON))
}

func extractedOutputValues(output *structuredExtractionOutput, fields []templateonboarding.Field) map[string]any {
	if output == nil {
		return map[string]any{}
	}
	declared := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		declared[field.ID] = struct{}{}
	}
	values := make(map[string]any, len(output.Values))
	for _, item := range output.Values {
		fieldID := strings.TrimSpace(item.FieldID)
		if _, ok := declared[fieldID]; !ok {
			continue
		}
		value := strings.TrimSpace(item.Value)
		if value == "" {
			continue
		}
		values[fieldID] = value
	}
	return values
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

func respondExtractionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llm.ErrSystemModelNotConfigured):
		_ = orihttp.RespondServiceUnavailable(w, "system model is not configured")
	default:
		_ = orihttp.RespondInternalError(w, "failed to extract template onboarding values")
	}
}

func ExtractValuesFromText(fields []templateonboarding.Field, message string) map[string]any {
	message = strings.TrimSpace(message)
	if message == "" {
		return map[string]any{}
	}

	extracted := make(map[string]any)
	for _, field := range fields {
		for _, alias := range fieldAliases(field) {
			value, ok := extractAliasValue(message, alias)
			if !ok {
				continue
			}
			extracted[field.ID] = value
			break
		}
	}
	return extracted
}

func fieldAliases(field templateonboarding.Field) []string {
	seen := make(map[string]struct{}, 3)
	aliases := make([]string, 0, 3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		aliases = append(aliases, value)
	}
	add(field.ID)
	add(strings.ReplaceAll(field.ID, "_", " "))
	add(field.Label)
	return aliases
}

func extractAliasValue(message, alias string) (string, bool) {
	quoted := regexp.QuoteMeta(alias)
	pattern := `(?im)(?:^|[\n;,])\s*` + quoted + `\s*(?::|=|-)\s*([^\n;,]+)`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", false
	}
	match := re.FindStringSubmatch(message)
	if len(match) < 2 {
		return "", false
	}
	value := strings.TrimSpace(match[1])
	value = strings.Trim(value, `"'`)
	return value, value != ""
}
