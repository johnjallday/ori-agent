package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newTestHTTPClient(fn roundTripperFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func newJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestLMStudioProviderMetadata(t *testing.T) {
	provider := NewLMStudioProvider(ProviderConfig{})

	if provider.Name() != "lmstudio" {
		t.Fatalf("expected lmstudio provider name, got %q", provider.Name())
	}
	if provider.Type() != ProviderTypeLocal {
		t.Fatalf("expected local provider type, got %q", provider.Type())
	}

	caps := provider.Capabilities()
	if caps.RequiresAPIKey {
		t.Fatal("expected LM Studio provider to not require an API key")
	}
	if !caps.SupportsCustomEndpoint {
		t.Fatal("expected LM Studio provider to support custom endpoints")
	}
}

func TestMLXLMProviderMetadata(t *testing.T) {
	provider := NewMLXLMProvider(ProviderConfig{})

	if provider.Name() != "mlx_lm" {
		t.Fatalf("expected mlx_lm provider name, got %q", provider.Name())
	}
	if provider.Type() != ProviderTypeLocal {
		t.Fatalf("expected local provider type, got %q", provider.Type())
	}
}

func TestOpenAICompatibleLocalProviderDefaultModels_UsesLiveEndpoint(t *testing.T) {
	provider := NewLMStudioProvider(ProviderConfig{BaseURL: "http://local.test"})
	provider.httpClient = newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
			}, nil
		}
		return newJSONResponse(`{
			"data": [
				{"id": "openai/gpt-oss-20b"},
				{"id": "mlx-community/Llama-3.2-3B-Instruct-4bit"}
			]
		}`), nil
	})
	models := provider.DefaultModels()

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %v", models)
	}
	if models[0] != "mlx-community/Llama-3.2-3B-Instruct-4bit" || models[1] != "openai/gpt-oss-20b" {
		t.Fatalf("expected sorted model list, got %v", models)
	}
	if !provider.HasModel("OPENAI/GPT-OSS-20B") {
		t.Fatalf("expected case-insensitive HasModel match for %v", models)
	}
}

func TestOpenAICompatibleLocalProviderDefaultModels_FallsBackToConfiguredModel(t *testing.T) {
	provider := NewMLXLMProvider(ProviderConfig{
		BaseURL: "http://127.0.0.1:1",
		Model:   "mlx-community/Llama-3.2-3B-Instruct-4bit",
	})

	models := provider.DefaultModels()
	if len(models) != 1 || models[0] != "mlx-community/Llama-3.2-3B-Instruct-4bit" {
		t.Fatalf("expected fallback model list, got %v", models)
	}
}

func TestOpenAICompatibleLocalProviderChat_UsesOpenAICompatibleAPI(t *testing.T) {
	var sawAuthHeader string
	testHTTPClient := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
			}, nil
		}

		sawAuthHeader = r.Header.Get("Authorization")
		return newJSONResponse(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":123,
			"model":"openai/gpt-oss-20b",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello from local"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
		}`), nil
	})

	provider := NewLMStudioProvider(ProviderConfig{BaseURL: "http://local.test"})
	provider.httpClient = testHTTPClient
	provider.client = openai.NewClient(
		option.WithBaseURL("http://local.test/v1"),
		option.WithHTTPClient(testHTTPClient),
		option.WithHeader("authorization", ""),
		option.WithHeader("OpenAI-Organization", ""),
		option.WithHeader("OpenAI-Project", ""),
	)
	resp, err := provider.Chat(context.Background(), ChatRequest{
		Model: "openai/gpt-oss-20b",
		Messages: []Message{
			NewUserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Provider != "lmstudio" {
		t.Fatalf("expected provider name lmstudio, got %q", resp.Provider)
	}
	if strings.TrimSpace(resp.Content) != "hello from local" {
		t.Fatalf("expected local response content, got %q", resp.Content)
	}
	if sawAuthHeader != "" {
		t.Fatalf("expected empty Authorization header for local provider, got %q", sawAuthHeader)
	}
}
