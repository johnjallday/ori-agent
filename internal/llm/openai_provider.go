package llm

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// isReasoningModel checks if the model is an OpenAI model that requires
// max_completion_tokens instead of max_tokens
func isReasoningModel(model string) bool {
	// o1, o3, o4 series and gpt-5 series models use max_completion_tokens
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.HasPrefix(model, "gpt-5") ||
		strings.Contains(model, "-nano")
}

// OpenAIProvider implements the Provider interface for OpenAI
type OpenAIProvider struct {
	client     openai.Client
	apiKey     string
	httpClient *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config ProviderConfig) *OpenAIProvider {
	httpClient := NewHTTPClient(DefaultCloudTimeout)

	var client openai.Client
	if config.APIKey != "" {
		client = openai.NewClient(
			option.WithAPIKey(config.APIKey),
			option.WithHTTPClient(httpClient),
		)
	}

	return &OpenAIProvider{
		client:     client,
		apiKey:     config.APIKey,
		httpClient: httpClient,
	}
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Type returns the provider type
func (p *OpenAIProvider) Type() ProviderType {
	return ProviderTypeCloud
}

// Capabilities returns OpenAI's capabilities
func (p *OpenAIProvider) Capabilities() ProviderCapabilities {
	return CloudProviderCapabilities(128000) // GPT-4o context window
}

// ValidateConfig validates the OpenAI configuration
func (p *OpenAIProvider) ValidateConfig(config ProviderConfig) error {
	if config.APIKey == "" {
		return fmt.Errorf("OpenAI API key is required")
	}
	return nil
}

// DefaultModels returns available OpenAI models from the curated pricing data.
// This provides a consistent list without requiring an API call.
func (p *OpenAIProvider) DefaultModels() []string {
	return modelinfo.GetOpenAIModels()
}

// Chat sends a chat completion request to OpenAI
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required (set OPENAI_API_KEY or configure it in settings)")
	}

	// Convert messages to OpenAI format
	messages := convertMessagesToOpenAI(req.Messages, req.SystemPrompt)

	// Build OpenAI request parameters
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(req.Model), // Convert string to ChatModel type
		Messages: messages,
	}

	// Add temperature if specified (reasoning models only support default temperature of 1)
	// Use >= 0 to allow temperature 0 for deterministic output; use -1 to skip
	if req.Temperature >= 0 && !isReasoningModel(req.Model) {
		params.Temperature = openai.Float(req.Temperature)
	}

	// Add max tokens if specified
	// Note: reasoning models use max_completion_tokens instead of max_tokens
	if req.MaxTokens > 0 {
		if isReasoningModel(req.Model) {
			params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
		} else {
			params.MaxTokens = openai.Int(int64(req.MaxTokens))
		}
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		tools := convertToolsToOpenAI(req.Tools)
		params.Tools = tools
	}

	// Make API call
	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai api error: %w", err)
	}

	// Check for empty response (can happen with invalid models)
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no response choices - model %q may be invalid or unavailable", req.Model)
	}

	// Convert response
	return convertOpenAIChatCompletion("openai", completion), nil
}

// StreamChat streams a chat completion response (not yet implemented)
func (p *OpenAIProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for OpenAI provider")
}

// StructuredOutputRequest contains parameters for structured output requests
type StructuredOutputRequest struct {
	Model           string
	Messages        []Message
	SystemPrompt    string
	ReasoningEffort string
	SchemaName      string
	Schema          interface{}
}

// GenerateSchema creates a JSON schema from a Go struct type for use with structured outputs
func GenerateSchema[T any]() interface{} {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T
	return reflector.Reflect(v)
}

// ChatWithStructuredOutput sends a chat request with structured output format
func (p *OpenAIProvider) ChatWithStructuredOutput(ctx context.Context, req StructuredOutputRequest) (*ChatResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	// Convert messages to OpenAI format
	var openaiMessages []openai.ChatCompletionMessageParamUnion
	if req.SystemPrompt != "" {
		openaiMessages = append(openaiMessages, openai.SystemMessage(req.SystemPrompt))
	}
	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			openaiMessages = append(openaiMessages, openai.UserMessage(msg.Content))
		case RoleAssistant:
			openaiMessages = append(openaiMessages, openai.AssistantMessage(msg.Content))
		}
	}

	// Build schema parameter
	schemaParam := openai.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:   req.SchemaName,
		Schema: req.Schema,
		Strict: openai.Bool(true),
	}

	// Build request params
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(req.Model),
		Messages: openaiMessages,
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{JSONSchema: schemaParam},
		},
	}

	// Make API call
	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai api error: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no response choices")
	}

	return p.convertResponse(completion), nil
}

