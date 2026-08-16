package workspaceplan

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// This file is the only place that knows Ori's LLM package exists. Everything
// above it works against PlanModel, which is what keeps the planning domain
// provider-agnostic and stops a provider response object from becoming the
// canonical Plan schema (FR-18).

// LLMPlanModel adapts a provider that supports structured output to PlanModel.
// Every provider is asked for the same schema, so plan content does not depend
// on which model produced it (PRD 7.4).
type LLMPlanModel struct {
	resolve ProviderResolver
}

// ProviderResolver returns the provider and model to plan with. It is a
// function rather than a fixed provider because the configured planning model
// can change while the server runs, and a Plan drafted after a settings change
// should use the new model without a restart.
//
// Returning an error, a nil provider, or a provider without structured-output
// support all mean the same thing to the caller: generation is unavailable
// right now, and everything that does not need a model still works (FR-58).
type ProviderResolver func(ctx context.Context) (llm.Provider, string, error)

// NewLLMPlanModel returns a PlanModel backed by Ori's provider abstraction.
func NewLLMPlanModel(resolve ProviderResolver) *LLMPlanModel {
	return &LLMPlanModel{resolve: resolve}
}

var _ PlanModel = (*LLMPlanModel)(nil)

// GenerateStructured asks the configured provider for a response matching the
// schema, and returns its raw JSON for the generator to decode and validate.
//
// It deliberately returns the raw text rather than a decoded object: decoding
// belongs to the schema contract in this package, so a provider that returns
// slightly different envelopes cannot change what a Plan means.
func (m *LLMPlanModel) GenerateStructured(ctx context.Context, req StructuredRequest) (string, error) {
	if m == nil || m.resolve == nil {
		return "", ErrModelUnavailable
	}

	provider, model, err := m.resolve(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrModelUnavailable, err)
	}
	if provider == nil {
		return "", ErrModelUnavailable
	}

	structured, ok := provider.(llm.StructuredOutputProvider)
	if !ok {
		// A provider that cannot honor a schema is not usable for planning:
		// falling back to free text would reintroduce exactly the "prose
		// defines dependencies" problem the typed schema exists to prevent
		// (FR-40, FR-45).
		return "", fmt.Errorf("%w: provider %q does not support structured output",
			ErrModelUnavailable, provider.Name())
	}

	response, err := structured.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
		Model:        model,
		SystemPrompt: req.System,
		SchemaName:   req.SchemaName,
		Schema:       req.Schema,
		Messages: []llm.Message{{
			Role:    "user",
			Content: req.Prompt,
		}},
	})
	if err != nil {
		return "", fmt.Errorf("plan generation failed: %w", err)
	}
	if response == nil || strings.TrimSpace(response.Content) == "" {
		return "", fmt.Errorf("%w: provider returned an empty response", ErrValidation)
	}
	return response.Content, nil
}
