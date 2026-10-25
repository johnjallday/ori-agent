package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
)

func TestClaudeProviderMetadata(t *testing.T) {
	config := ProviderConfig{
		APIKey: "test-api-key",
	}
	provider := NewClaudeProvider(config)

	// Test Name
	if provider.Name() != "claude" {
		t.Errorf("Expected name 'claude', got '%s'", provider.Name())
	}

	// Test Type
	if provider.Type() != ProviderTypeCloud {
		t.Errorf("Expected type ProviderTypeCloud, got '%s'", provider.Type())
	}

	// Test Capabilities
	caps := provider.Capabilities()
	if !caps.SupportsTools {
		t.Error("Expected Claude to support tools")
	}
	if caps.SupportsStreaming {
		t.Error("Claude streaming is unimplemented; capability should be false")
	}
	if !caps.SupportsSystemPrompt {
		t.Error("Expected Claude to support system prompts")
	}
	if !caps.SupportsTemperature {
		t.Error("Expected Claude to support temperature")
	}
	if !caps.RequiresAPIKey {
		t.Error("Expected Claude to require API key")
	}
	if caps.SupportsCustomEndpoint {
		t.Error("Expected Claude to not support custom endpoint")
	}
	if caps.MaxContextWindow != 200000 {
		t.Errorf("Expected max context window 200000, got %d", caps.MaxContextWindow)
	}
}

// claudeModelListJSON is a /v1/models page: a current model, a dated model
// whose alias the catalog knows, a deprecated one, and a retired one that the
// list should never offer.
const claudeModelListJSON = `{"data":[
	{"type":"model","id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","created_at":"2025-10-01T00:00:00Z","max_input_tokens":200000,"max_tokens":64000,"lifecycle":"active"},
	{"type":"model","id":"claude-sonnet-4-5-20250929","display_name":"Claude Sonnet 4.5","created_at":"2025-09-29T00:00:00Z","max_input_tokens":200000,"max_tokens":64000,"lifecycle":"deprecated"},
	{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-09-15T00:00:00Z","max_input_tokens":1000000,"max_tokens":128000,"lifecycle":"active"},
	{"type":"model","id":"claude-3-opus-20240229","display_name":"Claude Opus 3","created_at":"2024-02-29T00:00:00Z","max_input_tokens":200000,"max_tokens":4096,"lifecycle":"retired"}
],"has_more":false,"first_id":"claude-haiku-4-5-20251001","last_id":"claude-3-opus-20240229"}`

// claudeTestServer answers /v1/models with models (or status when it is not
// 200) and /v1/messages with a one-line reply, recording every request.
type claudeTestServer struct {
	*httptest.Server
	modelRequests int
	messageBodies [][]byte
}

