package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

const workspaceProposalFixture = `{"name":"OK Go — How They Started","description":"Goal: research how OK Go started. Proposed starter work: find interviews, build an early-years timeline, write a sourced origin story. Dates and first-break details need verification."}`

func workspaceProposalReply(arguments string) llm.ChatResponse {
	return llm.ChatResponse{ToolCalls: []llm.ToolCall{{Name: workspaceProposalTool, Arguments: arguments}}}
}

func TestWorkspaceReviewCurrentExplicitPreparationNotHistoricalConsent(t *testing.T) {
	for _, prompt := range []string{"Prepare a workspace review for this idea", "Please prepare a workspace proposal", "Let's set up a workspace for this plan", "Yes, prepare a workspace review"} {
		if !isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("explicit route hint missed: %q", prompt)
		}
	}
	for _, prompt := range []string{"yes", "use that", "Should we set up a workspace for this?", "Don't prepare a workspace review", `Explain "prepare a workspace review"`, "Prepare a workspace reviewable report"} {
		if isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("discussion became explicit route hint: %q", prompt)
		}
	}
	policy := buildAssistantConversationSystemPrompt(nil)
	if !strings.Contains(policy, "respect a decline") || !strings.Contains(policy, "proposal-only tool") || !strings.Contains(policy, "do not require a particular phrase") {
		t.Fatal("optional natural proposal/decline guidance missing")
	}
}

func TestWorkspaceReviewNaturalRequestsUseOneExistingModelTurn(t *testing.T) {
	for _, prompt := range []string{"how they started. can we create a workspace", "Let's organize this research somewhere", "Prepare a workspace review for researching how OK Go started."} {
		for _, failedSave := range []bool{false, true} {
			f, store, source := newResearchHostFixture(t)
			store.seed("canonical", f.work.HQWorkspaceID, f.work.ConversationAgent,
				PersonalAssistantConversationMessage{Role: "user", Content: "wanna research on okgo band"},
				PersonalAssistantConversationMessage{Role: "assistant", Content: "Music, videos or how they started?"})
			store.failSave = failedSave
			f.provider.script = []llm.ChatResponse{workspaceProposalReply(workspaceProposalFixture)}
			response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: prompt, Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
			if source.localCalls != 0 || source.externalCalls != 0 || len(f.provider.requests) != 1 {
				t.Fatal("proposal caused extra model, source or discovery work")
			}
			request := f.provider.requests[0]
			serialized, _ := json.Marshal(request)
			if request.WorkspaceID != "" || request.ExecutionScope != nil || len(request.MCPServers) != 0 || !strings.Contains(string(serialized), "okgo band") || !strings.Contains(string(serialized), workspaceProposalTool) {
				t.Fatal("missing canonical history/tool or inherited native authority")
			}
			if failedSave {
				if response.RequiresConfirmation || response.Confirmation != nil {
					t.Fatal("failed save supplied a form proposal")
				}
				continue
			}
			if !response.RequiresConfirmation || response.Confirmation == nil || response.Confirmation.ActionType != HomeActionPrepareWorkspace || response.Conversation == nil || !response.Conversation.Stored || response.Conversation.ID != "canonical" {
				t.Fatalf("missing canonical editable proposal: %+v", response)
			}
			if !strings.Contains(response.Response, response.Confirmation.Arguments["description"].(string)) || homeMutatingActionTypes[HomeActionPrepareWorkspace] {
				t.Fatal("proposal content diverged or acquired mutation authority")
			}
		}
	}
}

type workspaceProposalMutationProvider struct {
	llm.Provider
	after func(context.Context, llm.ChatRequest)
	err   error
}

func (p *workspaceProposalMutationProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	result, err := p.Provider.Chat(ctx, request)
	if p.after != nil {
		p.after(ctx, request)
	}
	if p.err != nil {
		return nil, p.err
	}
	return result, err
}

