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

func TestSetOpenAIChatTemperature_SkipsReasoningModels(t *testing.T) {
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel("gpt-5-nano"),
	}

	setOpenAIChatTemperature(&params, "gpt-5-nano", 0)

	if params.Temperature.Valid() {
		t.Fatalf("expected temperature to be omitted for gpt-5 reasoning models")
	}
}

func TestSetOpenAIChatTemperature_SetsNonReasoningModels(t *testing.T) {
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel("gpt-4o-mini"),
	}

	setOpenAIChatTemperature(&params, "gpt-4o-mini", 0.2)

	if !params.Temperature.Valid() {
		t.Fatalf("expected temperature to be set for non-reasoning models")
	}
	if params.Temperature.Value != 0.2 {
		t.Fatalf("expected temperature 0.2, got %v", params.Temperature.Value)
	}
}
