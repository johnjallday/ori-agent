package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// failingToolProvider is a tool-capable provider whose calls fail with err.
type failingToolProvider struct {
	name string
	err  error
}

func (p *failingToolProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &llm.ChatResponse{Content: "ok"}, nil
}

func (p *failingToolProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}
func (p *failingToolProvider) Name() string                            { return p.name }
func (p *failingToolProvider) Type() llm.ProviderType                  { return llm.ProviderTypeCloud }
func (p *failingToolProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p *failingToolProvider) DefaultModels() []string                 { return nil }
func (p *failingToolProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true}
}

func agentOn(t *testing.T, provider, model string) *workspace.InMemoryStore {
	t.Helper()
	store := workspace.NewInMemoryStore()
	if err := store.SaveWorkspaceAgent("ws", "Albums Manager", &agent.Agent{Settings: types.Settings{Provider: provider, Model: model}}); err != nil {
		t.Fatal(err)
	}
	return store
}

func modelCause(t *testing.T, err error) folderlook.Cause {
	t.Helper()
	var failed *folderlook.ModelError
	if !errors.As(err, &failed) {
		t.Fatalf("error %v is not a ModelError", err)
	}
	return failed.Cause
}

func TestFolderAgentTurns_EachResolveFailureIsDistinct(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("ollama", &capturingChatProvider{}) // cannot call tools
	cases := []struct {
		name            string
		store           workspace.Store
		kind            string
		provider, model string
	}{
		{"agent not in the workspace", workspace.NewInMemoryStore(), folderlook.CauseNoModel, "", ""},
		{"no model chosen", agentOn(t, "anthropic", ""), folderlook.CauseNoModel, "", ""},
		{"API provider without its key", agentOn(t, "anthropic", "claude-sonnet-5-5"), folderlook.CauseNotConfigured, "Anthropic", "claude-sonnet-5-5"},
		{"CLI provider not on this Mac", agentOn(t, "codex", "gpt-6.1-sol"), folderlook.CauseNotAvailable, "Codex", "gpt-6.1-sol"},
		{"a model that cannot call tools", agentOn(t, "ollama", "tiny"), folderlook.CauseNoTools, "Ollama", "tiny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turns := folderAgentTurns{builder: &ServerBuilder{workspaceStore: tc.store, llmFactory: factory}}
			chat, _, err := turns.Model(context.Background(), "ws", "Albums Manager")
			if chat != nil {
				t.Fatal("a failed resolve returned a chat")
			}
			cause := modelCause(t, err)
			if cause.Kind != tc.kind || cause.Provider != tc.provider || cause.Model != tc.model {
				t.Fatalf("cause = %+v", cause)
			}
		})
	}
}

func TestFolderAgentTurns_ACLIThatRefusesIsNotSignedIn(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("codex", &failingToolProvider{name: "codex",
		err: llm.NewProviderError("codex", llm.CategoryAuthentication, errors.New("please run codex login"))})
	turns := folderAgentTurns{builder: &ServerBuilder{workspaceStore: agentOn(t, "codex", "gpt-6.1-sol"), llmFactory: factory}}
	chat, ref, err := turns.Model(context.Background(), "ws", "Albums Manager")
	if err != nil || chat == nil || ref != (folderlook.ModelRef{Provider: "Codex", Model: "gpt-6.1-sol"}) {
		t.Fatalf("resolve = %v %+v", err, ref)
	}
	result := folderlook.RunTurn(context.Background(), folderlook.TurnInput{Chat: chat, Model: ref.Model, Provider: ref.Provider})
	if result.Cause == nil || result.Cause.Kind != folderlook.CauseNotSignedIn {
		t.Fatalf("cause = %+v", result.Cause)
	}
	if sentence, fixable := folderlook.CauseSentence(*result.Cause, "Albums Manager"); sentence != "Codex is not signed in." || !fixable {
		t.Fatalf("sentence = %q %v", sentence, fixable)
	}
}

func TestFolderAgentTurns_AnAPIProviderErrorIsClassifiedNotMissing(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("claude", &failingToolProvider{name: "claude",
		err: llm.NewProviderError("claude", llm.CategoryInvalidModel, errors.New(`404 Workspace wrkspc_9 not found`))})
	turns := folderAgentTurns{builder: &ServerBuilder{workspaceStore: agentOn(t, "anthropic", "claude-sonnet-5-5"), llmFactory: factory}}
	chat, ref, err := turns.Model(context.Background(), "ws", "Albums Manager")
	if err != nil {
		t.Fatal(err)
	}
	result := folderlook.RunTurn(context.Background(), folderlook.TurnInput{Chat: chat, Model: ref.Model, Provider: ref.Provider})
	sentence, _ := folderlook.CauseSentence(*result.Cause, "Albums Manager")
	if sentence != "Anthropic returned an error for claude-sonnet-5-5." {
		t.Fatalf("sentence = %q", sentence)
	}
}
