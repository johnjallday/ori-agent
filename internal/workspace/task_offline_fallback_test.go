package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
)

// fakeLocalProvider is a local provider that always fails with a fixed error, to
// exercise offline / cold-load classification in the tool loop.
type fakeLocalProvider struct {
	name string
	err  error
}

func (p *fakeLocalProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, p.err
}
func (p *fakeLocalProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
func (p *fakeLocalProvider) Name() string           { return p.name }
func (p *fakeLocalProvider) Type() llm.ProviderType { return llm.ProviderTypeLocal }
func (p *fakeLocalProvider) Capabilities() llm.ProviderCapabilities {
	return llm.LocalProviderCapabilities(8192)
}
func (p *fakeLocalProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p *fakeLocalProvider) DefaultModels() []string                 { return nil }

func agentWithSettings(s types.Settings) *resolvedTaskAgent {
	return &resolvedTaskAgent{Agent: &agent.Agent{Settings: s}}
}

func TestIsLocalProviderOfflineError(t *testing.T) {
	offline := &TaskBlockedError{ReasonCode: reasonLocalProviderOffline}
	if !isLocalProviderOfflineError(offline) {
		t.Fatal("offline block should be recognized")
	}
	other := &TaskBlockedError{ReasonCode: "context_overflow"}
	if isLocalProviderOfflineError(other) {
		t.Fatal("a different blocked reason should not match")
	}
	if isLocalProviderOfflineError(errors.New("plain error")) {
		t.Fatal("a plain error should not match")
	}
}

func TestResolveAgentFallback(t *testing.T) {
	// No fallback configured.
	if _, _, ok := resolveAgentFallback(agentWithSettings(types.Settings{Model: "m"})); ok {
		t.Fatal("no fallback should report ok=false")
	}
	// Fallback provider set; model defaults to primary model.
	p, m, ok := resolveAgentFallback(agentWithSettings(types.Settings{
		Model:            "llama3.1:8b",
		FallbackProvider: "openai",
	}))
	if !ok || p != "openai" || m != "llama3.1:8b" {
		t.Fatalf("fallback = (%q,%q,%v), want (openai, llama3.1:8b, true)", p, m, ok)
	}
	// Explicit fallback model honored.
	p, m, ok = resolveAgentFallback(agentWithSettings(types.Settings{
		Model:            "llama3.1:8b",
		FallbackProvider: "openai",
		FallbackModel:    "gpt-5",
	}))
	if !ok || p != "openai" || m != "gpt-5" {
		t.Fatalf("fallback = (%q,%q,%v), want (openai, gpt-5, true)", p, m, ok)
	}
}

func TestExecuteTaskConversation_OfflineBlocks(t *testing.T) {
	h := &LLMTaskHandler{}
	prov := &fakeLocalProvider{name: "ollama", err: errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")}
	ag := agentWithSettings(types.Settings{Model: "llama3.1:8b"})

	_, err := h.executeTaskConversation(context.Background(), prov, "ollama", "llama3.1:8b", 8192, ag, "agent-a",
		Task{WorkspaceID: "ws-x"}, []llm.Message{llm.NewSystemMessage("sys"), llm.NewUserMessage("do it")}, nil)
	be, ok := AsTaskBlockedError(err)
	if !ok || be.ReasonCode != reasonLocalProviderOffline {
		t.Fatalf("expected local_provider_offline block, got %v", err)
	}
	// The blocked error carries actionable choices.
	found := false
	for _, a := range be.SuggestedActions {
		if a == "switch_agent_retry" {
			found = true
		}
	}
	if !found {
		t.Fatalf("offline block missing switch_agent_retry choice: %+v", be.SuggestedActions)
	}
}

func TestFallbackCloudSpendGate(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("openai", &fakeNativeProvider{caps: llm.ProviderCapabilities{}}) // cloud (Type()=cloud)
	factory.Register("ollama-2", &fakeLocalProvider{name: "ollama-2"})
	h := &LLMTaskHandler{llmFactory: factory}
	task := Task{WorkspaceID: "ws-x"}

	// Local->cloud without opt-in: gated with a confirmation block.
	gate := h.fallbackCloudSpendGate(agentWithSettings(types.Settings{}), task, "agent-a", "openai")
	if be, ok := AsTaskBlockedError(gate); !ok || be.ReasonCode != reasonFallbackNeedsCloudOK {
		t.Fatalf("cloud fallback should be gated, got %v", gate)
	}

	// Local->cloud with explicit opt-in: allowed.
	allow := true
	if gate := h.fallbackCloudSpendGate(agentWithSettings(types.Settings{FallbackAllowCloud: &allow}), task, "agent-a", "openai"); gate != nil {
		t.Fatalf("opted-in cloud fallback should not be gated, got %v", gate)
	}

	// Local->local: never gated.
	if gate := h.fallbackCloudSpendGate(agentWithSettings(types.Settings{}), task, "agent-a", "ollama-2"); gate != nil {
		t.Fatalf("local->local fallback should not be gated, got %v", gate)
	}
}
