package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type designerSettings struct{ provider, model, effort string }

func (s *designerSettings) GetSystemModel() (string, string) { return s.provider, s.model }
func (s *designerSettings) GetSystemReasoningEffort() string { return s.effort }

type designerProvider struct {
	*assessmentProvider
	isolation bool
	respond   func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
	calls     int
}

func (p *designerProvider) DefaultModels() []string { panic("model catalog must not be queried") }
func (p *designerProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsIsolatedChat: p.isolation, SupportsStructuredOutput: true}
}
func (p *designerProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	if p.respond != nil {
		return p.respond(ctx, req)
	}
	return p.assessmentProvider.Chat(ctx, req)
}

func designerFixture() (*learningDesigner, *designerSettings, *designerProvider, learningmissions.ContextSnapshot) {
	settings := &designerSettings{provider: "chosen", model: "exact-configured-model", effort: "low"}
	provider := &designerProvider{assessmentProvider: &assessmentProvider{}, isolation: true}
	factory := llm.NewFactory()
	factory.Register("chosen", provider)
	designer := &learningDesigner{settings: settings, factory: factory}
	snapshot := learningmissions.ContextSnapshot{Data: learningmissions.DesignContext{Version: 1, Focus: "A community workshop", Guidance: learningmissions.GuidanceBasic,
		Candidates: []learningmissions.DesignCandidate{{Ref: "host-candidate", Structure: learningmissions.DelegateInspect, Version: 1, Steps: []string{"scope", "execute", "review"}, Ready: true, Effect: "internal_draft"}}}}
	return designer, settings, provider, snapshot
}

func TestLearningDesignerUsesOnlyTheConfiguredBoundedDataCall(t *testing.T) {
	designer, _, provider, snapshot := designerFixture()
	provider.respond = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > learningmissions.GenerationTimeout || llm.CheckIsolatedRequest(req) != nil || !req.Isolated ||
			!req.SingleAttempt || req.MaxTokens != 1200 || req.ProviderConfiguration != provider.BoundedTextConfiguration() ||
			req.Model != "exact-configured-model" || req.ReasoningEffort != "low" || len(req.Messages) != 1 || req.ResponseSchema == nil || req.Stream ||
			req.WorkspaceDir != "" || len(req.Messages[0].Images) != 0 || req.SystemPrompt != learningmissions.DesignSystemPrompt {
			t.Fatalf("unexpected authority/limits: %+v", req)
		}
		if !strings.Contains(req.Messages[0].Content, "explicit_focus") || strings.Contains(req.Messages[0].Content, "Bindings") {
			t.Fatal("not the data-only context")
		}
		return &llm.ChatResponse{Content: `{"controlled":"reply; strict validation is the host's next step"}`, FinishReason: llm.FinishReasonStop,
			Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, nil
	}
	config, err := designer.Configuration(t.Context())
	if err != nil || provider.calls != 0 {
		t.Fatalf("configuration performed a call: %v", err)
	}
	out, err := designer.Generate(t.Context(), config, snapshot)
	if err != nil || out.Text == "" || out.ActualTokens == nil || *out.ActualTokens != 15 || provider.calls != 1 {
		t.Fatalf("one call: %+v %v", out, err)
	}
}

func TestLearningDesignerRefusesUnavailableChangedAndUnboundedModelsWithoutFallback(t *testing.T) {
	for _, reason := range []string{"missing", "isolation", "unbounded", "changed", "no_candidates", "oversized", "cancelled"} {
		t.Run(reason, func(t *testing.T) {
			designer, settings, provider, snapshot := designerFixture()
			config, err := designer.Configuration(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch reason {
			case "missing":
				settings.provider = "not-configured"
			case "isolation":
				provider.isolation = false
			case "unbounded":
				designer.factory.Register("chosen", struct{ llm.Provider }{provider})
			case "changed":
				settings.model = "different-model"
			case "no_candidates":
				snapshot.Data.Candidates = nil
			case "oversized":
				snapshot.Data.Focus = strings.Repeat("x", learningmissions.MaxInputBytes)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := designer.Generate(ctx, config, snapshot); err == nil || provider.calls != 0 {
				t.Fatalf("inadmissible request reached a provider: %d %v", provider.calls, err)
			}
		})
	}
}

func TestLearningDesignerRejectsBadRepliesAndAccountsUnknownUsageWithoutRepair(t *testing.T) {
	for _, reason := range []string{"offline", "timeout", "nil", "tools", "length", "oversized", "over_budget", "changed", "partial_usage"} {
		t.Run(reason, func(t *testing.T) {
			designer, settings, provider, snapshot := designerFixture()
			config, _ := designer.Configuration(t.Context())
			provider.respond = func(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
				reply := &llm.ChatResponse{Content: `{}`, FinishReason: llm.FinishReasonStop, Usage: llm.Usage{TotalTokens: 10}}
				switch reason {
				case "offline":
					return nil, errors.New("private endpoint diagnostic")
				case "timeout":
					<-ctx.Done()
					return nil, ctx.Err()
				case "nil":
					return nil, nil
				case "tools":
					reply.ToolCalls = []llm.ToolCall{{Name: "do-not-run"}}
				case "length":
					reply.FinishReason = llm.FinishReasonLength
				case "oversized":
					reply.Content = strings.Repeat("x", learningmissions.MaxResponseBytes+1)
				case "over_budget":
					reply.Usage.CompletionTokens = learningmissions.MaxOutputTokens + 1
				case "changed":
					settings.effort = "high"
				}
				return reply, nil
			}
			ctx := t.Context()
			if reason == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
				defer cancel()
			}
			out, err := designer.Generate(ctx, config, snapshot)
			if (err == nil) != (reason == "partial_usage") || out.ActualTokens != nil || provider.calls != 1 {
				t.Fatalf("repair/accounting: calls=%d out=%+v err=%v", provider.calls, out, err)
			}
			if err != nil && (out.Text != "" || strings.Contains(err.Error(), "private endpoint")) {
				t.Fatalf("leaked rejected reply/diagnostic: %+v %v", out, err)
			}
		})
	}
}
