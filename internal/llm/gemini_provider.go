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
)

const geminiDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiProvider implements the Provider interface for Google Gemini (AI Studio).
type GeminiProvider struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider(config ProviderConfig) *GeminiProvider {
	httpClient := NewHTTPClient(DefaultCloudTimeout)
	baseURL := geminiDefaultBaseURL
	if config.BaseURL != "" {
		baseURL = strings.TrimRight(config.BaseURL, "/")
	}

	return &GeminiProvider{
		apiKey:     config.APIKey,
		httpClient: httpClient,
		baseURL:    baseURL,
	}
}

// Name returns the provider name.
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// Type returns the provider type.
func (p *GeminiProvider) Type() ProviderType {
	return ProviderTypeCloud
}

// Capabilities returns Gemini's capabilities.
func (p *GeminiProvider) Capabilities() ProviderCapabilities {
	return CloudProviderCapabilities(0)
}

// ValidateConfig validates the Gemini configuration.
func (p *GeminiProvider) ValidateConfig(config ProviderConfig) error {
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("Gemini API key is required")
	}
	return nil
}

// DefaultModels returns the default Gemini models.
func (p *GeminiProvider) DefaultModels() []string {
	return []string{
		"gemini-2.5-flash",
		"gemini-2.5-pro",
	}
}

// Chat sends a chat request to Gemini.
func (p *GeminiProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("Gemini API key is required (set GEMINI_API_KEY or configure it in settings)")
	}

	requestBody, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/%s:generateContent", p.baseURL, geminiModelPath(req.Model))
	respBody, err := p.doRequest(ctx, endpoint, requestBody)
	if err != nil {
		return nil, err
	}

	if len(respBody.Candidates) == 0 {
		return nil, fmt.Errorf("gemini returned no response candidates - model %q may be invalid or unavailable", req.Model)
	}

	return p.convertResponse(respBody, req.Model), nil
}

// StreamChat streams a chat response from Gemini.
func (p *GeminiProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("Gemini API key is required (set GEMINI_API_KEY or configure it in settings)")
	}

	requestBody, err := p.buildRequest(req)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", p.baseURL, geminiModelPath(req.Model))
	httpReq, err := p.newRequest(ctx, endpoint, requestBody)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("gemini API error (status %d): failed to read error body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("gemini API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return &geminiStreamReader{
		scanner: bufio.NewScanner(resp.Body),
		resp:    resp,
	}, nil
}

func (p *GeminiProvider) buildRequest(req ChatRequest) (*geminiGenerateContentRequest, error) {
	var contents []geminiContent
	var systemParts []geminiPart

	if strings.TrimSpace(req.SystemPrompt) != "" {
		systemParts = append(systemParts, geminiPart{Text: strings.TrimSpace(req.SystemPrompt)})
	}

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, geminiPart{Text: strings.TrimSpace(msg.Content)})
			}
			continue
		}

		role := geminiRoleForMessage(msg)
		if role == "" {
			continue
		}

		var parts []geminiPart
		isToolMessage := msg.Role == RoleTool
		if strings.TrimSpace(msg.Content) != "" && !isToolMessage {
			parts = append(parts, geminiPart{Text: msg.Content})
		}

		for _, img := range msg.Images {
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: img.MimeType,
					Data:     img.Base64Data,
				},
			})
		}

		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				args := parseGeminiToolArguments(tc.Arguments)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: args,
					},
				})
			}
		}

		if msg.Role == RoleTool {
			name := toolCallNameFromMessage(msg)
			if name != "" {
				parts = append(parts, geminiPart{
					FunctionResponse: &geminiFunctionResponse{
						Name:     name,
						Response: buildGeminiFunctionResponse(msg.Content),
					},
				})
			} else if strings.TrimSpace(msg.Content) != "" {
				parts = append(parts, geminiPart{Text: msg.Content})
			}
		}

		if len(parts) == 0 {
			continue
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	var systemInstruction *geminiSystemInstruction
	if len(systemParts) > 0 {
		systemInstruction = &geminiSystemInstruction{
			Parts: systemParts,
		}
	}

	var tools []geminiTool
	if len(req.Tools) > 0 {
		funcDecls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, tool := range req.Tools {
			funcDecls = append(funcDecls, geminiFunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			})
		}
		tools = []geminiTool{{FunctionDeclarations: funcDecls}}
	}

	genConfig := &geminiGenerationConfig{}
	temp := req.Temperature
	genConfig.Temperature = &temp
	if req.MaxTokens > 0 {
		maxTokens := req.MaxTokens
		genConfig.MaxOutputTokens = &maxTokens
	}

	requestBody := &geminiGenerateContentRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             tools,
		GenerationConfig:  genConfig,
	}

	return requestBody, nil
}

func (p *GeminiProvider) doRequest(ctx context.Context, endpoint string, body *geminiGenerateContentRequest) (*geminiGenerateContentResponse, error) {
	httpReq, err := p.newRequest(ctx, endpoint, body)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("gemini API error (status %d): failed to read error body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("gemini API error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var response geminiGenerateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}

func (p *GeminiProvider) newRequest(ctx context.Context, endpoint string, body *geminiGenerateContentRequest) (*http.Request, error) {
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)
	return httpReq, nil
}

