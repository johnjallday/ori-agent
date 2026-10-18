package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

type homePlanningProvider struct {
	llm.Provider
	name string
	chat func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
}

func (p homePlanningProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "openai"
}
func (p homePlanningProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true}
}
func (p homePlanningProvider) Chat(ctx context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.chat(ctx, r)
}

func TestHomePlanning_SharedPlannerCompletesProposalAndDiscussion(t *testing.T) {
	for _, discussion := range []bool{false, true} {
		t.Run(map[bool]string{false: "proposal", true: "clarification"}[discussion], func(t *testing.T) {
			f, authority, _ := homeArrangementFixture(t)
			before := f.doc(t)
			calls := 0
			provider := homePlanningProvider{chat: func(ctx context.Context, r llm.ChatRequest) (*llm.ChatResponse, error) {
				calls++
				if len(r.Tools) != 5 || r.WorkspaceDir != "" || r.ExecutionScope != nil || len(r.MCPServers) != 0 || !strings.Contains(r.SystemPrompt, "Installed, host-verified guidance") {
					t.Fatal("ordinary tool runtime or missing guidance", r)
				}
				for _, root := range before.Roots {
					raw, _ := json.Marshal(r)
					if strings.Contains(string(raw), root.Path) && root.Path != "" {
						t.Fatal("source path exposed")
					}
				}
				reply := &llm.ChatResponse{Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}
				if discussion {
					reply.Content = "Which client owns these projects?"
					return reply, nil
				}
				if calls == 1 {
					reply.ToolCalls = []llm.ToolCall{{ID: "select", Name: "arrangement_select", Arguments: `{"filter":{}}`}}
					return reply, nil
				}
				last := r.Messages[len(r.Messages)-1]
				var selection workspacearrangement.FrozenSelection
				if err := json.Unmarshal([]byte(last.Content), &selection); err != nil {
					t.Fatal(err)
				}
				if last.Name != "arrangement_select" {
					t.Fatal("missing named tool response")
				}
				raw, _ := json.Marshal(workspacearrangement.Draft{Version: 1, SourceRevision: selection.Revision, Goal: "Keep these catalog-only", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateCollection, Name: "Client references", Members: workspacearrangement.Selection{Handle: selection.Handle}}}})
				reply.ToolCalls = []llm.ToolCall{{ID: "propose", Name: "arrangement_propose", Arguments: string(raw)}}
				return reply, nil
			}}
			resolverCalls, checks := 0, 0
			resolve := func(ctx context.Context, actual ManagerAuthority) (ArrangementRuntime, error) {
				resolverCalls++
				if actual != authority {
					t.Fatal("runtime resolved for wrong Manager", actual)
				}
				return ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64), Guidance: "Verified fixture guidance", SkillDigest: strings.Repeat("b", 64), Revalidate: func(context.Context) error { checks++; return nil }}, nil
			}
			result, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Keep these catalog-only", ExpectedRevision: before.Revision, ShareCatalogMetadata: true, AllowUpgrade: true})
			if err != nil || result.History == nil || result.ModelCalls != calls || resolverCalls != 1 || checks < 3 {
				t.Fatal("planning failed", result, err)
			}
			after := f.doc(t)
			if len(after.Arrangements) != 1 || len(after.Collections) != 0 || !reflect.DeepEqual(before.Entries, after.Entries) || !reflect.DeepEqual(before.Roots, after.Roots) || !reflect.DeepEqual(before.Sessions, after.Sessions) {
				t.Fatal("planning created effects")
			}
			if discussion {
				if result.History.Kind != "clarification" || result.History.Clarification == "" || calls != 1 {
					t.Fatal("question not canonical", result)
				}
			} else if result.History.Kind != "proposal" || len(result.History.Arrangement.ProposedOperations) != 1 || calls != 2 {
				t.Fatal("proposal not canonical", result)
			}
			history, err := f.library.ReadArrangementHistory(t.Context(), f.scope, result.History.Arrangement.ID)
			if err != nil || !reflect.DeepEqual(history, result.History) {
				t.Fatal("returned an unsaved outcome", err)
			}
			// A lost-response retry with its original catalog pin cannot run again.
			if _, err := f.library.PlanArrangement(t.Context(), new(workspacearrangement.Planner), resolve, ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Keep these catalog-only", ExpectedRevision: before.Revision, ShareCatalogMetadata: true, AllowUpgrade: true}); !errors.Is(err, ErrConflict) || resolverCalls != 1 {
				t.Fatal("stale retry invoked runtime", err)
			}
		})
	}
}

