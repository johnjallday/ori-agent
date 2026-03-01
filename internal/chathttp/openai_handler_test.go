package chathttp

import (
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestInjectRuntimeSystemPrompt_InsertsBeforeTrailingUser(t *testing.T) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("base"),
		openai.UserMessage("hello"),
	}

	withRuntime := injectRuntimeSystemPrompt(messages, "runtime")
	if len(withRuntime) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(withRuntime))
	}
	if withRuntime[1].OfSystem == nil {
		t.Fatalf("expected runtime system message before trailing user message")
	}
	if withRuntime[2].OfUser == nil {
		t.Fatalf("expected trailing user message to remain last")
	}
}

func TestInjectRuntimeSystemPrompt_AppendsWhenLastIsNotUser(t *testing.T) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("base"),
		openai.AssistantMessage("done"),
	}

	withRuntime := injectRuntimeSystemPrompt(messages, "runtime")
	if len(withRuntime) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(withRuntime))
	}
	if withRuntime[2].OfSystem == nil {
		t.Fatalf("expected runtime system message to be appended")
	}
}
