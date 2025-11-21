package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaProvider implements the Provider interface for Ollama
type OllamaProvider struct {
	baseURL    string
	httpClient *http.Client
}

// NewOllamaProvider creates a new Ollama provider
func NewOllamaProvider(config ProviderConfig) *OllamaProvider {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	httpClient := &http.Client{
		Timeout: 5 * time.Minute, // Longer timeout for local models
	}

	return &OllamaProvider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// Name returns the provider name
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Type returns the provider type
func (p *OllamaProvider) Type() ProviderType {
	return ProviderTypeLocal
}

// Capabilities returns what this provider supports
func (p *OllamaProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          true, // Ollama supports tool calling
		SupportsStreaming:      true,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    true,
		RequiresAPIKey:         false,
		SupportsCustomEndpoint: true,
		MaxContextWindow:       8192, // Varies by model, using conservative default
		SupportedFormats:       []string{"text"},
	}
}

// ValidateConfig validates the provider configuration
func (p *OllamaProvider) ValidateConfig(config ProviderConfig) error {
	if config.BaseURL == "" {
		return fmt.Errorf("baseURL is required for Ollama provider")
	}

	// Test connection to Ollama
	resp, err := http.Get(config.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to Ollama at %s: %w", config.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("Ollama server returned error status: %d", resp.StatusCode)
	}

	return nil
}

// ollamaModel represents a model from Ollama's /api/tags endpoint
type ollamaModel struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// ollamaTagsResponse represents the response from /api/tags
type ollamaTagsResponse struct {
	Models []ollamaModel `json:"models"`
}

// DefaultModels returns the available models from the Ollama instance
func (p *OllamaProvider) DefaultModels() []string {
	// Try to fetch models from Ollama
	models, err := p.fetchAvailableModels()
	if err != nil {
		// Fallback to hardcoded list if Ollama is not available
		return []string{
			"llama2",
			"llama2:13b",
			"llama2:70b",
			"mistral",
			"mixtral",
			"codellama",
			"phi",
			"neural-chat",
			"starling-lm",
			"orca-mini",
			"vicuna",
		}
	}
	return models
}

// fetchAvailableModels queries Ollama for installed models
func (p *OllamaProvider) fetchAvailableModels() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models from Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama API returned status %d", resp.StatusCode)
	}

	var tagsResp ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract model names
	models := make([]string, 0, len(tagsResp.Models))
	for _, model := range tagsResp.Models {
		models = append(models, model.Name)
	}

	// If no models found, return error to trigger fallback
	if len(models) == 0 {
		return nil, fmt.Errorf("no models found in Ollama")
	}

	return models, nil
}

// ollamaMessage represents a message in Ollama format
type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

// ollamaToolCall represents a tool call in Ollama format
type ollamaToolCall struct {
	Function ollamaFunction `json:"function"`
}

// ollamaFunction represents a function call
type ollamaFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ollamaTool represents a tool definition in Ollama format
type ollamaTool struct {
	Type     string            `json:"type"`
	Function ollamaFunctionDef `json:"function"`
}

// ollamaFunctionDef represents a function definition
type ollamaFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ollamaRequest represents a request to Ollama
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
}

// ollamaOptions represents Ollama request options
type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"` // max tokens
}

// ollamaResponse represents a response from Ollama
type ollamaResponse struct {
	Model     string        `json:"model"`
	CreatedAt string        `json:"created_at"`
	Message   ollamaMessage `json:"message"`
	Done      bool          `json:"done"`
}