func newClaudeTestServer(t *testing.T, status int, models string) *claudeTestServer {
	t.Helper()
	s := &claudeTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			s.modelRequests++
			if r.Header.Get("X-Api-Key") != "test-key" {
				t.Errorf("model list sent API key %q", r.Header.Get("X-Api-Key"))
			}
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"unavailable"}}`)
				return
			}
			_, _ = io.WriteString(w, models)
		case "/v1/messages":
			body, _ := io.ReadAll(r.Body)
			s.messageBodies = append(s.messageBodies, body)
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture",
				"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *claudeTestServer) provider() *ClaudeProvider {
	return &ClaudeProvider{apiKey: "test-key", httpClient: s.Client(), client: anthropic.NewClient(
		option.WithBaseURL(s.URL), option.WithHTTPClient(s.Client()), option.WithAPIKey("test-key"), option.WithMaxRetries(0))}
}

func TestClaudeProviderDefaultModels_ListsAnthropicsModelsNewestFirst(t *testing.T) {
	server := newClaudeTestServer(t, http.StatusOK, claudeModelListJSON)
	provider := server.provider()

	want := []string{
		"claude-opus-5-5",
		"claude-haiku-4-5", "claude-haiku-4-5-20251001",
		"claude-sonnet-4-5", "claude-sonnet-4-5-20250929",
	}
	if got := provider.DefaultModels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	// The list is cached, not fetched again for every caller.
	provider.DefaultModels()
	if server.modelRequests != 1 {
		t.Fatalf("model list fetched %d times, want 1", server.modelRequests)
	}
}

func TestClaudeProviderDefaultModels_FallsBackToTheCatalogWhenTheListFails(t *testing.T) {
	server := newClaudeTestServer(t, http.StatusInternalServerError, "")
	provider := server.provider()

	if got, want := provider.DefaultModels(), modelinfo.GetClaudeModels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want the catalog %v", got, want)
	}
	// A failure is not retried at once, so an unreachable API does not make
	// every caller wait on the fetch timeout.
	provider.DefaultModels()
	if server.modelRequests != 1 {
		t.Fatalf("model list fetched %d times, want 1", server.modelRequests)
	}
}

func TestClaudeProviderUpdateClient_DropsTheCachedModelList(t *testing.T) {
	server := newClaudeTestServer(t, http.StatusOK, claudeModelListJSON)
	provider := server.provider()
	provider.DefaultModels()

	provider.UpdateClient("another-key")
	if provider.knownMaxTokens("claude-opus-5-5") != 0 {
		t.Fatal("the previous key's model list survived a key change")
	}
}

func TestClaudeProviderChat_SendsTemperatureOnlyToModelsThatAcceptIt(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6", true},
		{"claude-haiku-4-5-20251001", true},
		{"claude-opus-4-7", false},
		{"claude-opus-5-5", false},
		{"claude-sonnet-5-5", false},
		{"claude-next-model", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			server := newClaudeTestServer(t, http.StatusOK, claudeModelListJSON)
			_, err := server.provider().Chat(t.Context(), ChatRequest{
				Model: tc.model, Temperature: 0.4, Messages: []Message{NewUserMessage("Hello")},
			})
			if err != nil {
				t.Fatal(err)
			}
			var sent map[string]any
			if err := json.Unmarshal(server.messageBodies[0], &sent); err != nil {
				t.Fatal(err)
			}
			if _, got := sent["temperature"]; got != tc.want {
				t.Fatalf("temperature sent = %v, want %v (%s)", got, tc.want, server.messageBodies[0])
			}
		})
	}
}

func TestClaudeProviderChat_DefaultsMaxTokensWithinTheModelsLimit(t *testing.T) {
	models := strings.Replace(claudeModelListJSON, `"max_tokens":64000,"lifecycle":"active"`, `"max_tokens":8192,"lifecycle":"active"`, 1)
	server := newClaudeTestServer(t, http.StatusOK, models)
	provider := server.provider()

	// Before the list is fetched, and for a model it does not name, the default applies.
	for _, model := range []string{"claude-haiku-4-5-20251001", "claude-opus-5-5"} {
		if _, err := provider.Chat(t.Context(), ChatRequest{Model: model, Messages: []Message{NewUserMessage("Hello")}}); err != nil {
			t.Fatal(err)
		}
	}
	provider.DefaultModels()
	// Chat reads the cached list and never fetches it itself.
	for _, model := range []string{"claude-haiku-4-5-20251001", "claude-haiku-4-5", "claude-opus-5-5"} {
		if _, err := provider.Chat(t.Context(), ChatRequest{Model: model, Messages: []Message{NewUserMessage("Hello")}}); err != nil {
			t.Fatal(err)
		}
	}
	if server.modelRequests != 1 {
		t.Fatalf("model list fetched %d times, want 1", server.modelRequests)
	}

	var got []float64
	for _, body := range server.messageBodies {
		var sent struct {
			MaxTokens float64 `json:"max_tokens"`
		}
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatal(err)
		}
		got = append(got, sent.MaxTokens)
	}
	if want := []float64{16000, 16000, 8192, 8192, 16000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("max_tokens = %v, want %v", got, want)
	}
}

func TestClaudeProviderValidateConfig(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{})

	tests := []struct {
		name      string
		config    ProviderConfig
		expectErr bool
	}{
		{
			name:      "Valid config with API key",
			config:    ProviderConfig{APIKey: "test-key"},
			expectErr: false,
		},
		{
			name:      "Invalid config without API key",
			config:    ProviderConfig{},
			expectErr: true,
		},
		{
			name:      "Invalid config with empty API key",
			config:    ProviderConfig{APIKey: ""},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := provider.ValidateConfig(tt.config)
			if tt.expectErr && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestClaudeProviderConvertMessages(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

	tests := []struct {
		name        string
		messages    []Message
		expectedLen int
	}{
		{
			name: "User and assistant messages",
			messages: []Message{
				NewUserMessage("Hello"),
				NewAssistantMessage("Hi there"),
			},
			expectedLen: 2,
		},
		{
			name: "System message is left out of the messages",
			messages: []Message{
				NewSystemMessage("You are helpful"),
				NewUserMessage("Hello"),
			},
			expectedLen: 1, // Sent as system text instead; see claudeSystemText
		},
		{
			name: "Tool message converted to user message",
			messages: []Message{
				NewUserMessage("What is 2+2?"),
				NewToolMessage("tool-123", "4"),
			},
			expectedLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converted := provider.convertMessages(tt.messages)
			if len(converted) != tt.expectedLen {
				t.Errorf("Expected %d messages, got %d", tt.expectedLen, len(converted))
			}
		})
	}
}

func TestClaudeProviderConvertTools(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

	tools := []Tool{
		{
			Name:        "calculator",
			Description: "Perform calculations",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"operation": map[string]any{
						"type": "string",
					},
				},
				"required": []string{"operation"},
			},
		},
		{
			Name:        "search",
			Description: "Search the web",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type": "string",
					},
				},
			},
		},
	}

	converted := provider.convertTools(tools)

	if len(converted) != len(tools) {
		t.Errorf("Expected %d tools, got %d", len(tools), len(converted))
	}

	// Just verify that tools were converted (the anthropic types are opaque)
	// Detailed verification would require making actual API calls
}

func TestClaudeProviderUpdateClient(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "initial-key"})

	if provider.apiKey != "initial-key" {
		t.Errorf("Expected initial API key 'initial-key', got '%s'", provider.apiKey)
	}

	// Update with new key
	provider.UpdateClient("new-key")

	if provider.apiKey != "new-key" {
		t.Errorf("Expected updated API key 'new-key', got '%s'", provider.apiKey)
	}

	// Update with empty key should not change
	provider.UpdateClient("")

	if provider.apiKey != "new-key" {
		t.Errorf("Expected API key to remain 'new-key', got '%s'", provider.apiKey)
	}
}

func TestClaudeProviderStreamChatNotImplemented(t *testing.T) {
	provider := NewClaudeProvider(ProviderConfig{APIKey: "test-key"})

	req := ChatRequest{
		Model: "claude-3-5-sonnet-20241022",
		Messages: []Message{
			NewUserMessage("Hello"),
		},
	}

	_, err := provider.StreamChat(context.Background(), req)
	if err == nil {
		t.Error("Expected error for unimplemented StreamChat, got nil")
	}

	expectedMsg := "streaming not yet implemented"
	if err != nil && len(err.Error()) > 0 {
		if err.Error()[:len(expectedMsg)] != expectedMsg {
			t.Errorf("Expected error message to start with '%s', got '%s'", expectedMsg, err.Error())
		}
	}
}

// TestClaudeProviderLive_ListsModelsAndRunsAToolRoundTrip calls the real
// Claude API: it fetches the model list, then runs one tool round trip on a
// current model with a temperature set, which that model must not be sent.
// It spends a few cents, so it runs only with ORI_LIVE_CLAUDE_API=1 and
// ANTHROPIC_API_KEY set; ORI_LIVE_CLAUDE_API_MODEL picks another model.
func TestClaudeProviderLive_ListsModelsAndRunsAToolRoundTrip(t *testing.T) {
	if os.Getenv("ORI_LIVE_CLAUDE_API") != "1" {
		t.Skip("live Claude API check; set ORI_LIVE_CLAUDE_API=1 and ANTHROPIC_API_KEY to run")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Fatal("ORI_LIVE_CLAUDE_API=1 needs ANTHROPIC_API_KEY")
	}
	model := os.Getenv("ORI_LIVE_CLAUDE_API_MODEL")
	if model == "" {
		model = "claude-opus-5-5"
	}
	provider := NewClaudeProvider(ProviderConfig{APIKey: apiKey})

	// Fetched directly first: DefaultModels would hide a failure behind the catalog.
	if _, err := fetchClaudeModels(provider.snapshot()); err != nil {
		t.Fatalf("model list: %v", err)
	}
	models := provider.DefaultModels()
	t.Logf("models: %v", models)
	if provider.knownMaxTokens(model) == 0 {
		t.Fatalf("%s is not in the list Anthropic returned: %v", model, models)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tools := []Tool{{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	}}
	messages := []Message{NewUserMessage("What is the weather in Paris right now? Use the get_weather tool, then answer in one sentence.")}
	first, err := provider.Chat(ctx, ChatRequest{Model: model, Temperature: 0.7, Messages: messages, Tools: tools})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(first.ToolCalls) == 0 {
		t.Fatalf("no tool call; stop reason %q, reply %q", first.FinishReason, first.Content)
	}
	messages = append(messages, Message{Role: RoleAssistant, Content: first.Content, ToolCalls: first.ToolCalls})
	for _, call := range first.ToolCalls {
		messages = append(messages, NewToolMessage(call.ID, `{"city":"Paris","temperature_c":18,"conditions":"light rain"}`))
	}
	second, err := provider.Chat(ctx, ChatRequest{Model: model, Temperature: 0.7, Messages: messages, Tools: tools})
	if err != nil {
		t.Fatalf("turn after the tool result: %v", err)
	}
	if strings.TrimSpace(second.Content) == "" {
		t.Fatalf("empty answer after the tool result; stop reason %q", second.FinishReason)
	}
	t.Logf("answer: %s", second.Content)
	t.Logf("usage: first %+v, second %+v", first.Usage, second.Usage)
}
