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
	"sync"
	"time"
)

// defaultMaxNumCtx caps the num_ctx we ask Ollama to allocate, so an optimistic
// per-model context_window config cannot exhaust VRAM (WS1.4). Configurable per
// provider via ProviderConfig.Options["max_num_ctx"].
const defaultMaxNumCtx = 32768

// modelListCacheTTL bounds how long a fetched /api/tags model list is reused so
// HasModel / FindLocalProviderByModel do not issue a live HTTP request per task
// (WS7.29).
const modelListCacheTTL = 60 * time.Second

// OllamaProvider implements the Provider interface for Ollama
type OllamaProvider struct {
	baseURL              string
	httpClient           *http.Client
	maxNumCtx            int
	defaultContextWindow int            // provider-level default (0 = use fallback)
	contextWindows       map[string]int // per-model overrides, keyed lower-case

	// modelCache memoizes /api/tags results for modelListCacheTTL.
	modelCacheMu      sync.Mutex
	modelCache        []string
	modelCacheExpires time.Time
}

// NewOllamaProvider creates a new Ollama provider
func NewOllamaProvider(config ProviderConfig) *OllamaProvider {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	defaultWindow, perModel := resolveContextWindows(config)

	return &OllamaProvider{
		baseURL:              strings.TrimRight(baseURL, "/"),
		httpClient:           NewHTTPClient(DefaultLocalTimeout),
		maxNumCtx:            resolveMaxNumCtx(config),
		defaultContextWindow: defaultWindow,
		contextWindows:       perModel,
	}
}

// ModelContextWindow returns the configured context window for a specific model,
// falling back to the provider-level default (or 0 if neither is set, letting
// ResolveModelContextWindow fall back to Capabilities().MaxContextWindow).
func (p *OllamaProvider) ModelContextWindow(model string) int {
	if w, ok := p.contextWindows[strings.ToLower(strings.TrimSpace(model))]; ok && w > 0 {
		return w
	}
	return p.defaultContextWindow
}

// resolveMaxNumCtx reads an optional per-provider num_ctx ceiling from config,
// falling back to defaultMaxNumCtx.
func resolveMaxNumCtx(config ProviderConfig) int {
	if config.Options != nil {
		if v, ok := config.Options["max_num_ctx"]; ok {
			if n := toInt(v); n > 0 {
				return n
			}
		}
	}
	return defaultMaxNumCtx
}

// clampNumCtx bounds a requested context window by the provider's ceiling.
func (p *OllamaProvider) clampNumCtx(n int) int {
	ceiling := p.maxNumCtx
	if ceiling <= 0 {
		ceiling = defaultMaxNumCtx
	}
	if n > ceiling {
		return ceiling
	}
	return n
}

// Name returns the provider name
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Type returns the provider type
func (p *OllamaProvider) Type() ProviderType {
	return ProviderTypeLocal
}

// Capabilities returns what this provider supports. The context window reflects
// the configured provider-level default when set, else a conservative fallback;
// per-model values are resolved via ModelContextWindow / ResolveModelContextWindow.
func (p *OllamaProvider) Capabilities() ProviderCapabilities {
	window := p.defaultContextWindow
	if window <= 0 {
		window = defaultLocalContextWindow
	}
	return LocalProviderCapabilities(window)
}

