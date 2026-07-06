package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOllamaBuildOptions_TemperatureSentinel(t *testing.T) {
	p := NewOllamaProvider(ProviderConfig{BaseURL: "http://localhost:11434"})

	// Temperature 0 must be SENT (the WS7.27 bug: omitempty/>0 dropped it).
	opts := p.buildOptions(ChatRequest{Temperature: 0})
	if opts == nil || opts.Temperature == nil {
		t.Fatalf("temperature 0 should be sent, got opts=%+v", opts)
	}
	if *opts.Temperature != 0 {
		t.Fatalf("expected temperature 0, got %v", *opts.Temperature)
	}

	// A negative sentinel means "unset" — omit temperature entirely.
	opts = p.buildOptions(ChatRequest{Temperature: -1})
	if opts != nil && opts.Temperature != nil {
		t.Fatalf("negative temperature should be omitted, got %v", *opts.Temperature)
	}

	// A positive temperature is passed through.
	opts = p.buildOptions(ChatRequest{Temperature: 0.7})
	if opts == nil || opts.Temperature == nil || *opts.Temperature != 0.7 {
		t.Fatalf("expected temperature 0.7, got %+v", opts)
	}
}

func TestOllamaBuildOptions_NumCtxClamp(t *testing.T) {
	p := NewOllamaProvider(ProviderConfig{BaseURL: "http://localhost:11434"})

	// Below the clamp: passed through.
	opts := p.buildOptions(ChatRequest{ContextWindowTokens: 8192})
	if opts == nil || opts.NumCtx != 8192 {
		t.Fatalf("expected num_ctx 8192, got %+v", opts)
	}

	// Above the default clamp (32768): clamped.
	opts = p.buildOptions(ChatRequest{ContextWindowTokens: 200000})
	if opts == nil || opts.NumCtx != defaultMaxNumCtx {
		t.Fatalf("expected num_ctx clamped to %d, got %+v", defaultMaxNumCtx, opts)
	}

	// Configurable ceiling honored.
	p2 := NewOllamaProvider(ProviderConfig{
		BaseURL: "http://localhost:11434",
		Options: map[string]any{"max_num_ctx": 16384},
	})
	opts = p2.buildOptions(ChatRequest{ContextWindowTokens: 200000})
	if opts == nil || opts.NumCtx != 16384 {
		t.Fatalf("expected num_ctx clamped to configured 16384, got %+v", opts)
	}

	// No option-worthy fields → nil options block.
	if got := p.buildOptions(ChatRequest{Temperature: -1}); got != nil {
		t.Fatalf("expected nil options, got %+v", got)
	}
}

func TestOllamaResponseUsage(t *testing.T) {
	r := ollamaResponse{PromptEvalCount: 10, EvalCount: 5}
	got := r.usage()
	want := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if got != want {
		t.Fatalf("usage() = %+v, want %+v", got, want)
	}
}

func TestOllamaModelContextWindow(t *testing.T) {
	p := NewOllamaProvider(ProviderConfig{
		BaseURL: "http://localhost:11434",
		Options: map[string]any{
			"context_window":  4096,
			"context_windows": map[string]any{"llama3.1:8b": 8192},
		},
	})

	if got := p.ModelContextWindow("llama3.1:8b"); got != 8192 {
		t.Fatalf("per-model window = %d, want 8192", got)
	}
	// Case-insensitive.
	if got := p.ModelContextWindow("LLAMA3.1:8B"); got != 8192 {
		t.Fatalf("case-insensitive per-model window = %d, want 8192", got)
	}
	// Unknown model → provider default.
	if got := p.ModelContextWindow("mistral"); got != 4096 {
		t.Fatalf("default window = %d, want 4096", got)
	}
	// Capabilities reflects the configured default.
	if got := p.Capabilities().MaxContextWindow; got != 4096 {
		t.Fatalf("Capabilities window = %d, want 4096", got)
	}
}

func TestResolveModelContextWindow_Fallback(t *testing.T) {
	// No config → ModelContextWindow returns 0 → fall back to Capabilities().
	p := NewOllamaProvider(ProviderConfig{BaseURL: "http://localhost:11434"})
	if got := ResolveModelContextWindow(p, "anything"); got != defaultLocalContextWindow {
		t.Fatalf("fallback window = %d, want %d", got, defaultLocalContextWindow)
	}
}

