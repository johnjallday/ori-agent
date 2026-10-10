package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// The fake is configured through the same factory/model reader production uses.
// No credentials, provider connection, or full server startup is involved.
type helpWiringProvider struct {
	llm.Provider
	calls atomic.Int32
}

func (p *helpWiringProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls.Add(1)
	return &llm.ChatResponse{Content: "Configured work model replied."}, nil
}
func (*helpWiringProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func TestGuideProductionWiringDoesNotCallConfiguredModel(t *testing.T) {
	cfg := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := cfg.SetSystemModel("openai", "gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}
	provider := &helpWiringProvider{}
	factory := llm.NewFactory()
	factory.Register("openai", provider)
	guide := agenthttp.NewGuideHandler()
	s := &Server{
		Core:     &CoreSystemFacade{ConfigManager: cfg, LLMFactory: factory},
		Handlers: &HandlerFacade{OriGuide: guide},
	}
	work := s.newHomeAssistantAskHandler()
	for _, question := range []string{"", "agent", "model setup", "unrecognized question", "draft a note", "/ask what is a workspace", "/task run it", "/note remember this"} {
		body, err := json.Marshal(agenthttp.GuideRequest{Question: question, Route: "/agents"})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		guide.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/ori-guide", bytes.NewReader(body)))
		if rr.Code != http.StatusOK {
			t.Fatalf("Help %q: status %d", question, rr.Code)
		}
		if provider.calls.Load() != 0 {
			t.Fatalf("Help %q called the configured provider", question)
		}
	}
	// Removing the Help phraser must not disconnect work's model wiring.
	response := work.Ask(context.Background(), agenthttp.HomeAssistantAskRequest{Prompt: "summarize my activity", Intent: "app_introspection"})
	if response.Response != "Configured work model replied." || provider.calls.Load() == 0 {
		t.Fatalf("work model disconnected: response=%q calls=%d", response.Response, provider.calls.Load())
	}
}
