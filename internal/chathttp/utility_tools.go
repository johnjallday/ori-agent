package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/oriagent/ori-pluginapi"
)

var (
	// ErrUtilityProviderUnavailable indicates the requested utility provider is not configured.
	ErrUtilityProviderUnavailable = errors.New("utility provider unavailable")
	// ErrUtilityInvalidInput indicates a malformed request payload for a utility tool.
	ErrUtilityInvalidInput = errors.New("utility invalid input")
)

// UtilityCallPolicy configures timeout/retry behavior for utility adapters.
type UtilityCallPolicy struct {
	Timeout       time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
}

// DefaultUtilityCallPolicy returns sane defaults for utility tool calls.
func DefaultUtilityCallPolicy() UtilityCallPolicy {
	return UtilityCallPolicy{
		Timeout:       5 * time.Second,
		RetryAttempts: 1,
		RetryDelay:    150 * time.Millisecond,
	}
}

// UtilityAdapters groups all native utility provider adapters.
type UtilityAdapters struct {
	Time       TimeAdapter
	Weather    WeatherAdapter
	AirQuality AirQualityAdapter
	WebSearch  WebSearchAdapter
	WebFetch   WebFetchAdapter
	Browser    BrowserAdapter
}

// TimeRequest is the normalized request contract for time lookups.
type TimeRequest struct {
	Timezone string `json:"timezone"`
}

// TimeResponse is the normalized response contract for time lookups.
type TimeResponse struct {
	Timezone    string `json:"timezone"`
	LocalTime   string `json:"local_time"`
	ISOTime     string `json:"iso_time"`
	Unix        int64  `json:"unix"`
	TimezoneAbv string `json:"timezone_abv"`
}

// WeatherRequest is the normalized request contract for weather lookups.
type WeatherRequest struct {
	Location string `json:"location"`
	Units    string `json:"units,omitempty"`
}

// WeatherResponse is the normalized response contract for weather lookups.
type WeatherResponse struct {
	Location    string  `json:"location"`
	Temperature float64 `json:"temperature"`
	Units       string  `json:"units"`
	Condition   string  `json:"condition"`
	Source      string  `json:"source"`
	ObservedAt  string  `json:"observed_at,omitempty"`
}

// AirQualityRequest is the normalized request contract for air quality lookups.
type AirQualityRequest struct {
	Location string `json:"location"`
	Standard string `json:"standard,omitempty"` // us or eu
}

// AirQualityResponse is the normalized response contract for air quality lookups.
type AirQualityResponse struct {
	Location   string  `json:"location"`
	AQI        float64 `json:"aqi"`
	Scale      string  `json:"scale"` // us or eu
	Category   string  `json:"category"`
	PM25       float64 `json:"pm2_5,omitempty"`
	PM10       float64 `json:"pm10,omitempty"`
	Source     string  `json:"source"`
	ObservedAt string  `json:"observed_at,omitempty"`
}

// WebSearchRequest is the normalized request contract for web search.
type WebSearchRequest struct {
	Query   string `json:"query"`
	Recency string `json:"recency,omitempty"`
}

// WebSearchResult holds one ranked search result item.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchResponse is the normalized response contract for web search.
type WebSearchResponse struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results"`
	Source  string            `json:"source,omitempty"`
}

// WebFetchRequest is the normalized request contract for web page fetch.
type WebFetchRequest struct {
	URL string `json:"url"`
}

// WebFetchResponse is the normalized response contract for web page fetch.
type WebFetchResponse struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
	Summary string `json:"summary,omitempty"`
	Source  string `json:"source,omitempty"`
}

