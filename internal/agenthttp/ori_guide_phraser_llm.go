package agenthttp

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// llmGuidePhraser restates approved guide copy using the configured system
// model.
//
// It is the only place the guide touches a model, and it is deliberately thin:
// one completion, no tools, no history, no retries. It satisfies GuidePhraser,
// whose signature is text-in/text-out, so nothing it returns can become an
// action (PRD FR-46).
type llmGuidePhraser struct {
	factory     *llm.Factory
	systemModel homeAskSystemModelReader
}

// NewLLMGuidePhraser builds phrasing over the configured system model. It reuses
// the same reader the Home assistant uses, so "which model is the system model"
// has one answer across the app.
func NewLLMGuidePhraser(factory *llm.Factory, systemModel homeAskSystemModelReader) GuidePhraser {
	return &llmGuidePhraser{factory: factory, systemModel: systemModel}
}

var errGuideModelNotConfigured = errors.New("no system model configured")

// Phrase returns a reworded answer, or an error. Every error path is a fallback
// to the approved text at the call site, so failing is always safe here.
func (p *llmGuidePhraser) Phrase(ctx context.Context, question, approved string) (string, error) {
	if p == nil || p.factory == nil || p.systemModel == nil {
		return "", errGuideModelNotConfigured
	}
	providerName, model := p.systemModel.GetSystemModel()
	if strings.TrimSpace(providerName) == "" {
		return "", errGuideModelNotConfigured
	}
	provider, err := p.factory.GetProvider(providerName)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(model) == "" {
		if models := provider.DefaultModels(); len(models) > 0 {
			model = models[0]
		}
	}

	system, user := BuildGuidePhrasingPrompt(question, approved)

	// No Tools field is set. The guide has nothing to call, so a model that
	// wanted to act would have nowhere to send it.
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
