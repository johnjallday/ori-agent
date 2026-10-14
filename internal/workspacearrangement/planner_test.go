package workspacearrangement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

type plannerProvider struct {
	llm.Provider
	name string
	chat func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
}

func (p plannerProvider) Name() string {
	if p.name == "" {
		return "openai"
	}
	return p.name
}
func (p plannerProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true}
}
func (p plannerProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.chat(ctx, request)
}
func plannerReply(calls ...llm.ToolCall) *llm.ChatResponse {
	return &llm.ChatResponse{ToolCalls: calls, Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}
}
func plannerInput(e *Evidence, provider plannerProvider) PlanInput {
	return PlanInput{Owner: "owner", ContextKey: "conversation", Model: "test-model", Goal: "Keep experiments catalog-only", Evidence: e, Provider: provider, Revalidate: func(context.Context) error { return nil }}
}

func TestPlanner_SystemRulesNamedResultsAndCanonicalCompletion(t *testing.T) {
	e, _ := evidenceFixture(t, 1000)
	calls, saved := 0, 0
	provider := plannerProvider{chat: func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		if !strings.Contains(request.SystemPrompt, plannerRules) || !strings.Contains(request.SystemPrompt, "verified guidance fixture") || request.MaxTokens != MaxOutputTokens || len(request.Tools) != 5 || request.WorkspaceDir != "" || request.ExecutionScope != nil || len(request.MCPServers) != 0 {
			t.Fatal("missing rules or unexpected authority")
		}
		for _, message := range request.Messages {
			if message.Role == llm.RoleSystem {
				t.Fatal("rules rely on unsupported system-role transport")
			}
		}
		if calls == 1 {
			return plannerReply(llm.ToolCall{ID: "select", Name: "arrangement_select", Arguments: `{"filter":{"root_id":"root-1"}}`}), nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != llm.RoleTool || last.Name != "arrangement_select" || last.ToolCallID != "select" {
			t.Fatal("tool result lost Gemini name or call ID")
		}
		var selected FrozenSelection
		if err := json.Unmarshal([]byte(last.Content), &selected); err != nil {
			t.Fatal(err)
		}
		draft := Draft{Version: Version, SourceRevision: selected.Revision, Goal: "Keep experiments catalog-only", Operations: []Operation{{Kind: CreateCollection, Name: "Experiments", Members: Selection{Handle: selected.Handle}}}}
		raw, _ := json.Marshal(draft)
		return plannerReply(llm.ToolCall{ID: "propose", Name: "arrangement_propose", Arguments: string(raw)}), nil
	}}
	input := plannerInput(e, provider)
	input.Guidance = "verified guidance fixture"
	result, err := new(Planner).Run(t.Context(), input, func(ctx context.Context, result PlanResult) error {
		if ctx.Err() != nil || result.Draft == nil || len(result.Draft.Operations[0].Members.IDs) != 250 {
			t.Fatal("completion lost exact membership")
		}
		saved++
		return nil
	})
	if err != nil || calls != 2 || saved != 1 || result.Draft == nil || result.ChargedTokens != 400 {
		t.Fatalf("run: %+v calls=%d saved=%d err=%v", result, calls, saved, err)
	}
}

func TestPlanner_FailuresNeverPublishAnUnsavedDraft(t *testing.T) {
	for _, failure := range []string{"save", "source", "binding", "cancel", "nil", "mixed", "output", "tokens", "missing_total", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			e, snapshot := evidenceFixture(t, 10)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			changed, completed := false, false
			input := plannerInput(e, plannerProvider{chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
				raw, _ := json.Marshal(Draft{Version: Version, SourceRevision: e.revision, Goal: "No changes"})
				reply := plannerReply(llm.ToolCall{ID: "propose", Name: "arrangement_propose", Arguments: string(raw)})
				switch failure {
				case "source":
					snapshot.Authority = "replacement"
				case "binding":
					changed = true
				case "cancel":
					cancel()
				case "nil":
					return nil, nil
				case "mixed":
					reply.ToolCalls = append(reply.ToolCalls, llm.ToolCall{ID: "overview", Name: "arrangement_overview", Arguments: `{}`})
				case "output":
					reply.Usage.CompletionTokens = MaxOutputTokens + 1
				case "tokens":
					reply.Usage.TotalTokens = MaxTurnTokens + 1
				case "missing_total":
					reply.Usage.TotalTokens = 0
					reply.Usage.PromptTokens = MaxTurnTokens + 1
				case "truncated":
					reply.FinishReason = llm.FinishReasonLength
				}
				return reply, nil
			}})
			input.Revalidate = func(context.Context) error {
				if changed {
					return ErrStale
				}
				return nil
			}
			result, err := new(Planner).Run(ctx, input, func(context.Context, PlanResult) error { completed = true; return errors.New("canonical save failed") })
			if err == nil || result.Draft != nil || result.Answer != "" || (failure != "save" && completed) {
				t.Fatalf("failure published: %+v completed=%v err=%v", result, completed, err)
			}
		})
	}
}

