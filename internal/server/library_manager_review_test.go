package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
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