func TestOllamaChat_SendsNumCtxAndParsesUsage(t *testing.T) {
	var captured ollamaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"llama3.1:8b","message":{"role":"assistant","content":"hi"},"done":true,"prompt_eval_count":12,"eval_count":7}`)
	}))
	defer srv.Close()

	p := NewOllamaProvider(ProviderConfig{BaseURL: srv.URL})
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:               "llama3.1:8b",
		Messages:            []Message{NewUserMessage("hello")},
		Temperature:         0,
		ContextWindowTokens: 8192,
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	// num_ctx and temperature 0 crossed the wire.
	if captured.Options == nil || captured.Options.NumCtx != 8192 {
		t.Fatalf("num_ctx not sent: %+v", captured.Options)
	}
	if captured.Options.Temperature == nil || *captured.Options.Temperature != 0 {
		t.Fatalf("temperature 0 not sent: %+v", captured.Options)
	}
	// Usage parsed.
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 19 {
		t.Fatalf("usage not parsed: %+v", resp.Usage)
	}
}

func TestOllamaBuildRequest_MapsResponseSchema(t *testing.T) {
	p := NewOllamaProvider(ProviderConfig{BaseURL: "http://localhost:11434"})
	schema := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}}
	r := p.buildRequest(ChatRequest{ResponseSchema: schema}, false)
	if len(r.Format) == 0 {
		t.Fatal("expected Format to be set from ResponseSchema")
	}
	var got map[string]any
	if err := json.Unmarshal(r.Format, &got); err != nil {
		t.Fatalf("Format is not valid JSON: %v", err)
	}
	if got["type"] != "object" {
		t.Fatalf("Format schema not mapped: %+v", got)
	}
	// No schema -> no format.
	if r2 := p.buildRequest(ChatRequest{}, false); len(r2.Format) != 0 {
		t.Fatalf("expected empty Format, got %s", r2.Format)
	}
}

func TestLooksLikeFormatUnsupported(t *testing.T) {
	if !looksLikeFormatUnsupported(http.StatusBadRequest, `{"error":"invalid format schema"}`) {
		t.Fatal("400 mentioning format should be treated as unsupported")
	}
	if looksLikeFormatUnsupported(http.StatusInternalServerError, "boom") {
		t.Fatal("non-400 should not be treated as format-unsupported")
	}
	if looksLikeFormatUnsupported(http.StatusBadRequest, "unrelated error") {
		t.Fatal("400 without format/schema/json should not downgrade")
	}
}

func TestOllamaChat_FormatDowngrade(t *testing.T) {
	var sawSchemaObject, sawJSONString int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		trimmed := bytes.TrimSpace(req.Format)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte(`"json"`)) {
			// Old server rejects a schema object.
			atomic.AddInt32(&sawSchemaObject, 1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"json: cannot unmarshal object into Go value (format)"}`)
			return
		}
		atomic.AddInt32(&sawJSONString, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"{}"},"done":true}`)
	}))
	defer srv.Close()

	p := NewOllamaProvider(ProviderConfig{BaseURL: srv.URL})
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:          "llama3.1:8b",
		Messages:       []Message{NewUserMessage("go")},
		ResponseSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Chat error after downgrade: %v", err)
	}
	if resp.Content != "{}" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
	if atomic.LoadInt32(&sawSchemaObject) != 1 || atomic.LoadInt32(&sawJSONString) != 1 {
		t.Fatalf("expected one schema-object attempt then one json downgrade, got schema=%d json=%d",
			sawSchemaObject, sawJSONString)
	}
}

func TestLocalCapabilitiesSupportStructuredOutput(t *testing.T) {
	ollama := NewOllamaProvider(ProviderConfig{BaseURL: "http://localhost:11434"})
	if !ollama.Capabilities().SupportsStructuredOutput {
		t.Fatal("ollama should advertise structured output support")
	}
	lm := NewLMStudioProvider(ProviderConfig{})
	if !lm.Capabilities().SupportsStructuredOutput {
		t.Fatal("lmstudio should advertise structured output support")
	}
}

func TestOllamaModelListCache(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"name":"llama3.1:8b"}]}`)
	}))
	defer srv.Close()

	p := NewOllamaProvider(ProviderConfig{BaseURL: srv.URL})
	for i := 0; i < 5; i++ {
		if !p.HasModel("llama3.1:8b") {
			t.Fatalf("expected model present on call %d", i)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 /api/tags fetch (cached), got %d", got)
	}
}
