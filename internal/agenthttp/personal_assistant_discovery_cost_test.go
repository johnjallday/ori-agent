package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestResearchHostShortDiscussionColdAndWarmDoesNotScanOrSummarize(t *testing.T) {
	f, _, source := newResearchHostFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Discussion only."}, {Content: "The correction stays in this discussion."}}
	for i, prompt := range []string{"Would a membership community be useful?", "No, not talent coaching; just recurring membership."} {
		response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: prompt, Intent: homeAssistantConversationIntent.Key, Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs})
		if response.Conversation == nil || !response.Conversation.Stored || response.Conversation.ID != "canonical" {
			t.Fatalf("cold/warm discussion did not save: %+v", response)
		}
		if len(f.provider.requests) != i+1 || source.localCalls != 0 || source.externalCalls != 0 || response.ResearchReview != nil {
			t.Fatalf("ordinary discussion paid extra work: models=%d local=%d external=%d", len(f.provider.requests), source.localCalls, source.externalCalls)
		}
	}
}