// BrowserRequest is the normalized request contract for browser automation.
type BrowserRequest struct {
	Action   string `json:"action"`
	URL      string `json:"url,omitempty"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
}

// BrowserResponse is the normalized response contract for browser automation.
type BrowserResponse struct {
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
}

// TimeAdapter provides real-time clock lookups.
type TimeAdapter interface {
	GetCurrentTime(ctx context.Context, req TimeRequest) (TimeResponse, error)
}

// WeatherAdapter provides weather lookups.
type WeatherAdapter interface {
	GetWeather(ctx context.Context, req WeatherRequest) (WeatherResponse, error)
}

// AirQualityAdapter provides air quality lookups.
type AirQualityAdapter interface {
	GetAirQuality(ctx context.Context, req AirQualityRequest) (AirQualityResponse, error)
}

// WebSearchAdapter provides web search lookups.
type WebSearchAdapter interface {
	WebSearch(ctx context.Context, req WebSearchRequest) (WebSearchResponse, error)
}

// WebFetchAdapter provides URL fetch + extraction.
type WebFetchAdapter interface {
	WebFetch(ctx context.Context, req WebFetchRequest) (WebFetchResponse, error)
}

// BrowserAdapter provides browser automation actions.
type BrowserAdapter interface {
	BrowserAction(ctx context.Context, req BrowserRequest) (BrowserResponse, error)
}

type nativeUtilityTool struct {
	definition pluginapi.Tool
	call       func(ctx context.Context, args string) (string, error)
}

func (t *nativeUtilityTool) Definition() pluginapi.Tool {
	return t.definition
}

func (t *nativeUtilityTool) Call(ctx context.Context, args string) (string, error) {
	if t.call == nil {
		return "", fmt.Errorf("%w: %s", ErrUtilityProviderUnavailable, t.definition.Name)
	}
	return t.call(ctx, args)
}

// UtilityToolRegistry stores native utility tools as plugin-compatible tools.
type UtilityToolRegistry struct {
	mu     sync.RWMutex
	tools  map[string]pluginapi.PluginTool
	policy UtilityCallPolicy
}

// NewUtilityToolRegistry creates and registers all native utility tools.
func NewUtilityToolRegistry(adapters UtilityAdapters, policy UtilityCallPolicy) *UtilityToolRegistry {
	if policy.Timeout <= 0 {
		policy = DefaultUtilityCallPolicy()
	}
	if policy.RetryAttempts < 0 {
		policy.RetryAttempts = 0
	}
	if policy.RetryDelay <= 0 {
		policy.RetryDelay = DefaultUtilityCallPolicy().RetryDelay
	}

	r := &UtilityToolRegistry{
		tools:  make(map[string]pluginapi.PluginTool),
		policy: policy,
	}

	r.registerTimeTool(adapters.Time)
	r.registerWeatherTool(adapters.Weather)
	r.registerAirQualityTool(adapters.AirQuality)
	r.registerWebSearchTool(adapters.WebSearch)
	r.registerWebFetchTool(adapters.WebFetch)
	r.registerBrowserTool(adapters.Browser)

	return r
}

// NewDefaultUtilityToolRegistry configures the registry with built-in defaults.
func NewDefaultUtilityToolRegistry() *UtilityToolRegistry {
	weatherAdapter := NewOpenMeteoWeatherAdapter(nil)
	return NewUtilityToolRegistry(
		UtilityAdapters{
			Time:       SystemTimeAdapter{},
			Weather:    weatherAdapter,
			AirQuality: weatherAdapter,
			WebSearch:  NewDefaultWebSearchAdapter(nil),
			WebFetch:   NewHTTPWebFetchAdapter(nil, DefaultWebFetchAdapterConfig()),
			Browser:    NewSimpleBrowserAutomationAdapter(nil, DefaultBrowserAutomationPolicy()),
		},
		DefaultUtilityCallPolicy(),
	)
}

// SetPolicy updates execution policy for all future utility calls.
func (r *UtilityToolRegistry) SetPolicy(policy UtilityCallPolicy) {
	if policy.Timeout <= 0 {
		policy.Timeout = DefaultUtilityCallPolicy().Timeout
	}
	if policy.RetryAttempts < 0 {
		policy.RetryAttempts = 0
	}
	if policy.RetryDelay <= 0 {
		policy.RetryDelay = DefaultUtilityCallPolicy().RetryDelay
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = policy
}

// GetTool returns a native utility tool by name.
func (r *UtilityToolRegistry) GetTool(name string) (pluginapi.PluginTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// ListToolDefinitions returns all registered utility tool definitions.
func (r *UtilityToolRegistry) ListToolDefinitions() []pluginapi.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]pluginapi.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	return defs
}

func (r *UtilityToolRegistry) register(name string, tool pluginapi.PluginTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
}

func (r *UtilityToolRegistry) getPolicy() UtilityCallPolicy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy
}

func (r *UtilityToolRegistry) registerTimeTool(adapter TimeAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "time",
			Description: "Get the current real time for a timezone (IANA format, e.g. Asia/Tokyo).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "IANA timezone name like Asia/Tokyo. Defaults to UTC when omitted.",
					},
				},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: time", ErrUtilityProviderUnavailable)
			}
			var req TimeRequest
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &req); err != nil {
					return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
				}
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (TimeResponse, error) {
				return adapter.GetCurrentTime(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("time", tool)
}

func (r *UtilityToolRegistry) registerWeatherTool(adapter WeatherAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "weather",
			Description: "Get current weather for a city or location.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{
						"type":        "string",
						"description": "City or place name (for example: San Francisco, CA).",
					},
					"units": map[string]interface{}{
						"type":        "string",
						"description": "Temperature unit: celsius or fahrenheit.",
						"enum":        []string{"celsius", "fahrenheit"},
					},
				},
				"required": []string{"location"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: weather", ErrUtilityProviderUnavailable)
			}
			var req WeatherRequest
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
			}
			if strings.TrimSpace(req.Location) == "" {
				return "", fmt.Errorf("%w: location is required", ErrUtilityInvalidInput)
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (WeatherResponse, error) {
				return adapter.GetWeather(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("weather", tool)
}

func (r *UtilityToolRegistry) registerAirQualityTool(adapter AirQualityAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "air_quality",
			Description: "Get current air quality (AQI, PM2.5, PM10) for a city or location.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{
						"type":        "string",
						"description": "City or place name (for example: Seoul, KR).",
					},
					"standard": map[string]interface{}{
						"type":        "string",
						"description": "AQI standard: us or eu. Defaults to us.",
						"enum":        []string{"us", "eu"},
					},
				},
				"required": []string{"location"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: air_quality", ErrUtilityProviderUnavailable)
			}
			var req AirQualityRequest
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
			}
			if strings.TrimSpace(req.Location) == "" {
				return "", fmt.Errorf("%w: location is required", ErrUtilityInvalidInput)
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (AirQualityResponse, error) {
				return adapter.GetAirQuality(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("air_quality", tool)
}

func (r *UtilityToolRegistry) registerWebSearchTool(adapter WebSearchAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "web_search",
			Description: "Search the web for recent and relevant information.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query text.",
					},
					"recency": map[string]interface{}{
						"type":        "string",
						"description": "Optional recency hint like day, week, or month.",
					},
				},
				"required": []string{"query"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: web_search", ErrUtilityProviderUnavailable)
			}
			var req WebSearchRequest
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
			}
			if strings.TrimSpace(req.Query) == "" {
				return "", fmt.Errorf("%w: query is required", ErrUtilityInvalidInput)
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (WebSearchResponse, error) {
				return adapter.WebSearch(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("web_search", tool)
}

func (r *UtilityToolRegistry) registerWebFetchTool(adapter WebFetchAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "web_fetch",
			Description: "Fetch content from a URL and return extracted text.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to fetch.",
					},
				},
				"required": []string{"url"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: web_fetch", ErrUtilityProviderUnavailable)
			}
			var req WebFetchRequest
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
			}
			if strings.TrimSpace(req.URL) == "" {
				return "", fmt.Errorf("%w: url is required", ErrUtilityInvalidInput)
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (WebFetchResponse, error) {
				return adapter.WebFetch(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("web_fetch", tool)
}

func (r *UtilityToolRegistry) registerBrowserTool(adapter BrowserAdapter) {
	tool := &nativeUtilityTool{
		definition: pluginapi.Tool{
			Name:        "browser",
			Description: "Run basic browser automation actions (open, click, type, extract).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "Action name: open_url, click, type, or extract_text.",
						"enum":        []string{"open_url", "click", "type", "extract_text"},
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL for open_url action.",
					},
					"selector": map[string]interface{}{
						"type":        "string",
						"description": "CSS selector for click/type/extract_text actions.",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "Text value for type action.",
					},
				},
				"required": []string{"action"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			if adapter == nil {
				return "", fmt.Errorf("%w: browser", ErrUtilityProviderUnavailable)
			}
			var req BrowserRequest
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("%w: %v", ErrUtilityInvalidInput, err)
			}
			if strings.TrimSpace(req.Action) == "" {
				return "", fmt.Errorf("%w: action is required", ErrUtilityInvalidInput)
			}

			resp, err := executeUtilityCall(ctx, r.getPolicy(), func(callCtx context.Context) (BrowserResponse, error) {
				return adapter.BrowserAction(callCtx, req)
			})
			if err != nil {
				return "", normalizeUtilityError(err)
			}
			raw, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return "", normalizeUtilityError(marshalErr)
			}
			return string(raw), nil
		},
	}
	r.register("browser", tool)
}

// SystemTimeAdapter is the default built-in adapter for accurate timezone-aware time.
type SystemTimeAdapter struct{}

// GetCurrentTime returns current time using system clock and IANA timezone conversion.
func (SystemTimeAdapter) GetCurrentTime(_ context.Context, req TimeRequest) (TimeResponse, error) {
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return TimeResponse{}, fmt.Errorf("%w: invalid timezone %q", ErrUtilityInvalidInput, tz)
	}
	now := time.Now().In(loc)
	return TimeResponse{
		Timezone:    loc.String(),
		LocalTime:   now.Format("2006-01-02 15:04:05 MST"),
		ISOTime:     now.Format(time.RFC3339),
		Unix:        now.Unix(),
		TimezoneAbv: now.Format("MST"),
	}, nil
}

func executeUtilityCall[T any](ctx context.Context, policy UtilityCallPolicy, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	attempts := policy.RetryAttempts + 1
	if attempts < 1 {
		attempts = 1
	}
	if policy.Timeout <= 0 {
		policy.Timeout = DefaultUtilityCallPolicy().Timeout
	}
	if policy.RetryDelay <= 0 {
		policy.RetryDelay = DefaultUtilityCallPolicy().RetryDelay
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
		result, err := fn(callCtx)
		cancel()
		if err == nil {
			return result, nil
		}

		lastErr = err
		if !isRetriableUtilityError(err) || attempt == attempts-1 {
			break
		}

		timer := time.NewTimer(policy.RetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}

	return zero, lastErr
}

func isRetriableUtilityError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUtilityInvalidInput) {
		return false
	}
	if errors.Is(err, ErrUtilityProviderUnavailable) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Deadline/timeout and provider/transient errors are retriable.
	return true
}

func normalizeUtilityError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUtilityInvalidInput) {
		return err
	}
	if errors.Is(err, ErrUtilityProviderUnavailable) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("utility request timed out")
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("utility request canceled")
	}
	logger.Debug("Normalizing utility tool error", logger.Fields{"error": err.Error()})
	return fmt.Errorf("utility request failed")
}
