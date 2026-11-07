package llm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBoundedTextConfigurationPinsTheTransportWithoutExposingCredentials(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected call", http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("OPENAI_BASE_URL", server.URL)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	config := ProviderConfig{APIKey: "never-expose-credential", BaseURL: server.URL}
	providers := []BoundedTextProvider{NewOpenAIProvider(config), NewClaudeProvider(config), NewGeminiProvider(config), NewOllamaProvider(config), NewLMStudioProvider(config), NewMLXLMProvider(config)}
	for _, p := range providers {
		t.Run(p.Name(), func(t *testing.T) {
			fingerprint := p.BoundedTextConfiguration()
			if len(fingerprint) != 64 || strings.Contains(fingerprint, config.APIKey) || strings.Contains(fingerprint, server.URL) {
				t.Fatalf("not an opaque destination identity: %q", fingerprint)
			}
			_, err := p.Chat(t.Context(), ChatRequest{Model: "test", Isolated: true, SingleAttempt: true, MaxTokens: 1200,
				ProviderConfiguration: "stale", Messages: []Message{NewUserMessage("data")}})
			if !errors.Is(err, ErrProviderConfigurationChanged) {
				t.Fatalf("stale endpoint not refused: %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("configuration/status checks made a network call")
	}
	for _, p := range []Provider{&CodexProvider{}, &ClaudeCodeProvider{}} {
		if _, ok := p.(BoundedTextProvider); ok {
			t.Fatal("CLI without an enforced output cap claimed bounded execution")
		}
	}
}

func TestBoundedTextConfigurationFollowsClientReplacementNotMutableEnvironment(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:30100")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:30100")
	openai := NewOpenAIProvider(ProviderConfig{APIKey: "fixture-key"})
	claude := NewClaudeProvider(ProviderConfig{APIKey: "fixture-key"})
	for _, p := range []interface {
		BoundedTextProvider
		UpdateClient(string)
	}{openai, claude} {
		before := p.BoundedTextConfiguration()
		t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:30101")
		t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:30101")
		if p.BoundedTextConfiguration() != before {
			t.Fatal("identity no longer represents the actual frozen client")
		}
		p.UpdateClient("another-fixture-key")
		if p.BoundedTextConfiguration() == before {
			t.Fatal("destination change retained consent")
		}
		_, err := p.Chat(t.Context(), ChatRequest{Model: "test", Isolated: true, SingleAttempt: true, MaxTokens: 1200, ProviderConfiguration: before})
		if !errors.Is(err, ErrProviderConfigurationChanged) {
			t.Fatalf("old consent reached new destination: %v", err)
		}
	}
}

func TestBoundedTextOllamaMakesOneAttemptWithoutSchemaRepair(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unsupported format schema", http.StatusBadRequest)
	}))
	defer server.Close()
	provider := NewOllamaProvider(ProviderConfig{BaseURL: server.URL})
	_, err := provider.Chat(t.Context(), ChatRequest{Model: "test", Isolated: true, SingleAttempt: true, MaxTokens: 1200,
		ProviderConfiguration: provider.BoundedTextConfiguration(), ResponseSchema: map[string]any{"type": "object"}})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("bounded schema refusal: calls=%d err=%v", calls.Load(), err)
	}
}