// Chat sends a chat request to Ollama
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// Convert messages to Ollama format
	messages := make([]ollamaMessage, 0, len(req.Messages))

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		messages = append(messages, ollamaMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		ollamaMsg := ollamaMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Convert tool calls if present (for assistant messages)
		if len(msg.ToolCalls) > 0 {
			ollamaMsg.ToolCalls = make([]ollamaToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				ollamaMsg.ToolCalls[i] = ollamaToolCall{
					Function: ollamaFunction{
						Name:      tc.Name,
						Arguments: json.RawMessage(tc.Arguments),
					},
				}
			}
		}

		messages = append(messages, ollamaMsg)
	}

	// Build request options
	var options *ollamaOptions
	if req.Temperature > 0 || req.MaxTokens > 0 {
		options = &ollamaOptions{}
		if req.Temperature > 0 {
			options.Temperature = req.Temperature
		}
		if req.MaxTokens > 0 {
			options.NumPredict = req.MaxTokens
		}
	}

	// Convert tools to Ollama format
	var tools []ollamaTool
	if len(req.Tools) > 0 {
		tools = make([]ollamaTool, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = ollamaTool{
				Type:     "function",
				Function: ollamaFunctionDef(tool),
			}
		}
	}

	// Build Ollama request
	ollamaReq := ollamaRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   false,
		Options:  options,
		Tools:    tools,
	}

	// Marshal request
	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Debug: Log the request being sent to Ollama
	fmt.Printf("🐛 Ollama Request: %s\n", string(reqBody))

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Debug: Log the response from Ollama
	respJSON, _ := json.Marshal(ollamaResp)
	fmt.Printf("🐛 Ollama Response: %s\n", string(respJSON))

	// Convert to common format
	chatResp := &ChatResponse{
		Content: ollamaResp.Message.Content,
	}

	// Convert tool calls if present
	if len(ollamaResp.Message.ToolCalls) > 0 {
		chatResp.ToolCalls = make([]ToolCall, len(ollamaResp.Message.ToolCalls))
		for i, tc := range ollamaResp.Message.ToolCalls {
			chatResp.ToolCalls[i] = ToolCall{
				ID:        fmt.Sprintf("call_%d", i), // Generate ID
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			}
		}
	}

	return chatResp, nil
}

// ollamaStreamReader implements StreamReader for Ollama streaming responses
type ollamaStreamReader struct {
	scanner *bufio.Scanner
	resp    *http.Response
}

// Next reads the next chunk from the stream
func (r *ollamaStreamReader) Next() (*StreamChunk, error) {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}

	line := r.scanner.Text()
	if line == "" {
		return r.Next() // Skip empty lines
	}

	var chunk ollamaResponse
	if err := json.Unmarshal([]byte(line), &chunk); err != nil {
		return nil, fmt.Errorf("failed to decode stream chunk: %w", err)
	}

	streamChunk := &StreamChunk{
		Content: chunk.Message.Content,
		Done:    chunk.Done,
	}

	// Handle tool calls if present
	if len(chunk.Message.ToolCalls) > 0 {
		// For now, just handle the first tool call
		tc := chunk.Message.ToolCalls[0]
		streamChunk.ToolCall = &ToolCall{
			ID:        "call_0",
			Name:      tc.Function.Name,
			Arguments: string(tc.Function.Arguments),
		}
	}

	if chunk.Done {
		return streamChunk, io.EOF
	}

	return streamChunk, nil
}

// Close closes the stream reader
func (r *ollamaStreamReader) Close() error {
	if r.resp != nil && r.resp.Body != nil {
		return r.resp.Body.Close()
	}
	return nil
}

// StreamChat sends a streaming chat request to Ollama
func (p *OllamaProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	// Convert messages to Ollama format
	messages := make([]ollamaMessage, 0, len(req.Messages))

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		messages = append(messages, ollamaMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, msg := range req.Messages {
		ollamaMsg := ollamaMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Convert tool calls if present (for assistant messages)
		if len(msg.ToolCalls) > 0 {
			ollamaMsg.ToolCalls = make([]ollamaToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				ollamaMsg.ToolCalls[i] = ollamaToolCall{
					Function: ollamaFunction{
						Name:      tc.Name,
						Arguments: json.RawMessage(tc.Arguments),
					},
				}
			}
		}

		messages = append(messages, ollamaMsg)
	}

	// Build request options
	var options *ollamaOptions
	if req.Temperature > 0 || req.MaxTokens > 0 {
		options = &ollamaOptions{}
		if req.Temperature > 0 {
			options.Temperature = req.Temperature
		}
		if req.MaxTokens > 0 {
			options.NumPredict = req.MaxTokens
		}
	}

	// Convert tools to Ollama format
	var tools []ollamaTool
	if len(req.Tools) > 0 {
		tools = make([]ollamaTool, len(req.Tools))
		for i, tool := range req.Tools {
			tools[i] = ollamaTool{
				Type:     "function",
				Function: ollamaFunctionDef(tool),
			}
		}
	}

	// Build Ollama request with streaming enabled
	ollamaReq := ollamaRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   true,
		Options:  options,
		Tools:    tools,
	}

	// Marshal request
	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	return &ollamaStreamReader{
		scanner: bufio.NewScanner(resp.Body),
		resp:    resp,
	}, nil
}