func TestWorkspaceReviewInFlightMutationCannotOfferOrSaveProposal(t *testing.T) {
	for _, mutation := range []string{"append", "delete", "owner", "relationship", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			f, store, _ := newResearchHostFixture(t)
			// Independent of research wiring: this proposal owns its own pin.
			f.handler.Discovery = nil
			f.provider.script = []llm.ChatResponse{workspaceProposalReply(workspaceProposalFixture)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.handler.LLMFactory.Register("fake", &workspaceProposalMutationProvider{Provider: f.provider, after: func(context.Context, llm.ChatRequest) {
				switch mutation {
				case "append":
					_, _ = store.Append(ctx, "canonical", "user", "A newer correction.")
				case "delete":
					_ = store.Discard(ctx, "canonical")
				case "owner":
					store.sessions["canonical"].record.AgentName = "Foreign"
				case "relationship":
					f.work.StateVersion++
				case "cancel":
					cancel()
				}
			}})
			response := f.handler.Ask(ctx, HomeAssistantAskRequest{Prompt: "how they started. can we create a workspace", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
			if response.Confirmation != nil || response.RequiresConfirmation || (response.Conversation != nil && response.Conversation.Stored) {
				t.Fatalf("stale proposal retained a review: %+v", response)
			}
		})
	}
}

func TestWorkspaceReviewRefusesMalformedOversizedSecretAndMixedToolOutput(t *testing.T) {
	for _, args := range []string{
		"not JSON", "null", workspaceProposalFixture + " {}",
		`{"name":"X","description":"brief","create":true}`,
		`{"name":"X","description":"` + strings.Repeat("界", 1601) + `"}`,
		`{"name":"` + strings.Repeat("N", 81) + `","description":"brief"}`,
		`{"name":"X","description":"api_key=private-sentinel"}`,
		`{"name":"X\nY","description":"brief"}`,
		`{"name":"X","description":"unsafe\u202etext"}`,
		`{"name":"X","description":"unsafe\u0000text"}`,
	} {
		f, store, _ := newResearchHostFixture(t)
		before := store.messageCount()
		f.provider.script = []llm.ChatResponse{workspaceProposalReply(args)}
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "how they started. can we create a workspace", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
		if response.Confirmation != nil || store.messageCount() != before || !response.ModelUnavailable || response.FailureReason != "invalid_workspace_proposal" || response.Conversation == nil || response.Conversation.ID != "canonical" {
			t.Fatalf("invalid proposal was saved or lost failure category/thread: %+v", response)
		}
	}
	f, _, source := newResearchHostFixture(t)
	mixed := workspaceProposalReply(workspaceProposalFixture)
	mixed.ToolCalls = append(mixed.ToolCalls, llm.ToolCall{Name: "assistant_installed_capabilities", Arguments: `{}`})
	f.provider.script = []llm.ChatResponse{mixed}
	response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "prepare a workspace review", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
	if response.FailureReason != "invalid_workspace_proposal" || source.localCalls != 0 {
		t.Fatal("mixed batch ran a call")
	}
}

func TestWorkspaceReviewProviderFailuresAreDistinctAndUseExistingDeadline(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "model_timeout"}, {context.Canceled, "request_cancelled"}, {errors.New("private-provider-error"), "provider_unavailable"}} {
		f, store, _ := newResearchHostFixture(t)
		before := store.messageCount()
		f.handler.LLMFactory.Register("fake", &workspaceProposalMutationProvider{Provider: f.provider, err: test.err, after: func(ctx context.Context, _ llm.ChatRequest) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 20*time.Second {
				t.Fatal("proposal still has its old 12-second deadline")
			}
		}})
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "prepare a workspace review", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
		if response.FailureReason != test.code || !response.ModelUnavailable || response.Conversation == nil || store.messageCount() != before || strings.Contains(response.Response, "private-provider-error") {
			t.Fatalf("failure not safe/distinct: %+v", response)
		}
	}
}

func TestWorkspaceReviewSnapshotOnlyDoesNotAddAnotherModelOrTools(t *testing.T) {
	f, _, _ := newResearchHostFixture(t)
	snapshot := &scriptedProvider{answers: []string{"Open workspace form manually from More assistant options. Nothing is created."}}
	f.handler.LLMFactory.Register("fake", snapshot)
	response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "prepare a workspace review", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
	if response.Confirmation != nil || len(snapshot.requests) != 1 || len(snapshot.requests[0].Tools) != 0 || !strings.Contains(snapshot.requests[0].Messages[0].Content, workspaceProposalManualInstructions) {
		t.Fatal("snapshot path escalated or added proposal generation")
	}
}

func TestWorkspaceReviewProseIsNeverAnExecutableProposal(t *testing.T) {
	f, _, _ := newResearchHostFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: workspaceProposalFixture}}
	response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "yes", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
	if response.Confirmation != nil {
		t.Fatal("prose became an action")
	}
}