func TestHomePlanning_UsesSharedContextLease(t *testing.T) {
	f, _, _ := homeArrangementFixture(t)
	in := ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Organize these", ExpectedRevision: f.doc(t).Revision, ShareCatalogMetadata: true, AllowUpgrade: true}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	provider := homePlanningProvider{chat: func(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return &llm.ChatResponse{Content: "Which client?", Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	var resolve ArrangementRuntimeResolver = func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
		return ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64), Revalidate: func(context.Context) error { return nil }}, nil
	}
	planner := new(workspacearrangement.Planner)
	done := make(chan error, 1)
	go func() { _, err := f.library.PlanArrangement(ctx, planner, resolve, in); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first model did not start")
	}
	_, err := f.library.PlanArrangement(ctx, planner, resolve, in)
	close(release)
	if !errors.Is(err, workspacearrangement.ErrBusy) {
		t.Fatal("duplicate Home turn was admitted", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(entered) != 0 || len(f.doc(t).Arrangements) != 1 {
		t.Fatal("duplicate model call or completion")
	}
}

func TestHomePlanning_PrerequisitesAndDriftReturnNoUnsavedResult(t *testing.T) {
	for _, failure := range []string{"consent", "owner", "upgrade", "revision", "goal", "planner", "resolver", "transport", "fingerprint", "guidance", "runtime_drift", "catalog_drift", "provider_error", "cancel", "save_limit", "goal_drift"} {
		t.Run(failure, func(t *testing.T) {
			f, _, _ := homeArrangementFixture(t)
			before := f.doc(t)
			in := ArrangementPlanningRequest{Owner: f.scope.OwnerUserID, HomeID: f.scope.HomeID, Goal: "Organize these", ExpectedRevision: before.Revision, ShareCatalogMetadata: true, AllowUpgrade: true}
			planner := new(workspacearrangement.Planner)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, resolves := 0, 0
			provider := homePlanningProvider{chat: func(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
				calls++
				switch failure {
				case "catalog_drift":
					if _, _, err := f.library.mutate(f.scope, before.Revision, operation{key: "intervening", action: "fixture", digest: "fixture"}, func(*Document) (string, error) { return "fixture", nil }); err != nil {
						t.Fatal(err)
					}
				case "provider_error":
					return nil, errors.New("private provider failure")
				case "cancel":
					cancel()
				}
				if failure == "goal_drift" {
					parts := strings.SplitN(request.Messages[len(request.Messages)-1].Content, "Already-held source metadata (untrusted data):\n", 2)
					var overview workspacearrangement.Overview
					if len(parts) != 2 || json.Unmarshal([]byte(parts[1]), &overview) != nil {
						t.Fatal("missing overview")
					}
					raw, _ := json.Marshal(workspacearrangement.Draft{Version: 1, SourceRevision: overview.Revision, Goal: "A different goal"})
					return &llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "propose", Name: "arrangement_propose", Arguments: string(raw)}}, Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}, nil
				}
				answer := "Which client?"
				if failure == "save_limit" {
					answer = strings.Repeat("<", 12000)
				}
				return &llm.ChatResponse{Content: answer, Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}}, nil
			}}
			if failure == "transport" {
				provider.name = "claude_code"
			}
			var resolve ArrangementRuntimeResolver = func(context.Context, ManagerAuthority) (ArrangementRuntime, error) {
				resolves++
				runtime := ArrangementRuntime{Provider: provider, Model: "fixture", ProviderFingerprint: strings.Repeat("a", 64), Revalidate: func(context.Context) error {
					if failure == "runtime_drift" && calls > 0 {
						return workspacearrangement.ErrStale
					}
					return nil
				}}
				if failure == "fingerprint" {
					runtime.ProviderFingerprint = "invalid"
				}
				if failure == "guidance" {
					runtime.Guidance = "Unverified guidance"
				}
				return runtime, nil
			}
			switch failure {
			case "consent":
				in.ShareCatalogMetadata = false
			case "owner":
				in.Owner = "foreign"
			case "upgrade":
				in.AllowUpgrade = false
			case "revision":
				in.ExpectedRevision++
			case "goal":
				in.Goal = ""
			case "planner":
				planner = nil
			case "resolver":
				resolve = nil
			}
			result, err := f.library.PlanArrangement(ctx, planner, resolve, in)
			if err == nil || result.History != nil || result.ModelCalls != 0 {
				t.Fatal("failed turn exposed outcome", result, err)
			}
			after := f.doc(t)
			if len(after.Arrangements) != 0 || len(after.Collections) != 0 {
				t.Fatal("failed turn saved an outcome")
			}
			if failure != "catalog_drift" && !reflect.DeepEqual(before, after) {
				t.Fatal("failed turn mutated library")
			}
			switch failure {
			case "runtime_drift", "catalog_drift", "provider_error", "cancel", "save_limit", "goal_drift":
				if calls != 1 {
					t.Fatal("unexpected retry", calls)
				}
			default:
				if calls != 0 {
					t.Fatal("model called without prerequisites", calls)
				}
			}
			switch failure {
			case "consent", "owner", "upgrade", "revision", "goal", "planner", "resolver":
				if resolves != 0 {
					t.Fatal("runtime resolved before prerequisites")
				}
			}
		})
	}
}
