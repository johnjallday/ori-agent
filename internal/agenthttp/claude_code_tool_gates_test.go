package agenthttp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// claudeCodeShaped keeps a fixture's scripted Chat but reports the Claude
// Code provider's real capabilities, so the assistant's tool gates are checked
// against the declaration the product ships rather than a stub's.
type claudeCodeShaped struct{ llm.Provider }

func (p claudeCodeShaped) Name() string { return "claude_code" }

func (p claudeCodeShaped) Capabilities() llm.ProviderCapabilities {
	return (&llm.ClaudeCodeProvider{}).Capabilities()
}

func TestClaudeCodeAssistant_AskAdvertisesTheRegistryReaders(t *testing.T) {
	f := newReaderFixture(t)
	f.handler.LLMFactory.Register("fake", claudeCodeShaped{f.provider})
	f.provider.script = []llm.ChatResponse{{Content: "The release plan is to ship on Friday."}}

	f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "What does the release plan say?", Intent: "assistant_conversation", Context: f.refs})

	if len(f.provider.requests) == 0 {
		t.Fatal("the assistant never asked the model")
	}
	offered := offeredTools(f.provider.requests[0])
	registry, _ := f.registry(t)
	for _, tool := range registry.Definitions() {
		if !slices.Contains(offered, tool.Name) {
			t.Fatalf("a claude_code assistant was not offered %q (offered %v)", tool.Name, offered)
		}
	}
	if !slices.Contains(offered, readerNote) {
		t.Fatalf("expected the note reader among the offered tools, got %v", offered)
	}
}

func TestClaudeCodeAssistant_OperationsAreAvailable(t *testing.T) {
	f := newOperationFixture(t)
	f.handler.LLMFactory.Register("fake", claudeCodeShaped{f.provider})

	capability := f.handler.OperationCapability()
	if !capability.Available || capability.Reason != "" {
		t.Fatalf("operations must be available on claude_code: %+v", capability)
	}
	if !f.handler.OperationRequestSupported(context.Background(), "Open the Album-1 workspace") {
		t.Fatal("the router must divert an explicit operation request on claude_code")
	}
}

func TestClaudeCodeAssistant_ResearchBrokersTools(t *testing.T) {
	f, _, source := newResearchHostFixture(t)
	f.handler.LLMFactory.Register("fake", claudeCodeShaped{f.provider})
	f.provider.script = []llm.ChatResponse{{Content: "Let's compare existing tools before building."}}

	resp := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Find a Telegram skill", Intent: homeAssistantConversationIntent.Key, Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs})

	if resp.ResearchCapability == nil || !resp.ResearchCapability.BrokerTools || resp.ResearchCapability.Provider != "claude_code" {
		t.Fatalf("research must report brokered tools on claude_code: %+v", resp.ResearchCapability)
	}
	if len(f.provider.requests) == 0 || len(f.provider.requests[0].Tools) == 0 {
		t.Fatal("a claude_code research turn was offered no tools")
	}
	if strings.Contains(f.provider.requests[0].SystemPrompt, "snapshot-only") {
		t.Fatal("a claude_code research turn was told it is snapshot-only")
	}
	if source.externalCalls != 0 {
		t.Fatal("discussion performed an external lookup")
	}
}
