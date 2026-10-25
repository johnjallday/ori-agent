package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
)

// claudeModelCacheTTL bounds how long the model list fetched from Anthropic is
// reused. Anthropic adds models rarely, so an hour keeps the list current
// without a request each time a model picker opens or a task checks its model.
const claudeModelCacheTTL = time.Hour

// claudeModelRetryAfter is how long a failed model-list fetch is remembered,
// so an offline machine does not wait on the fetch timeout at every call.
const claudeModelRetryAfter = time.Minute

// claudeDefaultMaxTokens caps a reply when the caller sets no MaxTokens.
// Current models think before they answer and the thinking counts against
// this cap, so it needs room; a much larger cap on a non-streaming call is
// refused by the SDK because the reply could outlast its HTTP timeout.
const claudeDefaultMaxTokens int64 = 16000

// claudeTemperatureModelPrefixes are the models still served that accept a
// temperature. Claude Opus 4.7 and every later model reject one with a 400
// (Claude Sonnet 5.5 and Claude Haiku 5.5 any value but the default), so a
// model not listed here, including one released later, is sent none.
var claudeTemperatureModelPrefixes = []string{
	"claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5",
	"claude-opus-4-6", "claude-sonnet-4-6",
}

// claudeModel is one model from Anthropic's model list.
type claudeModel struct {
	id        string
	maxTokens int64 // the largest max_tokens the model accepts
}

// ClaudeProvider implements the Provider interface for Anthropic's Claude
type ClaudeProvider struct {
	mu         sync.RWMutex
	client     anthropic.Client
	apiKey     string
	httpClient *http.Client

	// fetchMu lets one model-list fetch run at a time; callers that wait on
	// it reuse its result. modelsMu guards the cached list, so reading it
	// never waits on a fetch.
	fetchMu        sync.Mutex
	modelsMu       sync.RWMutex
	models         []claudeModel // newest first
	modelsLoaded   time.Time
	modelsFailedAt time.Time
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
	// A plain API call: this provider adds no tool of its own to a request.
	caps.SupportsIsolatedChat = true
	return caps
}

// ValidateConfig validates the Claude configuration
func (p *ClaudeProvider) ValidateConfig(config ProviderConfig) error {
	if config.APIKey == "" {
		return fmt.Errorf("claude API key is required")
	}
	return nil
}

// DefaultModels returns the models Anthropic lists for this API key, newest
// first, or the curated catalog when that list cannot be fetched.
func (p *ClaudeProvider) DefaultModels() []string {
	models := p.liveModels()
	if len(models) == 0 {
		return modelinfo.GetClaudeModels()
	}
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.id
	}
	return ids
}

// liveModels returns Anthropic's model list for this key, fetching it when
// the cached copy is older than claudeModelCacheTTL. A failed fetch keeps the
// previous list and is not retried for claudeModelRetryAfter.
func (p *ClaudeProvider) liveModels() []claudeModel {
	p.fetchMu.Lock()
	defer p.fetchMu.Unlock()

	p.modelsMu.RLock()
	models, loaded, failedAt := p.models, p.modelsLoaded, p.modelsFailedAt
	p.modelsMu.RUnlock()
	now := time.Now()
	if (len(models) > 0 && now.Sub(loaded) < claudeModelCacheTTL) ||
		(!failedAt.IsZero() && now.Sub(failedAt) < claudeModelRetryAfter) {
		return models
	}

	client, apiKey := p.snapshot()
	fetched, err := fetchClaudeModels(client, apiKey)

	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if _, current := p.snapshot(); current != apiKey {
		// The key changed during the fetch; its list belongs to the old key.
		return p.models
	}
	if err != nil {
		p.modelsFailedAt = now
		return p.models
	}
	p.models, p.modelsLoaded, p.modelsFailedAt = fetched, now, time.Time{}
	return p.models
}

