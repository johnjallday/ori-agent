package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type learningModelSettings interface {
	GetSystemModel() (string, string)
	GetSystemReasoningEffort() string
}

type learningDesigner struct {
	settings learningModelSettings
	factory  *llm.Factory
}

func (d *learningDesigner) resolve() (learningmissions.ModelConfiguration, llm.BoundedTextProvider, error) {
	unavailable := learningmissions.ErrUnavailable
	if d == nil || d.settings == nil || d.factory == nil {
		return learningmissions.ModelConfiguration{}, nil, unavailable
	}
	name, model := d.settings.GetSystemModel()
	name, model = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(model)
	if name == "" || model == "" {
		return learningmissions.ModelConfiguration{}, nil, unavailable
	}
	// Do not call DefaultModels/GetSystemModelProvider: model discovery can
	// perform network I/O. Never infer or fall back to a different provider.
	provider, err := d.factory.GetProvider(name)
	if err != nil || provider == nil {
		return learningmissions.ModelConfiguration{}, nil, unavailable
	}
	bounded, ok := provider.(llm.BoundedTextProvider)
	if !ok || bounded.BoundedTextConfiguration() == "" || !provider.Capabilities().SupportsIsolatedChat {
		return learningmissions.ModelConfiguration{}, nil, unavailable
	}
	config := learningmissions.ModelConfiguration{Provider: name, Model: model, Effort: d.settings.GetSystemReasoningEffort()}
	data, err := json.Marshal([]any{learningmissions.DisclosureVersion, config.Provider, config.Model, config.Effort, bounded.BoundedTextConfiguration()})
	if err != nil {
		return learningmissions.ModelConfiguration{}, nil, unavailable
	}
	config.Fingerprint = learningDigest(data)
	return config, bounded, config.Validate()
}

func (d *learningDesigner) Configuration(ctx context.Context) (learningmissions.ModelConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return learningmissions.ModelConfiguration{}, err
	}
	config, _, err := d.resolve()
	return config, err
}

func knownLearningUsage(usage llm.Usage) *int {
	// Zero/missing components or inconsistent totals stay unknown. Do not
	// convert partial provider accounting into a free call in diagnostics.
	if usage.PromptTokens <= 0 || usage.CompletionTokens <= 0 || usage.TotalTokens < usage.PromptTokens ||
		usage.TotalTokens-usage.PromptTokens < usage.CompletionTokens {
		return nil
	}
	total := usage.TotalTokens
	return &total
}

func (d *learningDesigner) Generate(ctx context.Context, reviewed learningmissions.ModelConfiguration, snapshot learningmissions.ContextSnapshot) (learningmissions.GenerationReply, error) {
	out := learningmissions.GenerationReply{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	config, provider, err := d.resolve()
	if err != nil {
		return out, err
	}
	if reviewed.Validate() != nil || config != reviewed {
		return out, learningmissions.ErrConflict
	}
	data, err := snapshot.ModelJSON()
	if err != nil || len(data)+len(learningmissions.DesignSystemPrompt) > learningmissions.MaxInputBytes || len(snapshot.Data.Candidates) == 0 {
		return out, learningmissions.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, learningmissions.GenerationTimeout)
	defer cancel()
	request := llm.ChatRequest{Model: config.Model, SystemPrompt: learningmissions.DesignSystemPrompt,
		Messages: []llm.Message{llm.NewUserMessage(string(data))}, ReasoningEffort: config.Effort,
		MaxTokens: learningmissions.MaxOutputTokens, Isolated: true, SingleAttempt: true,
		ProviderConfiguration: provider.BoundedTextConfiguration()}
	// resolve() and this snapshot must describe the same destination. The
	// provider additionally checks the pin atomically at its transport snapshot.
	current, err := d.Configuration(ctx)
	if err != nil || current != reviewed {
		return out, learningmissions.ErrConflict
	}
	if provider.Capabilities().SupportsStructuredOutput {
		request.ResponseSchema = learningmissions.ProposalSchema()
		schema, err := json.Marshal(request.ResponseSchema)
		if err != nil || len(data)+len(request.SystemPrompt)+len(schema) > learningmissions.MaxInputBytes {
			return out, learningmissions.ErrInvalid
		}
	}
	response, err := provider.Chat(ctx, request)
	if response != nil {
		out.ActualTokens = knownLearningUsage(response.Usage)
	}
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, learningmissions.ErrUnavailable // no raw provider error/prompt in owner responses
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if response == nil || len(response.ToolCalls) != 0 || response.FinishReason != llm.FinishReasonStop ||
		len(response.Content) == 0 || len(response.Content) > learningmissions.MaxResponseBytes || response.Usage.CompletionTokens > learningmissions.MaxOutputTokens {
		return out, learningmissions.ErrInvalid
	}
	current, err = d.Configuration(ctx)
	if err != nil || current != reviewed {
		return out, learningmissions.ErrConflict
	}
	out.Text = response.Content
	return out, nil
}