// convertMessages converts unified messages to OpenAI format
func (p *OpenAIProvider) convertMessages(messages []Message, systemPrompt string) []openai.ChatCompletionMessageParamUnion {
	return convertMessagesToOpenAI(messages, systemPrompt)
}

func convertMessagesToOpenAI(messages []Message, systemPrompt string) []openai.ChatCompletionMessageParamUnion {
	var openaiMessages []openai.ChatCompletionMessageParamUnion
	toolCallIDMap := map[string]string{}

	normalizeToolCallID := func(toolCallID string) string {
		trimmed := strings.TrimSpace(toolCallID)
		if trimmed == "" {
			return ""
		}
		if existing, ok := toolCallIDMap[trimmed]; ok {
			return existing
		}
		if len(trimmed) <= 40 {
			toolCallIDMap[trimmed] = trimmed
			return trimmed
		}

		sum := sha1.Sum([]byte(trimmed))
		normalized := "call_" + hex.EncodeToString(sum[:])[:35]
		toolCallIDMap[trimmed] = normalized
		return normalized
	}

	// Add system message if provided
	if systemPrompt != "" {
		openaiMessages = append(openaiMessages, openai.SystemMessage(systemPrompt))
	}

	// Convert each message
	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			openaiMessages = append(openaiMessages, openai.SystemMessage(msg.Content))

		case RoleUser:
			openaiMessages = append(openaiMessages, openai.UserMessage(msg.Content))

		case RoleAssistant:
			if len(msg.ToolCalls) == 0 {
				openaiMessages = append(openaiMessages, openai.AssistantMessage(msg.Content))
				continue
			}

			assistant := openai.ChatCompletionAssistantMessageParam{}
			if strings.TrimSpace(msg.Content) != "" {
				assistant.Content.OfString = param.NewOpt(msg.Content)
			}
			assistant.ToolCalls = make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(msg.ToolCalls))
			for index, toolCall := range msg.ToolCalls {
				toolCallID := normalizeToolCallID(toolCall.ID)
				if toolCallID == "" {
					toolCallID = normalizeToolCallID(fmt.Sprintf("tool_%d_%s", index+1, strings.TrimSpace(toolCall.Name)))
				}
				assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: toolCallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      toolCall.Name,
							Arguments: toolCall.Arguments,
						},
					},
				})
			}
			openaiMessages = append(openaiMessages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &assistant,
			})

		case RoleTool:
			// Tool response message
			openaiMessages = append(openaiMessages, openai.ToolMessage(msg.Content, normalizeToolCallID(msg.ToolCallID)))
		}
	}

	return openaiMessages
}

// convertTools converts unified tools to OpenAI format
func (p *OpenAIProvider) convertTools(tools []Tool) []openai.ChatCompletionToolUnionParam {
	return convertToolsToOpenAI(tools)
}

func convertToolsToOpenAI(tools []Tool) []openai.ChatCompletionToolUnionParam {
	var openaiTools []openai.ChatCompletionToolUnionParam

	for _, tool := range tools {
		funcDef := openai.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  openai.FunctionParameters(tool.Parameters),
		}
		openaiTools = append(openaiTools, openai.ChatCompletionFunctionTool(funcDef))
	}

	return openaiTools
}

// convertResponse converts OpenAI response to unified format
func (p *OpenAIProvider) convertResponse(completion *openai.ChatCompletion) *ChatResponse {
	return convertOpenAIChatCompletion("openai", completion)
}

func convertOpenAIChatCompletion(providerName string, completion *openai.ChatCompletion) *ChatResponse {
	response := &ChatResponse{
		Model:    completion.Model,
		Provider: providerName,
		Usage: Usage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}

	if len(completion.Choices) > 0 {
		choice := completion.Choices[0]

		// Set finish reason
		response.FinishReason = string(choice.FinishReason)

		// Extract content
		if choice.Message.Content != "" {
			response.Content = choice.Message.Content
		}

		// Extract tool calls
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				response.ToolCalls = append(response.ToolCalls, ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		}
	}

	return response
}

// UpdateClient updates the OpenAI client with a new API key
func (p *OpenAIProvider) UpdateClient(apiKey string) {
	if apiKey != "" {
		p.apiKey = apiKey
		p.client = openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithHTTPClient(p.httpClient),
		)
	}
}
