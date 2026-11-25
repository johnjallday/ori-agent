package agenthttp

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"strings"
	"testing"
)

func TestWorkspaceRequest_ExplicitChoiceNeverCallsModelOrProposesHire(t *testing.T) {
	for prompt, action := range map[string]string{"Create a workspace from Field Notes": "navigate", "Create a workspace for marine biology": "blank_workspace_review", "Can we create a workspace for the comparison?": "blank_workspace_review"} {
		f, _, _, proposals := newHireProposalFixture(t)
		response := askHire(f, prompt)
		if response.Confirmation != nil || len(f.provider.requests) != 0 || len(proposals.order) != 0 || len(response.Actions) != 1 || response.Actions[0].Type != action {
			t.Fatalf("%s: %+v", prompt, response)
		}
	}
}
func TestHireProposalAdviceAndAssentWithholdTool(t *testing.T) {
	for _, prompt := range []string{"How should I compare interviews?", "yes", "Make it warmer"} {
		f, _, _, proposals := newHireProposalFixture(t)
		f.provider.script = []llm.ChatResponse{{Content: "A short answer."}}
		answer := askHire(f, prompt)
		if answer.Confirmation != nil || len(answer.Actions) > 0 || len(proposals.order) != 0 || strings.Contains(strings.Join(toolNamesOf(f.provider.requests[0]), ","), hireProposalTool) {
			t.Fatalf("%s got an offer/tool", prompt)
		}
	}
}

func TestHireProposalSettledChoiceRefusesHireBeforeAgentOrWorkspace(t *testing.T) {
	for _, state := range []personalassistant.HireProposalState{personalassistant.HireProposalDeclined, personalassistant.HireProposalWorkspaceDraft, personalassistant.HireProposalAdjusted} {
		f := newHireFixture(t)
		f.h.SetHireConversationAllowed(func(context.Context, string, string) bool { return true })
		_, err := f.proposals.Create(t.Context(), personalassistant.HireProposal{ID: "proposal", UserID: "local", ConversationID: "thread", Role: "researcher", Name: "Ruth", Brief: "Compare interviews", WorkspaceName: "Research", Blueprint: "research-project", BlueprintLabel: "Research Project", State: state})
		if err != nil {
			t.Fatal(err)
		}
		res, _ := f.hire(t, `{"request_id":"proposal","proposal_conversation_id":"thread","role":"researcher","name":"Ruth","brief":"Compare interviews"}`)
		if res.Code != 409 || f.workspaceCount(t) != 0 || len(f.agents.ListAgents()) != 0 {
			t.Fatalf("hire after %s: %d %s", state, res.Code, res.Body.String())
		}
	}
}
