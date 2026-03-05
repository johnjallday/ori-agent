package workspace

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

type providerStub struct {
	name string
}

func (p *providerStub) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *providerStub) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func (p *providerStub) Name() string {
	return p.name
}

func (p *providerStub) Type() llm.ProviderType {
	return llm.ProviderTypeCloud
}

func (p *providerStub) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (p *providerStub) ValidateConfig(_ llm.ProviderConfig) error {
	return nil
}

func (p *providerStub) DefaultModels() []string {
	return nil
}

func newTestTaskHandler(providerNames ...string) *LLMTaskHandler {
	factory := llm.NewFactory()
	for _, providerName := range providerNames {
		factory.Register(providerName, &providerStub{name: providerName})
	}
	return &LLMTaskHandler{llmFactory: factory}
}

func TestGetProviderForAgent_UsesConfiguredProvider(t *testing.T) {
	handler := newTestTaskHandler("openai", "claude_code")
	got := handler.getProviderForAgent("claude_code", "haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code, got %q", got)
	}
}

func TestGetProviderForAgent_CorrectsOpenAIMismatchForHaiku(t *testing.T) {
	handler := newTestTaskHandler("openai", "claude_code")
	got := handler.getProviderForAgent("openai", "haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code for openai+haiku mismatch, got %q", got)
	}
}

func TestGetProviderForAgent_DoesNotFallbackToOpenAIForHaikuWhenClaudeUnavailable(t *testing.T) {
	handler := newTestTaskHandler("openai")
	got := handler.getProviderForAgent("openai", "haiku")
	if got != "claude" {
		t.Fatalf("expected claude for openai+haiku when claude provider unavailable, got %q", got)
	}
}

func TestGetProviderForAgent_NormalizesAnthropicAlias(t *testing.T) {
	handler := newTestTaskHandler("claude")
	got := handler.getProviderForAgent("anthropic", "claude-3-5-haiku-latest")
	if got != "claude" {
		t.Fatalf("expected claude for anthropic alias, got %q", got)
	}
}

func TestGetProviderForModel_InfersClaudeCodeForShortAlias(t *testing.T) {
	handler := newTestTaskHandler("claude_code")
	got := handler.getProviderForModel("haiku")
	if got != "claude_code" {
		t.Fatalf("expected claude_code, got %q", got)
	}
}

func TestGetProviderForModel_UsesClaudeFamilyForShortAliasWithoutClaudeProviders(t *testing.T) {
	handler := newTestTaskHandler("openai")
	got := handler.getProviderForModel("haiku")
	if got != "claude" {
		t.Fatalf("expected claude for short alias without claude providers, got %q", got)
	}
}

func TestGetProviderForModel_InfersClaudeForClaudeModel(t *testing.T) {
	handler := newTestTaskHandler("claude")
	got := handler.getProviderForModel("claude-3-5-haiku-latest")
	if got != "claude" {
		t.Fatalf("expected claude, got %q", got)
	}
}

func TestNormalizeModelForProvider_ClaudeAliases(t *testing.T) {
	handler := newTestTaskHandler("claude")

	if got := handler.normalizeModelForProvider("claude", "haiku"); got != "claude-3-5-haiku-latest" {
		t.Fatalf("expected claude haiku alias mapping, got %q", got)
	}

	if got := handler.normalizeModelForProvider("claude", "sonnet"); got != "claude-3-5-sonnet-latest" {
		t.Fatalf("expected claude sonnet alias mapping, got %q", got)
	}

	if got := handler.normalizeModelForProvider("claude", "opus"); got != "claude-3-opus-latest" {
		t.Fatalf("expected claude opus alias mapping, got %q", got)
	}
}