// ValidateConfig validates the provider configuration
func (p *OllamaProvider) ValidateConfig(config ProviderConfig) error {
	if config.BaseURL == "" {
		return fmt.Errorf("baseURL is required for Ollama provider")
	}

	// Test connection to Ollama
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(config.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to Ollama at %s: %w", config.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("ollama server returned error status: %d", resp.StatusCode)
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

// fetchAvailableModels returns installed models, served from a short-lived
// cache (modelListCacheTTL) so per-task HasModel / provider-inference lookups
// do not each hit /api/tags (WS7.29). A failed refresh does not poison the
// cache; the caller sees the error and any prior cached value is left intact.
func (p *OllamaProvider) fetchAvailableModels() ([]string, error) {
	p.modelCacheMu.Lock()
	defer p.modelCacheMu.Unlock()

	if p.modelCache != nil && time.Now().Before(p.modelCacheExpires) {
		cached := make([]string, len(p.modelCache))
		copy(cached, p.modelCache)
		return cached, nil
	}

	models, err := p.fetchAvailableModelsUncached()
	if err != nil {
		return nil, err
	}

	p.modelCache = models
	p.modelCacheExpires = time.Now().Add(modelListCacheTTL)

	cached := make([]string, len(models))
	copy(cached, models)
	return cached, nil
}

// fetchAvailableModelsUncached performs the live /api/tags query.
func (p *OllamaProvider) fetchAvailableModelsUncached() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultModelFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models from Ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama API returned status %d", resp.StatusCode)
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

// HasModel checks if a specific model is available in Ollama
func (p *OllamaProvider) HasModel(modelName string) bool {
	models, err := p.fetchAvailableModels()
	if err != nil {
		return false
	}

	// Check for exact match
	for _, m := range models {
		if m == modelName {
			return true
		}
	}

	return false
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
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ollamaRequest represents a request to Ollama
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
}

// ollamaOptions represents Ollama request options.
// Temperature is a pointer so an explicit 0 (deterministic) is sent rather than
// dropped by omitempty — the whole point of WS7.27.
type ollamaOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"` // max tokens
	NumCtx      int      `json:"num_ctx,omitempty"`     // context window (prompt+gen)
}

// ollamaResponse represents a response from Ollama. The *_count fields carry
// token usage on the final (done) message for both blocking and streaming
// responses (WS7.28).
type ollamaResponse struct {
	Model           string        `json:"model"`
	CreatedAt       string        `json:"created_at"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

// usage converts Ollama's eval counters into the unified Usage shape.
func (r ollamaResponse) usage() Usage {
	return Usage{
		PromptTokens:     r.PromptEvalCount,
		CompletionTokens: r.EvalCount,
		TotalTokens:      r.PromptEvalCount + r.EvalCount,
	}
}

// toOllamaMessages converts unified messages (plus an optional system prompt)
// into Ollama's wire format. Shared by Chat and StreamChat so the two paths
// cannot drift.
func toOllamaMessages(req ChatRequest) []ollamaMessage {
	messages := make([]ollamaMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		ollamaMsg := ollamaMessage{Role: msg.Role, Content: msg.Content}
		if len(msg.Images) > 0 {
			ollamaMsg.Images = make([]string, len(msg.Images))
			for i, img := range msg.Images {
				// Ollama expects raw base64 data, not data URLs.
				ollamaMsg.Images[i] = img.Base64Data
			}
		}
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
	return messages
}

// toOllamaTools converts unified tool defs into Ollama's wire format.
func toOllamaTools(tools []Tool) []ollamaTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ollamaTool, len(tools))
	for i, tool := range tools {
		out[i] = ollamaTool{Type: "function", Function: ollamaFunctionDef(tool)}
	}
	return out
}

// buildOptions assembles Ollama's options block from the unified request,
// applying the temperature sentinel (>= 0 is sent, including 0) and clamping
// num_ctx. Returns nil when no option needs setting so the request stays lean.
func (p *OllamaProvider) buildOptions(req ChatRequest) *ollamaOptions {
	var opts ollamaOptions
	set := false
	if req.Temperature >= 0 {
		t := req.Temperature
		opts.Temperature = &t
		set = true
	}
	if req.MaxTokens > 0 {
		opts.NumPredict = req.MaxTokens
		set = true
	}
	if req.ContextWindowTokens > 0 {
		opts.NumCtx = p.clampNumCtx(req.ContextWindowTokens)
		set = true
	}
	if !set {
		return nil
	}
	return &opts
}

// buildRequest assembles a full Ollama chat request shared by both paths.
func (p *OllamaProvider) buildRequest(req ChatRequest, stream bool) ollamaRequest {
	return ollamaRequest{
		Model:    req.Model,
		Messages: toOllamaMessages(req),
		Stream:   stream,
		Options:  p.buildOptions(req),
		Tools:    toOllamaTools(req.Tools),
	}
}

// Chat sends a chat request to Ollama
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	ollamaReq := p.buildRequest(req, false)

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("ollama API error (status %d): failed to read error body: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to common format
	chatResp := &ChatResponse{
		Content:  ollamaResp.Message.Content,
		Model:    ollamaResp.Model,
		Provider: p.Name(),
		Usage:    ollamaResp.usage(),
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
	if chunk.Done {
		streamChunk.Usage = chunk.usage()
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
	ollamaReq := p.buildRequest(req, true)

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
		body, err := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Log but continue with the primary error
			fmt.Printf("Warning: failed to close response body: %v\n", closeErr)
		}
		if err != nil {
			return nil, fmt.Errorf("ollama API error (status %d): failed to read error body: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	return &ollamaStreamReader{
		scanner: bufio.NewScanner(resp.Body),
		resp:    resp,
	}, nil
}
