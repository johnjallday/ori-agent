package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
)

// Default max tokens per Claude model
var claudeModelDefaults = map[string]int64{
	"claude-opus-4-20250514":     4096,
	"claude-sonnet-4-20250514":   4096,
	"claude-3-5-sonnet-20241022": 8192,
	"claude-3-5-sonnet-latest":   8192,
	"claude-3-5-haiku-20241022":  8192,
	"claude-3-5-haiku-latest":    8192,
	"claude-3-opus-20240229":     4096,
	"claude-3-opus-latest":       4096,
	"claude-3-sonnet-20240229":   4096,
	"claude-3-haiku-20240307":    4096,
}

// ClaudeProvider implements the Provider interface for Anthropic's Claude
type ClaudeProvider struct {
	mu         sync.RWMutex
	client     anthropic.Client
	apiKey     string
	httpClient *http.Client
}

// snapshot returns the current client and api key under read lock.
func (p *ClaudeProvider) snapshot() (anthropic.Client, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client, p.apiKey
}

// NewClaudeProvider creates a new Claude provider
func NewClaudeProvider(config ProviderConfig) *ClaudeProvider {
	httpClient := NewHTTPClient(DefaultCloudTimeout)

	var client anthropic.Client
	if config.APIKey != "" {
		client = anthropic.NewClient(
			option.WithHTTPClient(httpClient),
			option.WithAPIKey(config.APIKey),
			// One shared retry budget, owned by Ori — see the OpenAI provider.
			option.WithMaxRetries(0),
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

// Capabilities returns Claude's capabilities. Streaming is currently
// unimplemented, so the cloud-default `true` is overridden here to keep
// the advertised surface honest.
func (p *ClaudeProvider) Capabilities() ProviderCapabilities {
	caps := CloudProviderCapabilities(200000) // Claude 3.5 Sonnet context window
	caps.SupportsStreaming = false
	return caps
}

// ValidateConfig validates the Claude configuration
func (p *ClaudeProvider) ValidateConfig(config ProviderConfig) error {
	if config.APIKey == "" {
		return fmt.Errorf("claude API key is required")
	}
	return nil
}

// DefaultModels returns available Claude models from the curated pricing data.
// This provides a consistent list without requiring an API call.
func (p *ClaudeProvider) DefaultModels() []string {
	return modelinfo.GetClaudeModels()
}

// Chat sends a chat completion request to Claude
func (p *ClaudeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to Claude format
	messages := p.convertMessages(req.Messages)

	// Build Claude request parameters
	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		// Use model-specific default, fallback to 4096
		if modelDefault, ok := claudeModelDefaults[req.Model]; ok {
			maxTokens = modelDefault
		} else {
			maxTokens = 4096 // Fallback default
		}
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
	client, _ := p.snapshot()
	message, err := client.Messages.New(ctx, params)
	if err != nil {
		return nil, classifyAnthropicError("anthropic", err)
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
			// Build content blocks for the user message
			var contentBlocks []anthropic.ContentBlockParamUnion

			// Add text content first
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(msg.Content))
			}

			// Add image blocks if present (for vision support)
			for _, img := range msg.Images {
				contentBlocks = append(contentBlocks, anthropic.NewImageBlockBase64(
					img.MimeType,
					img.Base64Data,
				))
			}

			// Only add the message if there's content
			if len(contentBlocks) > 0 {
				claudeMessages = append(claudeMessages, anthropic.NewUserMessage(contentBlocks...))
			}

		case RoleAssistant:
			// Assistant turns must replay tool_use blocks alongside any text:
			// a later tool_result referencing a tool_use the API never saw is
			// rejected, which breaks every multi-turn tool conversation.
			var assistantBlocks []anthropic.ContentBlockParamUnion
			if msg.Content != "" {
				assistantBlocks = append(assistantBlocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, toolCall := range msg.ToolCalls {
				input := json.RawMessage(toolCall.Arguments)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				assistantBlocks = append(assistantBlocks, anthropic.NewToolUseBlock(toolCall.ID, input, toolCall.Name))
			}
			if len(assistantBlocks) > 0 {
				claudeMessages = append(claudeMessages, anthropic.NewAssistantMessage(assistantBlocks...))
			}

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
			if reqSlice, ok := req.([]any); ok {
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
	if apiKey == "" {
		return
	}
	httpClient := NewHTTPClient(DefaultCloudTimeout)
	client := anthropic.NewClient(
		option.WithHTTPClient(httpClient),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0), // Ori owns the retry budget; see NewClaudeProvider.
	)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiKey = apiKey
	p.httpClient = httpClient
	p.client = client
}
