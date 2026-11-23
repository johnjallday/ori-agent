package llm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClaudeReviewDestinationMatchesTransportAndRefusesRedirect(t *testing.T) {
	var forwarded atomic.Bool
	next := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Store(true) }))
	defer next.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, next.URL, http.StatusTemporaryRedirect)
	}))
	defer start.Close()
	t.Setenv("ANTHROPIC_BASE_URL", "")
	p := NewClaudeProvider(ClaudeProviderConfig("fixture", ""))
	name, pin := p.DirectTextDestination()
	if name == "" || pin != p.BoundedTextConfiguration() {
		t.Fatal("missing direct transport")
	}
	t.Setenv("ANTHROPIC_BASE_URL", start.URL)
	if actual, again := p.DirectTextDestination(); actual != name || again != pin {
		t.Fatal("disclosure followed environment instead of current client")
	}
	p.UpdateClient("new-fixture")
	if processor, _ := p.DirectTextDestination(); processor != "" {
		t.Fatal("overridden endpoint described as Anthropic")
	}
	_, err := p.Chat(t.Context(), ChatRequest{Model: "fixture", ProviderConfiguration: p.BoundedTextConfiguration(), Isolated: true, SingleAttempt: true, MaxTokens: 12, Messages: []Message{NewUserMessage("approved disposable text")}})
	if err == nil || forwarded.Load() {
		t.Fatal("pinned request followed redirect")
	}
}
