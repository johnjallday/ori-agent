package server

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type constrainedChatProvider struct {
	supports bool
	request  llm.ChatRequest
}

func (provider *constrainedChatProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	provider.request = request
	return &llm.ChatResponse{Content: `{"candidates":[]}`}, nil
}

func (provider *constrainedChatProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (provider *constrainedChatProvider) Name() string { return "local-test" }
func (provider *constrainedChatProvider) Type() llm.ProviderType {
	return llm.ProviderTypeLocal
}
func (provider *constrainedChatProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsStructuredOutput: provider.supports}
}
func (provider *constrainedChatProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (provider *constrainedChatProvider) DefaultModels() []string                 { return []string{"local-model"} }

func TestAssistantReflectionModelUsesConstrainedChatForLocalProvider(t *testing.T) {
	provider := &constrainedChatProvider{supports: true}
	model := newLLMAssistantReflectionModel(func(context.Context) (llm.Provider, string, error) {
		return provider, "local-model", nil
	}, nil)
	schema := map[string]any{"type": "object"}
	response, err := model.GenerateAssistantReflection(context.Background(), workspace.AssistantReflectionModelRequest{
		SystemPrompt: "Return candidates.", SchemaName: "assistant_reflection_v1", Schema: schema,
		Snapshot: workspace.AssistantReflectionSnapshot{ProgramID: "program-1"},
	})
	if err != nil || response != `{"candidates":[]}` {
		t.Fatalf("local constrained reflection = (%q, %v)", response, err)
	}
	if provider.request.Model != "local-model" || provider.request.Temperature != 0 || provider.request.MaxTokens != 2048 || provider.request.ResponseSchema["type"] != "object" {
		t.Fatalf("constrained chat request = %+v", provider.request)
	}
	if len(provider.request.Messages) != 1 || provider.request.Messages[0].Role != "user" {
		t.Fatalf("bounded evidence message = %+v", provider.request.Messages)
	}
}

func TestAssistantReflectionModelRejectsUnconstrainedProvider(t *testing.T) {
	provider := &constrainedChatProvider{}
	model := newLLMAssistantReflectionModel(func(context.Context) (llm.Provider, string, error) {
		return provider, "local-model", nil
	}, nil)
	_, err := model.GenerateAssistantReflection(context.Background(), workspace.AssistantReflectionModelRequest{
		Schema: map[string]any{"type": "object"},
	})
	if !errors.Is(err, workspace.ErrAssistantReflectionUnavailable) {
		t.Fatalf("unconstrained provider error = %v", err)
	}
}
