package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// modelListProvider is a registered provider that offers a fixed model list.
type modelListProvider struct {
	name   string
	models []string
}

func (p modelListProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil
}
func (p modelListProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
func (p modelListProvider) Name() string                            { return p.name }
func (p modelListProvider) Type() llm.ProviderType                  { return llm.ProviderTypeCloud }
func (p modelListProvider) Capabilities() llm.ProviderCapabilities  { return llm.ProviderCapabilities{} }
func (p modelListProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p modelListProvider) DefaultModels() []string                 { return p.models }

func TestHireRunnableModel_KeepsASetUpTierModelElseUsesTheSystemModel(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("codex", modelListProvider{name: "codex", models: []string{"gpt-5.6-luna", "gpt-6.1-sol"}})
	system := func() (string, string) { return "Codex", "gpt-5.6-luna" }
	choose := hireRunnableModel(factory, system)

	// A tier model a registered provider offers runs there, named explicitly.
	if provider, model := choose("gpt-6.1-sol"); provider != "codex" || model != "gpt-6.1-sol" {
		t.Fatalf("set-up tier model: %q %q", provider, model)
	}
	// A tier model nobody set up gives way to the system model, so the look
	// runs where the goal-run check said it would.
	if provider, model := choose("claude-3-opus-20240229"); provider != "codex" || model != "gpt-5.6-luna" {
		t.Fatalf("unset tier model: %q %q", provider, model)
	}
	if provider, model := choose(""); provider != "codex" || model != "gpt-5.6-luna" {
		t.Fatalf("no tier model: %q %q", provider, model)
	}
	// With nothing runnable the tier's model stays, as catalog create keeps it.
	none := hireRunnableModel(factory, func() (string, string) { return "", "" })
	if provider, model := none("claude-3-opus-20240229"); provider != "" || model != "claude-3-opus-20240229" {
		t.Fatalf("nothing runnable: %q %q", provider, model)
	}
	if provider, model := hireRunnableModel(nil, system)("m"); provider != "" || model != "m" {
		t.Fatalf("no factory: %q %q", provider, model)
	}
}
