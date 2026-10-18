package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The review turn resolves the Manager's model the way chat does: an explicit
// provider wins, a few namespaces are unambiguous, and an empty provider is
// never assumed to be OpenAI.
func TestLibraryManagerProviderName_MatchesChatResolution(t *testing.T) {
	for _, tc := range []struct{ provider, model, want string }{
		{"anthropic", "claude-sonnet-5-5", "claude"},
		{"OpenAI", "gpt-4o", "openai"},
		{"claude_code", "", "claude_code"},
		{"", "claude-haiku-4-5", "claude"},
		{"", "gemini-2.5-pro", "gemini"},
		{"", "gpt-4o", ""},
		{"", "", ""},
		{"mystery", "gpt-4o", ""},
	} {
		if got := libraryManagerProviderName(tc.provider, tc.model, nil); got != tc.want {
			t.Fatalf("provider %q model %q: got %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

// Claude Code round-trips Ori's tools through the brokered protocol, so a
// Manager on it is a model for the review turn. A provider that cannot call
// tools still is not.
func TestLibraryManagerModel_ClaudeCodeManagerIsAModel(t *testing.T) {
	store := workspace.NewInMemoryStore()
	if err := store.SaveWorkspaceAgent("home", "Manager", &agent.Agent{Settings: types.Settings{Provider: "claude_code", Model: "sonnet"}}); err != nil {
		t.Fatal(err)
	}
	factory := llm.NewFactory()
	factory.Register("claude_code", &llm.ClaudeCodeProvider{})
	b := &ServerBuilder{workspaceStore: store, llmFactory: factory}

	chat, model, err := b.libraryManagerModel(context.Background(), "home", "Manager")
	if err != nil || chat == nil || model != "sonnet" {
		t.Fatalf("claude_code Manager was not a model: chat=%v model=%q err=%v", chat != nil, model, err)
	}

	factory.Register("claude_code", &capturingChatProvider{})
	if chat, _, err := b.libraryManagerModel(context.Background(), "home", "Manager"); chat != nil || !errors.Is(err, projectlibrary.ErrNoManagerModel) {
		t.Fatalf("a tool-free provider became a Manager model: %v", err)
	}
}

func TestLibraryManagerModel_WithoutStoresReportsNoModel(t *testing.T) {
	b := &ServerBuilder{}
	if chat, model, err := b.libraryManagerModel(context.Background(), "home", "Manager"); chat != nil || model != "" ||
		!errors.Is(err, projectlibrary.ErrNoManagerModel) {
		t.Fatalf("unconfigured host produced a model: %q %v", model, err)
	}
	if tools := b.libraryManagerReviewTools(projectlibrary.ManagerAuthority{HomeID: "home"}); tools != nil {
		t.Fatalf("unconfigured host produced tools: %d", len(tools))
	}
}
