package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
)

// OpenAIProvider implements the Provider interface for OpenAI
type OpenAIProvider struct {
	client     openai.Client
	apiKey     string
	httpClient *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(config ProviderConfig) *OpenAIProvider {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

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
	return ProviderCapabilities{
		SupportsTools:          true,
		SupportsStreaming:      true,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    true,
		RequiresAPIKey:         true,
		SupportsCustomEndpoint: false,
		MaxContextWindow:       128000, // GPT-4o context window
		SupportedFormats:       []string{"text"},
	}
}

// ValidateConfig validates the OpenAI configuration
func (p *OpenAIProvider) ValidateConfig(config ProviderConfig) error {
	if config.APIKey == "" {
		return fmt.Errorf("OpenAI API key is required")
	}
	return nil
}

// DefaultModels returns available OpenAI models
func (p *OpenAIProvider) DefaultModels() []string {
	return []string{
		// Tool-calling tier (cheapest)
		"gpt-5-nano",
		"gpt-4.1-nano",
		// General purpose tier (mid-tier)
		"gpt-5-mini",
		"gpt-4.1-mini",
		"gpt-4o-mini",
		// Research tier (expensive)
		"gpt-5",
		"gpt-4.1",
		"gpt-4o",
		"gpt-4-turbo",
		"gpt-4",
		"gpt-3.5-turbo",
		"o1-preview",
		"o1-mini",
	}
}

// Chat sends a chat completion request to OpenAI
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to OpenAI format
	messages := p.convertMessages(req.Messages, req.SystemPrompt)

	// Build OpenAI request parameters
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: messages,
	}

	// Add temperature if specified
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	// Add max tokens if specified
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		tools := p.convertTools(req.Tools)
		params.Tools = tools
	}

	// Make API call
	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai api error: %w", err)
	}

	// Convert response
	return p.convertResponse(completion), nil
}

// StreamChat streams a chat completion response (not yet implemented)
func (p *OpenAIProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for OpenAI provider")
}

// convertMessages converts unified messages to OpenAI format
func (p *OpenAIProvider) convertMessages(messages []Message, systemPrompt string) []openai.ChatCompletionMessageParamUnion {
	var openaiMessages []openai.ChatCompletionMessageParamUnion

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
			openaiMessages = append(openaiMessages, openai.AssistantMessage(msg.Content))

		case RoleTool:
			// Tool response message
			openaiMessages = append(openaiMessages, openai.ToolMessage(msg.ToolCallID, msg.Content))
		}
	}

	return openaiMessages
}

// convertTools converts unified tools to OpenAI format
func (p *OpenAIProvider) convertTools(tools []Tool) []openai.ChatCompletionToolUnionParam {
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
	response := &ChatResponse{
		Model:    completion.Model,
		Provider: "openai",
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
