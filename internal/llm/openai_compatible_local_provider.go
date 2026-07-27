package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const (
	lmStudioDefaultBaseURL = "http://localhost:1234/v1"
	mlxLMDefaultBaseURL    = "http://localhost:8080/v1"
)

type openAICompatibleModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// OpenAICompatibleLocalProvider implements local providers that expose
// OpenAI-compatible chat completion endpoints, such as LM Studio and MLX-LM.
type OpenAICompatibleLocalProvider struct {
	name                 string
	baseURL              string
	client               openai.Client
	httpClient           *http.Client
	fallbackModels       []string
	defaultContextWindow int            // provider-level default (0 = use fallback)
	contextWindows       map[string]int // per-model overrides, keyed lower-case

	// modelCache memoizes /models results for modelListCacheTTL so per-task
	// HasModel lookups do not each hit the server (WS7.29).
	modelCacheMu      sync.Mutex
	modelCache        []string
	modelCacheExpires time.Time
}

// NewLMStudioProvider creates a provider for LM Studio's OpenAI-compatible server.
func NewLMStudioProvider(config ProviderConfig) *OpenAICompatibleLocalProvider {
	return newOpenAICompatibleLocalProvider("lmstudio", lmStudioDefaultBaseURL, config)
}

// NewMLXLMProvider creates a provider for mlx_lm.server.
func NewMLXLMProvider(config ProviderConfig) *OpenAICompatibleLocalProvider {
	return newOpenAICompatibleLocalProvider("mlx_lm", mlxLMDefaultBaseURL, config)
}

func newOpenAICompatibleLocalProvider(name, defaultBaseURL string, config ProviderConfig) *OpenAICompatibleLocalProvider {
	baseURL := normalizeOpenAICompatibleBaseURL(config.BaseURL, defaultBaseURL)
	httpClient := NewHTTPClient(DefaultLocalTimeout)

	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(httpClient),
		// Local OpenAI-compatible servers typically do not require auth. Clear any
		// inherited OpenAI environment headers so we do not leak cloud credentials.
		option.WithHeader("authorization", ""),
		option.WithHeader("OpenAI-Organization", ""),
		option.WithHeader("OpenAI-Project", ""),
		// Ori owns the retry budget; see NewOpenAIProvider.
		option.WithMaxRetries(0),
	}

	fallbackModels := make([]string, 0, 1)
	if model := strings.TrimSpace(config.Model); model != "" {
		fallbackModels = append(fallbackModels, model)
	}

	defaultWindow, perModel := resolveContextWindows(config)

	return &OpenAICompatibleLocalProvider{
		name:                 name,
		baseURL:              baseURL,
		client:               openai.NewClient(opts...),
		httpClient:           httpClient,
		fallbackModels:       fallbackModels,
		defaultContextWindow: defaultWindow,
		contextWindows:       perModel,
	}
}

// ModelContextWindow returns the configured context window for a specific model,
// falling back to the provider-level default (0 if neither is set). For these
// providers the value is advisory — the server sizes its own context — and is
// used for prompt budgeting only (WS1.1).
func (p *OpenAICompatibleLocalProvider) ModelContextWindow(model string) int {
	if w, ok := p.contextWindows[strings.ToLower(strings.TrimSpace(model))]; ok && w > 0 {
		return w
	}
	return p.defaultContextWindow
}

func normalizeOpenAICompatibleBaseURL(baseURL, defaultBaseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func (p *OpenAICompatibleLocalProvider) Name() string {
	return p.name
}

func (p *OpenAICompatibleLocalProvider) Type() ProviderType {
	return ProviderTypeLocal
}

func (p *OpenAICompatibleLocalProvider) Capabilities() ProviderCapabilities {
	window := p.defaultContextWindow
	if window <= 0 {
		window = defaultLocalContextWindow
	}
	return LocalProviderCapabilities(window)
}

func (p *OpenAICompatibleLocalProvider) ValidateConfig(config ProviderConfig) error {
	baseURL := normalizeOpenAICompatibleBaseURL(config.BaseURL, p.baseURL)
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s provider: %w", p.name, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s at %s: %w", p.name, baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s server returned error status: %d", p.name, resp.StatusCode)
	}

	return nil
}

func (p *OpenAICompatibleLocalProvider) DefaultModels() []string {
	models, err := p.fetchAvailableModels()
	if err == nil {
		return models
	}

	if len(p.fallbackModels) == 0 {
		return []string{}
	}

	models = make([]string, len(p.fallbackModels))
	copy(models, p.fallbackModels)
	return models
}

func (p *OpenAICompatibleLocalProvider) HasModel(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return false
	}

	models, err := p.fetchAvailableModels()
	if err != nil {
		models = p.fallbackModels
	}

	for _, model := range models {
		if strings.EqualFold(model, trimmed) {
			return true
		}
	}
	return false
}

func (p *OpenAICompatibleLocalProvider) fetchAvailableModels() ([]string, error) {
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

func (p *OpenAICompatibleLocalProvider) fetchAvailableModelsUncached() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultModelFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models from %s: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s API returned status %d", p.name, resp.StatusCode)
	}

	var modelsResp openAICompatibleModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to decode %s model response: %w", p.name, err)
	}

	models := make([]string, 0, len(modelsResp.Data))
	for _, model := range modelsResp.Data {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		models = append(models, modelID)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no models found in %s", p.name)
	}

	sort.Strings(models)
	return models, nil
}

func (p *OpenAICompatibleLocalProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	messages := convertMessagesToOpenAI(req.Messages, req.SystemPrompt)

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(req.Model),
		Messages: messages,
	}

	if req.Temperature >= 0 && !isReasoningModel(req.Model) {
		params.Temperature = openai.Float(req.Temperature)
	}

	if req.MaxTokens > 0 {
		if isReasoningModel(req.Model) {
			params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
		} else {
			params.MaxTokens = openai.Int(int64(req.MaxTokens))
		}
	}

	if len(req.Tools) > 0 {
		params.Tools = convertToolsToOpenAI(req.Tools)
	}

	// Constrained decoding via response_format json_schema (WS3.12). Strict is
	// left off — local OpenAI-compatible servers vary in strict-mode support, and
	// a derived task schema may not satisfy strict's additionalProperties/required
	// constraints.
	if len(req.ResponseSchema) > 0 {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "task_output",
					Schema: req.ResponseSchema,
				},
			},
		}
	}

	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, classifyOpenAIError(p.name, err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("%s returned no response choices - model %q may be invalid or unavailable", p.name, req.Model)
	}

	return convertOpenAIChatCompletion(p.name, completion), nil
}

func (p *OpenAICompatibleLocalProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for %s provider", p.name)
}