func TestPlanner_UnsupportedTransportAndOversizedRequestNeverCallProvider(t *testing.T) {
	for _, mode := range []string{"codex", "claude_code", "unknown", "history"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := evidenceFixture(t, 1)
			input := plannerInput(e, plannerProvider{name: mode, chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
				t.Fatal("unsupported request reached provider")
				return nil, nil
			}})
			if mode == "history" {
				input.Provider = plannerProvider{chat: input.Provider.Chat}
				input.History = []llm.Message{llm.NewUserMessage(strings.Repeat("x", MaxRequestBytes))}
			}
			if _, err := new(Planner).Run(t.Context(), input, func(context.Context, PlanResult) error { t.Fatal("unexpected completion"); return nil }); err == nil {
				t.Fatal("unsupported request accepted")
			}
		})
	}
}

func TestPlanner_ClarificationAndMissingUsageAreBounded(t *testing.T) {
	e, _ := evidenceFixture(t, 1)
	input := plannerInput(e, plannerProvider{chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "Which projects belong to this client?"}, nil
	}})
	result, err := new(Planner).Run(t.Context(), input, func(_ context.Context, result PlanResult) error {
		if result.Draft != nil {
			t.Fatal("clarification manufactured draft")
		}
		return nil
	})
	if err != nil || result.ChargedTokens == 0 || !strings.Contains(result.Answer, "Suggestion only") {
		t.Fatalf("clarification: %+v %v", result, err)
	}
}

func TestPlanner_StopsAtModelBudgetAndReportedModelDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		e, _ := evidenceFixture(t, 1)
		calls := 0
		input := plannerInput(e, plannerProvider{chat: func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			calls++
			reply := plannerReply(llm.ToolCall{ID: fmt.Sprintf("overview-%d", calls), Name: "arrangement_overview", Arguments: `{}`})
			reply.Model = "pinned-model-version"
			if drift && calls == 2 {
				reply.Model = "replacement-version"
			}
			return reply, nil
		}})
		_, err := new(Planner).Run(t.Context(), input, func(context.Context, PlanResult) error { t.Fatal("unfinished turn completed"); return nil })
		if drift {
			if !errors.Is(err, ErrStale) || calls != 2 {
				t.Fatalf("model drift: calls=%d err=%v", calls, err)
			}
		} else if !errors.Is(err, ErrLimit) || calls != MaxModelCalls {
			t.Fatalf("model budget: calls=%d err=%v", calls, err)
		}
	}
}

func TestPlanner_LeasesBoundContextsOwnersAndCancelledTurns(t *testing.T) {
	planner := new(Planner)
	ctx, cancel := context.WithCancel(t.Context())
	release, err := planner.acquire(ctx, "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := planner.acquire(t.Context(), "owner", "one"); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent context allowed")
	}
	secondRelease, err := planner.acquire(t.Context(), "owner", "two")
	if err != nil {
		t.Fatal(err)
	}
	defer secondRelease()
	if _, err := planner.acquire(t.Context(), "owner", "three"); !errors.Is(err, ErrBusy) {
		t.Fatal("owner exceeded two turns")
	}
	cancel()
	replacementRelease, err := planner.acquire(t.Context(), "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	defer replacementRelease()
	release() // An obsolete completion must not release the new lease.
	if _, err := planner.acquire(t.Context(), "owner", "one"); !errors.Is(err, ErrBusy) {
		t.Fatal("old lease released replacement")
	}
}