// fetchClaudeModels lists the models the key can call. Retired models are
// left out, and each dated model is preceded by its undated alias when the
// curated catalog knows that alias (claude-opus-4-5 for
// claude-opus-4-5-20251101): the API lists only dated IDs, and agents and
// the system model may be set to the alias.
func fetchClaudeModels(client anthropic.Client, apiKey string) ([]claudeModel, error) {
	if apiKey == "" {
		return nil, errors.New("claude API key is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultModelFetchTimeout)
	defer cancel()

	var infos []anthropic.ModelInfo
	pager := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{Limit: anthropic.Int(1000)})
	for pager.Next() {
		info := pager.Current()
		if info.ID == "" || info.Lifecycle == anthropic.ModelInfoLifecycleRetired {
			continue
		}
		infos = append(infos, info)
	}
	if err := pager.Err(); err != nil {
		return nil, classifyAnthropicError("anthropic", err)
	}
	if len(infos) == 0 {
		return nil, errors.New("anthropic listed no models")
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].CreatedAt.Equal(infos[j].CreatedAt) {
			return infos[i].CreatedAt.After(infos[j].CreatedAt)
		}
		return infos[i].ID < infos[j].ID
	})

	listed := make(map[string]bool, len(infos))
	for _, info := range infos {
		listed[info.ID] = true
	}
	models := make([]claudeModel, 0, len(infos))
	for _, info := range infos {
		if alias := claudeModelAlias(info.ID); alias != "" && !listed[alias] {
			listed[alias] = true
			models = append(models, claudeModel{id: alias, maxTokens: info.MaxTokens})
		}
		models = append(models, claudeModel{id: info.ID, maxTokens: info.MaxTokens})
	}
	return models, nil
}

// claudeModelAlias returns the undated alias of a dated model ID such as
// claude-opus-4-5-20251101, or "" when the ID has no date suffix or the
// curated catalog does not list the alias.
func claudeModelAlias(id string) string {
	i := strings.LastIndexByte(id, '-')
	if i < 0 || len(id)-i-1 != 8 {
		return ""
	}
	for _, r := range id[i+1:] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if alias := id[:i]; modelinfo.HasClaudeModel(alias) {
		return alias
	}
	return ""
}

// knownMaxTokens is the largest max_tokens Anthropic listed for model, or 0
// when the model is not in the cached list. It never fetches the list, so a
// chat call never waits on one.
func (p *ClaudeProvider) knownMaxTokens(model string) int64 {
	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	for _, m := range p.models {
		if m.id == model {
			return m.maxTokens
		}
	}
	return 0
}

// ClaudeAPIModelForAlias returns the Claude API model that a Claude Code alias
// ("haiku", "sonnet" or "opus") stands for when the request goes to the API
// instead of the CLI, or "" for any other name.
func ClaudeAPIModelForAlias(alias string) string {
	switch strings.ToLower(strings.TrimSpace(alias)) {
	case "haiku":
		return "claude-haiku-5-5"
	case "sonnet":
		return "claude-sonnet-5-5"
	case "opus":
		return "claude-opus-5-5"
	}
	return ""
}

// claudeAcceptsTemperature reports whether model accepts a temperature; see
// claudeTemperatureModelPrefixes.
func claudeAcceptsTemperature(model string) bool {
	for _, prefix := range claudeTemperatureModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// Chat sends a chat completion request to Claude
func (p *ClaudeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if err := CheckIsolatedRequest(req); err != nil {
		return nil, err
	}
	// Convert messages to Claude format
	messages := p.convertMessages(req.Messages)

	// Build Claude request parameters
	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = claudeDefaultMaxTokens
	}
	// A max_tokens above the model's own limit is a 400.
	if limit := p.knownMaxTokens(req.Model); limit > 0 && maxTokens > limit {
		maxTokens = limit
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	// Add system prompt if specified
	if system := claudeSystemText(req); system != "" {
		params.System = []anthropic.TextBlockParam{
			{
				Text: system,
			},
		}
	}

	// Add temperature if specified and the model accepts one
	if req.Temperature > 0 && claudeAcceptsTemperature(req.Model) {
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

// claudeSystemText is the system text a request sends: SystemPrompt first, then
// the content of every system-role message in order, joined by a blank line,
// with empty ones left out. The Anthropic API takes its system text apart from
// the messages, so a system-role message that was only skipped would lose its
// caller's instructions without an error, while every other provider sends it.
func claudeSystemText(req ChatRequest) string {
	var parts []string
	if strings.TrimSpace(req.SystemPrompt) != "" {
		parts = append(parts, req.SystemPrompt)
	}
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem && strings.TrimSpace(msg.Content) != "" {
			parts = append(parts, msg.Content)
		}
	}
	return strings.Join(parts, "\n\n")
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
			// Sent as part of the System parameter by claudeSystemText, never as
			// a message: the Anthropic API has no system role in Messages.
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
	p.apiKey = apiKey
	p.httpClient = httpClient
	p.client = client
	p.mu.Unlock()

	// A different key can call a different set of models.
	p.modelsMu.Lock()
	p.models, p.modelsLoaded, p.modelsFailedAt = nil, time.Time{}, time.Time{}
	p.modelsMu.Unlock()
}
