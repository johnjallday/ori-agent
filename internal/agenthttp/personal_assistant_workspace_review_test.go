package agenthttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

const workspaceProposalFixture = `{"name":"Community Membership","description":"Goal: recurring community membership, not talent coaching. Proposed starter steps: define the audience, compare membership workflows, outline a small pilot. Telegram compatibility remains unknown."}`

func TestWorkspaceReviewCurrentExplicitPreparationNotHistoricalConsent(t *testing.T) {
	for _, prompt := range []string{"Prepare a workspace review for this idea", "Please prepare a workspace proposal", "Let's set up a workspace for this plan", "Yes, prepare a workspace review"} {
		if !isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("explicit preparation missed: %q", prompt)
		}
	}
	for _, prompt := range []string{"yes", "use that", "Should we set up a workspace for this?", "Don't prepare a workspace review", `Explain "prepare a workspace review"`, "Prepare a workspace reviewable report"} {
		if isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("discussion/ambiguous consent became preparation: %q", prompt)
		}
	}
	prompt := buildAssistantConversationSystemPrompt(nil)
	if !strings.Contains(prompt, "may offer an optional workspace review even if") || strings.Contains(prompt, "do not suggest creating a workspace") || !strings.Contains(prompt, "respect a decline") {
		t.Fatal("conversation cannot naturally offer an optional review")
	}
}

func TestWorkspaceReviewBoundedToolFreeCanonicalProposalCreatesNothing(t *testing.T) {
	for _, failedSave := range []bool{false, true} {
		f, store, source := newResearchHostFixture(t)
		store.seed("canonical", f.work.HQWorkspaceID, f.work.ConversationAgent,
			PersonalAssistantConversationMessage{Role: "user", Content: "Community membership, not talent coaching."},
			PersonalAssistantConversationMessage{Role: "assistant", Content: "A membership pilot is a tentative option, not a user decision."})
		store.failSave = failedSave
		f.provider.script = []llm.ChatResponse{{Content: workspaceProposalFixture}}
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Prepare a workspace review for this idea", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
		if source.localCalls != 0 || source.externalCalls != 0 || len(f.provider.requests) != 1 {
			t.Fatal("preparation scanned sources or added model work")
		}
		request := f.provider.requests[0]
		serialized, _ := json.Marshal(request)
		if len(request.Tools) != 0 || request.WorkspaceID != "" || request.ExecutionScope != nil || request.MaxTokens != 700 || !strings.Contains(string(serialized), "not talent coaching") || strings.Contains(string(serialized), "PRIVATE_PROFILE_SENTINEL") {
			t.Fatalf("proposal lacks canonical constraints or inherited authority: %+v", request)
		}
		if failedSave {
			if response.RequiresConfirmation || response.Confirmation != nil {
				t.Fatal("unsaved/stale proposal opened a review")
			}
			continue
		}
		if !response.RequiresConfirmation || response.Confirmation == nil || response.Confirmation.ActionType != HomeActionPrepareWorkspace || response.Conversation == nil || !response.Conversation.Stored || response.Conversation.ID != "canonical" {
			t.Fatalf("missing canonical editable proposal: %+v", response)
		}
		if response.Confirmation.Arguments["description"] != "Goal: recurring community membership, not talent coaching. Proposed starter steps: define the audience, compare membership workflows, outline a small pilot. Telegram compatibility remains unknown." {
			t.Fatal("proposal changed between saved reply and editable form")
		}
		// It is not a mutation type; even a forged ConfirmedAction cannot run it.
		if homeMutatingActionTypes[HomeActionPrepareWorkspace] {
			t.Fatal("proposal has mutation authority")
		}
	}
}

type workspaceProposalMutationProvider struct {
	llm.Provider
	after func()
}

func (p *workspaceProposalMutationProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	result, err := p.Provider.Chat(ctx, request)
	p.after()
	return result, err
}

func TestWorkspaceReviewInFlightMutationCannotOfferOrSaveProposal(t *testing.T) {
	for _, mutation := range []string{"append", "delete", "owner", "relationship", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			f, store, _ := newResearchHostFixture(t)
			f.provider.script = []llm.ChatResponse{{Content: workspaceProposalFixture}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.handler.LLMFactory.Register("fake", &workspaceProposalMutationProvider{Provider: f.provider, after: func() {
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
			response := f.handler.Ask(ctx, HomeAssistantAskRequest{Prompt: "Prepare a workspace review for this idea", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
			if response.Confirmation != nil || response.RequiresConfirmation || (response.Conversation != nil && response.Conversation.Stored) {
				t.Fatalf("stale proposal retained a review: %+v", response)
			}
		})
	}
}

func TestWorkspaceReviewRefusesUnsafeOrOversizedRequestBeforeModel(t *testing.T) {
	for _, prompt := range []string{
		"Prepare a workspace review " + strings.Repeat("界", 2001),
		"Prepare a workspace review with api_key=private-sentinel",
	} {
		f, store, _ := newResearchHostFixture(t)
		before := store.messageCount()
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: prompt, Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
		if response.Confirmation != nil || store.messageCount() != before || len(f.provider.requests) != 0 {
			t.Fatal("unsafe request reached proposal model or saved a turn")
		}
	}
}

func TestWorkspaceReviewRefusesMalformedOversizedSecretAndToolCallingOutput(t *testing.T) {
	for _, result := range []llm.ChatResponse{
		{Content: "not JSON"},
		{Content: workspaceProposalFixture + " {}"},
		{Content: `{"name":"X","description":"brief","create":true}`},
		{Content: `{"name":"X","description":"` + strings.Repeat("界", 1601) + `"}`},
		{Content: `{"name":"X","description":"api_key=private-sentinel"}`},
		{Content: workspaceProposalFixture, ToolCalls: []llm.ToolCall{{Name: "create_workspace"}}},
	} {
		f, store, _ := newResearchHostFixture(t)
		before := store.messageCount()
		f.provider.script = []llm.ChatResponse{result}
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Prepare a workspace review for this idea", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
		if response.Confirmation != nil || response.RequiresConfirmation || store.messageCount() != before || !response.ModelUnavailable {
			t.Fatalf("invalid proposal offered a review or wrote a turn: %+v", response)
		}
	}
}
