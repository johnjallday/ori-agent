package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudeProvider implements the Provider interface for Anthropic's Claude
type ClaudeProvider struct {
	client     anthropic.Client
	apiKey     string
	httpClient *http.Client
}

// NewClaudeProvider creates a new Claude provider
func NewClaudeProvider(config ProviderConfig) *ClaudeProvider {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	var client anthropic.Client
	if config.APIKey != "" {
		client = anthropic.NewClient(
			option.WithAPIKey(config.APIKey),
			option.WithHTTPClient(httpClient),
		)
	}

	return &ClaudeProvider{
		client:     client,
		apiKey:     config.APIKey,
		httpClient: httpClient,
	}
}

// Name returns the provider name
func (p *ClaudeProvider) Name() string {
	return "claude"
}

// Type returns the provider type
func (p *ClaudeProvider) Type() ProviderType {
	return ProviderTypeCloud
}

// Capabilities returns Claude's capabilities
func (p *ClaudeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          true,
		SupportsStreaming:      true,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    true,
		RequiresAPIKey:         true,
		SupportsCustomEndpoint: false,
		MaxContextWindow:       200000, // Claude 3.5 Sonnet context window
		SupportedFormats:       []string{"text"},
	}
}

// ValidateConfig validates the Claude configuration
func (p *ClaudeProvider) ValidateConfig(config ProviderConfig) error {
	if config.APIKey == "" {
		return fmt.Errorf("Claude API key is required")
	}
	return nil
}

// DefaultModels returns available Claude models
func (p *ClaudeProvider) DefaultModels() []string {
	return []string{
		"claude-sonnet-4-5",        // Claude Sonnet 4.5 (latest, best for coding)
		"claude-sonnet-4",          // Claude Sonnet 4
		"claude-opus-4-1",          // Claude Opus 4.1 (most capable)
		"claude-3-opus-20240229",   // Claude 3 Opus
		"claude-3-sonnet-20240229", // Claude 3 Sonnet
		"claude-3-haiku-20240307",  // Claude 3 Haiku
	}
}

// Chat sends a chat completion request to Claude
func (p *ClaudeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to Claude format
	messages := p.convertMessages(req.Messages)

	// Build Claude request parameters
	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 4096 // Default max tokens
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	// Add system prompt if specified
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{
				Text: req.SystemPrompt,
			},
		}
	}

	// Add temperature if specified
	if req.Temperature > 0 {
		params.Temperature = anthropic.Float(req.Temperature)
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		tools := p.convertTools(req.Tools)
		params.Tools = tools
	}

	// Make API call
	message, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("claude api error: %w", err)
	}

	// Convert response
	return p.convertResponse(message), nil
}

// StreamChat streams a chat completion response (not yet implemented)
func (p *ClaudeProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for Claude provider")
}

// convertMessages converts unified messages to Claude format
func (p *ClaudeProvider) convertMessages(messages []Message) []anthropic.MessageParam {
	var claudeMessages []anthropic.MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			claudeMessages = append(claudeMessages, anthropic.NewUserMessage(
				anthropic.NewTextBlock(msg.Content),
			))

		case RoleAssistant:
			claudeMessages = append(claudeMessages, anthropic.NewAssistantMessage(
				anthropic.NewTextBlock(msg.Content),
			))

		case RoleTool:
			// Claude uses tool_result content blocks in user messages
			// We need to add this as a user message with tool_result content
			claudeMessages = append(claudeMessages, anthropic.NewUserMessage(
				anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false),
			))

		case RoleSystem:
			// System messages are handled via the System parameter, not in Messages array
			// Skip them here as they should be passed via req.SystemPrompt
			continue
		}
	}

	return claudeMessages
}

// convertTools converts unified tools to Claude format
func (p *ClaudeProvider) convertTools(tools []Tool) []anthropic.ToolUnionParam {
	var claudeTools []anthropic.ToolUnionParam

	for _, tool := range tools {
		// Build ToolInputSchemaParam from parameters map
		var inputSchema anthropic.ToolInputSchemaParam

		// Extract properties and required fields from the tool parameters
		if props, ok := tool.Parameters["properties"]; ok {
			inputSchema.Properties = props
		}
		if req, ok := tool.Parameters["required"]; ok {
			if reqSlice, ok := req.([]interface{}); ok {
				inputSchema.Required = make([]string, len(reqSlice))
				for i, v := range reqSlice {
					if str, ok := v.(string); ok {
						inputSchema.Required[i] = str
					}
				}
			} else if reqSlice, ok := req.([]string); ok {
				inputSchema.Required = reqSlice
			}
		}

		// Copy any extra fields
		inputSchema.ExtraFields = make(map[string]any)
		for k, v := range tool.Parameters {
			if k != "properties" && k != "required" && k != "type" {
				inputSchema.ExtraFields[k] = v
			}
		}

		claudeTool := anthropic.ToolUnionParamOfTool(
			inputSchema,
			tool.Name,
		)

		// Add description if provided
		if tool.Description != "" {
			if claudeTool.OfTool != nil {
				claudeTool.OfTool.Description = anthropic.String(tool.Description)
			}
		}

		claudeTools = append(claudeTools, claudeTool)
	}

	return claudeTools
}

// convertResponse converts Claude response to unified format
func (p *ClaudeProvider) convertResponse(message *anthropic.Message) *ChatResponse {
	response := &ChatResponse{
		Model:    string(message.Model),
		Provider: "claude",
		Usage: Usage{
			PromptTokens:     int(message.Usage.InputTokens),
			CompletionTokens: int(message.Usage.OutputTokens),
			TotalTokens:      int(message.Usage.InputTokens + message.Usage.OutputTokens),
		},
		FinishReason: string(message.StopReason),
	}

	// Extract content and tool calls from response
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			textBlock := block.AsText()
			// Append text content
			if response.Content != "" {
				response.Content += "\n"
			}
			response.Content += textBlock.Text

		case "tool_use":
			toolBlock := block.AsToolUse()
			// Extract tool call
			// Arguments are in Input as json.RawMessage
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				ID:        toolBlock.ID,
				Name:      toolBlock.Name,
				Arguments: string(toolBlock.Input),
			})
		}
	}

	return response
}

// UpdateClient updates the Claude client with a new API key
func (p *ClaudeProvider) UpdateClient(apiKey string) {
	if apiKey != "" {
		p.apiKey = apiKey
		p.client = anthropic.NewClient(
			option.WithAPIKey(apiKey),
			option.WithHTTPClient(p.httpClient),
		)
	}
}
