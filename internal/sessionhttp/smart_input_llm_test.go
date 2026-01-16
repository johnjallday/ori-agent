package sessionhttp

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

type stubProvider struct {
	content string
	err     error
}

func (s *stubProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &llm.ChatResponse{Content: s.content}, nil
}

func (s *stubProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Name() string {
	return "stub"
}

func (s *stubProvider) Type() llm.ProviderType {
	return llm.ProviderTypeLocal
}

func (s *stubProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (s *stubProvider) ValidateConfig(config llm.ProviderConfig) error {
	return nil
}

func (s *stubProvider) DefaultModels() []string {
	return []string{"stub-model"}
}

func TestClassifySmartInputLLM_ParsesJSON(t *testing.T) {
	provider := &stubProvider{
		content: `{"decision":"chat","confidence":0.42,"reasoning":"question"}`,
	}

	result, err := classifySmartInputLLM(context.Background(), provider, "stub", "What is the plan?")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Decision != SmartInputDecisionChat {
		t.Fatalf("expected chat decision, got %s", result.Decision)
	}
	if result.Confidence != 0.42 {
		t.Fatalf("expected confidence 0.42, got %f", result.Confidence)
	}
}

func TestClassifySmartInputLLM_ParsesCodeBlock(t *testing.T) {
	provider := &stubProvider{
		content: "```json\n{\"decision\":\"task\",\"confidence\":0.9}\n```",
	}

	result, err := classifySmartInputLLM(context.Background(), provider, "stub", "todo: update docs")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Decision != SmartInputDecisionTask {
		t.Fatalf("expected task decision, got %s", result.Decision)
	}
	if result.Confidence != 0.9 {
		t.Fatalf("expected confidence 0.9, got %f", result.Confidence)
	}
}

func TestClassifySmartInputLLM_InvalidDecision(t *testing.T) {
	provider := &stubProvider{
		content: `{"decision":"other","confidence":0.5}`,
	}

	_, err := classifySmartInputLLM(context.Background(), provider, "stub", "something")
	if err == nil {
		t.Fatal("expected error for invalid decision")
	}
}