func (p *GeminiProvider) convertResponse(resp *geminiGenerateContentResponse, model string) *ChatResponse {
	response := &ChatResponse{
		Model:    model,
		Provider: "gemini",
	}

	if resp.UsageMetadata != nil {
		response.Usage = Usage{
			PromptTokens:     resp.UsageMetadata.PromptTokenCount,
			CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      resp.UsageMetadata.TotalTokenCount,
		}
	}

	if len(resp.Candidates) == 0 {
		return response
	}

	candidate := resp.Candidates[0]
	response.FinishReason = candidate.FinishReason

	var textParts []string
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsJSON := "{}"
			if part.FunctionCall.Args != nil {
				if argBytes, err := json.Marshal(part.FunctionCall.Args); err == nil {
					argsJSON = string(argBytes)
				}
			}
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				ID:        geminiToolCallID(part.FunctionCall.Name, len(response.ToolCalls)),
				Name:      part.FunctionCall.Name,
				Arguments: argsJSON,
			})
		}
	}

	response.Content = strings.Join(textParts, "\n")

	return response
}

func geminiModelPath(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

func geminiRoleForMessage(msg Message) string {
	switch msg.Role {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "model"
	case RoleTool:
		return "function"
	default:
		return ""
	}
}

func geminiToolCallID(name string, index int) string {
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("call_%d", index)
	}
	return fmt.Sprintf("%s:%d", name, index)
}

func toolCallNameFromMessage(msg Message) string {
	if strings.TrimSpace(msg.Name) != "" {
		return msg.Name
	}
	if strings.TrimSpace(msg.ToolCallID) == "" {
		return ""
	}
	if colon := strings.Index(msg.ToolCallID, ":"); colon > 0 {
		return msg.ToolCallID[:colon]
	}
	return msg.ToolCallID
}

func parseGeminiToolArguments(raw string) map[string]interface{} {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]interface{}{
			"raw": raw,
		}
	}
	return parsed
}

func buildGeminiFunctionResponse(content string) interface{} {
	if strings.TrimSpace(content) == "" {
		return map[string]interface{}{
			"output": "",
		}
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return map[string]interface{}{
			"output": content,
		}
	}

	if parsedMap, ok := parsed.(map[string]interface{}); ok {
		if _, hasOutput := parsedMap["output"]; hasOutput {
			return parsedMap
		}
		if _, hasError := parsedMap["error"]; hasError {
			return parsedMap
		}
	}

	return map[string]interface{}{
		"output": parsed,
	}
}

// Gemini request/response structures.
type geminiGenerateContentRequest struct {
	Contents          []geminiContent          `json:"contents,omitempty"`
	SystemInstruction *geminiSystemInstruction `json:"system_instruction,omitempty"`
	Tools             []geminiTool             `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiGenerateContentResponse struct {
	Candidates    []geminiCandidate   `json:"candidates,omitempty"`
	UsageMetadata *geminiUsageMetrics `json:"usageMetadata,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content,omitempty"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiUsageMetrics struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

func (p *geminiPart) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if v, ok := raw["text"]; ok {
		_ = json.Unmarshal(v, &p.Text)
	}
	if v, ok := raw["inline_data"]; ok {
		var inline geminiInlineData
		if err := json.Unmarshal(v, &inline); err == nil {
			p.InlineData = &inline
		}
	}
	if v, ok := raw["inlineData"]; ok {
		var inline geminiInlineData
		if err := json.Unmarshal(v, &inline); err == nil {
			p.InlineData = &inline
		}
	}
	if v, ok := raw["functionCall"]; ok {
		var call geminiFunctionCall
		if err := json.Unmarshal(v, &call); err == nil {
			p.FunctionCall = &call
		}
	}
	if v, ok := raw["function_call"]; ok {
		var call geminiFunctionCall
		if err := json.Unmarshal(v, &call); err == nil {
			p.FunctionCall = &call
		}
	}
	if v, ok := raw["functionResponse"]; ok {
		var resp geminiFunctionResponse
		if err := json.Unmarshal(v, &resp); err == nil {
			p.FunctionResponse = &resp
		}
	}
	if v, ok := raw["function_response"]; ok {
		var resp geminiFunctionResponse
		if err := json.Unmarshal(v, &resp); err == nil {
			p.FunctionResponse = &resp
		}
	}
	return nil
}

type geminiInlineData struct {
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name,omitempty"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string      `json:"name,omitempty"`
	Response interface{} `json:"response,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

// geminiStreamReader implements StreamReader for Gemini streaming responses.
type geminiStreamReader struct {
	scanner *bufio.Scanner
	resp    *http.Response
}

func (r *geminiStreamReader) Next() (*StreamChunk, error) {
	for r.scanner.Scan() {
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return &StreamChunk{Done: true}, io.EOF
			}

			var resp geminiGenerateContentResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				return nil, fmt.Errorf("failed to decode stream chunk: %w", err)
			}

			chunk := geminiStreamChunkFromResponse(&resp)
			if chunk.Done {
				return chunk, io.EOF
			}
			return chunk, nil
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (r *geminiStreamReader) Close() error {
	if r.resp != nil && r.resp.Body != nil {
		return r.resp.Body.Close()
	}
	return nil
}

func geminiStreamChunkFromResponse(resp *geminiGenerateContentResponse) *StreamChunk {
	chunk := &StreamChunk{}
	if resp == nil || len(resp.Candidates) == 0 {
		return chunk
	}

	candidate := resp.Candidates[0]
	if candidate.FinishReason != "" {
		chunk.Done = true
	}

	var textParts []string
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsJSON := "{}"
			if part.FunctionCall.Args != nil {
				if argBytes, err := json.Marshal(part.FunctionCall.Args); err == nil {
					argsJSON = string(argBytes)
				}
			}
			chunk.ToolCall = &ToolCall{
				ID:        geminiToolCallID(part.FunctionCall.Name, 0),
				Name:      part.FunctionCall.Name,
				Arguments: argsJSON,
			}
		}
	}
	chunk.Content = strings.Join(textParts, "\n")
	return chunk
}
